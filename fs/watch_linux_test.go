//go:build linux

package fs

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// lxHarness wraps a watcher with a wake channel the way the Service uses it.
type lxHarness struct {
	t    *testing.T
	w    *lxWatcher
	wake chan struct{}
	got  []Change
}

func lxNewHarness(t *testing.T) *lxHarness {
	t.Helper()
	h := &lxHarness{t: t, wake: make(chan struct{}, 1)}
	w, err := newWatcher(func() {
		select {
		case h.wake <- struct{}{}:
		default:
		}
	})
	if err != nil {
		ostCannot(t, "inotify unavailable: %v", err)
	}
	h.w = w.(*lxWatcher)
	t.Cleanup(h.w.close)
	return h
}

// flush lets in-flight events from earlier steps land, then forgets them.
func (h *lxHarness) flush() {
	h.t.Helper()
	time.Sleep(100 * time.Millisecond)
	h.got = h.w.drain(h.got[:0])
	select {
	case <-h.wake:
	default:
	}
}

// expect waits for a wake that delivers Change{id, kind} and returns how
// long that took. Other changes drained along the way are kept in h.got.
func (h *lxHarness) expect(id uint32, kind ChangeKind, within time.Duration) time.Duration {
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
func (h *lxHarness) quiet(d time.Duration) {
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

func lxMustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxWatchChanges(t *testing.T) {
	h := lxNewHarness(t)
	dir := t.TempDir()
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "a.txt")
	steps := []struct {
		name string
		do   func()
	}{
		{"create", func() { lxMustWrite(t, f, "x") }},
		{"modify", func() {
			g, err := os.OpenFile(f, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			g.WriteString("more")
			g.Close()
		}},
		{"chmod", func() { os.Chmod(f, 0o600) }},
		{"rename", func() {
			if err := os.Rename(f, filepath.Join(dir, "b.txt")); err != nil {
				t.Fatal(err)
			}
		}},
		{"delete", func() { os.Remove(filepath.Join(dir, "b.txt")) }},
		{"mkdir", func() { os.Mkdir(filepath.Join(dir, "sub"), 0o755) }},
		{"rmdir", func() { os.Remove(filepath.Join(dir, "sub")) }},
		{"symlink", func() { os.Symlink("nowhere", filepath.Join(dir, "ln")) }},
	}
	for _, s := range steps {
		h.flush()
		s.do()
		d := h.expect(1, ChangeDir, 250*time.Millisecond)
		t.Logf("%-8s -> ChangeDir after %v", s.name, d)
	}
	// Moving a file in from elsewhere and out again.
	other := t.TempDir()
	lxMustWrite(t, filepath.Join(other, "in.txt"), "x")
	h.flush()
	os.Rename(filepath.Join(other, "in.txt"), filepath.Join(dir, "in.txt"))
	h.expect(1, ChangeDir, 250*time.Millisecond)
	h.flush()
	os.Rename(filepath.Join(dir, "in.txt"), filepath.Join(other, "in.txt"))
	h.expect(1, ChangeDir, 250*time.Millisecond)
}

// Changes below the watched directory are not reported (not recursive).
func TestLinuxWatchNotRecursive(t *testing.T) {
	h := lxNewHarness(t)
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	lxMustWrite(t, filepath.Join(sub, "deep.txt"), "x")
	h.quiet(300 * time.Millisecond)
}

func TestLinuxWatchIdle(t *testing.T) {
	h := lxNewHarness(t)
	dir := t.TempDir()
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	h.quiet(600 * time.Millisecond)
}

func TestLinuxWatchCoalesces(t *testing.T) {
	h := lxNewHarness(t)
	dir := t.TempDir()
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	for i := 0; i < 2000; i++ {
		lxMustWrite(t, filepath.Join(dir, fmt.Sprintf("f-%04d", i)), "x")
	}
	h.expect(1, ChangeDir, 250*time.Millisecond)
	var wakes int
	deadline := time.After(500 * time.Millisecond)
loop:
	for {
		select {
		case <-h.wake:
			wakes++
			c := h.w.drain(nil)
			if len(c) > 1 {
				t.Fatalf("one directory queued %d changes in a single drain: %v", len(c), c)
			}
		case <-deadline:
			break loop
		}
	}
	t.Logf("2000 creates: %d further wakes", wakes)
}

func TestLinuxWatchGone(t *testing.T) {
	h := lxNewHarness(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "watched")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lxMustWrite(t, filepath.Join(dir, "x"), "x")
	if err := h.w.add(7, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	d := h.expect(7, ChangeGone, time.Second)
	t.Logf("RemoveAll -> ChangeGone after %v", d)
	h.flush()
	// The watch is over; the same path can be watched again.
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(7, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	lxMustWrite(t, filepath.Join(dir, "y"), "y")
	h.expect(7, ChangeDir, 250*time.Millisecond)
}

func TestLinuxWatchMovedAway(t *testing.T) {
	h := lxNewHarness(t)
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
		t.Fatal(err)
	}
	h.expect(3, ChangeGone, time.Second)
	// Changes inside the moved directory no longer reach us.
	h.flush()
	lxMustWrite(t, filepath.Join(parent, "elsewhere", "z"), "z")
	h.quiet(300 * time.Millisecond)
}

func TestLinuxWatchTwoIDsOneDirectory(t *testing.T) {
	h := lxNewHarness(t)
	dir := t.TempDir()
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(2, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	lxMustWrite(t, filepath.Join(dir, "a"), "a")
	h.expect(1, ChangeDir, 250*time.Millisecond)
	seen := map[uint32]bool{}
	for _, c := range h.got {
		seen[c.ID] = true
	}
	if !seen[2] {
		time.Sleep(50 * time.Millisecond)
		for _, c := range h.w.drain(nil) {
			seen[c.ID] = true
		}
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("both ids must be notified, got %v", seen)
	}
	// Removing one id keeps the kernel watch for the other.
	h.w.remove(1)
	h.flush()
	lxMustWrite(t, filepath.Join(dir, "b"), "b")
	h.expect(2, ChangeDir, 250*time.Millisecond)
	for _, c := range h.got {
		if c.ID == 1 {
			t.Fatalf("removed id 1 still notified: %v", h.got)
		}
	}
	h.w.remove(2)
	h.flush()
	lxMustWrite(t, filepath.Join(dir, "c"), "c")
	h.quiet(300 * time.Millisecond)
	// Removing the last id also removes the kernel watch; remove is idempotent.
	h.w.remove(2)
	h.w.remove(99)
}

func TestLinuxWatchRemoveDropsPending(t *testing.T) {
	h := lxNewHarness(t)
	dir := t.TempDir()
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	h.flush()
	lxMustWrite(t, filepath.Join(dir, "a"), "a")
	select {
	case <-h.wake:
	case <-time.After(time.Second):
		t.Fatal("no wake")
	}
	h.w.remove(1)
	if c := h.w.drain(nil); len(c) != 0 {
		t.Fatalf("changes for a removed id: %v", c)
	}
}

func TestLinuxWatchReaddReplaces(t *testing.T) {
	h := lxNewHarness(t)
	a, b := t.TempDir(), t.TempDir()
	if err := h.w.add(1, a); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(1, b); err != nil {
		t.Fatal(err)
	}
	h.flush()
	lxMustWrite(t, filepath.Join(a, "x"), "x")
	h.quiet(300 * time.Millisecond)
	lxMustWrite(t, filepath.Join(b, "x"), "x")
	h.expect(1, ChangeDir, 250*time.Millisecond)
}

func TestLinuxWatchFollowsSymlinkedDirectory(t *testing.T) {
	h := lxNewHarness(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := h.w.add(1, link); err != nil {
		t.Fatal(err)
	}
	h.flush()
	lxMustWrite(t, filepath.Join(real, "x"), "x")
	h.expect(1, ChangeDir, 250*time.Millisecond)
}

func TestLinuxWatchAddErrors(t *testing.T) {
	h := lxNewHarness(t)
	dir := t.TempDir()
	if err := h.w.add(1, filepath.Join(dir, "missing")); !os.IsNotExist(err) {
		t.Errorf("missing dir: %v", err)
	}
	f := filepath.Join(dir, "file")
	lxMustWrite(t, f, "x")
	if err := h.w.add(1, f); err == nil {
		t.Error("watching a regular file succeeded (IN_ONLYDIR)")
	}
	if err := h.w.add(1, dir+"\x00x"); err == nil {
		t.Error("path with NUL succeeded")
	}
	h.w.close()
	if err := h.w.add(1, dir); err == nil {
		t.Error("add after close succeeded")
	}
}

func TestLinuxWatchClose(t *testing.T) {
	h := lxNewHarness(t)
	dir := t.TempDir()
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
	lxMustWrite(t, filepath.Join(dir, "x"), "x")
	h.quiet(200 * time.Millisecond)
}

// lxEvent encodes one struct inotify_event with a name padded the way the
// kernel pads it (to a multiple of 16 bytes here; any padding is legal).
func lxEvent(wd int32, mask uint32, name string) []byte {
	n := 0
	if name != "" {
		n = (len(name) + 1 + 15) &^ 15
	}
	b := make([]byte, 16+n)
	binary.NativeEndian.PutUint32(b[0:], uint32(wd))
	binary.NativeEndian.PutUint32(b[4:], mask)
	binary.NativeEndian.PutUint32(b[12:], uint32(n))
	copy(b[16:], name)
	return b
}

// Event decoding on synthetic buffers, including cases the kernel makes
// rare in a test (queue overflow, unmount).
func TestLinuxWatchDecode(t *testing.T) {
	newW := func() *lxWatcher {
		return &lxWatcher{ifd: -1, efd: -1, ws: []lxWatch{{1, 10}, {2, 10}, {3, 11}}}
	}
	cat := func(parts ...[]byte) (b []byte) {
		for _, p := range parts {
			b = append(b, p...)
		}
		return
	}
	drain := func(w *lxWatcher) map[uint32]ChangeKind {
		m := map[uint32]ChangeKind{}
		for _, c := range w.drain(nil) {
			if _, dup := m[c.ID]; dup {
				t.Errorf("id %d queued twice", c.ID)
			}
			m[c.ID] = c.Kind
		}
		return m
	}

	w := newW()
	q := w.handle(cat(lxEvent(10, lxInCreate, "new.txt"), lxEvent(10, lxInModify, "new.txt"), lxEvent(11, lxInMovedTo, "é")))
	if m := drain(w); !q || len(m) != 3 || m[1] != ChangeDir || m[2] != ChangeDir || m[3] != ChangeDir {
		t.Errorf("plain events: queued %v, %v", q, m)
	}
	if w.handle(lxEvent(99, lxInCreate, "x")) {
		t.Error("event for an unknown descriptor queued a change")
	}
	if len(w.pend) != 0 {
		t.Error("unknown descriptor left pending changes")
	}

	w = newW()
	w.handle(lxEvent(-1, lxInQOverflow, ""))
	if m := drain(w); len(m) != 3 || m[1] != ChangeOverflow || m[2] != ChangeOverflow || m[3] != ChangeOverflow {
		t.Errorf("overflow: %v", m)
	}

	w = newW()
	w.handle(cat(lxEvent(10, lxInCreate, "a"), lxEvent(10, lxInDeleteSelf, ""), lxEvent(10, lxInIgnored, ""), lxEvent(11, lxInDelete, "b")))
	if m := drain(w); len(m) != 3 || m[1] != ChangeGone || m[2] != ChangeGone || m[3] != ChangeDir {
		t.Errorf("delete-self: %v", m)
	}
	if len(w.ws) != 1 || w.ws[0].id != 3 {
		t.Errorf("gone ids still registered: %v", w.ws)
	}

	w = newW()
	w.handle(cat(lxEvent(11, lxInMoveSelf, ""), lxEvent(10, lxInUnmount, ""), lxEvent(11, lxInCreate, "late")))
	if m := drain(w); m[3] != ChangeGone || m[1] != ChangeGone {
		t.Errorf("move-self / unmount: %v", m)
	}
}

func TestLinuxWatchDrainZeroAlloc(t *testing.T) {
	w := &lxWatcher{ifd: -1, efd: -1}
	dst := make([]Change, 0, 8)
	post := func() {
		w.pmu.Lock()
		w.post(1, ChangeDir)
		w.post(2, ChangeGone)
		w.post(1, ChangeOverflow)
		w.pmu.Unlock()
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

// A producer that never lets the inotify queue run dry must neither delay
// the announcement of changes nor keep close() from stopping the worker.
func TestLinuxWatchSustainedEvents(t *testing.T) {
	h := lxNewHarness(t)
	dir := t.TempDir()
	if err := h.w.add(1, dir); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		p := filepath.Join(dir, "busy")
		for {
			select {
			case <-stop:
				return
			default:
			}
			os.WriteFile(p, []byte("x"), 0o644) // create/modify events, as fast as possible
			os.Remove(p)
		}
	}()
	defer func() { close(stop); <-finished }()

	// Announced while the producer is still running, many times over.
	wakes := 0
	deadline := time.After(1500 * time.Millisecond)
	first := time.Now()
loop:
	for {
		select {
		case <-h.wake:
			if wakes == 0 {
				t.Logf("first wake after %v", time.Since(first))
				if d := time.Since(first); d > 250*time.Millisecond {
					t.Errorf("first announcement took %v under sustained events", d)
				}
			}
			wakes++
			h.w.drain(nil)
		case <-deadline:
			break loop
		}
	}
	t.Logf("%d wakes in 1.5 s of continuous events", wakes)
	if wakes < 10 {
		t.Errorf("only %d wakes in 1.5 s of continuous events", wakes)
	}
	closed := make(chan struct{})
	go func() { h.w.close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close() did not finish while events kept coming")
	}
}
