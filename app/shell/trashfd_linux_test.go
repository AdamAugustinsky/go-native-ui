//go:build linux

package shell

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// failRenameat2 makes the call to renameat2 fail with e.
func failRenameat2(t *testing.T, e syscall.Errno) {
	t.Helper()
	renameat2Errno = e
	t.Cleanup(func() { renameat2Errno = 0 })
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

// A move never replaces what is at the destination.
func TestRenameIntoNeverReplaces(t *testing.T) {
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
	if err := openForTest(t, b).renameInto(openForTest(t, a), "f", "h"); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(b, "h")) != "new" {
		t.Error("not moved")
	}
}

// Without renameat2 (ENOSYS), or on a file system that does not support the flag
// (EINVAL), nothing is moved, whatever the file is: no hard link and unlink that
// could delete a file that replaced the source, no rename that could replace one.
func TestWithoutRenameat2NothingIsMoved(t *testing.T) {
	for _, e := range []syscall.Errno{syscall.ENOSYS, syscall.EINVAL} {
		t.Run(e.Error(), func(t *testing.T) {
			failRenameat2(t, e)
			a, b := t.TempDir(), t.TempDir()
			write(t, filepath.Join(a, "f"), "mine")
			write(t, filepath.Join(a, "d", "inside"), "x")
			os.Symlink("/etc/hostname", filepath.Join(a, "l"))
			write(t, filepath.Join(b, "taken"), "old")
			write(t, filepath.Join(a, "taken"), "new")
			for _, name := range []string{"f", "d", "l", "taken"} {
				err := openForTest(t, b).renameInto(openForTest(t, a), name, name)
				if !errors.Is(err, errors.ErrUnsupported) {
					t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
				}
				if err != nil && !strings.Contains(err.Error(), "cannot move this file safely") {
					t.Errorf("%s: the error does not say why: %v", name, err)
				}
			}
			if read(t, filepath.Join(a, "f")) != "mine" || read(t, filepath.Join(a, "d", "inside")) != "x" || read(t, filepath.Join(a, "taken")) != "new" {
				t.Error("a source was changed")
			}
			if _, err := os.Lstat(filepath.Join(a, "l")); err != nil {
				t.Error("the link was moved")
			}
			if read(t, filepath.Join(b, "taken")) != "old" {
				t.Error("a file at the destination was replaced")
			}
			if ents, _ := os.ReadDir(b); len(ents) != 1 {
				t.Errorf("something was put in the destination: %v", ents)
			}
		})
	}
}

// Trash refuses, leaves the file and the trash as they were, and says why.
func TestTrashRefusesWithoutRenameat2(t *testing.T) {
	failRenameat2(t, syscall.ENOSYS)
	root := t.TempDir()
	data := filepath.Join(root, "data")
	f := filepath.Join(root, "work", "f")
	write(t, f, "mine")
	err := trashXDG(f, data, 1000, stamp)
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
	if read(t, f) != "mine" {
		t.Error("the file was moved")
	}
	for _, d := range []string{"files", "info"} {
		if ents, _ := os.ReadDir(filepath.Join(data, "Trash", d)); len(ents) != 0 {
			t.Errorf("%s holds %v", d, ents)
		}
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
