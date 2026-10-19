//go:build linux

package shell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

const (
	renameNoReplace = 1        // RENAME_NOREPLACE
	oPath           = 0x200000 // O_PATH, which package syscall does not define for every architecture
)

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

// renameat2Errno, when not 0, is returned in place of the call to renameat2:
// tests use it to make the call fail with ENOSYS or EINVAL.
var renameat2Errno syscall.Errno

// renameInto moves src/srcName to d/dstName, failing with EEXIST rather than
// replacing something there. renameat2 with RENAME_NOREPLACE does the check and
// the move in one step, and it is the only way to do it: there is no fallback.
// A check followed by renameat would replace a file that appeared in between,
// and a hard link followed by unlink would delete a file that replaced the
// source in between (an editor saving). Where renameat2 is missing (ENOSYS) or
// the file system does not support the flag (EINVAL), the move is refused with
// ErrUnsupported and nothing is changed. EXDEV and the other errors are returned
// as they are.
func (d *tdir) renameInto(src *tdir, srcName, dstName string) error {
	from, err := syscall.BytePtrFromString(srcName)
	if err != nil {
		return err
	}
	to, err := syscall.BytePtrFromString(dstName)
	if err != nil {
		return err
	}
	e := renameat2Errno
	switch {
	case e != 0:
	case sysRenameat2 == 0:
		e = syscall.ENOSYS
	default:
		_, _, e = syscall.Syscall6(sysRenameat2, uintptr(src.fd), uintptr(unsafe.Pointer(from)), uintptr(d.fd), uintptr(unsafe.Pointer(to)), renameNoReplace, 0)
	}
	switch e {
	case 0:
		return nil
	case syscall.ENOSYS, syscall.EINVAL:
		return fmt.Errorf("%w: the trash cannot move this file safely on this file system (renameat2 with RENAME_NOREPLACE is not available: %v)", errors.ErrUnsupported, e)
	}
	return e
}

// statat is fstatat(AT_SYMLINK_NOFOLLOW): what the entry name of d is, not what it
// points to. It is the fstat of a descriptor opened with O_PATH, which does not
// open the file itself.
func (d *tdir) statat(name string) (st syscall.Stat_t, err error) {
	for {
		var fd int
		fd, err = syscall.Openat(d.fd, name, oPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return st, err
		}
		defer syscall.Close(fd)
		err = syscall.Fstat(fd, &st)
		return st, err
	}
}
