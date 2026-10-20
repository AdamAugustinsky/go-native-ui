//go:build windows

package shell

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The real Recycle Bin takes a temporary file.
func TestTrashRealWindows(t *testing.T) {
	var b [6]byte
	rand.Read(b[:])
	f := filepath.Join(t.TempDir(), "gonativeui-shell-test-"+hex.EncodeToString(b[:])+".txt")
	if err := os.WriteFile(f, []byte("to the bin"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Trash(f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file is still in its directory: %v", err)
	}
}
