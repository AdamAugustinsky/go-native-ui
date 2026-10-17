//go:build darwin

package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The watcher is not recursive: changes deeper than the watched directory
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

func fileWatches(w watcher) int {
	k := w.(*kqWatcher)
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.nfiles
}

func openFDs(t *testing.T) int {
	t.Helper()
	es, err := os.ReadDir("/dev/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

// In-place writes touch only the file's vnode, so the watcher also watches
// the regular files of a directory (up to maxDirFiles). The watch set
// follows creations and deletions, and releases its descriptors.
func TestKqueueFileWatches(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(dir, string(rune('a'+i))), []byte("x"), 0o644)
	}
	os.Mkdir(filepath.Join(dir, "sub"), 0o755) // directories are not watched individually
	r := newWatchRig(t)
	before := openFDs(t)
	if err := r.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	if n := fileWatches(r.w); n != 5 {
		t.Fatalf("%d file watches, want 5", n)
	}
	r.quiet()

	os.WriteFile(filepath.Join(dir, "c"), []byte("in place"), 0o644)
	r.wait(time.Now(), 1, ChangeDir)
	r.quiet()

	os.WriteFile(filepath.Join(dir, "new"), nil, 0o644)
	r.wait(time.Now(), 1, ChangeDir)
	r.quiet()
	if n := fileWatches(r.w); n != 6 {
		t.Errorf("%d file watches after a create, want 6", n)
	}
	os.WriteFile(filepath.Join(dir, "new"), []byte("now with content"), 0o644) // the new file is watched too
	r.wait(time.Now(), 1, ChangeDir)
	r.quiet()

	os.Remove(filepath.Join(dir, "a"))
	r.wait(time.Now(), 1, ChangeDir)
	r.quiet()
	if n := fileWatches(r.w); n != 5 {
		t.Errorf("%d file watches after a delete, want 5", n)
	}
	r.w.remove(1)
	if n := fileWatches(r.w); n != 0 {
		t.Errorf("%d file watches after remove", n)
	}
	if after := openFDs(t); after > before+2 { // the kqueue itself
		t.Errorf("descriptors: %d before, %d after remove", before, after)
	}
}

// Above the cap a directory is watched structurally only: documented gap.
func TestKqueueFileCap(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxDirFiles+1; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d", i)), nil, 0o644)
	}
	r := newWatchRig(t)
	if err := r.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	if n := fileWatches(r.w); n != 0 {
		t.Fatalf("%d file watches in an oversized directory", n)
	}
	r.quiet()
	os.WriteFile(filepath.Join(dir, "extra"), nil, 0o644)
	r.wait(time.Now(), 1, ChangeDir) // structure is still covered
}

func TestKqueueNoDescriptorLeak(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d", i)), nil, 0o644)
	}
	r := newWatchRig(t)
	before := openFDs(t)
	for i := 0; i < 50; i++ {
		if err := r.w.add(uint32(i%3), dir); err != nil { // re-adding an id replaces it
			t.Fatal(err)
		}
		if i%2 == 0 {
			r.w.remove(uint32(i % 3))
		}
	}
	for id := uint32(0); id < 3; id++ {
		r.w.remove(id)
	}
	if after := openFDs(t); after != before {
		t.Errorf("descriptors: %d before, %d after", before, after)
	}
}
