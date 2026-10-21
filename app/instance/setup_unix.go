//go:build unix

package instance

import (
	"fmt"
	"os"
	"path/filepath"

	"gonativeui/app/paths"
)

// What every Unix transport shares: where the application's files go, and who
// may connect. The socket calls themselves are in transport_unix.go (net) and
// transport_darwin.go (raw system calls, so that a macOS binary links no net).

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

// removeEndpoint removes the socket file (closing a Go listener already does,
// when it made the file; this covers the rest).
func removeEndpoint(addr string) { os.Remove(addr) }

// peerCheck refuses a connection whose peer is not this user's process, where
// the system can say; where it cannot, nil. Failing to find out is a refusal.
func peerCheck(c conn) error {
	uid, known, err := peerUID(c)
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
