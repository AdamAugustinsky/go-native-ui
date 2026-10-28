//go:build darwin

package shell

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gonativeui/internal/cf"
)

func uniqueName(prefix string) string {
	var b [6]byte
	rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// The real NSFileManager moves a temporary file to the user's Trash. The file
// has a name no one else uses, and the test removes the Trash copy by that name
// afterwards (best effort: a Trash the process may not list stays as it is).
func TestTrashRealDarwin(t *testing.T) {
	dir := t.TempDir()
	name := uniqueName("gonativeui-shell-test-") + ".txt"
	f := filepath.Join(dir, name)
	if err := os.WriteFile(f, []byte("to the trash"), 0o600); err != nil {
		t.Fatal(err)
	}
	if home, err := os.UserHomeDir(); err == nil {
		t.Cleanup(func() { os.Remove(filepath.Join(home, ".Trash", name)) })
	}
	if err := Trash(f); err != nil {
		t.Skipf("this session cannot use the Trash: %v", err)
	}
	if _, err := os.Lstat(f); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file is still in its directory: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("the directory still holds %v", entries)
	}
}

func TestTrashRealDirectoryDarwin(t *testing.T) {
	dir := t.TempDir()
	name := uniqueName("gonativeui-shell-dir-")
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Join(d, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(d, "sub", "f"), []byte("x"), 0o600)
	if home, err := os.UserHomeDir(); err == nil {
		t.Cleanup(func() { os.RemoveAll(filepath.Join(home, ".Trash", name)) })
	}
	if err := Trash(d); err != nil {
		t.Skipf("this session cannot use the Trash: %v", err)
	}
	if _, err := os.Lstat(d); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the folder is still there: %v", err)
	}
}

// The system's own error for a file that is not there maps to os.ErrNotExist
// (the package function checks first; this is the backend alone).
func TestTrashMissingBackendDarwin(t *testing.T) {
	err := osSystem{}.trash(filepath.Join(t.TempDir(), "missing"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("trash of a missing file = %v", err)
	}
}

func TestOpenURLRejectedByBackendDarwin(t *testing.T) {
	// A string CFURLCreateWithString cannot parse never reaches Launch Services.
	if err := (osSystem{}).openURL("http://[bad host/"); err == nil {
		t.Error("an unparsable URL was accepted")
	}
}

// Reveal would open a Finder window, which the tests do not do; this checks
// what it relies on: AppKit loads off the main thread, NSWorkspace is there and
// answers the selector.
func TestRevealPlumbingDarwin(t *testing.T) {
	if err := ready(); err != nil {
		t.Fatal(err)
	}
	fr := cf.Acquire()
	defer cf.Release(fr)
	defer cf.Pool(fr)()
	ws := cf.Msg(fr, cf.Cls(fr, "NSWorkspace"), cf.Sel(fr, "sharedWorkspace"))
	if ws == 0 {
		t.Fatal("no NSWorkspace")
	}
	if !cf.MsgBool(fr, ws, cf.Sel(fr, "respondsToSelector:"), cf.Sel(fr, "activateFileViewerSelectingURLs:")) {
		t.Error("NSWorkspace does not answer activateFileViewerSelectingURLs:")
	}
	url := cf.Msg(fr, cf.Cls(fr, "NSURL"), cf.Sel(fr, "fileURLWithPath:"), uintptr(cf.String(fr, "/tmp")))
	if got := cf.GoString(fr, cf.Msg(fr, url, cf.Sel(fr, "path"))); got != "/tmp" {
		t.Errorf("NSURL path = %q", got)
	}
}

// A folder swapped for another between the check and the move: the Trash takes
// the one that was swapped in, and Trash notices (the item in the Trash is not
// the one that was opened), puts it back and reports the error. Here the folder
// swapped in is the user's home.
func TestTrashSwapIsUndoneDarwin(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(base, uniqueName("gonativeui-shell-home-"))
	target := filepath.Join(base, uniqueName("gonativeui-shell-target-"))
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(victim, "precious"), "the user's files")
	write(filepath.Join(target, "note"), "an ordinary folder")
	t.Setenv("HOME", victim)
	if home, err := os.UserHomeDir(); err != nil || home != victim {
		t.Fatalf("home = %q, %v", home, err)
	}
	// Probe: can this session use the Trash at all?
	probe := filepath.Join(base, uniqueName("gonativeui-shell-probe-")+".txt")
	write(probe, "x")
	if _, err := trashURL(probe); err != nil {
		t.Skipf("this session cannot use the Trash: %v", err)
	}

	beforeMove = func() {
		os.Rename(target, target+".aside")
		os.Rename(victim, target) // the home folder is now where the folder was
	}
	t.Cleanup(func() { beforeMove = func() {} })

	err := (osSystem{}).trash(target)
	if err == nil {
		t.Fatal("a swapped path was trashed without an error")
	}
	if got, rerr := os.ReadFile(filepath.Join(target, "precious")); rerr != nil || string(got) != "the user's files" {
		t.Fatalf("the swapped-in folder was not put back: %q, %v (trash error: %v)", got, rerr, err)
	}
	if got, _ := os.ReadFile(filepath.Join(target+".aside", "note")); string(got) != "an ordinary folder" {
		t.Errorf("the folder that was checked changed: %q", got)
	}
}

// Without a swap, what the path names after links are resolved is what moves,
// and a link in the path is not followed past what was checked.
func TestTrashThroughALinkDarwin(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	os.MkdirAll(real, 0o700)
	if err := os.Symlink(real, filepath.Join(base, "alias")); err != nil {
		t.Fatal(err)
	}
	name := uniqueName("gonativeui-shell-via-link-") + ".txt"
	f := filepath.Join(real, name)
	os.WriteFile(f, []byte("x"), 0o600)
	if home, err := os.UserHomeDir(); err == nil {
		t.Cleanup(func() { os.Remove(filepath.Join(home, ".Trash", name)) })
	}
	if err := Trash(filepath.Join(base, "alias", name)); err != nil {
		t.Skipf("this session cannot use the Trash: %v", err)
	}
	if _, err := os.Lstat(f); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file is still there: %v", err)
	}
}

// A link is trashed as a link; what it points to stays.
func TestTrashLinkNotTargetDarwin(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target.txt")
	os.WriteFile(target, []byte("keep"), 0o600)
	name := uniqueName("gonativeui-shell-link-")
	link := filepath.Join(base, name)
	os.Symlink(target, link)
	if home, err := os.UserHomeDir(); err == nil {
		t.Cleanup(func() { os.Remove(filepath.Join(home, ".Trash", name)) })
	}
	if err := Trash(link); err != nil {
		t.Skipf("this session cannot use the Trash: %v", err)
	}
	if b, err := os.ReadFile(target); err != nil || string(b) != "keep" {
		t.Errorf("the link's target changed: %q, %v", b, err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the link is still there: %v", err)
	}
}
