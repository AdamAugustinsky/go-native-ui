//go:build linux

package fs

import (
	"os"
	"syscall"
	"unsafe"
)

// dirMTime is the modification time of the directory (or of what a symlink
// points to) in nanoseconds since the epoch, and whether it could be read. It
// is called on every relist of a watched directory, so it is one statx on a
// pooled buffer and allocates nothing (os.Stat allocates the FileInfo and the
// path). Kernels without statx take os.Stat.
func dirMTime(path string) (int64, bool) {
	if len(path) >= lxPathMax || lxHasNUL(path) {
		return 0, false
	}
	s := lxGetScratch()
	defer lxPutScratch(s)
	s.path[copy(s.path[:], path)] = 0
	var e syscall.Errno
	for {
		_, _, e = syscall.Syscall6(sysStatx, lxAtFdcwd, uintptr(unsafe.Pointer(&s.path[0])),
			lxAtStatxDontSync, lxStatxMtime, uintptr(unsafe.Pointer(&s.stx)), 0)
		if e != syscall.EINTR {
			break
		}
	}
	switch e {
	case 0:
		return s.stx.Mtime.Sec*1e9 + int64(s.stx.Mtime.Nsec), true
	case syscall.ENOSYS, syscall.EPERM: // no statx (old kernel, seccomp filter)
		fi, err := os.Stat(path)
		if err != nil {
			return 0, false
		}
		return fi.ModTime().UnixNano(), true
	}
	return 0, false
}

func lxHasNUL(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0 {
			return true
		}
	}
	return false
}
