//go:build unix && !darwin

package instance

import (
	"fmt"
	"net"
	"os"
	"time"
)

// The Unix transport: a Unix-domain socket in the private directory.

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
