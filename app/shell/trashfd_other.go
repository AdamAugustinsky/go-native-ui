//go:build unix && !linux

package shell

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// The path-based stand-in for the descriptor-relative calls, for systems
// without openat and friends in package syscall. The XDG trash is only used on
// Linux; this keeps the logic testable on a developer's Mac. Each call first
// checks that the descriptor still names the directory at the path, which
// narrows the window a swapped directory has but does not close it, as the real
// calls do.

func (d *tdir) same() error {
	var a, b syscall.Stat_t
	if err := syscall.Fstat(d.fd, &a); err != nil {
		return err
	}
	stat := syscall.Lstat
	if d.follow {
		stat = syscall.Stat
	}
	if err := stat(d.path, &b); err != nil {
		return err
	}
	if a.Dev != b.Dev || a.Ino != b.Ino {
		return errors.New("directory replaced")
	}
	return nil
}

func (d *tdir) child(name string) (*tdir, error) {
	if err := d.same(); err != nil {
		return nil, err
	}
	return openDir(filepath.Join(d.path, name), false)
}

func (d *tdir) mkdir(name string, mode uint32) error {
	if err := d.same(); err != nil {
		return err
	}
	return syscall.Mkdir(filepath.Join(d.path, name), mode)
}

func (d *tdir) createExcl(name string, mode uint32) (*os.File, error) {
	if err := d.same(); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(d.path, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, os.FileMode(mode))
}

func (d *tdir) remove(name string) error {
	if err := d.same(); err != nil {
		return err
	}
	return syscall.Unlink(filepath.Join(d.path, name))
}

func (d *tdir) renameInto(src *tdir, srcName, dstName string) error {
	if err := d.same(); err != nil {
		return err
	}
	if err := src.same(); err != nil {
		return err
	}
	to := filepath.Join(d.path, dstName)
	var st syscall.Stat_t
	if err := syscall.Lstat(to, &st); err == nil {
		return syscall.EEXIST
	}
	return os.Rename(filepath.Join(src.path, srcName), to)
}

// statat is lstat of the entry name of d.
func (d *tdir) statat(name string) (st syscall.Stat_t, err error) {
	if err = d.same(); err != nil {
		return
	}
	err = syscall.Lstat(filepath.Join(d.path, name), &st)
	return
}
