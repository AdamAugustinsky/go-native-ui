//go:build darwin

package fs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Finding 12: names that do not fit the 32-bit arena are an error with a
// partial listing, never wrapped offsets.
func TestListDirTooLarge(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"aaaaaaaaaa", "bbbbbbbbbb", "cccccccccc", "dddddddddd"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	old := arenaLimit
	defer func() { arenaLimit = old }()
	arenaLimit = 25 // room for two 10-byte names
	l := new(Listing)
	err := ListDir(dir, l)
	if !errors.Is(err, ErrTooLarge) || !errors.Is(l.Err, ErrTooLarge) {
		t.Fatalf("err = %v, l.Err = %v", err, l.Err)
	}
	if len(l.Entries) != 2 || len(l.Names) != 20 {
		t.Fatalf("partial listing: %d entries, %d name bytes", len(l.Entries), len(l.Names))
	}
	for i := range l.Entries {
		if n := string(l.Name(i)); len(n) != 10 {
			t.Errorf("entry %d name %q", i, n)
		}
	}
	arenaLimit = 1 // more entries than indices
	if err := ListDir(dir, l); !errors.Is(err, ErrTooLarge) {
		t.Errorf("entry count: %v", err)
	}
}
