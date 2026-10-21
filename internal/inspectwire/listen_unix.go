//go:build !windows

package inspectwire

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Listen creates the socket at path. The path must be absolute, its directory
// must be safe (CheckDir) and a file already at path is replaced only if it is
// a stale socket of the user's (clearStale).
//
// The socket is never visible to others while it is made: it is bound inside a
// fresh 0700 directory next to its final place, stripped of any ACL, set to
// mode 0600, and only then renamed (atomically, in one file system) to path.
// Mode bits alone do not make a socket private: an ACL it inherits from a
// directory can grant another user access whatever its mode says.
func Listen(path string) (*Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("inspect: socket path %q is not absolute", path)
	}
	path = filepath.Clean(path)
	dir, err := CheckDir(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	final := filepath.Join(dir, filepath.Base(path))
	if err := clearStale(final); err != nil {
		return nil, err
	}
	return publish(dir, final)
}

// publish binds a socket in a private directory inside dir and renames it to
// final.
func publish(dir, final string) (l *Listener, err error) {
	stage, err := os.MkdirTemp(dir, ".gi")
	if err != nil {
		return nil, fmt.Errorf("inspect: staging directory in %s: %w", dir, err)
	}
	sock := filepath.Join(stage, "s")
	var ln net.Listener
	defer func() {
		if err != nil {
			if ln != nil {
				ln.Close()
			}
			os.Remove(sock)
		}
		os.Remove(stage)
	}()
	if err = os.Chmod(stage, 0o700); err != nil { // not the umask's choice
		return nil, fmt.Errorf("inspect: %w", err)
	}
	if err = stripACL(stage); err != nil {
		return nil, fmt.Errorf("inspect: staging directory: %w", err)
	}
	if err = noACL(stage); err != nil {
		return nil, err
	}
	ul, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("inspect: listen on %s: %w (a socket path is limited to about 100 bytes)", final, err)
	}
	ln = ul
	ul.SetUnlinkOnClose(false) // Close removes the path only if it is still this socket
	if err = os.Chmod(sock, 0o600); err != nil {
		return nil, fmt.Errorf("inspect: %w", err)
	}
	fi, err := os.Lstat(sock)
	if err != nil {
		return nil, fmt.Errorf("inspect: %w", err)
	}
	if fi.Mode()&os.ModeSocket == 0 || !ownedByUser(fi) || fi.Mode().Perm() != 0o600 {
		return nil, fmt.Errorf("inspect: %s is not a socket of yours with mode 0600 (mode %v)", sock, fi.Mode())
	}
	if err = noACL(sock); err != nil {
		return nil, err
	}
	if _, e := os.Lstat(final); e == nil {
		return nil, fmt.Errorf("inspect: %s appeared while the socket was being made; refusing to replace it", final)
	}
	if err = os.Rename(sock, final); err != nil {
		return nil, fmt.Errorf("inspect: publish the socket: %w", err)
	}
	id, err := os.Lstat(final)
	if err != nil || !os.SameFile(id, fi) {
		return nil, fmt.Errorf("inspect: %s is not the socket that was published", final)
	}
	return &Listener{ln: ul, path: final, id: id}, nil
}

// noACL fails if path has an extended ACL.
func noACL(path string) error {
	has, err := hasACL(path)
	if err != nil {
		return fmt.Errorf("inspect: reading the ACL of %s: %w", path, err)
	}
	if has {
		return fmt.Errorf("inspect: %s has an access control list that could grant others access; refusing", path)
	}
	return nil
}

// dialStale is how clearStale asks whether a socket is alive (a variable for
// the tests).
var dialStale = func(path string) (net.Conn, error) { return net.DialTimeout("unix", path, 250*time.Millisecond) }

// clearStale removes what is at path if it is a stale socket of the user's.
// Only a socket the user owns is looked at, and only a refused connection
// (ECONNREFUSED: nobody listens) proves it stale; an answer, a timeout, a
// permission error or any other failure leaves it alone. The file is checked
// to be the same one again before it is removed.
func clearStale(path string) error {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect: %w", err)
	case fi.Mode()&os.ModeSocket == 0:
		return fmt.Errorf("inspect: %s exists and is not a socket; refusing to replace it", path)
	case !ownedByUser(fi):
		return fmt.Errorf("inspect: %s is a socket of another user; refusing to replace it", path)
	}
	c, err := dialStale(path)
	if err == nil {
		c.Close()
		return fmt.Errorf("inspect: %s is in use by a running process", path)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("inspect: cannot tell whether the socket %s is stale (%v); refusing to replace it", path, err)
	}
	// (a socket, and the same one: an inode number can be reused by the file that replaced it)
	if again, err := os.Lstat(path); err != nil || again.Mode()&os.ModeSocket == 0 || !os.SameFile(fi, again) {
		return fmt.Errorf("inspect: %s changed while it was being checked; refusing to replace it", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("inspect: remove the stale socket: %w", err)
	}
	return nil
}

// CheckDir reports whether dir may hold the socket and returns it with symbolic
// links resolved (the path to bind in; they are resolved once, here, and the
// chain checked is the resolved one):
//
//   - dir itself is a directory owned by the user, writable by neither the group
//     nor others, with no ACL and no default POSIX ACL;
//   - every ancestor up to the root is owned by the user or by root, and is not
//     writable by the group or others unless it has the sticky bit (OpenSSH's
//     StrictModes): otherwise whoever can write there could rename dir away and
//     put a directory of their own in its place.
func CheckDir(dir string) (string, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("inspect: socket directory: %w", err)
	}
	fi, err := os.Lstat(real)
	if err != nil {
		return "", fmt.Errorf("inspect: socket directory: %w", err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("inspect: %s is not a directory", real)
	}
	if !ownedByUser(fi) {
		return "", fmt.Errorf("inspect: %s is not owned by you; refusing to put the socket there", real)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return "", fmt.Errorf("inspect: %s is writable by its group or by others (mode %04o); use a directory with mode 0700 or 0755", real, fi.Mode().Perm())
	}
	if has, err := dirHasACL(real); err != nil {
		return "", fmt.Errorf("inspect: reading the ACL of %s: %w", real, err)
	} else if has {
		return "", fmt.Errorf("inspect: %s has an access control list (an ACL it grants, or hands to what is created in it, can give others access to the socket); use a directory without one", real)
	}
	for a := filepath.Dir(real); ; a = filepath.Dir(a) {
		afi, err := os.Lstat(a)
		if err != nil {
			return "", fmt.Errorf("inspect: %w", err)
		}
		if st, ok := afi.Sys().(*syscall.Stat_t); !ok || (int(st.Uid) != os.Geteuid() && st.Uid != 0) {
			return "", fmt.Errorf("inspect: %s, above the socket directory, is owned by another user; they could replace %s", a, real)
		}
		if afi.Mode().Perm()&0o022 != 0 && afi.Mode()&os.ModeSticky == 0 {
			return "", fmt.Errorf("inspect: %s, above the socket directory, is writable by its group or by others without the sticky bit (mode %04o); they could replace %s", a, afi.Mode().Perm(), real)
		}
		if a == filepath.Dir(a) {
			break
		}
	}
	return real, nil
}

// ownedByUser reports whether the file belongs to the user running the process.
func ownedByUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
