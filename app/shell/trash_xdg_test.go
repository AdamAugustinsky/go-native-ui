//go:build unix

package shell

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var stamp = time.Date(2026, 10, 19, 8, 30, 5, 0, time.Local)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTrashXDGHome(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	f := filepath.Join(root, "work", "my file %1.txt")
	write(t, f, "hello")
	if err := trashXDG(f, data, 1000, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(f); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file is still there: %v", err)
	}
	trash := filepath.Join(data, "Trash")
	if got := read(t, filepath.Join(trash, "files", "my file %1.txt")); got != "hello" {
		t.Errorf("trashed content %q", got)
	}
	info := read(t, filepath.Join(trash, "info", "my file %1.txt.trashinfo"))
	want := "[Trash Info]\nPath=" + strings.ReplaceAll(strings.ReplaceAll(f, "%", "%25"), " ", "%20") + "\nDeletionDate=2026-10-19T08:30:05\n"
	if info != want {
		t.Errorf("info file\n%q\nwant\n%q", info, want)
	}
	for _, d := range []string{"files", "info"} {
		if fi, err := os.Stat(filepath.Join(trash, d)); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi, err)
		}
	}
}

func TestTrashXDGNamesDoNotCollide(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	for i, content := range []string{"one", "two", "three"} {
		f := filepath.Join(root, "dir"+string(rune('a'+i)), "same.txt")
		write(t, f, content)
		if err := trashXDG(f, data, 1000, stamp); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := os.ReadDir(filepath.Join(data, "Trash", "files"))
	infos, _ := os.ReadDir(filepath.Join(data, "Trash", "info"))
	if len(files) != 3 || len(infos) != 3 {
		t.Fatalf("%d files and %d info files, want 3 and 3", len(files), len(infos))
	}
	got := map[string]bool{}
	for _, fe := range files {
		got[read(t, filepath.Join(data, "Trash", "files", fe.Name()))] = true
		if _, err := os.Stat(filepath.Join(data, "Trash", "info", fe.Name()+".trashinfo")); err != nil {
			t.Errorf("no info file for %s: %v", fe.Name(), err)
		}
	}
	if !got["one"] || !got["two"] || !got["three"] {
		t.Errorf("contents %v", got)
	}
}

func TestTrashXDGDirectoryAndSymlink(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	d := filepath.Join(root, "folder")
	write(t, filepath.Join(d, "inner", "x"), "x")
	target := filepath.Join(root, "target")
	write(t, target, "keep me")
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := trashXDG(d, data, 1000, stamp); err != nil {
		t.Fatal(err)
	}
	if err := trashXDG(link, data, 1000, stamp); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(data, "Trash", "files", "folder", "inner", "x")); got != "x" {
		t.Errorf("folder content %q", got)
	}
	if got := read(t, target); got != "keep me" {
		t.Errorf("trashing the link changed its target: %q", got)
	}
	if fi, err := os.Lstat(filepath.Join(data, "Trash", "files", "link")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the trashed link: %v %v", fi, err)
	}
}

// A file on another file system goes to a trash at the top of its own: here the
// rename into the home trash is made to fail with EXDEV, and the top is the
// temporary directory's own mount point.
func TestTrashXDGOtherVolume(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	f := filepath.Join(root, "vol", "sub", "doc.txt")
	write(t, f, "payload")
	top := filepath.Join(root, "vol")
	topdirFn = func(string) (string, error) { return top, nil }
	t.Cleanup(func() { topdirFn = topdir })
	homeTrash := filepath.Join(data, "Trash")
	renameFn = func(from, to string) error {
		if strings.HasPrefix(to, homeTrash) {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EXDEV}
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { renameFn = os.Rename })

	if err := trashXDG(f, data, 4242, stamp); err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(top, f)
	trash := filepath.Join(top, ".Trash-4242")
	t.Cleanup(func() { os.RemoveAll(trash) })
	if got := read(t, filepath.Join(trash, "files", "doc.txt")); got != "payload" {
		t.Errorf("content %q", got)
	}
	if info := read(t, filepath.Join(trash, "info", "doc.txt.trashinfo")); !strings.Contains(info, "\nPath="+rel+"\n") {
		t.Errorf("the info file should record the path relative to %s (%s):\n%s", top, rel, info)
	}
	if _, err := os.Lstat(filepath.Join(homeTrash, "info", "doc.txt.trashinfo")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the failed attempt left an info file in the home trash: %v", err)
	}
}

// topdir stops at the top of the file system, which for a file of the temporary
// directory is an ancestor on the same device.
func TestTopdir(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a", "b")
	write(t, f, "x")
	top, err := topdir(f)
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := filepath.Rel(top, f); err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("topdir %q is not an ancestor of %q", top, f)
	}
	d1, _ := deviceOf(top)
	d2, _ := deviceOf(f)
	if d1 != d2 {
		t.Errorf("devices %d and %d differ", d1, d2)
	}
	if parent := filepath.Dir(top); parent != top {
		if dp, _ := deviceOf(parent); dp == d1 {
			t.Errorf("the parent of %q is on the same device: not the top", top)
		}
	}
}

func TestTrashXDGAdminTrash(t *testing.T) {
	root := t.TempDir()
	admin := filepath.Join(root, ".Trash")
	if err := os.Mkdir(admin, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(admin, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if !adminTrashOK(admin) {
		t.Fatal("a sticky directory should be accepted")
	}
	plain := filepath.Join(root, "plain")
	os.Mkdir(plain, 0o755)
	if adminTrashOK(plain) {
		t.Error("a directory without the sticky bit should not be")
	}
	link := filepath.Join(root, "link")
	os.Symlink(admin, link)
	if adminTrashOK(link) {
		t.Error("a symbolic link should not be")
	}
}

func TestTrashIntoFailureLeavesNothing(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "x")
	write(t, f, "x")
	boom := errors.New("disk on fire")
	renameFn = func(string, string) error { return boom }
	t.Cleanup(func() { renameFn = os.Rename })
	trash := filepath.Join(root, "Trash")
	if err := trashInto(f, trash, "", stamp); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if infos, _ := os.ReadDir(filepath.Join(trash, "info")); len(infos) != 0 {
		t.Errorf("an info file was left behind: %v", infos)
	}
	if read(t, f) != "x" {
		t.Error("the file was damaged")
	}
}
