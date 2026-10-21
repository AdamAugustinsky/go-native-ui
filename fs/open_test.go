package fs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A Go test binary opens nothing: Open fails with ErrTestBinary for a path
// that exists, and as before for one that does not.
func TestOpenLaunchesNothingInATestBinary(t *testing.T) {
	t.Setenv("GNUI_ALLOW_OPEN", "")
	f := filepath.Join(t.TempDir(), "Report.gnui-guard-test")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Open(f); !errors.Is(err, ErrTestBinary) {
		t.Errorf("Open(existing) = %v, want ErrTestBinary", err)
	}
	if err := Open(filepath.Join(t.TempDir(), "missing")); !os.IsNotExist(err) {
		t.Errorf("Open(missing) = %v, want a not-exist error", err)
	}
}
