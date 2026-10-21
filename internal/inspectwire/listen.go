package inspectwire

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Listener is the bridge's socket: a Unix domain socket that only its owner
// can connect to.
type Listener struct {
	ln   *net.UnixListener
	path string
}

// Listen creates the socket at path. The path must be absolute and its
// directory must be safe (owned by the user, not writable by the group or
// others; see CheckDir). A file already at path is replaced only if it is a
// socket owned by the user that no process answers on. The socket has mode
// 0600 from the moment it exists.
func Listen(path string) (*Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("inspect: socket path %q is not absolute", path)
	}
	path = filepath.Clean(path)
	dir, err := CheckDir(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	path = filepath.Join(dir, filepath.Base(path))
	if err := clearStale(path); err != nil {
		return nil, err
	}
	ln, err := listenUnix(path)
	if err != nil {
		return nil, fmt.Errorf("inspect: listen on %s: %w (a socket path is limited to about 100 bytes)", path, err)
	}
	if err := checkSocket(path); err != nil {
		ln.Close()
		os.Remove(path)
		return nil, err
	}
	return &Listener{ln: ln.(*net.UnixListener), path: path}, nil
}

// Path is the socket's path, with symbolic links in its directory resolved.
func (l *Listener) Path() string { return l.path }

// Close stops accepting and removes the socket file.
func (l *Listener) Close() error {
	err := l.ln.Close() // removes the file it created
	if fi, e := os.Lstat(l.path); e == nil && fi.Mode()&os.ModeSocket != 0 && ownedByUser(fi) {
		os.Remove(l.path)
	}
	return err
}

// clearStale removes what is at path if it is a stale socket of the user's:
// not a socket, or another user's, or one that answers, is an error.
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
	if c, err := net.DialTimeout("unix", path, 250*time.Millisecond); err == nil {
		c.Close()
		return fmt.Errorf("inspect: %s is in use by a running process", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("inspect: remove the stale socket: %w", err)
	}
	return nil
}
