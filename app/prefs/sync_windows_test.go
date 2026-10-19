package prefs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// moveFile replaces an existing file (MOVEFILE_REPLACE_EXISTING) and reports a
// failure as an error naming both paths. That the move was written through to
// disk cannot be observed from a test; the flag is the OS's promise.
func TestMoveFileReplacesAndReportsFailure(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src ü"), filepath.Join(dir, "dst ü")
	os.WriteFile(dst, []byte("old"), 0o600)
	os.WriteFile(src, []byte("new"), 0o600)
	if err := moveFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "new" {
		t.Errorf("content %q", b)
	}
	if _, err := os.Stat(src); err == nil {
		t.Error("source still exists")
	}
	err := moveFile(src, dst)
	var le *os.LinkError
	if !errors.As(err, &le) || !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing source: %v", err)
	}
}
