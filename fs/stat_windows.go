//go:build windows

package fs

import "syscall"

import "unsafe"

// dirMTime is the modification time of the directory in nanoseconds since the
// epoch, and whether it could be read. It is called on every relist of a
// watched directory, so it is one GetFileAttributesExW on a pooled buffer and
// allocates nothing (os.Stat allocates the FileInfo and the path).
func dirMTime(path string) (int64, bool) {
	s := winGetScratch()
	defer winPutScratch(s)
	p, e := s.winDirPath(path)
	if e != 0 {
		return 0, false
	}
	// WIN32_FILE_ATTRIBUTE_DATA starts like WIN32_FIND_DATAW: attributes, the
	// three FILETIMEs, the size; s.fd has room for it.
	const getFileExInfoStandard = 0
	r, _, _ := syscall.SyscallN(pGetFileAttributesExW, uintptr(unsafe.Pointer(&p[0])),
		getFileExInfoStandard, uintptr(unsafe.Pointer(&s.fd)))
	if uint32(r) == 0 { // a BOOL: only the low 32 bits of the register
		return 0, false
	}
	return winUnixNanos(uint64(s.fd.Write[1])<<32 | uint64(s.fd.Write[0])), true
}
