//go:build unix

package shell

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
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

// asOtherVolume makes the rename into the home trash fail with EXDEV, as it does
// for a file on another file system, and makes top the top of that file system.
func asOtherVolume(t *testing.T, homeTrash, top string) {
	t.Helper()
	real := renameFn
	renameFn = func(dst, src *tdir, srcName, dstName string) error {
		if strings.HasPrefix(dst.path, homeTrash) {
			return &os.LinkError{Op: "rename", Old: srcName, New: dstName, Err: syscall.EXDEV}
		}
		return real(dst, src, srcName, dstName)
	}
	topdirFn = func(string) (string, error) { return top, nil }
	t.Cleanup(func() { renameFn, topdirFn = real, topdir })
}

// A file on another file system goes to a trash at the top of its own.
func TestTrashXDGOtherVolume(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	top := filepath.Join(root, "vol")
	f := filepath.Join(top, "sub", "doc.txt")
	write(t, f, "payload")
	asOtherVolume(t, filepath.Join(data, "Trash"), top)

	if err := trashXDG(f, data, 4242, stamp); err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(top, f)
	trash := filepath.Join(top, ".Trash-4242")
	if got := read(t, filepath.Join(trash, "files", "doc.txt")); got != "payload" {
		t.Errorf("content %q", got)
	}
	if info := read(t, filepath.Join(trash, "info", "doc.txt.trashinfo")); !strings.Contains(info, "\nPath="+rel+"\n") {
		t.Errorf("the info file should record the path relative to %s (%s):\n%s", top, rel, info)
	}
	if _, err := os.Lstat(filepath.Join(data, "Trash", "info", "doc.txt.trashinfo")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the failed attempt left an info file in the home trash: %v", err)
	}
	for _, d := range []string{trash, filepath.Join(trash, "files"), filepath.Join(trash, "info")} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi, err)
		}
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

// ---- hostile trash directories (the top of a volume is shared) -----------------

// volume sets up a file on a "volume" whose home-trash attempt fails with EXDEV.
func volume(t *testing.T) (data, top, file string) {
	t.Helper()
	root := t.TempDir()
	data, top = filepath.Join(root, "data"), filepath.Join(root, "vol")
	file = filepath.Join(top, "work", "secret.txt")
	write(t, file, "private")
	asOtherVolume(t, filepath.Join(data, "Trash"), top)
	return
}

func gone(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s still exists: %v", p, err)
	}
}

func untouched(t *testing.T, file, victim string) {
	t.Helper()
	if read(t, file) != "private" {
		t.Error("the file was moved or damaged")
	}
	entries, _ := os.ReadDir(victim)
	if len(entries) != 0 {
		t.Errorf("something was written into the directory a link pointed to: %v", entries)
	}
}

// .Trash-<uid> is a symbolic link to a directory the attacker chose.
func TestTrashRefusesASymlinkedUserTrash(t *testing.T) {
	data, top, file := volume(t)
	victim := filepath.Join(t.TempDir(), "victim")
	os.Mkdir(victim, 0o700)
	if err := os.Symlink(victim, filepath.Join(top, ".Trash-4242")); err != nil {
		t.Fatal(err)
	}
	if err := trashXDG(file, data, 4242, stamp); !errors.Is(err, errUntrusted) {
		t.Fatalf("err = %v, want errUntrusted", err)
	}
	untouched(t, file, victim)
}

// files/ and info/ inside an otherwise good .Trash-<uid> are links.
func TestTrashRefusesSymlinkedFilesAndInfo(t *testing.T) {
	for _, which := range []string{"files", "info"} {
		t.Run(which, func(t *testing.T) {
			data, top, file := volume(t)
			victim := filepath.Join(t.TempDir(), "victim")
			os.Mkdir(victim, 0o700)
			trash := filepath.Join(top, ".Trash-4242")
			os.Mkdir(trash, 0o700)
			os.Chmod(trash, 0o700)
			os.Symlink(victim, filepath.Join(trash, which))
			if err := trashXDG(file, data, 4242, stamp); !errors.Is(err, errUntrusted) {
				t.Fatalf("err = %v, want errUntrusted", err)
			}
			untouched(t, file, victim)
		})
	}
}

// A trash directory with the wrong mode, or that another user owns, is refused.
func TestTrashRefusesWrongModeAndOwner(t *testing.T) {
	t.Run("mode", func(t *testing.T) {
		data, top, file := volume(t)
		trash := filepath.Join(top, ".Trash-4242")
		os.Mkdir(trash, 0o755)
		os.Chmod(trash, 0o755)
		if err := trashXDG(file, data, 4242, stamp); !errors.Is(err, errUntrusted) {
			t.Fatalf("err = %v, want errUntrusted", err)
		}
		if read(t, file) != "private" {
			t.Error("the file was moved")
		}
	})
	t.Run("files mode", func(t *testing.T) {
		data, top, file := volume(t)
		trash := filepath.Join(top, ".Trash-4242")
		os.MkdirAll(filepath.Join(trash, "files"), 0o700)
		os.Chmod(trash, 0o700)
		os.Chmod(filepath.Join(trash, "files"), 0o777)
		if err := trashXDG(file, data, 4242, stamp); !errors.Is(err, errUntrusted) {
			t.Fatalf("err = %v, want errUntrusted", err)
		}
	})
	t.Run("owner", func(t *testing.T) {
		data, top, file := volume(t)
		os.Mkdir(filepath.Join(top, ".Trash-4242"), 0o700)
		euid = func() int { return os.Geteuid() + 1 } // everything on disk is now someone else's
		t.Cleanup(func() { euid = os.Geteuid })
		if err := trashXDG(file, data, 4242, stamp); !errors.Is(err, errUntrusted) {
			t.Fatalf("err = %v, want errUntrusted", err)
		}
		if read(t, file) != "private" {
			t.Error("the file was moved")
		}
	})
}

// A usable shared $topdir/.Trash (sticky, a real directory) is used with
// $topdir/.Trash/$uid; any of its defects sends the file to .Trash-$uid.
func TestTrashSharedTrash(t *testing.T) {
	good := func(t *testing.T, top string) string {
		shared := filepath.Join(top, ".Trash")
		os.MkdirAll(shared, 0o777)
		if err := os.Chmod(shared, 0o777|os.ModeSticky); err != nil {
			t.Fatal(err)
		}
		return shared
	}
	t.Run("used", func(t *testing.T) {
		data, top, file := volume(t)
		shared := good(t, top)
		if err := trashXDG(file, data, 4242, stamp); err != nil {
			t.Fatal(err)
		}
		if read(t, filepath.Join(shared, "4242", "files", "secret.txt")) != "private" {
			t.Error("not in the shared trash")
		}
		gone(t, filepath.Join(top, ".Trash-4242"))
	})
	for name, prep := range map[string]func(t *testing.T, top, shared string){
		"no sticky bit": func(t *testing.T, top, shared string) { os.Chmod(shared, 0o777) },
		"a symlink": func(t *testing.T, top, shared string) {
			os.RemoveAll(shared)
			os.Mkdir(filepath.Join(top, "elsewhere"), 0o777)
			os.Chmod(filepath.Join(top, "elsewhere"), 0o777|os.ModeSticky)
			os.Symlink(filepath.Join(top, "elsewhere"), shared)
		},
		"uid dir has a wrong mode": func(t *testing.T, top, shared string) {
			os.Mkdir(filepath.Join(shared, "4242"), 0o755)
			os.Chmod(filepath.Join(shared, "4242"), 0o755)
		},
		"uid dir is a symlink": func(t *testing.T, top, shared string) {
			os.Mkdir(filepath.Join(top, "victim"), 0o700)
			os.Symlink(filepath.Join(top, "victim"), filepath.Join(shared, "4242"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			data, top, file := volume(t)
			shared := good(t, top)
			prep(t, top, shared)
			if err := trashXDG(file, data, 4242, stamp); err != nil {
				t.Fatal(err)
			}
			if read(t, filepath.Join(top, ".Trash-4242", "files", "secret.txt")) != "private" {
				t.Error("the file did not go to .Trash-<uid>")
			}
			if entries, _ := os.ReadDir(filepath.Join(top, "victim")); len(entries) != 0 {
				t.Errorf("something was written through a link: %v", entries)
			}
			if entries, _ := os.ReadDir(filepath.Join(top, "elsewhere")); len(entries) != 0 {
				t.Errorf("something was written through a link: %v", entries)
			}
		})
	}
}

// The home trash is checked too: a link where Trash should be is refused.
func TestHomeTrashSymlinkIsRefused(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	victim := filepath.Join(root, "victim")
	os.MkdirAll(data, 0o700)
	os.Mkdir(victim, 0o700)
	os.Symlink(victim, filepath.Join(data, "Trash"))
	f := filepath.Join(root, "f")
	write(t, f, "private")
	if err := trashXDG(f, data, 1000, stamp); !errors.Is(err, errUntrusted) {
		t.Fatalf("err = %v, want errUntrusted", err)
	}
	untouched(t, f, victim)
}

// A name too long to keep with ".trashinfo" and a collision suffix is shortened in
// the trash, and the info file still records the real one.
func TestLongNamesAreBounded(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	long := strings.Repeat("\u00e9", 120) + ".tar.gz" // 247 bytes: the longest a name can be
	if len(long) > 255 {
		t.Fatalf("test name is %d bytes", len(long))
	}
	for i := 0; i < 3; i++ { // the same long name again: collision suffixes must fit too
		f := filepath.Join(root, fmt.Sprint("d", i), long)
		write(t, f, "x")
		if err := trashXDG(f, data, 1000, stamp); err != nil {
			t.Fatalf("trashing a %d-byte name (#%d): %v", len(long), i, err)
		}
	}
	files, _ := os.ReadDir(filepath.Join(data, "Trash", "files"))
	infos, _ := os.ReadDir(filepath.Join(data, "Trash", "info"))
	if len(files) != 3 || len(infos) != 3 {
		t.Fatalf("%d files, %d info files", len(files), len(infos))
	}
	for _, fe := range files {
		if len(fe.Name())+len(".trashinfo") > 255 || !utf8.ValidString(fe.Name()) {
			t.Errorf("trash name %q (%d bytes)", fe.Name(), len(fe.Name()))
		}
		if !strings.HasSuffix(fe.Name(), ".gz") && !strings.Contains(fe.Name(), ".gz.") {
			t.Errorf("the extension was lost: %q", fe.Name())
		}
	}
	for _, ie := range infos {
		info := read(t, filepath.Join(data, "Trash", "info", ie.Name()))
		if !strings.Contains(info, url.PathEscape(long)) {
			t.Errorf("%s does not record the original name:\n%s", ie.Name(), info)
		}
	}
}

func TestTrashName(t *testing.T) {
	if got := trashName("a.txt", 1); got != "a.txt" {
		t.Error(got)
	}
	if got := trashName("a.txt", 3); got != "a.txt.3" {
		t.Error(got)
	}
	for _, base := range []string{strings.Repeat("x", 300), strings.Repeat("\u4e16", 100) + ".txt", strings.Repeat("y", 300) + strings.Repeat("e", 40)} {
		for _, n := range []int{1, 2, 10000} {
			got := trashName(base, n)
			if len(got)+len(".trashinfo") > 255 || !utf8.ValidString(got) {
				t.Errorf("trashName(%.20q..., %d) = %d bytes, valid %v", base, n, len(got), utf8.ValidString(got))
			}
		}
	}
}

func TestTrashIntoFailureLeavesNothing(t *testing.T) {
	root := t.TempDir()
	f := filepath.Join(root, "x")
	write(t, f, "x")
	boom := errors.New("disk on fire")
	real := renameFn
	renameFn = func(*tdir, *tdir, string, string) error { return boom }
	t.Cleanup(func() { renameFn = real })
	data := filepath.Join(root, "data")
	if err := trashXDG(f, data, 1000, stamp); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if infos, _ := os.ReadDir(filepath.Join(data, "Trash", "info")); len(infos) != 0 {
		t.Errorf("an info file was left behind: %v", infos)
	}
	if read(t, f) != "x" {
		t.Error("the file was damaged")
	}
}
