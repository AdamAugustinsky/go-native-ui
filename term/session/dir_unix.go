//go:build darwin || linux

package session

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// A daemon's directory holds its socket and state, and nothing else:
const (
	sockName = "sock" // the Unix socket, 0600
	pidName  = "pid"  // the daemon's process ID
	lockName = "lock" // flock'd by the running daemon
	logName  = "log"  // the launched daemon's stdout and stderr
)

// DefaultDir is the directory of app's daemon: under $XDG_RUNTIME_DIR, else
// the temporary directory (per-user on macOS), named for the app and the
// user so apps and users never share a daemon.
func DefaultDir(app string) string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, fmt.Sprintf("gnui-session-%s-%d", app, os.Getuid()))
}

// ensureDir makes dir if it is missing and checks that it is a real
// directory owned by this user, with no access for anyone else.
func ensureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !fi.IsDir() || !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("session: %s is not a directory of this user's", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}
