//go:build !windows

package inspectwire

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// CheckDir reports whether dir may hold the socket: it exists, is a
// directory, is owned by the current user and is writable by neither the
// group nor others. It returns the directory with symbolic links resolved,
// the path to bind in.
func CheckDir(dir string) (string, error) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("inspect: socket directory: %w", err)
	}
	fi, err := os.Stat(real)
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
	return real, nil
}

// ownedByUser reports whether the file belongs to the user running the process.
func ownedByUser(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}

// listenUnix binds the socket with a umask that leaves it for its owner alone,
// so that it is never reachable by anyone else, not even before the chmod.
func listenUnix(path string) (net.Listener, error) {
	old := syscall.Umask(0o177)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err == nil {
		err = os.Chmod(path, 0o600)
		if err != nil {
			ln.Close()
		}
	}
	return ln, err
}

// checkSocket verifies what the bind made: a socket, the user's, mode 0600.
func checkSocket(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	if fi.Mode()&os.ModeSocket == 0 || !ownedByUser(fi) || fi.Mode().Perm() != 0o600 {
		return fmt.Errorf("inspect: %s is not a socket of yours with mode 0600 (mode %v)", path, fi.Mode())
	}
	return nil
}
