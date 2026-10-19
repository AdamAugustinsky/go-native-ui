//go:build linux

package shell

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func noRenameat2(t *testing.T) {
	t.Helper()
	useRenameat2 = false
	t.Cleanup(func() { useRenameat2 = true })
}

func openForTest(t *testing.T, p string) *tdir {
	t.Helper()
	d, err := openDir(p, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.close)
	return d
}

// Whether or not renameat2 is there, a move never replaces what is at the
// destination.
func TestRenameIntoNeverReplaces(t *testing.T) {
	for _, name := range []string{"renameat2", "link"} {
		t.Run(name, func(t *testing.T) {
			if name == "link" {
				noRenameat2(t)
			}
			a, b := t.TempDir(), t.TempDir()
			write(t, filepath.Join(a, "f"), "new")
			write(t, filepath.Join(b, "f"), "old")
			if err := openForTest(t, b).renameInto(openForTest(t, a), "f", "f"); !errors.Is(err, os.ErrExist) {
				t.Fatalf("err = %v, want EEXIST", err)
			}
			if read(t, filepath.Join(b, "f")) != "old" || read(t, filepath.Join(a, "f")) != "new" {
				t.Error("a file was replaced or lost")
			}
			// A dangling symbolic link at the destination counts as existing.
			os.Symlink(filepath.Join(b, "nowhere"), filepath.Join(b, "g"))
			write(t, filepath.Join(a, "g"), "new")
			if err := openForTest(t, b).renameInto(openForTest(t, a), "g", "g"); !errors.Is(err, os.ErrExist) {
				t.Fatalf("over a dangling link: %v, want EEXIST", err)
			}
			if _, err := os.Lstat(filepath.Join(b, "nowhere")); err == nil {
				t.Error("the link's target was created")
			}
			// And a free name works.
			if err := openForTest(t, b).renameInto(openForTest(t, a), "f", "h"); err != nil {
				t.Fatal(err)
			}
			if read(t, filepath.Join(b, "h")) != "new" {
				t.Error("not moved")
			}
			if _, err := os.Lstat(filepath.Join(a, "f")); !errors.Is(err, os.ErrNotExist) {
				t.Error("the old name is still there")
			}
		})
	}
}

// Without renameat2 a directory is refused, not renamed: the old fallback used
// renameat, which replaces an empty directory that appeared after the check.
func TestRenameFallbackRefusesADirectory(t *testing.T) {
	noRenameat2(t)
	a, b := t.TempDir(), t.TempDir()
	write(t, filepath.Join(a, "d", "inside"), "x")
	if err := openForTest(t, b).renameInto(openForTest(t, a), "d", "d"); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if read(t, filepath.Join(a, "d", "inside")) != "x" {
		t.Error("the directory was moved")
	}
	if _, err := os.Lstat(filepath.Join(b, "d")); err == nil {
		t.Error("something appeared at the destination")
	}
	// An empty directory that is already at the destination is left alone as well.
	os.Mkdir(filepath.Join(b, "e"), 0o700)
	os.Mkdir(filepath.Join(a, "e"), 0o700)
	if err := openForTest(t, b).renameInto(openForTest(t, a), "e", "e"); err == nil {
		t.Error("an empty directory replaced another")
	}
}

// A symbolic link is moved as itself.
func TestRenameFallbackMovesASymlinkItself(t *testing.T) {
	noRenameat2(t)
	a, b := t.TempDir(), t.TempDir()
	os.Symlink("/etc/hostname", filepath.Join(a, "l"))
	if err := openForTest(t, b).renameInto(openForTest(t, a), "l", "l"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(b, "l")); err != nil || got != "/etc/hostname" {
		t.Errorf("moved link: %q %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(a, "l")); err == nil {
		t.Error("the old name is still there")
	}
}

// A real trash works through the fallback, and a name that is taken moves on to
// the next one.
func TestTrashThroughTheFallback(t *testing.T) {
	noRenameat2(t)
	root := t.TempDir()
	data := filepath.Join(root, "data")
	write(t, filepath.Join(data, "Trash", "files", "f"), "already there") // no info file for it
	f := filepath.Join(root, "work", "f")
	write(t, f, "mine")
	if err := trashXDG(f, data, 1000, stamp); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(data, "Trash", "files", "f")) != "already there" {
		t.Error("an existing file in the trash was replaced")
	}
	if read(t, filepath.Join(data, "Trash", "files", "f.2")) != "mine" {
		t.Error("the file was not trashed under the next name")
	}
}

// statat looks at the entry, not at what it points to.
func TestStatatDoesNotFollow(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "target"), "x")
	os.Symlink("target", filepath.Join(dir, "link"))
	d := openForTest(t, dir)
	st, err := d.statat("link")
	if err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFLNK {
		t.Errorf("statat(link) = mode %o, %v; want a symbolic link", st.Mode, err)
	}
}
