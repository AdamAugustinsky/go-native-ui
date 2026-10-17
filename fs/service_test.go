package fs

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeList serves paths of the form "n:<count>:<tag>": count entries named
// "<tag>-<i>" (scrambled order, every 5th a directory). "block:<tag>" waits
// on gate first. "fail" fails.
type fakeFS struct {
	gate    chan struct{}
	started atomic.Int64
	calls   atomic.Int64
}

func (f *fakeFS) list(path string, l *Listing) error {
	f.calls.Add(1)
	f.started.Add(1)
	if strings.HasPrefix(path, "block:") {
		<-f.gate
		path = "n:3:" + path[len("block:"):]
	}
	if path == "fail" {
		return errors.New("fake failure")
	}
	_, rest, _ := strings.Cut(path, ":")
	count, tag, _ := strings.Cut(rest, ":")
	n, _ := strconv.Atoi(count)
	for i := 0; i < n; i++ {
		j := (i * 7) % n
		if n%7 == 0 {
			j = i
		}
		off := len(l.Names)
		l.Names = append(l.Names, tag...)
		l.Names = append(l.Names, '-')
		l.Names = strconv.AppendInt(l.Names, int64(j), 10)
		e := Entry{NameOff: uint32(off), NameLen: uint16(len(l.Names) - off), Size: int64(j), MTime: int64(j)}
		if j%5 == 0 {
			e.Kind = KindDir
		}
		l.Entries = append(l.Entries, e)
	}
	return nil
}

// checkFake verifies a delivered listing is intact for its request path.
func checkFake(t testing.TB, l *Listing, path string) {
	t.Helper()
	parts := strings.SplitN(path, ":", 3)
	n, _ := strconv.Atoi(parts[1])
	if len(l.Entries) != n || len(l.Order) != n {
		t.Fatalf("%s: %d entries, %d order, want %d", path, len(l.Entries), len(l.Order), n)
	}
	prevDir := true
	for k, idx := range l.Order {
		e := l.Entries[idx]
		name := string(l.Name(int(idx)))
		if !strings.HasPrefix(name, parts[2]+"-") {
			t.Fatalf("%s: entry %d is %q (buffer shared with another listing?)", path, k, name)
		}
		if !prevDir && e.Kind == KindDir {
			t.Fatalf("%s: directory after file at %d", path, k)
		}
		prevDir = e.Kind == KindDir
	}
}

type fakeWatcher struct {
	mu      sync.Mutex
	wake    func()
	pending []Change
	added   []string
	removed []uint32
	addErr  error
	addGate chan struct{}                      // if set, add waits for it to close
	addFn   func(id uint32, path string) error // if set, decides add's outcome (may block)
	closed  bool
}

func (w *fakeWatcher) add(id uint32, path string) error {
	w.mu.Lock()
	gate, fn := w.addGate, w.addFn
	w.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if fn != nil {
		if err := fn(id, path); err != nil {
			return err
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.addErr != nil {
		return w.addErr
	}
	w.added = append(w.added, fmt.Sprintf("%d=%s", id, path))
	return nil
}
func (w *fakeWatcher) remove(id uint32) {
	w.mu.Lock()
	w.removed = append(w.removed, id)
	n := 0 // the watcher contract: nothing of a removed watch is delivered afterwards
	for _, c := range w.pending {
		if c.ID != id {
			w.pending[n] = c
			n++
		}
	}
	w.pending = w.pending[:n]
	w.mu.Unlock()
}
func (w *fakeWatcher) drain(dst []Change) []Change {
	w.mu.Lock()
	dst = append(dst, w.pending...)
	w.pending = w.pending[:0]
	w.mu.Unlock()
	return dst
}
func (w *fakeWatcher) close() { w.mu.Lock(); w.closed = true; w.mu.Unlock() }
func (w *fakeWatcher) emit(cs ...Change) {
	w.mu.Lock()
	w.pending = append(w.pending, cs...)
	w.mu.Unlock()
	w.wake()
}
func (w *fakeWatcher) snapshot() (added []string, removed []uint32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append(added, w.added...), append(removed, w.removed...)
}

type harness struct {
	t    *testing.T
	s    *Service
	fs   *fakeFS
	w    *fakeWatcher
	wake chan struct{}
	buf  []Result
	held []Result // results polled but not yet claimed by an await
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, fs: &fakeFS{gate: make(chan struct{})}, w: &fakeWatcher{}, wake: make(chan struct{}, 1)}
	wake := func() {
		select {
		case h.wake <- struct{}{}:
		default:
		}
	}
	h.w.wake = wake
	h.s = newService(wake, h.fs.list, func(w func()) (watcher, error) { h.w.wake = w; return h.w, nil }, 20*time.Millisecond)
	t.Cleanup(h.s.Close)
	return h
}

// await returns the first result accepted by match, polling as needed.
// Results it passes over are kept for later awaits (release them yourself).
func (h *harness) await(timeout time.Duration, match func(Result) bool) Result {
	h.t.Helper()
	deadline := time.After(timeout)
	for {
		for i, r := range h.held {
			if match(r) {
				h.held = append(h.held[:i], h.held[i+1:]...)
				return r
			}
		}
		h.held = h.s.Poll(h.held)
		for i, r := range h.held {
			if match(r) {
				h.held = append(h.held[:i], h.held[i+1:]...)
				return r
			}
		}
		select {
		case <-h.wake:
		case <-deadline:
			h.t.Fatal("timed out waiting for a result")
		}
	}
}

// expectNone polls for d and fails if any result arrives.
func (h *harness) expectNone(d time.Duration) {
	h.t.Helper()
	if len(h.held) > 0 {
		h.t.Fatalf("unexpected result %+v", h.held[0])
	}
	deadline := time.After(d)
	for {
		h.buf = h.s.Poll(h.buf[:0])
		if len(h.buf) > 0 {
			h.t.Fatalf("unexpected result %+v", h.buf[0])
		}
		select {
		case <-h.wake:
		case <-deadline:
			return
		}
	}
}

func isListing(id uint32) func(Result) bool {
	return func(r Result) bool { return r.Kind == ResultListing && r.ID == id }
}

func TestServiceList(t *testing.T) {
	h := newHarness(t)
	req := h.s.List(1, "n:12:abc", SortName, false)
	r := h.await(2*time.Second, isListing(1))
	if r.Req != req || r.Refresh || r.Listing.Err != nil {
		t.Fatalf("result %+v, want req %d", r, req)
	}
	checkFake(t, r.Listing, "n:12:abc")
	h.s.Release(r.Listing, r.Lease)

	h.s.List(2, "fail", SortName, false)
	r = h.await(2*time.Second, isListing(2))
	if r.Listing.Err == nil || len(r.Listing.Entries) != 0 {
		t.Fatalf("failed listing: %+v", r.Listing)
	}
	h.s.Release(r.Listing, r.Lease)
}

func TestServiceSupersede(t *testing.T) {
	h := newHarness(t)
	// Both workers are held, so id 1's requests stay queued.
	h.s.List(10, "block:ten", SortName, false)
	h.s.List(11, "block:eleven", SortName, false)
	for h.fs.started.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	h.s.List(1, "n:3:old", SortName, false)
	h.s.List(1, "n:4:new", SortName, false)
	close(h.fs.gate)
	var got []Result
	deadline := time.Now().Add(2 * time.Second)
	for len(got) < 3 && time.Now().Before(deadline) {
		h.buf = h.s.Poll(h.buf[:0])
		got = append(got, h.buf...)
		time.Sleep(time.Millisecond)
	}
	if len(got) != 3 {
		t.Fatalf("%d results, want 3", len(got))
	}
	for _, r := range got {
		if r.ID == 1 {
			checkFake(t, r.Listing, "n:4:new")
		}
		h.s.Release(r.Listing, r.Lease)
	}
	if c := h.fs.calls.Load(); c != 3 {
		t.Errorf("fake listed %d times, want 3: the superseded request must never run", c)
	}

	// A request superseded while running is discarded, not delivered.
	h2 := newHarness(t)
	h2.s.List(1, "block:a", SortName, false)
	for h2.fs.started.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	h2.s.List(1, "n:5:b", SortName, false)
	close(h2.fs.gate)
	r := h2.await(2*time.Second, isListing(1))
	checkFake(t, r.Listing, "n:5:b")
	h2.s.Release(r.Listing, r.Lease)
	h2.expectNone(80 * time.Millisecond)
}

func TestServicePoolReuse(t *testing.T) {
	h := newHarness(t)
	seen := map[*Listing]bool{}
	for i := 0; i < 40; i++ {
		h.s.List(1, "n:50:p", SortName, i%2 == 0)
		r := h.await(2*time.Second, isListing(1))
		checkFake(t, r.Listing, "n:50:p")
		seen[r.Listing] = true
		h.s.Release(r.Listing, r.Lease)
	}
	if len(seen) > 2 {
		t.Errorf("%d distinct Listings for 40 sequential requests; the pool is not reusing", len(seen))
	}
	// Double release and foreign listings are ignored.
	l := new(Listing)
	h.s.Release(l, 0)
	h.s.Release(nil, 0)
	h.s.List(1, "n:2:q", SortName, false)
	r := h.await(2*time.Second, isListing(1))
	h.s.Release(r.Listing, r.Lease)
	h.s.Release(r.Listing, r.Lease)
	h.s.List(1, "n:2:q", SortName, false)
	h.s.List(2, "n:2:q", SortName, false)
	a := h.await(2*time.Second, isListing(1))
	b := h.await(2*time.Second, isListing(2))
	if a.Listing == b.Listing {
		t.Error("double Release handed one Listing to two owners")
	}
}

// Ownership under -race: listings cross goroutines through the ring and are
// reused after Release. Content checks catch a buffer that is still shared.
func TestServiceStress(t *testing.T) {
	h := newHarness(t)
	const ids, reqs = 4, 600
	var wg sync.WaitGroup
	for g := 0; g < 3; g++ { // requests come from several goroutines
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < reqs; i++ {
				p := fmt.Sprintf("n:%d:g%dr%d", 1+r.Intn(300), g, i)
				h.s.List(uint32(r.Intn(ids)), p, SortKey(r.Intn(4)), r.Intn(2) == 0)
				if i%50 == 0 {
					time.Sleep(time.Millisecond)
				}
			}
		}(g)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	type lease struct {
		l *Listing
		n uint32
	}
	var held []lease // the UI keeps a few listings for a while
	delivered := 0
	handle := func(r Result) {
		delivered++
		l := r.Listing
		// The listing's own contents say which request produced it.
		name := string(l.Name(0))
		tag := name[:strings.LastIndexByte(name, '-')]
		checkFake(t, l, fmt.Sprintf("n:%d:%s", len(l.Entries), tag))
		held = append(held, lease{l, r.Lease})
		if len(held) > 5 {
			h.s.Release(held[0].l, held[0].n)
			held = held[1:]
		}
	}
	timeout := time.After(20 * time.Second)
	producing := true
	for producing {
		h.buf = h.s.Poll(h.buf[:0])
		for _, r := range h.buf {
			handle(r)
		}
		select {
		case <-done:
			producing = false
		case <-h.wake:
		case <-time.After(2 * time.Millisecond):
		case <-timeout:
			t.Fatal("timed out")
		}
	}
	// One last request per id: its answer proves everything before it settled.
	for id := 0; id < ids; id++ {
		h.s.List(uint32(id), fmt.Sprintf("n:7:final%d", id), SortName, false)
	}
	finals := map[uint32]bool{}
	for len(finals) < ids {
		h.buf = h.s.Poll(h.buf[:0])
		for _, r := range h.buf {
			handle(r)
			if strings.HasPrefix(string(r.Listing.Name(0)), "final") {
				finals[r.ID] = true
			}
		}
		if len(finals) == ids {
			break
		}
		select {
		case <-h.wake:
		case <-timeout:
			t.Fatalf("only %d final results", len(finals))
		}
	}
	for _, x := range held {
		h.s.Release(x.l, x.n)
	}
	h.s.mu.Lock()
	pool := len(h.s.all)
	h.s.mu.Unlock()
	t.Logf("%d requests, %d delivered, pool of %d listings", 3*reqs, delivered, pool)
	if delivered == 0 || pool > 40 {
		t.Errorf("delivered %d, pool %d", delivered, pool)
	}
}

func TestServiceWatchDebounce(t *testing.T) {
	h := newHarness(t)
	h.s.List(1, "n:4:w", SortName, false)
	h.s.Watch(1, "/some/dir")
	h.await(2*time.Second, isListing(1))
	// Wait for the watcher to exist (control goroutine).
	for i := 0; ; i++ {
		if added, _ := h.w.snapshot(); len(added) > 0 {
			break
		}
		if i > 1000 {
			t.Fatal("watch never added")
		}
		time.Sleep(time.Millisecond)
	}
	before := h.fs.calls.Load()
	for i := 0; i < 6; i++ {
		h.w.emit(Change{1, ChangeDir})
	}
	r := h.await(2*time.Second, isListing(1))
	if !r.Refresh {
		t.Error("watch relist not marked Refresh")
	}
	checkFake(t, r.Listing, "n:4:w")
	h.s.Release(r.Listing, r.Lease)
	h.expectNone(100 * time.Millisecond)
	if c := h.fs.calls.Load() - before; c != 1 {
		t.Errorf("%d relists for 6 events in one window, want 1", c)
	}

	// A change for an unwatched id does nothing; Unwatch stops relists.
	h.w.emit(Change{99, ChangeDir})
	h.expectNone(80 * time.Millisecond)
	h.s.Unwatch(1)
	time.Sleep(20 * time.Millisecond)
	h.w.emit(Change{1, ChangeDir})
	h.expectNone(100 * time.Millisecond)
}

func TestServiceWatchGoneOverflowError(t *testing.T) {
	h := newHarness(t)
	h.s.List(1, "n:2:a", SortName, false)
	h.s.List(2, "n:3:b", SortName, false)
	h.s.Watch(1, "/a")
	h.s.Watch(2, "/b")
	h.await(2*time.Second, isListing(1))
	h.await(2*time.Second, isListing(2))
	for i := 0; ; i++ {
		if added, _ := h.w.snapshot(); len(added) == 2 {
			break
		}
		if i > 1000 {
			t.Fatal("watches never added")
		}
		time.Sleep(time.Millisecond)
	}

	h.w.emit(Change{1, ChangeOverflow})
	got := map[uint32]bool{}
	for len(got) < 2 {
		r := h.await(2*time.Second, func(r Result) bool { return r.Kind == ResultListing })
		got[r.ID] = r.Refresh
		h.s.Release(r.Listing, r.Lease)
	}
	if !got[1] || !got[2] {
		t.Errorf("overflow refreshed %v, want both marked Refresh", got)
	}

	h.w.emit(Change{2, ChangeGone})
	r := h.await(2*time.Second, func(r Result) bool { return r.Kind == ResultGone })
	if r.ID != 2 {
		t.Errorf("gone %+v", r)
	}
	h.expectNone(80 * time.Millisecond)
	for i := 0; ; i++ {
		if _, removed := h.w.snapshot(); len(removed) > 0 && removed[len(removed)-1] == 2 {
			break
		}
		if i > 1000 {
			t.Fatal("gone id never removed from the watcher")
		}
		time.Sleep(time.Millisecond)
	}
	h.w.emit(Change{2, ChangeDir}) // no longer watched
	h.expectNone(80 * time.Millisecond)

	h.w.mu.Lock()
	h.w.addErr = errors.New("no inotify")
	h.w.mu.Unlock()
	h.s.Watch(3, "/c")
	r = h.await(2*time.Second, func(r Result) bool { return r.Kind == ResultWatchError })
	if r.ID != 3 || r.Err == nil {
		t.Errorf("watch error %+v", r)
	}
}

// A watch event while the user's List is still outstanding must not mark
// the answer as a passive refresh.
func TestServiceRefreshFlag(t *testing.T) {
	h := newHarness(t)
	h.s.Watch(1, "/a")
	h.s.List(1, "block:x", SortName, false)
	for h.fs.started.Load() < 1 {
		time.Sleep(time.Millisecond)
	}
	for i := 0; ; i++ {
		if added, _ := h.w.snapshot(); len(added) > 0 {
			break
		}
		if i > 1000 {
			t.Fatal("watch never added")
		}
		time.Sleep(time.Millisecond)
	}
	h.w.emit(Change{1, ChangeDir})
	time.Sleep(60 * time.Millisecond)
	h.buf = h.s.Poll(h.buf[:0]) // arms and runs the debounce: relists the same path
	time.Sleep(40 * time.Millisecond)
	h.buf = h.s.Poll(h.buf[:0])
	close(h.fs.gate)
	r := h.await(2*time.Second, isListing(1))
	if r.Refresh {
		t.Error("answer to an outstanding List reported as Refresh")
	}
	h.s.Release(r.Listing, r.Lease)
}

// The UI-thread side must not allocate once warm: Poll when idle, and a
// complete List -> Poll -> Release cycle.
func TestServiceUIZeroAlloc(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 5; i++ { // warm the pool, maps and result slices
		h.s.List(1, "n:100:z", SortName, false)
		r0 := h.await(2*time.Second, isListing(1))
		h.s.Release(r0.Listing, r0.Lease)
	}
	if a := testing.AllocsPerRun(50, func() { h.buf = h.s.Poll(h.buf[:0]) }); a != 0 {
		t.Errorf("idle Poll allocates %v", a)
	}
	const reps = 100
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	for i := 0; i < reps; i++ {
		h.s.List(1, "n:100:z", SortKey(i%4), i%3 == 0)
		for {
			h.buf = h.s.Poll(h.buf[:0])
			if len(h.buf) > 0 {
				break
			}
			<-h.wake
		}
		for _, r := range h.buf {
			h.s.Release(r.Listing, r.Lease)
		}
	}
	runtime.ReadMemStats(&m1)
	perCycle := float64(m1.Mallocs-m0.Mallocs) / reps
	t.Logf("%.2f mallocs, %d bytes per List/Poll/Release cycle", perCycle, (m1.TotalAlloc-m0.TotalAlloc)/reps)
	if perCycle > 2 {
		t.Errorf("%.1f mallocs per cycle", perCycle)
	}
}

// A change made between the listing and the moment the OS watch becomes
// active is in neither the listing nor the event stream; the directory's
// mtime tells the Service to relist.
func TestServiceWatchGap(t *testing.T) {
	h := newHarness(t)
	h.s.listFn = listDir
	h.w.addGate = make(chan struct{})
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "before"), nil, 0o644)
	h.s.Watch(1, dir)
	h.s.List(1, dir, SortName, false)
	r := h.await(2*time.Second, isListing(1))
	if len(r.Listing.Entries) != 1 {
		t.Fatalf("%d entries", len(r.Listing.Entries))
	}
	h.s.Release(r.Listing, r.Lease)
	time.Sleep(30 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "during"), nil, 0o644) // the watch is not active yet
	close(h.w.addGate)
	r = h.await(2*time.Second, isListing(1))
	if !r.Refresh || len(r.Listing.Entries) != 2 {
		t.Errorf("gap relist: refresh=%v, %d entries", r.Refresh, len(r.Listing.Entries))
	}
	h.s.Release(r.Listing, r.Lease)
	h.expectNone(150 * time.Millisecond) // and only once
}
