package inspectwire

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "gi")
	if err != nil {
		t.Fatal(err)
	}
	if d, err = filepath.EvalSymlinks(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// The temporary file of a shot is finished through its open descriptor: a link
// put in its place before the chmod does not make the chmod, or the write,
// reach the link's target.
func TestShotTempFileSwappedForALinkChangesNothingElsewhere(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes")
	}
	d := tmpDir(t)
	out := filepath.Join(d, "out")
	os.Mkdir(out, 0o755)
	victim := filepath.Join(d, "victim")
	if err := os.WriteFile(victim, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(d, "x"), 0o700)
	dest, err := OpenShotDest(filepath.Join(d, "x", "s"), filepath.Join(out, "a.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	testHookAfterTemp = func(name string) {
		p := filepath.Join(out, name)
		os.Remove(p)
		if err := os.Symlink(victim, p); err != nil {
			t.Skipf("no symbolic links: %v", err)
		}
	}
	defer func() { testHookAfterTemp = nil }()
	if err := dest.Write([]byte("png")); err == nil {
		t.Error("a temporary file that was swapped was published")
	}
	fi, err := os.Stat(victim)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the target's mode changed: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious" {
		t.Errorf("the target was written: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(out, "a.png")); err == nil {
		t.Error("the swapped file became the shot")
	}
	if ents, _ := os.ReadDir(out); len(ents) != 0 {
		t.Errorf("left in the output directory: %v", ents)
	}
}
