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

func TestTrashRealDirectoryWindows(t *testing.T) {
	var b [6]byte
	rand.Read(b[:])
	d := filepath.Join(t.TempDir(), "gonativeui-shell-dir-"+hex.EncodeToString(b[:]))
	if err := os.MkdirAll(filepath.Join(d, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(d, "sub", "f"), []byte("x"), 0o600)
	if err := Trash(d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(d); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the folder is still there: %v", err)
	}
}

// A folder swapped for another between the check and the move: the bin takes the
// one that was swapped in, and Trash notices that the folder it opened is not in
// the bin. Here the folder swapped in is the user's home.
func TestTrashSwapIsReportedWindows(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, "home")
	target := filepath.Join(base, "target")
	for p, s := range map[string]string{filepath.Join(victim, "precious"): "the user's files", filepath.Join(target, "note"): "an ordinary folder"} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("USERPROFILE", victim)
	beforeMove = func() {
		os.Rename(target, target+".aside")
		os.Rename(victim, target)
	}
	t.Cleanup(func() { beforeMove = func() {} })

	err := (osSystem{}).trash(target)
	if err == nil {
		t.Fatal("a swapped path was trashed without an error")
	}
	if got, _ := os.ReadFile(filepath.Join(target+".aside", "note")); string(got) != "an ordinary folder" {
		t.Errorf("the folder that was checked changed: %q (trash error: %v)", got, err)
	}
}
