//go:build unix

package shell

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The backend itself refuses the home directory and the root, on the descriptor of
// the parent, whatever the caller checked before.
func TestTrashXDGRefusesHomeAndRoot(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "real", "home")
	os.MkdirAll(home, 0o700)
	t.Setenv("HOME", home)
	data := filepath.Join(base, "data")
	for _, p := range []string{home, "/"} {
		if err := trashXDG(p, data, 1000, stamp); !errors.Is(err, ErrInvalid) {
			t.Errorf("trashXDG(%q) = %v, want ErrInvalid", p, err)
		}
	}
	// The same folder through a linked parent, and through another bind of the name.
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := trashXDG(filepath.Join(base, "alias", "home"), data, 1000, stamp); !errors.Is(err, ErrInvalid) {
		t.Errorf("home through a linked parent: %v, want ErrInvalid", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("home is gone: %v", err)
	}
	// A link to home is only a link.
	link := filepath.Join(base, "link")
	os.Symlink(home, link)
	if err := trashXDG(link, data, 1000, stamp); err != nil {
		t.Errorf("a link to home: %v", err)
	}
	if _, err := os.Stat(home); err != nil {
		t.Fatalf("home is gone: %v", err)
	}
}

// The parent of the source is opened once. A link on the way that is retargeted
// afterwards, here at the folder that holds the home directory, does not change
// what is checked or what is moved: the home directory stays where it is.
func TestTrashXDGParentAliasRetargeted(t *testing.T) {
	base := t.TempDir()
	safe := filepath.Join(base, "safe")
	holder := filepath.Join(base, "holder")
	home := filepath.Join(holder, "home") // same name as the entry in safe
	write(t, filepath.Join(safe, "home", "note"), "an ordinary folder")
	write(t, filepath.Join(home, "precious"), "the user's files")
	t.Setenv("HOME", home)
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(safe, alias); err != nil {
		t.Fatal(err)
	}
	afterParentOpen = func() {
		os.Remove(alias)
		os.Symlink(holder, alias) // now alias/home is the home directory
	}
	t.Cleanup(func() { afterParentOpen = func() {} })

	err := trashXDG(filepath.Join(alias, "home"), filepath.Join(base, "data"), 1000, stamp)
	if got := read(t, filepath.Join(home, "precious")); got != "the user's files" {
		t.Fatalf("the home directory was touched: %q", got)
	}
	if runtime.GOOS == "linux" {
		// With descriptors, the folder that was opened is the one that is moved.
		if err != nil {
			t.Fatalf("trashXDG: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(safe, "home")); !errors.Is(err, os.ErrNotExist) {
			t.Error("the folder that was checked was not the one moved")
		}
	}
}
