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

// useRenameat2 is false in tests that exercise the fallback.
var useRenameat2 = true

// renameInto moves src/srcName to d/dstName, failing with EEXIST rather than
// replacing something there. renameat2 with RENAME_NOREPLACE does the check and
// the move in one step. Where the kernel or the file system lacks it (ENOSYS, or
// EINVAL from the file system), the move is a hard link followed by removing the
// old name: linkat never replaces anything and is atomic. A directory cannot be
// moved that way and is refused. There is no fallback to renameat, which
// replaces a file that appeared after any check.
func (d *tdir) renameInto(src *tdir, srcName, dstName string) error {
	from, err := syscall.BytePtrFromString(srcName)
	if err != nil {
		return err
	}
	to, err := syscall.BytePtrFromString(dstName)
	if err != nil {
		return err
	}
	if sysRenameat2 != 0 && useRenameat2 {
		_, _, e := syscall.Syscall6(sysRenameat2, uintptr(src.fd), uintptr(unsafe.Pointer(from)), uintptr(d.fd), uintptr(unsafe.Pointer(to)), renameNoReplace, 0)
		switch e {
		case 0:
			return nil
		case syscall.ENOSYS, syscall.EINVAL:
		default:
			return e
		}
	}
	if sysLinkat == 0 {
		return fmt.Errorf("%w: no way to move a file without risking to replace one on this architecture", errors.ErrUnsupported)
	}
	// No AT_SYMLINK_FOLLOW: a symbolic link is linked itself, not what it names.
	_, _, e := syscall.Syscall6(sysLinkat, uintptr(src.fd), uintptr(unsafe.Pointer(from)), uintptr(d.fd), uintptr(unsafe.Pointer(to)), 0, 0)
	switch e {
	case 0:
	case syscall.EPERM, syscall.EMLINK:
		// A directory, or a file system or a protection (fs.protected_hardlinks)
		// that does not allow a second name.
		return fmt.Errorf("%w: renameat2 is not available here and %s cannot be linked: %v", errors.ErrUnsupported, srcName, e)
	default:
		return e // EEXIST: the name is taken; EXDEV: another file system
	}
	if err := syscall.Unlinkat(src.fd, srcName); err != nil {
		syscall.Unlinkat(d.fd, dstName) // back out: the file keeps its old name only
		return err
	}
	return nil
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
