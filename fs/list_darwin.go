//go:build darwin

package fs

import (
	"encoding/binary"
	"os"
	"sync"
	"syscall"
	"unsafe"
)

// getattrlistbulk(2) lists a directory with the attributes of every entry
// in one call per buffer (DECISIONS D12: 88 ms for 100k entries against
// 198 ms for ReadDir+Info). Go's syscall table has no number for it, so the
// raw number is used through syscall.Syscall6 (zero-alloc, errno included).
const (
	sysGetattrlistbulk = 461

	attrBitMapCount = 5

	attrCmnName          = 0x00000001
	attrCmnObjType       = 0x00000008
	attrCmnModTime       = 0x00000400
	attrCmnAccessMask    = 0x00020000
	attrCmnFlags         = 0x00040000
	attrCmnReturnedAttrs = 0x80000000
	attrFileDataLength   = 0x00000200

	fsoptNoFollow = 0x00000001

	vReg, vDir, vLnk = 1, 2, 5

	ufHidden = 0x8000 // UF_HIDDEN in the BSD file flags

	sIFMT, sIFDIR = 0o170000, 0o040000

	bulkBufSize = 256 << 10
)

type attrList struct {
	bitmapCount uint16
	reserved    uint16
	commonAttr  uint32
	volAttr     uint32
	dirAttr     uint32
	fileAttr    uint32
	forkAttr    uint32
}

// bulkScratch is the per-call working memory of listDir. It is recycled
// through a free list (not a sync.Pool, which the GC empties) so a warm
// listing allocates nothing.
type bulkScratch struct {
	buf  [bulkBufSize]byte
	stat [256]byte // struct stat64 for symlink targets
	al   attrList
	path []byte // NUL-terminated directory path
	link []byte // NUL-terminated "dir/name" of a symlink being stat'ed
}

var (
	scratchMu   sync.Mutex
	scratchFree []*bulkScratch
)

func getScratch() *bulkScratch {
	scratchMu.Lock()
	defer scratchMu.Unlock()
	if n := len(scratchFree); n > 0 {
		s := scratchFree[n-1]
		scratchFree = scratchFree[:n-1]
		return s
	}
	return new(bulkScratch)
}

func putScratch(s *bulkScratch) {
	scratchMu.Lock()
	scratchFree = append(scratchFree, s)
	scratchMu.Unlock()
}

func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func le64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }

// listDir lists path into l without sorting. l's buffers must be reset.
func listDir(path string, l *Listing) error {
	s := getScratch()
	defer putScratch(s)
	s.path = append(append(s.path[:0], path...), 0)
	if len(path) != len(s.path)-1 || containsNUL(path) {
		return &os.PathError{Op: "open", Path: path, Err: syscall.EINVAL}
	}

	var fd uintptr
	for {
		r, _, errno := syscall.Syscall(syscall.SYS_OPEN, uintptr(unsafe.Pointer(&s.path[0])),
			syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return &os.PathError{Op: "open", Path: path, Err: errno}
		}
		fd = r
		break
	}
	defer syscall.Close(int(fd))

	s.al = attrList{
		bitmapCount: attrBitMapCount,
		commonAttr:  attrCmnReturnedAttrs | attrCmnName | attrCmnObjType | attrCmnModTime | attrCmnAccessMask | attrCmnFlags,
		fileAttr:    attrFileDataLength,
	}
	first := true
	for {
		n, _, errno := syscall.Syscall6(sysGetattrlistbulk, fd, uintptr(unsafe.Pointer(&s.al)),
			uintptr(unsafe.Pointer(&s.buf[0])), bulkBufSize, fsoptNoFollow, 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			if first && (errno == syscall.ENOTSUP || errno == syscall.EINVAL) {
				return listDirPortable(path, l) // file system without bulk support
			}
			return &os.PathError{Op: "getattrlistbulk", Path: path, Err: errno}
		}
		first = false
		if n == 0 {
			return nil
		}
		parseBulk(s, int(n), l)
	}
}

func containsNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}

// parseBulk appends the n entries in s.buf to l. Fields are read according
// to the returned-attributes bitmap of each entry, never by assumption:
// directories have no ATTR_FILE_DATALENGTH (D12).
func parseBulk(s *bulkScratch, n int, l *Listing) {
	buf := s.buf[:]
	p := 0
	for i := 0; i < n && p+24 <= len(buf); i++ {
		length := int(le32(buf[p:]))
		if length < 24 || p+length > len(buf) {
			return
		}
		e := buf[p : p+length]
		p += length
		retCmn, retFile := le32(e[4:]), le32(e[16:])
		if retCmn&attrCmnName == 0 {
			continue
		}
		o := 24
		nameStart := o + int(int32(le32(e[o:])))
		nameLen := int(le32(e[o+4:])) - 1 // drop the NUL
		o += 8
		var objType, mode, flags uint32
		var mtime, size int64
		if retCmn&attrCmnObjType != 0 {
			objType = le32(e[o:])
			o += 4
		}
		if retCmn&attrCmnModTime != 0 {
			mtime = int64(le64(e[o:]))*1e9 + int64(le64(e[o+8:]))
			o += 16
		}
		if retCmn&attrCmnAccessMask != 0 {
			mode = le32(e[o:])
			o += 4
		}
		if retCmn&attrCmnFlags != 0 {
			flags = le32(e[o:])
			o += 4
		}
		if retFile&attrFileDataLength != 0 {
			size = int64(le64(e[o:]))
		}
		if nameLen <= 0 || nameStart < 0 || nameStart+nameLen > len(e) {
			continue
		}
		if nameLen > 0xFFFF {
			nameLen = 0xFFFF
		}
		name := e[nameStart : nameStart+nameLen]

		var kind Kind
		var fl uint8
		switch objType {
		case vReg:
			kind = KindFile
		case vDir:
			kind, size = KindDir, 0
		case vLnk:
			kind = KindSymlink
		default:
			kind = KindOther
		}
		if name[0] == '.' || flags&ufHidden != 0 {
			fl |= FlagHidden
		}
		if kind == KindFile && mode&0o111 != 0 {
			fl |= FlagExecutable
		}
		if mode&0o200 == 0 && kind != KindSymlink {
			fl |= FlagReadOnly
		}
		if kind == KindSymlink && symlinkIsDir(s, name) {
			fl |= FlagSymlinkDir
		}
		l.Entries = append(l.Entries, Entry{
			Size: size, MTime: mtime,
			NameOff: uint32(len(l.Names)), NameLen: uint16(nameLen),
			Kind: kind, Flags: fl,
		})
		l.Names = append(l.Names, name...)
	}
}

// symlinkIsDir stats the link's target. Links are rare enough that one
// stat each is cheap, and the UI needs to know whether Enter descends.
func symlinkIsDir(s *bulkScratch, name []byte) bool {
	dir := s.path[:len(s.path)-1] // without the NUL
	s.link = append(append(append(s.link[:0], dir...), '/'), name...)
	s.link = append(s.link, 0)
	_, _, errno := syscall.Syscall(syscall.SYS_STAT64, uintptr(unsafe.Pointer(&s.link[0])),
		uintptr(unsafe.Pointer(&s.stat[0])), 0)
	if errno != 0 {
		return false
	}
	mode := uint32(s.stat[4]) | uint32(s.stat[5])<<8 // st_mode: uint16 at offset 4
	return mode&sIFMT == sIFDIR
}

// listDirPortable is the fallback for file systems without
// getattrlistbulk (some network and FUSE volumes). It allocates.
func listDirPortable(path string, l *Listing) error {
	des, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, d := range des {
		name := d.Name()
		e := Entry{NameOff: uint32(len(l.Names)), NameLen: uint16(min(len(name), 0xFFFF))}
		if name[0] == '.' {
			e.Flags |= FlagHidden
		}
		mode := d.Type()
		switch {
		case mode.IsDir():
			e.Kind = KindDir
		case mode&os.ModeSymlink != 0:
			e.Kind = KindSymlink
		case !mode.IsRegular():
			e.Kind = KindOther
		}
		if fi, err := d.Info(); err == nil {
			e.MTime = fi.ModTime().UnixNano()
			if e.Kind == KindFile {
				e.Size = fi.Size()
				if fi.Mode()&0o111 != 0 {
					e.Flags |= FlagExecutable
				}
			}
			if fi.Mode().Perm()&0o200 == 0 && e.Kind != KindSymlink {
				e.Flags |= FlagReadOnly
			}
		}
		if e.Kind == KindSymlink {
			if st, err := os.Stat(path + "/" + name); err == nil && st.IsDir() {
				e.Flags |= FlagSymlinkDir
			}
		}
		l.Entries = append(l.Entries, e)
		l.Names = append(l.Names, name[:e.NameLen]...)
	}
	return nil
}
