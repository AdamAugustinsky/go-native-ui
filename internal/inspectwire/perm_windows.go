package inspectwire

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// CheckDir reports whether dir may hold the socket. Windows files have no
// owner or mode to read here; the directory's access list is what keeps others
// out, and the user's profile directory is the one place whose list is the
// user's own, so the directory must be inside it (the temporary and cache
// directories are). It returns the directory with links resolved.
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
	home, err := os.UserHomeDir()
	if err == nil {
		home, err = filepath.EvalSymlinks(home)
	}
	if err != nil {
		return "", fmt.Errorf("inspect: the user's profile directory: %w", err)
	}
	if rel, err := filepath.Rel(home, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("inspect: %s is outside your profile directory %s; put the socket inside it", real, home)
	}
	return real, nil
}

// ownedByUser is true on Windows, which has no owner to compare (CheckDir
// stands in for it).
func ownedByUser(os.FileInfo) bool { return true }

// listenUnix binds the socket.
func listenUnix(path string) (net.Listener, error) { return net.Listen("unix", path) }

// checkSocket has nothing to verify on Windows.
func checkSocket(string) error { return nil }
