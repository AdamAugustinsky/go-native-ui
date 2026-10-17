//go:build windows

package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// winHarness wraps a watcher with a wake channel the way the Service uses it.
type winHarness struct {
	t    *testing.T
	w    *winWatcher
	wake chan struct{}
	got  []Change
}

func winNewHarness(t *testing.T) *winHarness {
	t.Helper()
	h := &winHarness{t: t, wake: make(chan struct{}, 1)}
	w, err := newWatcher(func() {
		select {
		case h.wake <- struct{}{}:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	h.w = w.(*winWatcher)
	return h // tests defer h.w.close() themselves: it must run before t.TempDir's cleanup
}

// flush lets in-flight notifications from earlier steps land, then forgets them.
func (h *winHarness) flush() {
	h.t.Helper()
	time.Sleep(150 * time.Millisecond)
	h.got = h.w.drain(h.got[:0])
	select {
	case <-h.wake:
	default:
	}
}

// expect waits for a wake that delivers Change{id, kind} and returns how
// long that took. Other changes drained along the way are kept in h.got.
func (h *winHarness) expect(id uint32, kind ChangeKind, within time.Duration) time.Duration {
	h.t.Helper()
	start := time.Now()
	h.got = h.got[:0]
	for {
		left := within - time.Since(start)
		if left <= 0 {
			h.t.Fatalf("no Change{%d, %d} within %v (got %v)", id, kind, within, h.got)
		}
		select {
		case <-h.wake:
		case <-time.After(left):
			h.t.Fatalf("no wake with Change{%d, %d} within %v (got %v)", id, kind, within, h.got)
		}
		h.got = h.w.drain(h.got)
		for _, c := range h.got {
			if c.ID == id && c.Kind == kind {
				return time.Since(start)
			}
		}
	}
}

// quiet fails if anything wakes or is queued within d.
func (h *winHarness) quiet(d time.Duration) {
	h.t.Helper()
	select {
	case <-h.wake:
		h.t.Fatalf("unexpected wake: %v", h.w.drain(nil))
	case <-time.After(d):
	}
	if c := h.w.drain(nil); len(c) != 0 {
		h.t.Fatalf("unexpected changes without a wake: %v", c)
	}
}

// count is the number of watched directories, and of watches including
// parent watches, as last published by the worker (add and remove are
// synchronous, so it is current after they return).
func (w *winWatcher) count() (dirs, total int) {
	return int(w.nDirs.Load()), int(w.nAll.Load())
}

func winMustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWinWatchChanges(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	dir := t.TempDir()
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "a.txt")
	steps := []struct {
		name string
		do   func()
	}{
		{"create", func() { winMustWrite(t, f, "x") }},
		{"modify", func() {
			g, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			g.WriteString("more")
			g.Close()
		}},
		{"attributes", func() { winSetAttrs(t, f, syscall.FILE_ATTRIBUTE_HIDDEN) }},
		{"rename", func() {
			if err := os.Rename(f, filepath.Join(dir, "b.txt")); err != nil {
				t.Fatal(err)
			}
		}},
		{"delete", func() {
			winSetAttrs(t, filepath.Join(dir, "b.txt"), syscall.FILE_ATTRIBUTE_NORMAL)
			os.Remove(filepath.Join(dir, "b.txt"))
		}},
		{"mkdir", func() { os.Mkdir(filepath.Join(dir, "sub"), 0o755) }},
		{"rmdir", func() { os.Remove(filepath.Join(dir, "sub")) }},
	}
	for _, s := range steps {
		h.flush()
		s.do()
		d := h.expect(1, ChangeDir, 250*time.Millisecond)
		t.Logf("%-10s -> ChangeDir after %v", s.name, d)
	}
	// Moving a file in from elsewhere and out again.
	other := t.TempDir()
	winMustWrite(t, filepath.Join(other, "in.txt"), "x")
	h.flush()
	os.Rename(filepath.Join(other, "in.txt"), filepath.Join(dir, "in.txt"))
	h.expect(1, ChangeDir, 250*time.Millisecond)
	h.flush()
	os.Rename(filepath.Join(dir, "in.txt"), filepath.Join(other, "in.txt"))
	h.expect(1, ChangeDir, 250*time.Millisecond)
}

// Paths beyond MAX_PATH are watched through the \\?\ form.
func TestWinWatchLongPath(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	deep := t.TempDir()
	for len(deep) < 300 {
		deep = filepath.Join(deep, strings.Repeat("w", 40))
	}
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(1, deep); err != nil {
		t.Fatal(err)
	}
	h.flush()
	winMustWrite(t, filepath.Join(deep, "x.txt"), "x")
	h.expect(1, ChangeDir, 250*time.Millisecond)
}

// Changes two levels below the watched directory are not reported (the
// watch is not recursive; a change to a direct child's own entry is).
func TestWinWatchNotRecursive(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	dir := t.TempDir()
	deeper := filepath.Join(dir, "sub", "deeper")
	if err := os.MkdirAll(deeper, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	winMustWrite(t, filepath.Join(deeper, "deep.txt"), "x")
	h.quiet(400 * time.Millisecond)
}

func TestWinWatchIdle(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	dir := t.TempDir()
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	h.quiet(600 * time.Millisecond)
}

func TestWinWatchCoalesces(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	dir := t.TempDir()
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	for i := 0; i < 2000; i++ {
		winMustWrite(t, filepath.Join(dir, fmt.Sprintf("f-%04d", i)), "x")
	}
	// 2000 creates overflow or fill the 32 KiB buffer; either is a valid
	// outcome, and each wake delivers at most one change per id.
	var kinds [3]int
	deadline := time.After(3 * time.Second)
	idle := time.NewTimer(600 * time.Millisecond)
loop:
	for {
		select {
		case <-h.wake:
			c := h.w.drain(nil)
			if len(c) > 1 {
				t.Fatalf("one directory queued %d changes in a single drain: %v", len(c), c)
			}
			for _, x := range c {
				kinds[x.Kind]++
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(600 * time.Millisecond)
		case <-idle.C:
			break loop
		case <-deadline:
			t.Fatal("notifications still arriving 3 s after the burst")
		}
	}
	t.Logf("2000 creates: %d ChangeDir, %d ChangeOverflow, %d ChangeGone", kinds[ChangeDir], kinds[ChangeOverflow], kinds[ChangeGone])
	if kinds[ChangeDir]+kinds[ChangeOverflow] == 0 || kinds[ChangeGone] != 0 {
		t.Errorf("unexpected outcome: %v", kinds)
	}
}

// winGoneLimit is how long a deleted or moved directory may take to be
// reported (the shared watch tests use the same limit): the directory's own
// read fails at once on deletion, and its parent's watch reports a rename.
// The 5 s periodic path check is only a backstop and must not be needed.
const winGoneLimit = time.Second

func TestWinWatchGone(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	parent := t.TempDir()
	dir := filepath.Join(parent, "watched")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	winMustWrite(t, filepath.Join(dir, "x"), "x")
	if err := h.w.add(7, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	d := h.expect(7, ChangeGone, winGoneLimit)
	t.Logf("RemoveAll -> ChangeGone after %v", d)
	h.flush()
	if n, total := h.w.count(); n != 0 || total != 0 {
		t.Errorf("%d watches (%d with parents) still registered after Gone", n, total)
	}
	// The watch is over and the directory is really gone (not delete-pending
	// behind a leaked handle); the same path can be created and watched again.
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(7, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	winMustWrite(t, filepath.Join(dir, "y"), "y")
	h.expect(7, ChangeDir, 250*time.Millisecond)
}

func TestWinWatchMovedAway(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	parent := t.TempDir()
	dir := filepath.Join(parent, "watched")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(3, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	if err := os.Rename(dir, filepath.Join(parent, "elsewhere")); err != nil {
		ostCannot(t, "Windows refuses to rename a watched directory here (%v); the move-away check is not exercised", err)
	}
	d := h.expect(3, ChangeGone, winGoneLimit)
	t.Logf("rename -> ChangeGone after %v", d)
}

func TestWinWatchTwoIDsOneDirectory(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	dir := t.TempDir()
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(2, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	winMustWrite(t, filepath.Join(dir, "a"), "a")
	deadline := time.Now().Add(250 * time.Millisecond)
	seen := map[uint32]bool{}
	for !(seen[1] && seen[2]) && time.Now().Before(deadline) {
		select {
		case <-h.wake:
		case <-time.After(10 * time.Millisecond):
		}
		for _, c := range h.w.drain(nil) {
			seen[c.ID] = true
		}
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("both ids must be notified, got %v", seen)
	}
	h.w.remove(1)
	h.flush()
	winMustWrite(t, filepath.Join(dir, "b"), "b")
	h.expect(2, ChangeDir, 250*time.Millisecond)
	for _, c := range h.got {
		if c.ID == 1 {
			t.Fatalf("removed id 1 still notified: %v", h.got)
		}
	}
	h.w.remove(2)
	h.flush()
	winMustWrite(t, filepath.Join(dir, "c"), "c")
	h.quiet(300 * time.Millisecond)
	h.w.remove(2) // idempotent
	h.w.remove(99)
}

func TestWinWatchRemoveDropsPendingAndReleasesDirectory(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	parent := t.TempDir()
	dir := filepath.Join(parent, "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	winMustWrite(t, filepath.Join(dir, "a"), "a")
	select {
	case <-h.wake:
	case <-time.After(time.Second):
		t.Fatal("no wake")
	}
	h.w.remove(1)
	if c := h.w.drain(nil); len(c) != 0 {
		t.Fatalf("changes for a removed id: %v", c)
	}
	// With the watch gone, the directory can be renamed and deleted.
	os.Remove(filepath.Join(dir, "a"))
	if err := os.Rename(dir, filepath.Join(parent, "e")); err != nil {
		t.Fatalf("directory still held after remove: %v", err)
	}
}

func TestWinWatchReaddReplaces(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	a, b := t.TempDir(), t.TempDir()
	if err := h.w.add(1, a); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(1, b); err != nil {
		t.Fatal(err)
	}
	if n, _ := h.w.count(); n != 1 {
		t.Fatalf("%d watches after re-adding an id", n)
	}
	h.flush()
	winMustWrite(t, filepath.Join(a, "x"), "x")
	h.quiet(300 * time.Millisecond)
	winMustWrite(t, filepath.Join(b, "x"), "x")
	h.expect(1, ChangeDir, 250*time.Millisecond)
}

func TestWinWatchLimit(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	base := t.TempDir()
	dirs := make([]string, winMaxWatches+1)
	for i := range dirs {
		dirs[i] = filepath.Join(base, fmt.Sprintf("d%02d", i))
		if err := os.Mkdir(dirs[i], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The directories share one parent, whose single watch counts too.
	added := 0
	for i, d := range dirs {
		if err := h.w.add(uint32(i), d); err != nil {
			break
		}
		added++
	}
	n, total := h.w.count()
	t.Logf("%d directories watched with %d watches in total", n, total)
	if added != winMaxWatches-1 || n != added || total != winMaxWatches {
		t.Fatalf("added %d directories (%d watched, %d watches), want %d directories in %d watches",
			added, n, total, winMaxWatches-1, winMaxWatches)
	}
	// The first and the last of the wait set both deliver.
	h.flush()
	winMustWrite(t, filepath.Join(dirs[added-1], "x"), "x")
	h.expect(uint32(added-1), ChangeDir, 250*time.Millisecond)
	h.flush()
	winMustWrite(t, filepath.Join(dirs[0], "x"), "x")
	h.expect(0, ChangeDir, 250*time.Millisecond)
	h.w.remove(5)
	if err := h.w.add(1000, dirs[winMaxWatches]); err != nil {
		t.Fatalf("add after a remove: %v", err)
	}
}

// Directories under different parents each take a parent watch while there
// is room, and still work without one when the wait set is nearly full.
func TestWinWatchParentsAreBestEffort(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	var dirs []string
	for i := 0; i < 32; i++ { // 31 get a parent watch (62 watches); the 32nd fits only alone
		d := filepath.Join(t.TempDir(), "child")
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, d)
	}
	for i, d := range dirs {
		if err := h.w.add(uint32(i), d); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	n, total := h.w.count()
	t.Logf("%d directories with distinct parents: %d watches in total", n, total)
	if n != len(dirs) || total != winMaxWatches {
		t.Fatalf("%d directories, %d watches", n, total)
	}
	h.flush()
	last := len(dirs) - 1 // the one without a parent watch
	if err := h.w.add(999, filepath.Join(t.TempDir())); err == nil {
		t.Error("the 64th watch was accepted")
	}
	winMustWrite(t, filepath.Join(dirs[last], "x"), "x")
	h.expect(uint32(last), ChangeDir, 250*time.Millisecond)
	// Gone is still reported for a directory without a parent watch.
	if err := os.RemoveAll(dirs[last]); err != nil {
		t.Fatal(err)
	}
	h.expect(uint32(last), ChangeGone, winGoneLimit)
}

func TestWinWatchAddErrors(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	dir := t.TempDir()
	if err := h.w.add(1, filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Errorf("missing dir: %v", err)
	}
	f := filepath.Join(dir, "file")
	winMustWrite(t, f, "x")
	if err := h.w.add(1, f); err == nil {
		t.Error("watching a regular file succeeded")
	}
	if err := h.w.add(1, dir+"\x00x"); err == nil {
		t.Error("path with NUL succeeded")
	}
	if n, total := h.w.count(); n != 0 || total != 0 {
		t.Errorf("%d watches (%d with parents) registered after failed adds", n, total)
	}
	h.w.close()
	if err := h.w.add(1, dir); err == nil {
		t.Error("add after close succeeded")
	}
}

func TestWinWatchClose(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	parent := t.TempDir()
	dir := filepath.Join(parent, "d")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	h.w.close()
	select {
	case <-h.w.done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker still running after close")
	}
	h.w.close() // idempotent
	h.w.remove(1)
	h.w.drain(nil)
	winMustWrite(t, filepath.Join(dir, "x"), "x")
	h.quiet(200 * time.Millisecond)
	os.Remove(filepath.Join(dir, "x"))
	if err := os.Rename(dir, filepath.Join(parent, "e")); err != nil {
		t.Fatalf("directory still held after close: %v", err)
	}
}

func TestWinWatchDrainZeroAlloc(t *testing.T) {
	w := &winWatcher{}
	dst := make([]Change, 0, 8)
	post := func() {
		w.post(1, ChangeDir)
		w.post(2, ChangeGone)
		w.post(1, ChangeOverflow)
	}
	post() // grow pend once
	dst = w.drain(dst[:0])
	if len(dst) != 2 || dst[0].Kind != ChangeOverflow {
		t.Fatalf("drain: %v", dst)
	}
	runtime.GC()
	var a, b runtime.MemStats
	runtime.ReadMemStats(&a)
	for i := 0; i < 1000; i++ {
		post()
		dst = w.drain(dst[:0])
		dst = w.drain(dst[:0])
	}
	runtime.ReadMemStats(&b)
	if n := b.Mallocs - a.Mallocs; n > 5 {
		t.Errorf("drain/post allocate: %d mallocs over 1000 rounds", n)
	}
}

func TestWinOpenPathErrors(t *testing.T) {
	if err := openPath(filepath.Join(t.TempDir(), "does-not-exist.txt")); err == nil {
		t.Error("opening a missing file reported no error")
	} else {
		t.Logf("missing file: %v", err)
	}
	if err := openPath("a\x00b"); err == nil {
		t.Error("path with NUL accepted")
	}
}

// add and remove (which cancel reads and close handles on the worker) never
// stall drain, the call the UI thread makes, and handles are never closed
// under a wait: many add/remove cycles with events flying must neither hang
// nor lose a watch that stays.
func TestWinWatchChurnDoesNotStallDrain(t *testing.T) {
	h := winNewHarness(t)
	defer h.w.close()
	keep, noisy := t.TempDir(), t.TempDir()
	if err := h.w.add(1, keep); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // events in the churned directory
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			os.WriteFile(filepath.Join(noisy, "f"), []byte{byte(i)}, 0o644)
			time.Sleep(time.Millisecond)
		}
	}()
	var worst time.Duration
	var drains int
	go func() { // the UI thread
		defer wg.Done()
		var dst []Change
		for {
			select {
			case <-stop:
				return
			default:
			}
			t0 := time.Now()
			dst = h.w.drain(dst[:0])
			if d := time.Since(t0); d > worst {
				worst = d
			}
			drains++
			time.Sleep(200 * time.Microsecond)
		}
	}()
	t0 := time.Now()
	const cycles = 60
	for i := 0; i < cycles; i++ {
		id := uint32(10 + i%5)
		if err := h.w.add(id, noisy); err != nil {
			t.Fatalf("cycle %d: %v", i, err)
		}
		if i%3 == 0 {
			h.w.remove(id)
		}
	}
	for id := uint32(10); id < 15; id++ {
		h.w.remove(id)
	}
	elapsed := time.Since(t0)
	close(stop)
	wg.Wait()
	t.Logf("%d add/remove cycles in %v (%v each); %d drains, worst %v", cycles, elapsed, elapsed/cycles, drains, worst)
	if worst > 250*time.Millisecond {
		t.Errorf("drain stalled for %v while the worker was adding and removing", worst)
	}
	if n, total := h.w.count(); n != 1 || total > 2 {
		t.Errorf("%d watches (%d with parents) left, want the one kept", n, total)
	}
	h.flush()
	winMustWrite(t, filepath.Join(keep, "still"), "x")
	h.expect(1, ChangeDir, 250*time.Millisecond)
}

// close() during add/remove traffic answers every caller and stops the worker.
func TestWinWatchCloseDuringTraffic(t *testing.T) {
	h := winNewHarness(t)
	dirs := make([]string, 4)
	for i := range dirs {
		dirs[i] = t.TempDir()
	}
	var wg sync.WaitGroup
	for i := range dirs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if h.w.add(uint32(i), dirs[i]) != nil {
					return // closed
				}
				h.w.remove(uint32(i))
			}
		}(i)
	}
	time.Sleep(50 * time.Millisecond)
	h.w.close()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("add/remove callers still blocked after close")
	}
	if err := h.w.add(1, dirs[0]); err == nil {
		t.Error("add after close succeeded")
	}
	h.w.remove(1)
}
