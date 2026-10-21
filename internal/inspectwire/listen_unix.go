//go:build !windows

package inspectwire

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Listen creates the socket at path. The path must be absolute and its
// directory must be safe (CheckDir).
//
// A lock file, <path>.lock in that directory, is the proof that a bridge is
// alive: Listen takes an exclusive flock on it, which the system releases when
// the process dies, and holds it until Close. If another bridge holds it,
// Listen refuses and does not touch the socket. A free lock proves the previous
// owner is dead, not that a socket at the path was its: a socket is removed only
// if the lock file existed before (a bridge used the path), and nothing answers
// when it is dialed once; a socket found when Listen had to create the lock file
// is another program's and is refused. An existing lock file must be private
// (mode, ACL). Asking the socket alone would not do: on macOS a live listener
// whose accept queue is full refuses connections like a dead one.
//
// The socket is not visible to others while it is made: it is bound inside a
// fresh 0700 directory next to its final place, stripped of any ACL, set to
// mode 0600, and only then renamed (atomically, in one file system) to path.
// Mode bits alone do not make a socket private: an ACL it inherits from a
// directory can grant another user access whatever its mode says.
//
// Threat model: other local users, and what a client sends. The directory is
// assumed to be changed by nobody but the user: a race against the user's own
// processes, which could delete or replace the socket anyway, is not defended.
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
	lock, created, err := lockFile(final + ".lock")
	if err != nil {
		return nil, err
	}
	if err := removeStale(final, created); err != nil {
		if created {
			os.Remove(final + ".lock") // it proved nothing: the next start must not take it for a bridge's
		}
		lock.Close()
		return nil, err
	}
	l, err := publish(dir, final)
	if err != nil {
		lock.Close()
		return nil, err
	}
	l.lock = lock
	return l, nil
}

// lockFile opens the lock file and flocks it. It is created here, with O_EXCL,
// if it is not there (created is true then): a lock file that was there before
// is the sign that a bridge has used this path. One that was there must be
// private to the user (no group or other permission bits, no ACL): the mode of
// a file is only applied when it is made, and a file others can open for
// reading is one they can flock, which would keep the bridge from ever
// starting. Nothing follows a link. EWOULDBLOCK means another bridge holds it.
func lockFile(path string) (f *os.File, created bool, err error) {
	const flags = syscall.O_RDWR | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	fd, err := syscall.Open(path, flags|syscall.O_CREAT|syscall.O_EXCL, 0o600)
	created = err == nil
	if errors.Is(err, syscall.EEXIST) {
		fd, err = syscall.Open(path, flags, 0)
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect: lock file %s: %w", path, err)
	}
	f = os.NewFile(uintptr(fd), path)
	fail := func(format string, a ...any) (*os.File, bool, error) {
		f.Close()
		if created {
			os.Remove(path)
		}
		return nil, false, fmt.Errorf(format, a...)
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || !ownedByUser(fi) {
		return fail("inspect: %s is not a regular file of yours; remove %s, or make it 0600", path, path)
	}
	if !created {
		if fi.Mode().Perm()&0o077 != 0 {
			return fail("inspect: the lock file %s has mode %04o, so others could hold its lock; remove %s, or make it 0600", path, fi.Mode().Perm(), path)
		}
		if has, err := hasACL(path); err != nil || has {
			return fail("inspect: the lock file %s has an access control list (or it could not be read: %v), so others could hold its lock; remove %s, or make it 0600 without one", path, err, path)
		}
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, fmt.Errorf("inspect: the socket %s is in use by a running process (it holds %s)", strings.TrimSuffix(path, ".lock"), path)
		}
		return nil, false, fmt.Errorf("inspect: lock %s: %w", path, err)
	}
	return f, created, nil
}

// dialStale is how removeStale asks whether anything answers on a socket (a
// variable for the tests).
var dialStale = func(path string) (net.Conn, error) { return net.DialTimeout("unix", path, 250*time.Millisecond) }

// removeStale removes what is at path, which no live bridge owns (the caller
// holds the lock). That the lock is free does not make the socket a bridge's: a
// socket of another program, put there by a wrong path, has no lock file. So a
// socket is removed only if the lock file was there before (created is false: a
// bridge used this path and died, and the system released its lock) and nothing
// answers on the socket (a refused connection or no file, not a timeout or any
// other error). A file that is not a socket of the user's is never removed.
func removeStale(path string, created bool) error {
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
	case created:
		return fmt.Errorf("inspect: a socket that is not a gonativeui bridge's is at %s (no bridge has used this path before); refusing to replace it", path)
	}
	c, err := dialStale(path)
	if err == nil {
		c.Close()
		return fmt.Errorf("inspect: the socket %s is in use by another program; refusing to replace it", path)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, syscall.ENOENT) {
		return fmt.Errorf("inspect: cannot tell whether the socket %s is in use (%v); refusing to replace it", path, err)
	}
	if again, err := os.Lstat(path); err != nil || again.Mode()&os.ModeSocket == 0 || !os.SameFile(fi, again) {
		return fmt.Errorf("inspect: %s changed while it was being checked; refusing to replace it", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("inspect: remove the stale socket: %w", err)
	}
	return nil
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

// CheckDir reports whether dir may hold the socket and returns it with symbolic
// links resolved (the path to bind in; they are resolved once, here, and the
// chain checked is the resolved one):
//
//   - dir itself is a directory owned by the user, writable by neither the group
//     nor others, with no ACL and no default POSIX ACL;
//   - every ancestor up to the root is owned by the user or by root, and is not
//     writable by the group or others unless it has the sticky bit (OpenSSH's
//     StrictModes): otherwise whoever can write there could rename dir away and
//     put a directory of their own in its place. An ACL on an ancestor (or on
//     dir) with an allow entry that grants anyone a right to change what is in
//     it or who owns it (write, add_file, append, add_subdirectory, delete,
//     delete_child, writesecurity, chown, writeattr, writeextattr) does the
//     same, and is refused; ACLs that only deny, or allow reading, are not.
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
	for a := real; ; a = filepath.Dir(a) {
		if bad, err := aclGrantsMutation(a); err != nil {
			return "", fmt.Errorf("inspect: reading the ACL of %s: %w", a, err)
		} else if bad {
			return "", fmt.Errorf("inspect: %s, on the path to the socket, has an ACL that grants someone the right to change or replace what is in it; use a directory without one", a)
		}
		if a != real {
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
