//go:build linux

package fs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gonativeui/core"
)

// Linux directory listing: getdents64 reads a directory in 64 KiB batches
// and statx(AT_STATX_DONT_SYNC) asks for exactly the fields an Entry needs
// (type, mode, size, mtime), straight from the name in the dirent buffer.
// There is no libc and no per-entry allocation. lxListReadDir is the
// os.ReadDir-style variant kept as the benchmark baseline (DECISIONS D12)
// and as the fallback when statx is unavailable (kernels before 4.11, or
// a seccomp filter that rejects it with ENOSYS or EPERM).
//
// Names are the raw bytes the file system stores. They are UTF-8 on every
// sane system but nothing enforces it; consumers must tolerate other bytes.
//
// Kind comes from the lstat mode. A symlink is KindSymlink, with
// FlagSymlinkDir when its target (a second statx, symlinks only) is a
// directory; a dangling link is a plain KindSymlink. FlagExecutable means
// any execute bit on a regular file (the target's, for a symlink);
// FlagHidden a leading dot; FlagReadOnly no write bit at all.

// lxStatx mirrors struct statx (include/uapi/linux/stat.h, linux master,
// fetched 2026-10-17): 256 bytes, the same on amd64 and arm64.
type lxStatx struct {
	Mask       uint32       // 0x00
	Blksize    uint32       // 0x04
	Attributes uint64       // 0x08
	Nlink      uint32       // 0x10
	UID        uint32       // 0x14
	GID        uint32       // 0x18
	Mode       uint16       // 0x1c
	_          uint16       // 0x1e
	Ino        uint64       // 0x20
	Size       uint64       // 0x28
	Blocks     uint64       // 0x30
	AttrMask   uint64       // 0x38
	Atime      lxStatxStamp // 0x40
	Btime      lxStatxStamp // 0x50
	Ctime      lxStatxStamp // 0x60
	Mtime      lxStatxStamp // 0x70
	_          [128]byte    // 0x80..0x100: device numbers, mount id, dio alignment, subvolume, atomic-write, spare
}

// lxStatxStamp mirrors struct statx_timestamp (16 bytes).
type lxStatxStamp struct {
	Sec  int64
	Nsec uint32
	_    int32
}

var (
	_ [unsafe.Sizeof(lxStatx{}) - 256]struct{}
	_ [256 - unsafe.Sizeof(lxStatx{})]struct{}
	_ [unsafe.Offsetof(lxStatx{}.Mode) - 0x1c]struct{}
	_ [0x1c - unsafe.Offsetof(lxStatx{}.Mode)]struct{}
	_ [unsafe.Offsetof(lxStatx{}.Size) - 0x28]struct{}
	_ [0x28 - unsafe.Offsetof(lxStatx{}.Size)]struct{}
	_ [unsafe.Offsetof(lxStatx{}.Mtime) - 0x70]struct{}
	_ [0x70 - unsafe.Offsetof(lxStatx{}.Mtime)]struct{}
	_ [unsafe.Sizeof(lxStatxStamp{}) - 16]struct{}
	_ [16 - unsafe.Sizeof(lxStatxStamp{})]struct{}
)

const (
	// statx(2) flags: fcntl.h AT_SYMLINK_NOFOLLOW 0x100, AT_NO_AUTOMOUNT
	// 0x800, AT_STATX_DONT_SYNC 0x4000.
	lxAtSymlinkNoFollow = 0x100
	lxAtNoAutomount     = 0x800
	lxAtStatxDontSync   = 0x4000

	// stat.h STATX_TYPE 0x1, STATX_MODE 0x2, STATX_MTIME 0x40, STATX_SIZE 0x200.
	lxStatxType  = 0x1
	lxStatxMode  = 0x2
	lxStatxMtime = 0x40
	lxStatxSize  = 0x200
	lxStatxWant  = lxStatxType | lxStatxMode | lxStatxMtime | lxStatxSize

	// File types in st_mode.
	lxSIFMT  = 0o170000
	lxSIFDIR = 0o040000
	lxSIFREG = 0o100000
	lxSIFLNK = 0o120000

	// linux_dirent64 (include/linux/dirent.h; d_reclen is 8-aligned, fs/readdir.c filldir64): d_ino u64 @0, d_off s64
	// @8, d_reclen u16 @16, d_type u8 @18, d_name @19.
	lxDirentReclen = 16
	lxDirentType   = 18
	lxDirentName   = 19
	lxDtDir        = 4
	lxDtReg        = 8
	lxDtLnk        = 10

	lxDentsSize = 64 << 10
	lxPathMax   = 4096
)

// lxAtFdcwd is AT_FDCWD (-100) as a syscall argument.
const lxAtFdcwd = ^uintptr(99)

// lxScratch is the per-listing working memory, pooled so a warm listing
// allocates nothing. It lives on the heap, which the kernel writes into.
type lxScratch struct {
	dents [lxDentsSize / 8]uint64 // 8-byte aligned
	stx   lxStatx                 // the entry itself (lstat semantics)
	tgt   lxStatx                 // a symlink's target
	path  [lxPathMax + 1]byte
}

// Scratch goes through a free list, not a sync.Pool: a Pool may hand a
// goroutine on another P a fresh object and the GC empties it, so a warm
// listing would still allocate.
var (
	lxScratchMu   core.Mutex
	lxScratchFree []*lxScratch
)

func lxGetScratch() *lxScratch {
	lxScratchMu.Lock()
	defer lxScratchMu.Unlock()
	if n := len(lxScratchFree); n > 0 {
		s := lxScratchFree[n-1]
		lxScratchFree = lxScratchFree[:n-1]
		return s
	}
	return new(lxScratch)
}

func lxPutScratch(s *lxScratch) {
	lxScratchMu.Lock()
	lxScratchFree = append(lxScratchFree, s)
	lxScratchMu.Unlock()
}

// statx availability: 0 unknown, 1 works, 2 unavailable (use ReadDir).
var lxStatxState atomic.Int32

var errLxNoStatx = errors.New("fs: statx unavailable")

// listDir lists path into l without sorting.
func listDir(path string, l *Listing) error {
	if lxStatxState.Load() == 2 {
		return lxListReadDir(path, l)
	}
	err := lxListStatx(path, l)
	if err == errLxNoStatx {
		lxStatxState.Store(2)
		return lxListReadDir(path, l)
	}
	return err
}

func lxPathErr(op, path string, e syscall.Errno) error {
	return &os.PathError{Op: op, Path: path, Err: e}
}

// lxStatxAt calls statx(dirfd, name, flags, mask, st), retrying on EINTR.
func lxStatxAt(dirfd int, name *byte, flags uintptr, st *lxStatx) syscall.Errno {
	for {
		_, _, e := syscall.Syscall6(sysStatx, uintptr(dirfd), uintptr(unsafe.Pointer(name)),
			flags, lxStatxWant, uintptr(unsafe.Pointer(st)), 0)
		if e != syscall.EINTR {
			return e
		}
	}
}

// lxListStatx is the getdents64 + statx listing.
func lxListStatx(path string, l *Listing) error {
	if len(path) >= lxPathMax {
		return lxPathErr("fs.list", path, syscall.ENAMETOOLONG)
	}
	if strings.IndexByte(path, 0) >= 0 {
		return lxPathErr("fs.list", path, syscall.EINVAL)
	}
	s := lxGetScratch()
	defer lxPutScratch(s)
	s.path[copy(s.path[:], path)] = 0

	var fd uintptr
	for {
		r, _, e := syscall.Syscall6(syscall.SYS_OPENAT, lxAtFdcwd, uintptr(unsafe.Pointer(&s.path[0])),
			syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0, 0, 0)
		if e == syscall.EINTR {
			continue
		}
		if e != 0 {
			return lxPathErr("fs.list", path, e)
		}
		fd = r
		break
	}
	defer syscall.Syscall(syscall.SYS_CLOSE, fd, 0, 0)

	entries0, names0 := len(l.Entries), len(l.Names)
	var skipped error // an entry that could not be added; reported at the end
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&s.dents[0])), lxDentsSize)
	for {
		n, _, e := syscall.Syscall(syscall.SYS_GETDENTS64, fd, uintptr(unsafe.Pointer(&buf[0])), lxDentsSize)
		if e == syscall.EINTR {
			continue
		}
		if e != 0 {
			return lxPathErr("fs.list", path, e)
		}
		if n == 0 {
			return lxListResult(path, skipped)
		}
		for off := 0; off < int(n); {
			if off+lxDirentName+1 > int(n) {
				return lxPathErr("fs.list", path, syscall.EIO)
			}
			rec := buf[off:]
			reclen := int(*(*uint16)(unsafe.Pointer(&rec[lxDirentReclen])))
			if reclen < lxDirentName+1 || off+reclen > int(n) {
				return lxPathErr("fs.list", path, syscall.EIO)
			}
			off += reclen
			ino := *(*uint64)(unsafe.Pointer(&rec[0]))
			dtype := rec[lxDirentType]
			nm := rec[lxDirentName:reclen]
			nl := 0
			for nl < len(nm) && nm[nl] != 0 {
				nl++
			}
			name := nm[:nl]
			if ino == 0 || nl == 0 || (name[0] == '.' && (nl == 1 || (nl == 2 && name[1] == '.'))) {
				continue
			}
			if nl == len(nm) { // not NUL-terminated within the record: corrupt
				return lxPathErr("fs.list", path, syscall.EIO)
			}
			if err := checkName(len(l.Names), nl); err != nil {
				if err == errArenaFull {
					return lxListResult(path, err) // nothing more fits
				}
				skipped = err
				continue
			}

			e := lxStatxAt(int(fd), &nm[0], lxAtSymlinkNoFollow|lxAtNoAutomount|lxAtStatxDontSync, &s.stx)
			if e != 0 {
				switch {
				case e == syscall.ENOENT:
					continue // removed since getdents
				case (e == syscall.ENOSYS || e == syscall.EPERM) && lxStatxState.Load() != 1:
					l.Entries, l.Names = l.Entries[:entries0], l.Names[:names0]
					return errLxNoStatx
				}
				s.stx = lxStatx{} // unreadable: list it from d_type alone
				switch dtype {
				case lxDtDir:
					s.stx.Mode, s.stx.Mask = lxSIFDIR, lxStatxType
				case lxDtLnk:
					s.stx.Mode, s.stx.Mask = lxSIFLNK, lxStatxType
				case lxDtReg:
					s.stx.Mode, s.stx.Mask = lxSIFREG, lxStatxType
				}
			} else if lxStatxState.Load() == 0 {
				lxStatxState.Store(1)
			}
			lxAddEntry(l, s, int(fd), &nm[0], name)
		}
	}
}

// lxListResult is the result of a listing that read the whole directory:
// nil, or the partial-listing error of an entry that could not be added.
func lxListResult(path string, skipped error) error {
	if skipped == nil {
		return nil
	}
	return &os.PathError{Op: "fs.list", Path: path, Err: skipped}
}

// lxAddEntry appends the entry whose statx result is in s.stx. The caller
// has checked that the name fits (checkName): Entry.NameOff and NameLen are
// narrowed here.
func lxAddEntry(l *Listing, s *lxScratch, dirfd int, cname *byte, name []byte) {
	st := &s.stx
	var kind Kind
	var flags uint8
	mode := uint32(st.Mode)
	size := int64(0)
	if st.Mask&lxStatxSize != 0 {
		size = int64(st.Size)
	}
	switch mode & lxSIFMT {
	case lxSIFREG:
		kind = KindFile
		if mode&0o111 != 0 {
			flags |= FlagExecutable
		}
	case lxSIFDIR:
		kind = KindDir
		size = 0
	case lxSIFLNK:
		kind = KindSymlink
		if lxStatxAt(dirfd, cname, lxAtNoAutomount|lxAtStatxDontSync, &s.tgt) == 0 {
			switch uint32(s.tgt.Mode) & lxSIFMT {
			case lxSIFDIR:
				flags |= FlagSymlinkDir
				size = 0
			case lxSIFREG:
				if s.tgt.Mode&0o111 != 0 {
					flags |= FlagExecutable
				}
			}
		}
	case 0:
		if st.Mask&lxStatxMode == 0 { // nothing known at all
			kind = KindFile
			break
		}
		fallthrough
	default:
		kind = KindOther
	}
	if mode&0o222 == 0 && st.Mask&lxStatxMode != 0 && kind != KindSymlink {
		flags |= FlagReadOnly
	}
	if name[0] == '.' {
		flags |= FlagHidden
	}
	var mtime int64
	if st.Mask&lxStatxMtime != 0 {
		mtime = st.Mtime.Sec*1e9 + int64(st.Mtime.Nsec)
	}
	off := len(l.Names)
	l.Names = append(l.Names, name...)
	l.Entries = append(l.Entries, Entry{
		Size:    size,
		MTime:   mtime,
		NameOff: uint32(off),
		NameLen: uint16(len(name)),
		Kind:    kind,
		Flags:   flags,
	})
}

// lxListReadDir is the portable variant: os.File.ReadDir plus DirEntry.Info
// (an lstat per entry). It allocates per entry; it is the benchmark baseline
// and the fallback without statx.
func lxListReadDir(path string, l *Listing) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	des, err := f.ReadDir(-1)
	var skipped error
	for _, de := range des {
		name := de.Name()
		if name == "" {
			continue
		}
		if cerr := checkName(len(l.Names), len(name)); cerr != nil {
			if cerr == errArenaFull {
				return lxListResult(path, cerr)
			}
			skipped = cerr
			continue
		}
		info, ierr := de.Info()
		if ierr != nil {
			continue // removed since the read
		}
		var e Entry
		m := info.Mode()
		switch {
		case m&os.ModeSymlink != 0:
			e.Kind = KindSymlink
			if t, serr := os.Stat(filepath.Join(path, name)); serr == nil {
				switch {
				case t.IsDir():
					e.Flags |= FlagSymlinkDir
				case t.Mode().IsRegular() && t.Mode().Perm()&0o111 != 0:
					e.Flags |= FlagExecutable
				}
			}
			e.Size = info.Size()
		case m.IsDir():
			e.Kind = KindDir
		case m.IsRegular():
			e.Kind = KindFile
			e.Size = info.Size()
			if m.Perm()&0o111 != 0 {
				e.Flags |= FlagExecutable
			}
		default:
			e.Kind = KindOther
			e.Size = info.Size()
		}
		if e.Flags&FlagSymlinkDir != 0 {
			e.Size = 0
		}
		if m.Perm()&0o222 == 0 && e.Kind != KindSymlink {
			e.Flags |= FlagReadOnly
		}
		if name[0] == '.' {
			e.Flags |= FlagHidden
		}
		e.MTime = info.ModTime().UnixNano()
		e.NameOff = uint32(len(l.Names))
		e.NameLen = uint16(len(name))
		l.Names = append(l.Names, name...)
		l.Entries = append(l.Entries, e)
	}
	if err != nil {
		return err
	}
	return lxListResult(path, skipped)
}
