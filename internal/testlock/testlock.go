// Package testlock serializes tests across processes: go test runs packages
// in parallel, and tests that need what the system gives one process at a
// time (the active app, the key window) hold a named lock meanwhile.
package testlock

import (
	"os"
	"path/filepath"
	"testing"
)

// Hold takes the lock name for the rest of t, waiting while another process
// (or test) holds it. The lock is a file in the temporary directory, locked
// with flock; it is released when t ends.
func Hold(t testing.TB, name string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "gnui-testlock-"+name), os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		t.Fatalf("testlock: %v", err)
	}
	if err := lock(f); err != nil {
		f.Close()
		t.Fatalf("testlock: %v", err)
	}
	t.Cleanup(func() { unlock(f); f.Close() })
}
