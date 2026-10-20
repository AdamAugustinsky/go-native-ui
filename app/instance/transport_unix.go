//go:build unix

package instance

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"gonativeui/app/paths"
)

// The Unix transport: a Unix-domain socket in the private directory.

// setup finds the application's directory, makes it, validates it and returns
// the key of the lock (the lock file) and the address of the socket. Everything
// after this uses the validated, resolved path it returns, never the path as
// given.
func setup(id string, o Options) (lockKey, addr string, err error) {
	dir := o.Dir
	if dir == "" {
		if dir, err = paths.Data(id); err != nil {
			return "", "", err
		}
	}
	if _, err := paths.Ensure(dir); err != nil {
		return "", "", err
	}
	canon, err := checkPrivate(dir)
	if err != nil {
		return "", "", err
	}
	addr, err = socketPath(canon)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(canon, "instance.lock"), addr, nil
}

type unixListener struct{ ln net.Listener }

func (l unixListener) Accept() (conn, error) { return l.ln.Accept() }
func (l unixListener) Close() error          { return l.ln.Close() }

// listen removes a stale socket (the caller holds the lock, so it is the leftover
// of a dead copy), listens, and gives the socket mode 0600. The directory is
// private, so no one else could reach the socket while it had the mode the umask
// gave it.
func listen(addr string) (listener, error) {
	os.Remove(addr)
	ln, err := net.Listen("unix", addr)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(addr, 0o600); err != nil {
		ln.Close()
		return nil, fmt.Errorf("setting the mode of %s: %w", addr, err)
	}
	return unixListener{ln}, nil
}

func dial(addr string, timeout time.Duration) (conn, error) {
	return net.DialTimeout("unix", addr, timeout)
}

// removeEndpoint removes the socket file (closing a Go listener already does,
// when it made the file; this covers the rest).
func removeEndpoint(addr string) { os.Remove(addr) }

// peerCheck refuses a connection whose peer is not this user's process, where
// the system can say; where it cannot, nil. Failing to find out is a refusal.
func peerCheck(c conn) error {
	nc, ok := c.(net.Conn)
	if !ok {
		return nil
	}
	uid, known, err := peerUID(nc)
	switch {
	case !known:
		return nil
	case err != nil:
		return fmt.Errorf("instance: cannot identify the peer: %w", err)
	case int(uid) != euid():
		return fmt.Errorf("instance: the peer belongs to user %d", uid)
	}
	return nil
}
