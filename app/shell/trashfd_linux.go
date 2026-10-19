//go:build linux

package shell

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const renameNoReplace = 1 // RENAME_NOREPLACE

// child opens the subdirectory name without following a symbolic link.
func (d *tdir) child(name string) (*tdir, error) {
	for {
		fd, err := syscall.Openat(d.fd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &tdir{fd: fd, path: filepath.Join(d.path, name)}, nil
	}
}

func (d *tdir) mkdir(name string, mode uint32) error { return syscall.Mkdirat(d.fd, name, mode) }

// createExcl makes the regular file name, which must not exist (a symbolic link
// of that name counts as existing).
func (d *tdir) createExcl(name string, mode uint32) (*os.File, error) {
	for {
		fd, err := syscall.Openat(d.fd, name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, mode)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return nil, err
		}
		return os.NewFile(uintptr(fd), filepath.Join(d.path, name)), nil
	}
}

func (d *tdir) remove(name string) error { return syscall.Unlinkat(d.fd, name) }

// renameInto moves src/srcName to d/dstName, failing with EEXIST rather than
// replacing something there. renameat2 does the check and the move in one step;
// where the kernel or the file system lacks it, a check and renameat do.
func (d *tdir) renameInto(src *tdir, srcName, dstName string) error {
	from, err := syscall.BytePtrFromString(srcName)
	if err != nil {
		return err
	}
	to, err := syscall.BytePtrFromString(dstName)
	if err != nil {
		return err
	}
	_, _, e := syscall.Syscall6(syscall.SYS_RENAMEAT2, uintptr(src.fd), uintptr(unsafe.Pointer(from)), uintptr(d.fd), uintptr(unsafe.Pointer(to)), renameNoReplace, 0)
	switch e {
	case 0:
		return nil
	case syscall.ENOSYS, syscall.EINVAL:
	default:
		return e
	}
	// A name that is a symbolic link, or a file we cannot open, still exists.
	fd, err := syscall.Openat(d.fd, dstName, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err == nil {
		syscall.Close(fd)
		return syscall.EEXIST
	}
	if err != syscall.ENOENT {
		return syscall.EEXIST
	}
	return syscall.Renameat(src.fd, srcName, d.fd, dstName)
}
