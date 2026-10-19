//go:build unix

package instance

import (
	"os"
	"path/filepath"
	"testing"
)

// Only the user can reach the socket, the lock and their directory.
func TestPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	os.MkdirAll(dir, 0o755) // an existing, too open directory is tightened
	acquire(t, dir, Options{})
	sock, _ := socketPath(dir)
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "instance.lock"): 0o600, sock: 0o600} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s: mode %o, want %o", path, got, want)
		}
	}
}
