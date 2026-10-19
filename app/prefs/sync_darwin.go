package prefs

import (
	"os"
	"syscall"
)

const fFullFsync = 51 // F_FULLFSYNC (sys/fcntl.h)

// syncFile flushes f. fsync on macOS only hands the data to the drive; with
// full it asks the drive to write it to the platter or flash (F_FULLFSYNC), and
// falls back to fsync where the file system does not support that (network
// volumes).
func syncFile(f *os.File, full bool) error {
	if full {
		var errno syscall.Errno
		err := rawControl(f, func(fd uintptr) {
			_, _, errno = syscall.Syscall(syscall.SYS_FCNTL, fd, fFullFsync, 0)
		})
		if err == nil && errno == 0 {
			return nil
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
