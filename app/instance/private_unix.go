//go:build unix

package instance

import (
	"fmt"
	"path/filepath"
	"syscall"
)

// checkPrivate verifies that dir is a place only this user controls, so that no
// one else can reach the lock file or the socket in it, or replace them, and
// returns the resolved path, which is the only one to use afterwards:
//
//   - dir itself is a real directory (not a link), owned by this user, with mode
//     0700; a directory with a wider mode is tightened, and failing to is an error;
//   - every ancestor of the resolved path, up to the root, is a directory that
//     belongs to this user or to root and that group and others cannot write
//     (ancestorOK), so no component of the path can be renamed or replaced.
//
// Symbolic links are resolved once, here; the checks are made on the resolved
// path, and a link that is retargeted later no longer matters because nothing
// reads the original path again. The directory is opened without following links
// and checked on its descriptor, and the descriptor is compared with the path at
// the end.
func checkPrivate(dir string) (string, error) {
	var ls syscall.Stat_t
	if err := syscall.Lstat(dir, &ls); err == nil && uint32(ls.Mode)&syscall.S_IFMT == syscall.S_IFLNK {
		return "", fmt.Errorf("%w: %s is a symbolic link", ErrUntrusted, dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("instance: %s: %w", dir, err)
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return "", err
	}
	var fd int
	for {
		fd, err = syscall.Open(real, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", ErrUntrusted, dir, err)
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return "", fmt.Errorf("instance: %s: %w", dir, err)
	}
	if int(st.Uid) != euid() {
		return "", fmt.Errorf("%w: %s belongs to another user", ErrUntrusted, dir)
	}
	if uint32(st.Mode)&0o777 != 0o700 {
		if err := syscall.Fchmod(fd, 0o700); err != nil {
			return "", fmt.Errorf("%w: %s has mode %04o and cannot be tightened: %v", ErrUntrusted, dir, uint32(st.Mode)&0o777, err)
		}
	}
	for p := filepath.Dir(real); ; p = filepath.Dir(p) {
		var ps syscall.Stat_t
		if err := syscall.Lstat(p, &ps); err != nil {
			return "", fmt.Errorf("instance: %s: %w", p, err)
		}
		if err := ancestorOK(&ps); err != nil {
			return "", fmt.Errorf("%w: %s: %v", ErrUntrusted, p, err)
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	// What we checked is still what the path names.
	var after syscall.Stat_t
	if err := syscall.Lstat(real, &after); err != nil || after.Dev != st.Dev || after.Ino != st.Ino {
		return "", fmt.Errorf("%w: %s changed while it was checked", ErrUntrusted, dir)
	}
	return real, nil
}

// ancestorOK says whether a directory above the instance directory can be
// trusted not to be swapped: a real directory, owned by this user or by root, that
// group and others cannot write. The sticky bit makes a writable directory safe
// for what is already in it (others cannot rename or remove what is not theirs),
// as in /tmp. There is no exemption for root's own directories, and none for a
// group: any member of a group that can write a directory can replace the
// directories in it, and on macOS every user is in "staff".
func ancestorOK(ps *syscall.Stat_t) error {
	mode := uint32(ps.Mode)
	switch {
	case mode&syscall.S_IFMT != syscall.S_IFDIR:
		return fmt.Errorf("not a directory")
	case ps.Uid != 0 && int(ps.Uid) != euid():
		return fmt.Errorf("belongs to another user")
	case mode&0o022 != 0 && mode&syscall.S_ISVTX == 0:
		return fmt.Errorf("mode %04o can be written by a group or by anyone", mode&0o7777)
	}
	return nil
}
