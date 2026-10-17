//go:build windows

package fs

import (
	"errors"
	"syscall"
	"unsafe"
)

// Windows directory listing. Three variants share winAddEntry:
//
//   - winListFind: FindFirstFileExW(FindExInfoBasic, FIND_FIRST_EX_LARGE_FETCH)
//     + FindNextFileW, one call per entry;
//   - winListExtd: GetFileInformationByHandleEx(FileIdExtdDirectoryInfo), one
//     call per 64 KiB of entries (Windows 8+; reports the reparse tag);
//   - winListFull: GetFileInformationByHandleEx(FileFullDirectoryInfo), same
//     batching, older info class (EaSize carries the reparse tag).
//
// DECISIONS D12 says to choose by measurement: TestWinListBackendChoice logs
// the numbers on CI. listDir uses winListExtd, falling back to winListFull
// and then winListFind when the file system rejects the info class (network
// shares, FAT on old systems return ERROR_INVALID_PARAMETER / NOT_SUPPORTED /
// INVALID_LEVEL).
//
// Symlinks and junctions are reported as KindSymlink, with FlagSymlinkDir
// when the link is a directory link (the directory attribute is set on the
// link itself at creation). Other reparse points (cloud placeholders,
// dedup, app-exec links) are listed as the plain file or directory they
// present as.

// winFindData mirrors WIN32_FIND_DATAW (minwinbase.h:79-95): 592 bytes.
type winFindData struct {
	Attr          uint32
	Creation      [2]uint32 // FILETIME {low, high}
	Access        [2]uint32
	Write         [2]uint32
	SizeHigh      uint32
	SizeLow       uint32
	Reserved0     uint32 // reparse tag when Attr has FILE_ATTRIBUTE_REPARSE_POINT
	Reserved1     uint32
	FileName      [260]uint16 // MAX_PATH
	AlternateName [14]uint16
}

// winFullDirInfo mirrors FILE_FULL_DIR_INFO (winbase.h:3259-3272).
type winFullDirInfo struct {
	NextEntryOffset uint32
	FileIndex       uint32
	CreationTime    int64
	LastAccessTime  int64
	LastWriteTime   int64
	ChangeTime      int64
	EndOfFile       int64
	AllocationSize  int64
	FileAttributes  uint32
	FileNameLength  uint32 // bytes
	EaSize          uint32
	FileName        [1]uint16
}

// winExtdDirInfo mirrors FILE_ID_EXTD_DIR_INFO (winbase.h:3310-3325).
type winExtdDirInfo struct {
	NextEntryOffset uint32
	FileIndex       uint32
	CreationTime    int64
	LastAccessTime  int64
	LastWriteTime   int64
	ChangeTime      int64
	EndOfFile       int64
	AllocationSize  int64
	FileAttributes  uint32
	FileNameLength  uint32 // bytes
	EaSize          uint32
	ReparsePointTag uint32
	FileID          [16]byte // FILE_ID_128
	FileName        [1]uint16
}

// Layout assertions: each line is a compile error when the Go struct and the
// C header disagree (an array length can't be negative).
var (
	_ [unsafe.Sizeof(winFindData{}) - 592]struct{}
	_ [592 - unsafe.Sizeof(winFindData{})]struct{}
	_ [unsafe.Offsetof(winFindData{}.Write) - 20]struct{}
	_ [20 - unsafe.Offsetof(winFindData{}.Write)]struct{}
	_ [unsafe.Offsetof(winFindData{}.SizeHigh) - 28]struct{}
	_ [28 - unsafe.Offsetof(winFindData{}.SizeHigh)]struct{}
	_ [unsafe.Offsetof(winFindData{}.Reserved0) - 36]struct{}
	_ [36 - unsafe.Offsetof(winFindData{}.Reserved0)]struct{}
	_ [unsafe.Offsetof(winFindData{}.FileName) - 44]struct{}
	_ [44 - unsafe.Offsetof(winFindData{}.FileName)]struct{}
	_ [unsafe.Offsetof(winFindData{}.AlternateName) - 564]struct{}
	_ [564 - unsafe.Offsetof(winFindData{}.AlternateName)]struct{}

	_ [unsafe.Offsetof(winFullDirInfo{}.LastWriteTime) - 24]struct{}
	_ [24 - unsafe.Offsetof(winFullDirInfo{}.LastWriteTime)]struct{}
	_ [unsafe.Offsetof(winFullDirInfo{}.EndOfFile) - 40]struct{}
	_ [40 - unsafe.Offsetof(winFullDirInfo{}.EndOfFile)]struct{}
	_ [unsafe.Offsetof(winFullDirInfo{}.FileAttributes) - 56]struct{}
	_ [56 - unsafe.Offsetof(winFullDirInfo{}.FileAttributes)]struct{}
	_ [unsafe.Offsetof(winFullDirInfo{}.EaSize) - 64]struct{}
	_ [64 - unsafe.Offsetof(winFullDirInfo{}.EaSize)]struct{}
	_ [unsafe.Offsetof(winFullDirInfo{}.FileName) - 68]struct{}
	_ [68 - unsafe.Offsetof(winFullDirInfo{}.FileName)]struct{}

	_ [unsafe.Offsetof(winExtdDirInfo{}.LastWriteTime) - 24]struct{}
	_ [24 - unsafe.Offsetof(winExtdDirInfo{}.LastWriteTime)]struct{}
	_ [unsafe.Offsetof(winExtdDirInfo{}.EndOfFile) - 40]struct{}
	_ [40 - unsafe.Offsetof(winExtdDirInfo{}.EndOfFile)]struct{}
	_ [unsafe.Offsetof(winExtdDirInfo{}.FileAttributes) - 56]struct{}
	_ [56 - unsafe.Offsetof(winExtdDirInfo{}.FileAttributes)]struct{}
	_ [unsafe.Offsetof(winExtdDirInfo{}.EaSize) - 64]struct{}
	_ [64 - unsafe.Offsetof(winExtdDirInfo{}.EaSize)]struct{}
	_ [unsafe.Offsetof(winExtdDirInfo{}.ReparsePointTag) - 68]struct{}
	_ [68 - unsafe.Offsetof(winExtdDirInfo{}.ReparsePointTag)]struct{}
	_ [unsafe.Offsetof(winExtdDirInfo{}.FileID) - 72]struct{}
	_ [72 - unsafe.Offsetof(winExtdDirInfo{}.FileID)]struct{}
	_ [unsafe.Offsetof(winExtdDirInfo{}.FileName) - 88]struct{}
	_ [88 - unsafe.Offsetof(winExtdDirInfo{}.FileName)]struct{}
)

// FILE_INFO_BY_HANDLE_CLASS values (minwinbase.h:141-174, counting the
// enumerators from FileBasicInfo = 0).
const (
	winClassStandardInfo     = 1  // FileStandardInfo
	winClassFullDir          = 14 // FileFullDirectoryInfo
	winClassFullDirRestart   = 15 // FileFullDirectoryRestartInfo
	winClassExtdDir          = 19 // FileIdExtdDirectoryInfo
	winClassExtdDirRestart   = 20 // FileIdExtdDirectoryRestartInfo
	winFindExInfoBasic       = 1  // FINDEX_INFO_LEVELS
	winFindExSearchNameMatch = 0  // FINDEX_SEARCH_OPS
	winFindFirstExLargeFetch = 2  // FIND_FIRST_EX_LARGE_FETCH

	winDirBufSize = 64 << 10
)

// errWinFallback means: this info class is not supported here, try the next
// listing method. The Listing has been truncated back to its start.
var errWinFallback = errors.New("fs: info class not supported")

// listDir lists path into l without sorting.
func listDir(path string, l *Listing) error { return winListExtd(path, l) }

func winListExtd(path string, l *Listing) error {
	err := winListByHandle(path, l, true)
	if err == errWinFallback {
		err = winListByHandle(path, l, false)
	}
	if err == errWinFallback {
		err = winListFind(path, l)
	}
	return err
}

func winListFull(path string, l *Listing) error {
	err := winListByHandle(path, l, false)
	if err == errWinFallback {
		err = winListFind(path, l)
	}
	return err
}

// winListByHandle lists with GetFileInformationByHandleEx. extd selects
// FileIdExtdDirectoryInfo, otherwise FileFullDirectoryInfo.
func winListByHandle(path string, l *Listing, extd bool) error {
	s := winGetScratch()
	defer winPutScratch(s)
	dir, e := s.winDirPath(path)
	if e != 0 {
		return winPathErr("fs.list", path, e)
	}
	h, _, e := syscall.SyscallN(pCreateFileW, uintptr(unsafe.Pointer(&dir[0])),
		winFileListDirectory, winShareAll, 0, winOpenExisting, winFlagBackupSemantics, 0)
	if h == winInvalidHandle {
		return winPathErr("fs.list", path, e)
	}
	defer winCloseHandle(h)

	if s.big == nil {
		s.big = make([]uint64, winDirBufSize/8)
	}
	buf := unsafe.Pointer(&s.big[0])
	class, next := winClassFullDirRestart, winClassFullDir
	nameOff := unsafe.Offsetof(winFullDirInfo{}.FileName)
	if extd {
		class, next = winClassExtdDirRestart, winClassExtdDir
		nameOff = unsafe.Offsetof(winExtdDirInfo{}.FileName)
	}
	entries0, names0 := len(l.Entries), len(l.Names)
	first := true
	for {
		r, _, e := syscall.SyscallN(pGetFileInformationByHandleEx, h, uintptr(class), uintptr(buf), winDirBufSize)
		if uint32(r) == 0 {
			switch {
			case e == winErrNoMoreFiles, first && e == winErrFileNotFound:
				return nil // done; or an empty volume root, which has no "." and ".." to return
			case first && (e == winErrInvalidParam || e == winErrNotSupported || e == winErrInvalidLevel):
				l.Entries, l.Names = l.Entries[:entries0], l.Names[:names0]
				return errWinFallback
			}
			return winPathErr("fs.list", path, e)
		}
		first = false
		class = next

		// Walk the packed records. The buffer is 8-byte aligned and the
		// kernel keeps NextEntryOffset a multiple of 8.
		pos := uintptr(0)
		for {
			p := unsafe.Add(buf, pos)
			var (
				attr, tag, nameLen, nextOff uint32
				size, write                 int64
			)
			if extd {
				x := (*winExtdDirInfo)(p)
				attr, tag, nameLen, nextOff = x.FileAttributes, x.ReparsePointTag, x.FileNameLength, x.NextEntryOffset
				size, write = x.EndOfFile, x.LastWriteTime
			} else {
				x := (*winFullDirInfo)(p)
				attr, tag, nameLen, nextOff = x.FileAttributes, x.EaSize, x.FileNameLength, x.NextEntryOffset
				size, write = x.EndOfFile, x.LastWriteTime
			}
			if pos+nameOff+uintptr(nameLen) > winDirBufSize {
				return winPathErr("fs.list", path, winErrInvalidParam)
			}
			name := unsafe.Slice((*uint16)(unsafe.Add(p, nameOff)), nameLen/2)
			winAddEntry(l, name, attr, tag, size, uint64(write))
			if nextOff == 0 {
				break
			}
			pos += uintptr(nextOff)
		}
	}
}

// winListFind lists with FindFirstFileExW / FindNextFileW.
func winListFind(path string, l *Listing) error {
	s := winGetScratch()
	defer winPutScratch(s)
	dir, e := s.winDirPath(path)
	if e != 0 {
		return winPathErr("fs.list", path, e)
	}
	pat := s.winPattern(dir)
	fd := &s.fd
	h, _, e := syscall.SyscallN(pFindFirstFileExW, uintptr(unsafe.Pointer(&pat[0])),
		winFindExInfoBasic, uintptr(unsafe.Pointer(fd)), winFindExSearchNameMatch, 0, winFindFirstExLargeFetch)
	if h == winInvalidHandle {
		if e == winErrFileNotFound {
			// "*" matches everything, so this only happens for the empty
			// root of a volume. A real directory is an empty listing.
			a, _, _ := syscall.SyscallN(pGetFileAttributesW, uintptr(unsafe.Pointer(&dir[0])))
			if uint32(a) != winInvalidFileAttributes && uint32(a)&winAttrDirectory != 0 {
				return nil
			}
		}
		return winPathErr("fs.list", path, e)
	}
	defer winFindClose(h)
	for {
		n := 0
		for n < len(fd.FileName) && fd.FileName[n] != 0 {
			n++
		}
		size := int64(fd.SizeHigh)<<32 | int64(fd.SizeLow)
		write := uint64(fd.Write[1])<<32 | uint64(fd.Write[0])
		winAddEntry(l, fd.FileName[:n], fd.Attr, fd.Reserved0, size, write)
		r, _, e := syscall.SyscallN(pFindNextFileW, h, uintptr(unsafe.Pointer(fd)))
		if uint32(r) == 0 {
			if e == winErrNoMoreFiles {
				return nil
			}
			return winPathErr("fs.list", path, e)
		}
	}
}
