package fs

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// waitFor polls cond until it holds or the timeout passes.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *harness) watchAdded(n int) bool {
	added, _ := h.w.snapshot()
	return len(added) >= n
}

// expectQuiet polls for d and fails on any result and on any wake: a Service
// with nothing to do must not keep waking the UI.
func (h *harness) expectQuiet(d time.Duration) {
	h.t.Helper()
	deadline := time.After(d)
	for {
		select {
		case <-h.wake:
			h.t.Fatal("the Service woke the UI while nothing changed")
		case <-deadline:
			h.buf = h.s.Poll(h.buf[:0])
			if len(h.buf) > 0 {
				h.t.Fatalf("unexpected result %+v", h.buf[0])
			}
			return
		}
	}
}

func (h *harness) drainWake() {
	select {
	case <-h.wake:
	default:
	}
}

// Finding 1: a watch refresh compared every later listing with the mtime at
// watch start, which had moved, so it relisted forever.
func TestServiceNoEndlessRefresh(t *testing.T) {
	h := newHarness(t)
	h.s.listFn = listDir
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "before"), nil, 0o644)
	h.s.Watch(1, dir)
	h.s.List(1, dir, SortName, false)
	r := h.await(2*time.Second, isListing(1))
	h.s.Release(r.Listing, r.Lease)
	waitFor(t, "watch", 2*time.Second, func() bool { return h.watchAdded(1) })
	time.Sleep(100 * time.Millisecond)

	os.WriteFile(filepath.Join(dir, "after"), nil, 0o644) // the directory's mtime moves
	h.w.emit(Change{1, ChangeDir})
	r = h.await(2*time.Second, isListing(1))
	if !r.Refresh || len(r.Listing.Entries) != 2 {
		t.Fatalf("refresh: %v, %d entries", r.Refresh, len(r.Listing.Entries))
	}
	h.s.Release(r.Listing, r.Lease)
	h.drainWake()
	h.expectQuiet(600 * time.Millisecond)
}

// Finding 8: Pilot calls Watch after the listing arrived; a change between
// the listing and the watch becoming live must still be picked up.
func TestServiceListThenWatchGap(t *testing.T) {
	h := newHarness(t)
	h.s.listFn = listDir
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "before"), nil, 0o644)
	h.s.List(1, dir, SortName, false)
	r := h.await(2*time.Second, isListing(1))
	h.s.Release(r.Listing, r.Lease)

	time.Sleep(20 * time.Millisecond)
	os.WriteFile(filepath.Join(dir, "between"), nil, 0o644) // no watch yet
	time.Sleep(20 * time.Millisecond)
	h.s.Watch(1, dir)
	r = h.await(2*time.Second, isListing(1))
	if !r.Refresh || len(r.Listing.Entries) != 2 {
		t.Fatalf("gap relist: refresh=%v, %d entries", r.Refresh, len(r.Listing.Entries))
	}
	h.s.Release(r.Listing, r.Lease)
	h.drainWake()
	h.expectQuiet(400 * time.Millisecond) // and only once
}

// Finding 4: a stale release of an earlier hand-out must not take the
// Listing from its present owner.
func TestServiceReleaseLease(t *testing.T) {
	h := newHarness(t)
	h.s.List(1, "n:5:a", SortName, false)
	a := h.await(2*time.Second, isListing(1))
	h.s.Release(a.Listing, a.Lease)

	h.s.List(2, "n:5:b", SortName, false)
	b := h.await(2*time.Second, isListing(2))
	if b.Listing != a.Listing {
		t.Skip("the pool did not reuse the Listing; nothing to test")
	}
	if b.Lease == a.Lease || b.Lease == 0 {
		t.Fatalf("leases %d and %d", a.Lease, b.Lease)
	}
	if h.s.IgnoredReleases() != 0 {
		t.Fatalf("%d ignored releases before any duplicate", h.s.IgnoredReleases())
	}
	h.s.Release(a.Listing, a.Lease) // the old owner releases again
	if n := h.s.IgnoredReleases(); n != 1 {
		t.Fatalf("ignored releases = %d, want 1", n)
	}
	// b still owns the Listing: a new request must get a different one.
	h.s.List(3, "n:5:c", SortName, false)
	c := h.await(2*time.Second, isListing(3))
	if c.Listing == b.Listing {
		t.Fatal("the pool handed out a Listing that its owner still holds")
	}
	checkFake(t, b.Listing, "n:5:b")
	// Releases with the right lease work; repeating one is counted.
	h.s.Release(b.Listing, b.Lease)
	h.s.Release(b.Listing, b.Lease)
	h.s.Release(c.Listing, c.Lease)
	if n := h.s.IgnoredReleases(); n != 2 {
		t.Errorf("ignored releases = %d, want 2", n)
	}
	// Foreign listings and lease 0 are ignored silently.
	h.s.Release(new(Listing), 0)
	h.s.Release(nil, 5)
	if n := h.s.IgnoredReleases(); n != 2 {
		t.Errorf("foreign release counted: %d", n)
	}
}

// Finding 6: Cancel left the queued job executable, and a worker then
// listed the cancelled path under the new request number.
func TestServiceCancelQueued(t *testing.T) {
	h := newHarness(t)
	h.s.List(10, "block:ten", SortName, false)
	h.s.List(11, "block:eleven", SortName, false)
	waitFor(t, "workers busy", 2*time.Second, func() bool { return h.fs.started.Load() >= 2 })
	h.s.List(1, "n:3:cancelled", SortName, false)
	h.s.Cancel(1)
	close(h.fs.gate)
	for i := 0; i < 2; i++ {
		r := h.await(2*time.Second, func(r Result) bool { return r.Kind == ResultListing })
		if r.ID == 1 {
			t.Fatalf("the cancelled request was delivered: %+v", r)
		}
		h.s.Release(r.Listing, r.Lease)
	}
	h.expectNone(100 * time.Millisecond)
	if c := h.fs.calls.Load(); c != 2 {
		t.Errorf("listed %d times, want 2: the cancelled job must not run", c)
	}
	// The id works again afterwards, once.
	h.s.List(1, "n:4:again", SortName, false)
	r := h.await(2*time.Second, isListing(1))
	checkFake(t, r.Listing, "n:4:again")
	h.s.Release(r.Listing, r.Lease)
	h.expectNone(60 * time.Millisecond)
	if c := h.fs.calls.Load(); c != 3 {
		t.Errorf("listed %d times, want 3", c)
	}
}

// Finding 7: a failure of a replaced watch must not clear the new one, and
// events of the old watch must not be applied to the replacement.
func TestServiceWatchGenerations(t *testing.T) {
	t.Run("old add fails after replacement", func(t *testing.T) {
		h := newHarness(t)
		gate := make(chan struct{})
		var started atomic.Int64
		h.w.addFn = func(id uint32, path string) error {
			if path == "/old" {
				started.Add(1)
				<-gate
				return errors.New("old watch failed")
			}
			return nil
		}
		h.s.List(1, "n:2:a", SortName, false)
		r := h.await(2*time.Second, isListing(1))
		h.s.Release(r.Listing, r.Lease)
		h.s.Watch(1, "/old")
		waitFor(t, "old add to start", 2*time.Second, func() bool { return started.Load() == 1 })
		h.s.Watch(1, "/new")
		close(gate)
		waitFor(t, "new add", 2*time.Second, func() bool {
			added, _ := h.w.snapshot()
			return len(added) == 1 && added[0] == "1=/new"
		})
		h.expectNone(100 * time.Millisecond) // the old failure is moot: no ResultWatchError
		h.w.emit(Change{1, ChangeDir})       // and the new watch is live
		r = h.await(2*time.Second, isListing(1))
		if !r.Refresh {
			t.Error("not a refresh")
		}
		h.s.Release(r.Listing, r.Lease)
	})
	t.Run("old gone is not applied to the replacement", func(t *testing.T) {
		h := newHarness(t)
		h.s.Watch(1, "/a")
		waitFor(t, "watch", 2*time.Second, func() bool { return h.watchAdded(1) })
		gate := make(chan struct{})
		h.w.mu.Lock()
		h.w.addGate = gate
		h.w.mu.Unlock()
		h.w.emit(Change{1, ChangeGone}) // pending in the old watch
		h.s.Watch(1, "/b")              // replaces it before the UI polled
		h.expectNone(80 * time.Millisecond)
		close(gate)
		waitFor(t, "second watch", 2*time.Second, func() bool { return h.watchAdded(2) })
		h.s.List(1, "n:2:x", SortName, false)
		r := h.await(2*time.Second, isListing(1))
		h.s.Release(r.Listing, r.Lease)
		h.w.emit(Change{1, ChangeDir}) // the replacement still works
		r = h.await(2*time.Second, isListing(1))
		h.s.Release(r.Listing, r.Lease)
	})
}

// Finding 9: a listing of a directory reported gone must not be delivered
// after ResultGone.
func TestServiceGoneInvalidatesListing(t *testing.T) {
	t.Run("same path", func(t *testing.T) {
		h := newHarness(t)
		h.s.Watch(1, "block:x")
		waitFor(t, "watch", 2*time.Second, func() bool { return h.watchAdded(1) })
		h.s.List(1, "block:x", SortName, false)
		waitFor(t, "list to start", 2*time.Second, func() bool { return h.fs.started.Load() == 1 })
		h.w.emit(Change{1, ChangeGone})
		r := h.await(2*time.Second, func(r Result) bool { return r.Kind == ResultGone })
		if r.ID != 1 {
			t.Fatalf("%+v", r)
		}
		close(h.fs.gate) // the running listing finishes now
		h.expectNone(150 * time.Millisecond)
	})
	t.Run("other path stands", func(t *testing.T) {
		h := newHarness(t)
		h.s.Watch(1, "/a")
		waitFor(t, "watch", 2*time.Second, func() bool { return h.watchAdded(1) })
		h.s.List(1, "block:y", SortName, false) // navigating elsewhere
		waitFor(t, "list to start", 2*time.Second, func() bool { return h.fs.started.Load() == 1 })
		h.w.emit(Change{1, ChangeGone})
		h.await(2*time.Second, func(r Result) bool { return r.Kind == ResultGone })
		close(h.fs.gate)
		r := h.await(2*time.Second, isListing(1))
		checkFake(t, r.Listing, "n:3:y")
		h.s.Release(r.Listing, r.Lease)
	})
	t.Run("queued", func(t *testing.T) {
		h := newHarness(t)
		h.s.List(10, "block:ten", SortName, false)
		h.s.List(11, "block:eleven", SortName, false)
		waitFor(t, "workers busy", 2*time.Second, func() bool { return h.fs.started.Load() >= 2 })
		h.s.Watch(1, "n:3:q")
		waitFor(t, "watch", 2*time.Second, func() bool { return h.watchAdded(1) })
		h.s.List(1, "n:3:q", SortName, false) // queued behind the busy workers
		h.w.emit(Change{1, ChangeGone})
		h.await(2*time.Second, func(r Result) bool { return r.Kind == ResultGone })
		close(h.fs.gate)
		for i := 0; i < 2; i++ {
			r := h.await(2*time.Second, func(r Result) bool { return r.Kind == ResultListing })
			if r.ID == 1 {
				t.Fatalf("listing of a gone directory delivered: %+v", r)
			}
			h.s.Release(r.Listing, r.Lease)
		}
		h.expectNone(100 * time.Millisecond)
	})
}

// Finding 5: Open must not block the caller, reports failures through Poll,
// and Close must not wait for a stuck open.
func TestServiceOpen(t *testing.T) {
	h := newHarness(t)
	gate := make(chan struct{})
	var calls atomic.Int64
	h.s.openFn = func(path string) error {
		calls.Add(1)
		switch path {
		case "stuck":
			<-gate
			return nil
		case "bad":
			return &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
		}
		return nil
	}
	t0 := time.Now()
	h.s.Open(1, "stuck")
	h.s.Open(2, "fine")
	h.s.Open(3, "bad")
	if d := time.Since(t0); d > 50*time.Millisecond {
		t.Fatalf("Open blocked the caller for %v", d)
	}
	r := h.await(2*time.Second, func(r Result) bool { return r.Kind == ResultOpenError })
	if r.ID != 3 || !errors.Is(r.Err, os.ErrNotExist) {
		t.Fatalf("open error %+v", r)
	}
	h.expectNone(60 * time.Millisecond) // the successful open and the stuck one report nothing

	// The number of opens in flight is bounded.
	for i := 0; i < maxOpens+2; i++ {
		h.s.Open(9, "stuck")
	}
	r = h.await(2*time.Second, func(r Result) bool { return r.Kind == ResultOpenError })
	if r.ID != 9 || r.Err == nil {
		t.Fatalf("overflow open: %+v", r)
	}

	done := make(chan struct{})
	go func() { h.s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hangs on a stuck open")
	}
	close(gate)
	h.s.Open(1, "again") // after Close: ignored, no panic
}

func TestArenaFits32(t *testing.T) {
	cases := []struct {
		used, add uint64
		want      bool
	}{
		{0, 0, true},
		{0, math.MaxUint32, true},
		{math.MaxUint32, 0, true},
		{math.MaxUint32 - 5, 5, true},
		{math.MaxUint32 - 5, 6, false},
		{math.MaxUint32, 1, false},
		{math.MaxUint32 + 1, 0, false},
		{1 << 40, 1, false},
		{10, math.MaxUint64, false},
	}
	for _, c := range cases {
		if got := arenaFits32(c.used, c.add); got != c.want {
			t.Errorf("arenaFits32(%d, %d) = %v, want %v", c.used, c.add, got, c.want)
		}
	}
}

// Finding 12: sorting a listing whose key arena or entry count does not fit
// 32-bit offsets reports an error instead of wrapping offsets.
func TestSortTooLarge(t *testing.T) {
	old := arenaLimit
	defer func() { arenaLimit = old }()
	l := synth("alpha", "beta", "gamma", "delta")
	arenaLimit = 12 // the keys need 4+5+5+... bytes
	Sort(l, SortName, false)
	if !errors.Is(l.Err, ErrTooLarge) {
		t.Fatalf("Err = %v, want ErrTooLarge", l.Err)
	}
	if len(l.Order) != len(l.Entries) {
		t.Errorf("Order has %d of %d indices", len(l.Order), len(l.Entries))
	}
	arenaLimit = 2 // more entries than indices
	l = synth("a", "b", "c")
	Sort(l, SortName, false)
	if !errors.Is(l.Err, ErrTooLarge) {
		t.Fatalf("entry count: Err = %v", l.Err)
	}
	arenaLimit = old
	l = synth("alpha", "beta")
	Sort(l, SortName, false)
	if l.Err != nil {
		t.Errorf("normal sort: %v", l.Err)
	}
}

// Close does not hang on a listing or a watch setup that never returns.
func TestServiceCloseBounded(t *testing.T) {
	old := closeWait
	closeWait = 200 * time.Millisecond
	defer func() { closeWait = old }()
	h := newHarness(t)
	gate := make(chan struct{})
	defer close(gate)
	h.w.addFn = func(uint32, string) error { <-gate; return nil }
	h.s.List(1, "block:x", SortName, false) // the fake's gate is never closed
	h.s.Watch(2, "/stuck")
	waitFor(t, "stuck work", 2*time.Second, func() bool { return h.fs.started.Load() == 1 })
	done := make(chan struct{})
	go func() { h.s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hangs on stuck file system work")
	}
}
