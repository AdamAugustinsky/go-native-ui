//go:build darwin

package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
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

// Finding 14: after an atomic save (write a temp file, rename it over the
// original) the replacement must be watched, however the kevent batches fall
// relative to the directory's resync.
func TestKqueueAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	const n = 300
	name := func(i int) string { return filepath.Join(dir, fmt.Sprintf("doc%03d.txt", i)) }
	for i := 0; i < n; i++ {
		os.WriteFile(name(i), []byte("v1"), 0o644)
	}
	r := newWatchRig(t)
	if err := r.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	r.quiet()
	for round := 0; round < 3; round++ {
		for i := 0; i < n; i++ { // a burst of replacements: events span several kevent batches
			tmp := filepath.Join(dir, fmt.Sprintf(".tmp%03d", i))
			if err := os.WriteFile(tmp, []byte("v2"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(tmp, name(i)); err != nil {
				t.Fatal(err)
			}
		}
		r.quiet()
		// Every replacement is now watched: an in-place write is noticed.
		missed := 0
		for i := 0; i < n; i++ {
			start := time.Now()
			os.WriteFile(name(i), []byte(fmt.Sprintf("round %d file %d", round, i)), 0o644)
			deadline := time.After(300 * time.Millisecond)
		wait:
			for {
				r.buf = r.w.drain(r.buf[:0])
				for _, c := range r.buf {
					if c.ID == 1 {
						break wait
					}
				}
				select {
				case <-r.wake:
				case <-deadline:
					missed++
					break wait
				}
			}
			_ = start
		}
		if missed > 0 {
			t.Fatalf("round %d: %d of %d in-place writes to replaced files went unnoticed", round, missed, n)
		}
	}
	if got := fileWatches(r.w); got != n {
		t.Errorf("%d file watches, want %d", got, n)
	}
}

// Finding 14, deterministic: the directory's event is handled (and its
// resync keeps the name-keyed watch of the old file) before the old file's
// own delete event. The later event must lead to the replacement being
// watched.
func TestKqueueReplaceEventOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a")
	os.WriteFile(path, []byte("old"), 0o644)
	os.WriteFile(filepath.Join(dir, "b"), []byte("other"), 0o644)
	k, err := newKq(func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(k.done); k.close() }()
	if err := k.add(1, dir); err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	d := k.dirs[1]
	dirFD := d.fd
	k.mu.Unlock()

	tmp := filepath.Join(dir, ".tmp")
	os.WriteFile(tmp, []byte("new"), 0o644)
	if err := os.Rename(tmp, path); err != nil { // atomic replace
		t.Fatal(err)
	}
	var dirEvs, fileEvs []syscall.Kevent_t
	buf := make([]syscall.Kevent_t, 32)
	ts := syscall.NsecToTimespec(int64(200 * time.Millisecond))
	for len(dirEvs) == 0 || len(fileEvs) == 0 {
		n, err := syscall.Kevent(k.kq, nil, buf, &ts)
		if err != nil || n == 0 {
			t.Skipf("kernel delivered dir=%d file=%d events (err %v)", len(dirEvs), len(fileEvs), err)
		}
		for _, ev := range buf[:n] {
			if ev.Filter != syscall.EVFILT_VNODE {
				continue
			}
			if int(ev.Ident) == dirFD {
				dirEvs = append(dirEvs, ev)
			} else {
				fileEvs = append(fileEvs, ev)
			}
		}
	}
	k.process(dirEvs) // resync: "a" still names the old vnode's watch
	k.process(fileEvs)

	k.mu.Lock()
	defer k.mu.Unlock()
	idx := d.fileIndex([]byte("a"))
	if idx < 0 || d.files[idx].fd < 0 {
		t.Fatal(`no watch on "a" after its old file went away`)
	}
	f := &d.files[idx]
	var fst, pst syscall.Stat_t
	if err := syscall.Fstat(f.fd, &fst); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Stat(path, &pst); err != nil {
		t.Fatal(err)
	}
	if fst.Ino != pst.Ino {
		t.Errorf(`the watch on "a" is on inode %d, the file is inode %d`, fst.Ino, pst.Ino)
	}
	if len(d.files) != 2 {
		t.Errorf("%d file watches, want 2", len(d.files))
	}
}

func TestServiceWatcherReusesListing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	s := newService(func() {}, func(path string, l *Listing) error { calls.Add(1); return listDir(path, l) }, newWatcher, defaultDebounce)
	defer s.Close()
	s.List(1, dir, SortName, false)
	s.Watch(1, dir)
	var held []Result
	collect := func(want int64) {
		waitFor(t, "listing and file watches", 2*time.Second, func() bool {
			held = s.Poll(held)
			s.mu.Lock()
			w := s.w
			s.mu.Unlock()
			if w == nil {
				return false
			}
			k := w.(*kqWatcher)
			k.mu.Lock()
			defer k.mu.Unlock()
			return len(held) > 0 && int64(k.nfiles) == want
		})
		for _, r := range held {
			if r.Kind != ResultListing || r.Listing.Err != nil {
				t.Fatalf("unexpected result: %+v", r)
			}
			s.Release(r.Listing, r.Lease)
		}
		held = held[:0]
	}
	collect(1)
	if calls.Load() != 1 {
		t.Fatalf("initial listing ran %d times", calls.Load())
	}
	if err := os.WriteFile(filepath.Join(dir, "b"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	collect(2)
	if calls.Load() != 2 {
		t.Fatalf("structural change ran %d listings", calls.Load()-1)
	}
	s.mu.Lock()
	k := s.w.(*kqWatcher)
	s.mu.Unlock()
	if k.lst.bufferBytes() != 0 {
		t.Fatal("service watcher retained its own listing")
	}
}

func TestKqueueFileRegistrationReusesScratch(t *testing.T) {
	dir := tempDirWith(t, 40)
	var l Listing
	if err := ListDir(dir, &l); err != nil {
		t.Fatal(err)
	}
	k, err := newKq(func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { close(k.done); k.close() }()
	if err := k.addListingWatch(1, dir, 1); err != nil {
		t.Fatal(err)
	}
	k.mu.Lock()
	d := k.dirs[1]
	register := func() {
		for i := range d.files {
			f := &d.files[i]
			if f.fd >= 0 {
				syscall.Close(f.fd)
				delete(k.byFD, f.fd)
				f.fd = -1
				k.nfiles--
				d.nfiles--
			}
		}
		k.syncLocked(d, &l)
	}
	register()
	register()
	n := testing.AllocsPerRun(30, register)
	files := d.nfiles
	k.mu.Unlock()
	if files != 40 || n != 0 {
		t.Fatalf("registration: %d files, %v allocations", files, n)
	}
}
