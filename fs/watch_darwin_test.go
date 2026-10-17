//go:build darwin

package fs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// FSEvents watches recursively; changes deeper than the watched directory
// must not make it relist. Symlinked watch paths resolve to the real path.
func TestWatcherIgnoresDeeperDirsAndResolvesLinks(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	deep := filepath.Join(dir, "sub", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	r := newWatchRig(t)
	if err := r.w.add(1, link); err != nil { // watched through a symlink
		t.Fatal(err)
	}
	r.quiet()
	os.WriteFile(filepath.Join(deep, "f"), []byte("x"), 0o644)
	time.Sleep(300 * time.Millisecond)
	r.buf = r.w.drain(r.buf[:0])
	if len(r.buf) != 0 {
		t.Errorf("changes %v for an event two levels down", r.buf)
	}
	os.WriteFile(filepath.Join(dir, "top"), nil, 0o644)
	r.wait(time.Now(), 1, ChangeDir)
}
