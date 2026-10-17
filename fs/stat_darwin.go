//go:build darwin

package fs

import (
	"syscall"
	"unsafe"
)

// dirMTime is the modification time of the directory (or of what a symlink
// points to) in nanoseconds since the epoch, and whether it could be read. It
// is called on every relist of a watched directory, so it is one stat64 on a
// pooled buffer and allocates nothing (os.Stat allocates the FileInfo and the
// path).
func dirMTime(path string) (int64, bool) {
	s := getScratch()
	defer putScratch(s)
	s.path = append(append(s.path[:0], path...), 0)
	if len(path) != len(s.path)-1 || containsNUL(path) {
		return 0, false
	}
	for {
		_, _, errno := syscall.Syscall(syscall.SYS_STAT64, uintptr(unsafe.Pointer(&s.path[0])),
			uintptr(unsafe.Pointer(&s.stat[0])), 0)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return 0, false
		}
		break
	}
	return statMTime(s.stat[:]), true
}

// statMTime reads st_mtimespec out of a struct stat64 (the 64-bit inode
// layout, <sys/stat.h>: st_dev i32 @0, st_mode u16 @4, st_nlink u16 @6,
// st_ino u64 @8, st_uid @16, st_gid @20, st_rdev @24, st_atimespec @32,
// st_mtimespec @48).
func statMTime(b []byte) int64 {
	return int64(le64(b[48:]))*1e9 + int64(le64(b[56:]))
}
