//go:build unix

package shell

import (
	"syscall"
)

// A tdir is an open directory. The trash code works relative to descriptors it
// has opened and checked, never relative to a path it checked a moment ago, so
// that a directory swapped for a symbolic link in between is not followed. On
// Linux the operations are openat, mkdirat, unlinkat and renameat2
// (trashfd_linux.go). Elsewhere (macOS, where the XDG trash is not used but the
// tests run) they are emulated with paths (trashfd_other.go).
type tdir struct {
	fd   int
	path string // for messages, and for the emulation
	// follow is set when the last component of path was allowed to be a link.
	follow bool
}

// openDir opens the directory at an absolute path. With follow unset the last
// component must not be a symbolic link.
func openDir(path string, follow bool) (*tdir, error) {
	flags := syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_CLOEXEC
	if !follow {
		flags |= syscall.O_NOFOLLOW
	}
	for {
		fd, err := syscall.Open(path, flags, 0)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &tdir{fd: fd, path: path, follow: follow}, nil
	}
}

func (d *tdir) close() {
	if d != nil && d.fd >= 0 {
		syscall.Close(d.fd)
		d.fd = -1
	}
}

// stat is fstat on the descriptor: what the directory is, not what its name now
// points to.
func (d *tdir) stat() (st syscall.Stat_t, err error) {
	err = syscall.Fstat(d.fd, &st)
	return
}

func (d *tdir) chmod(mode uint32) error { return syscall.Fchmod(d.fd, mode) }
