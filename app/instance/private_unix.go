//go:build unix

package instance

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// checkPrivate verifies that dir is a place only this user controls, so that no
// one else can reach the lock file or the socket in it, or replace them:
//
//   - dir itself is a real directory (not a link), owned by this user, with mode
//     0700; a directory with a wider mode is tightened, and failing to is an error;
//   - none of its parents can be used to rename or replace it: a parent
//     writable by other users (or by a group that is not this user's own) is
//     refused unless it has the sticky bit, which stops others deleting what is
//     not theirs (/tmp), and a parent must belong to this user or to root.
//
// Symbolic links in the path are resolved first, and the checks are made on
// what they lead to. All of it is done on a descriptor for dir.
func checkPrivate(dir string) error {
	if fi, err := os.Lstat(dir); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symbolic link", ErrUntrusted, dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("instance: %s: %w", dir, err)
	}
	var fd int
	for {
		fd, err = syscall.Open(real, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrUntrusted, dir, err)
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return fmt.Errorf("instance: %s: %w", dir, err)
	}
	if int(st.Uid) != euid() {
		return fmt.Errorf("%w: %s belongs to another user", ErrUntrusted, dir)
	}
	if uint32(st.Mode)&0o777 != 0o700 {
		if err := syscall.Fchmod(fd, 0o700); err != nil {
			return fmt.Errorf("%w: %s has mode %04o and cannot be tightened: %v", ErrUntrusted, dir, uint32(st.Mode)&0o777, err)
		}
	}
	egid := uint32(os.Getegid())
	for p := filepath.Dir(real); ; p = filepath.Dir(p) {
		var ps syscall.Stat_t
		if err := syscall.Lstat(p, &ps); err != nil {
			return fmt.Errorf("instance: %s: %w", p, err)
		}
		mode := uint32(ps.Mode)
		sticky := mode&syscall.S_ISVTX != 0
		switch {
		case mode&syscall.S_IFMT != syscall.S_IFDIR:
			return fmt.Errorf("%w: %s is not a directory", ErrUntrusted, p)
		case ps.Uid != 0 && int(ps.Uid) != euid():
			return fmt.Errorf("%w: %s belongs to another user", ErrUntrusted, p)
		case mode&0o002 != 0 && !sticky:
			return fmt.Errorf("%w: %s can be written by anyone", ErrUntrusted, p)
		case mode&0o020 != 0 && !sticky && ps.Gid != egid && ps.Uid != 0:
			return fmt.Errorf("%w: %s can be written by a group", ErrUntrusted, p)
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}
