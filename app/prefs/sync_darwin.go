package prefs

import (
	"os"
	"syscall"
)

const fFullFsync = 51 // F_FULLFSYNC (sys/fcntl.h)

// syncFile flushes f. fsync on macOS only hands the data to the drive; with
// full it asks the drive to write it to the platter or flash (F_FULLFSYNC). It
// falls back to plain fsync only where the file system says it does not support
// that request (some network volumes); a failure of the flush itself is an
// error.
func syncFile(f *os.File, full bool) error {
	if full {
		var errno syscall.Errno
		if err := rawControl(f, func(fd uintptr) {
			_, _, errno = syscall.Syscall(syscall.SYS_FCNTL, fd, fFullFsync, 0)
		}); err != nil {
			return err
		}
		if errno == 0 {
			return nil
		}
		if !fsyncUnsupported(errno) {
			return errno
		}
	}
	return f.Sync()
}

func rawControl(f *os.File, fn func(fd uintptr)) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	return rc.Control(fn)
}
