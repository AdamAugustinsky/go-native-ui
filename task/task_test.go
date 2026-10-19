package task

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/core"
)

type req struct {
	N    int
	Hold chan struct{} // a worker blocks on it when non-nil
}

type resp struct{ N, Seen int }

// gated is a function that records what it ran and, for a request with Hold,
// waits on it (or for cancellation).
type gated struct {
	mu      sync.Mutex
	ran     []int
	started chan int
	cancels atomic.Int32
}

func newGated() *gated { return &gated{started: make(chan int, 256)} }

func (g *gated) fn(t Token, in req) resp {
	g.mu.Lock()
	g.ran = append(g.ran, in.N)
	g.mu.Unlock()
	g.started <- in.N
	if in.Hold != nil {
		for !t.Cancelled() {
			select {
			case <-in.Hold:
				return resp{N: in.N, Seen: 1}
			case <-time.After(time.Millisecond):
			}
		}
		g.cancels.Add(1)
	}
	return resp{N: in.N}
}

func (g *gated) ranList() []int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]int(nil), g.ran...)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func awaitStart(t *testing.T, g *gated, n int) {
	t.Helper()
	select {
	case got := <-g.started:
		if got != n {
			t.Fatalf("started %d, want %d", got, n)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("job %d never started", n)
	}
}

// awaitStarts waits for jobs ns to start, in either order (two workers pick
// them up concurrently).
func awaitStarts(t *testing.T, g *gated, ns ...int) {
	t.Helper()
	want := map[int]bool{}
	for _, n := range ns {
		want[n] = true
	}
	for len(want) > 0 {
		select {
		case got := <-g.started:
			if !want[got] {
				t.Fatalf("started %d, want one of %v", got, ns)
			}
			delete(want, got)
		case <-time.After(5 * time.Second):
			t.Fatalf("jobs %v never started", want)
		}
	}
}

func pollAll(t *testing.T, p *Pool[req, resp], want int) []Result[resp] {
	t.Helper()
	var all []Result[resp]
	waitFor(t, "results", func() bool {
		all = p.Poll(all)
		return len(all) >= want
	})
	return all
}

func TestRoundTripAndWake(t *testing.T) {
	var woke atomic.Int32
	g := newGated()
	p := NewPool(Options{Workers: 2, Wake: func() { woke.Add(1) }}, g.fn)
	defer p.Close(time.Second)
	tk, err := p.Submit(req{N: 7}, 0)
	if err != nil || tk == 0 {
		t.Fatalf("Submit: %v %v", tk, err)
	}
	rs := pollAll(t, p, 1)
	if len(rs) != 1 || rs[0].Ticket != tk || rs[0].Out.N != 7 || rs[0].Key != 0 {
		t.Fatalf("results %+v, ticket %d", rs, tk)
	}
	if woke.Load() < 1 {
		t.Error("the loop was never woken")
	}
	if p.Pending() != 0 {
		t.Errorf("Pending %d after the result was polled", p.Pending())
	}
}

func TestOutWithPointersPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewPool accepted an Out with a string")
		}
	}()
	NewPool(Options{}, func(Token, int) struct{ S string } { return struct{ S string }{} })
}

func TestLatestWinsRunningJob(t *testing.T) {
	g := newGated()
	p := NewPool(Options{Workers: 2}, g.fn)
	defer p.Close(time.Second)
	hold := make(chan struct{})
	old, _ := p.Submit(req{N: 1, Hold: hold}, 5)
	awaitStart(t, g, 1)
	nw, err := p.Submit(req{N: 2}, 5)
	if err != nil || nw == old {
		t.Fatalf("Submit: %v %v", nw, err)
	}
	rs := pollAll(t, p, 1)
	waitFor(t, "the superseded job to see its cancellation", func() bool { return g.cancels.Load() == 1 })
	time.Sleep(20 * time.Millisecond)
	rs = p.Poll(rs)
	if len(rs) != 1 || rs[0].Ticket != nw || rs[0].Out.N != 2 || rs[0].Key != 5 {
		t.Fatalf("results %+v, want only ticket %d", rs, nw)
	}
	// The cancelled job's slot came back: the pool is idle.
	waitFor(t, "an empty pool", func() bool { return p.Pending() == 0 })
}

func TestLatestWinsQueuedJobKeepsItsPlace(t *testing.T) {
	g := newGated()
	p := NewPool(Options{Workers: 1}, g.fn)
	defer p.Close(time.Second)
	hold := make(chan struct{})
	p.Submit(req{N: 100, Hold: hold}, 1) // occupies the only worker
	awaitStart(t, g, 100)
	t1, _ := p.Submit(req{N: 1}, 9)
	p.Submit(req{N: 50}, 8)
	t2, _ := p.Submit(req{N: 2}, 9) // replaces N:1 before it ran
	if t1 == t2 {
		t.Fatal("a replaced submission kept its ticket")
	}
	close(hold)
	rs := pollAll(t, p, 3)
	time.Sleep(10 * time.Millisecond)
	rs = p.Poll(rs)
	ran := g.ranList()
	if len(ran) != 3 || ran[0] != 100 || ran[1] != 2 || ran[2] != 50 {
		t.Errorf("ran %v, want [100 2 50] (N:1 replaced in place, ahead of N:50)", ran)
	}
	for _, r := range rs {
		if r.Ticket == t1 {
			t.Errorf("the superseded ticket %d produced a result", t1)
		}
	}
}

func TestLatestWinsFinishedUnpolled(t *testing.T) {
	g := newGated()
	p := NewPool(Options{Workers: 1}, g.fn)
	defer p.Close(time.Second)
	p.Submit(req{N: 1}, 3)
	awaitStart(t, g, 1)
	waitFor(t, "the result", func() bool { return p.doneN.Load() == 1 })
	nw, _ := p.Submit(req{N: 2}, 3) // the unseen answer to N:1 is discarded
	rs := pollAll(t, p, 1)
	time.Sleep(10 * time.Millisecond)
	rs = p.Poll(rs)
	if len(rs) != 1 || rs[0].Ticket != nw || rs[0].Out.N != 2 {
		t.Fatalf("results %+v, want only N:2", rs)
	}
}

func TestDifferentKeysDoNotSupersede(t *testing.T) {
	g := newGated()
	p := NewPool(Options{Workers: 1}, g.fn)
	defer p.Close(time.Second)
	hold := make(chan struct{})
	p.Submit(req{N: 100, Hold: hold}, 1)
	awaitStart(t, g, 100)
	for i := 1; i <= 4; i++ {
		p.Submit(req{N: i}, uint64(10+i))
	}
	p.Submit(req{N: 5}, 0)
	p.Submit(req{N: 6}, 0) // key 0 never supersedes
	close(hold)
	rs := pollAll(t, p, 7)
	if len(rs) != 7 {
		t.Fatalf("%d results, want 7: %+v", len(rs), rs)
	}
}

func TestErrFullAndUnpolledResultsHoldTheirSlot(t *testing.T) {
	g := newGated()
	p := NewPool(Options{Workers: 1, Queue: 3}, g.fn)
	defer p.Close(time.Second)
	hold := make(chan struct{})
	p.Submit(req{N: 1, Hold: hold}, 0)
	awaitStart(t, g, 1)
	p.Submit(req{N: 2}, 0)
	p.Submit(req{N: 3}, 0)
	if _, err := p.Submit(req{N: 4}, 0); !errors.Is(err, ErrFull) {
		t.Fatalf("a fourth job: %v, want ErrFull", err)
	}
	// Latest-wins still works when the pool is full: it replaces, it does not add.
	if _, err := p.Submit(req{N: 30}, 0); !errors.Is(err, ErrFull) {
		t.Fatalf("key 0 into a full pool: %v", err)
	}
	close(hold)
	waitFor(t, "all three to finish", func() bool { return p.doneN.Load() == 3 })
	if _, err := p.Submit(req{N: 4}, 0); !errors.Is(err, ErrFull) {
		t.Fatalf("finished-but-unpolled results do not hold their slots: %v", err)
	}
	rs := p.Poll(nil)
	if len(rs) != 3 {
		t.Fatalf("%d results", len(rs))
	}
	if _, err := p.Submit(req{N: 4}, 0); err != nil {
		t.Fatalf("after Poll: %v", err)
	}
}

func TestSupersedingRunningJobsNeverReturnsErrFull(t *testing.T) {
	// Every worker is busy with a job that ignores its Token for a while; each
	// replacement of a running job needs a new slot while the old one lingers.
	g := newGated()
	p := NewPool(Options{Workers: 2, Queue: 2}, g.fn)
	defer p.Close(time.Second)
	h1, h2 := make(chan struct{}), make(chan struct{})
	p.Submit(req{N: 1, Hold: h1}, 1)
	p.Submit(req{N: 2, Hold: h2}, 2)
	awaitStarts(t, g, 1, 2)
	if _, err := p.Submit(req{N: 11}, 1); err != nil {
		t.Fatalf("replace running 1: %v", err)
	}
	if _, err := p.Submit(req{N: 22}, 2); err != nil {
		t.Fatalf("replace running 2: %v", err)
	}
	close(h1)
	close(h2)
	rs := pollAll(t, p, 2)
	if len(rs) != 2 {
		t.Fatalf("%d results", len(rs))
	}
}

func TestCancel(t *testing.T) {
	g := newGated()
	p := NewPool(Options{Workers: 1}, g.fn)
	defer p.Close(time.Second)
	hold := make(chan struct{})
	run, _ := p.Submit(req{N: 1, Hold: hold}, 0)
	awaitStart(t, g, 1)
	queued, _ := p.Submit(req{N: 2}, 0)
	if !p.Cancel(queued) {
		t.Error("Cancel of a queued job found nothing")
	}
	if p.Cancel(queued) {
		t.Error("Cancel found a job twice")
	}
	if !p.Cancel(run) {
		t.Error("Cancel of a running job found nothing")
	}
	waitFor(t, "the running job to see it", func() bool { return g.cancels.Load() == 1 })
	waitFor(t, "an empty pool", func() bool { return p.Pending() == 0 })
	if rs := p.Poll(nil); len(rs) != 0 {
		t.Errorf("cancelled jobs produced %+v", rs)
	}
	if got := g.ranList(); len(got) != 1 {
		t.Errorf("ran %v, want only the running job", got)
	}
	if p.Cancel(0) || p.CancelKey(0) || p.CancelKey(99) {
		t.Error("Cancel found a job that is not there")
	}
}

func TestTokenContext(t *testing.T) {
	ctxErr := make(chan error, 1)
	started := make(chan struct{})
	p := NewPool(Options{Workers: 1}, func(tk Token, in int) int {
		ctx := tk.Context()
		close(started)
		<-ctx.Done()
		ctxErr <- ctx.Err()
		return 0
	})
	defer p.Close(time.Second)
	tk, _ := p.Submit(1, 0)
	<-started
	p.Cancel(tk)
	select {
	case err := <-ctxErr:
		if err == nil {
			t.Error("context done without an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the context was not cancelled")
	}
}

func TestKeepAndSweep(t *testing.T) {
	g := newGated()
	p := NewPool(Options{Workers: 2}, g.fn)
	defer p.Close(time.Second)
	hold := make(chan struct{})
	if _, err := p.Keep(0, req{}); !errors.Is(err, ErrKey) {
		t.Fatalf("Keep(0): %v", err)
	}
	// Frame 1 declares A (long-running) and B (quick).
	ta, err := p.Keep(1, req{N: 1, Hold: hold})
	if err != nil {
		t.Fatal(err)
	}
	p.Keep(2, req{N: 2})
	p.Sweep()
	awaitStarts(t, g, 1, 2)
	// Frames 2 and 3 declare both again: nothing restarts, even after B finished.
	for f := 0; f < 2; f++ {
		if tk, _ := p.Keep(1, req{N: 99}); tk != ta {
			t.Fatalf("Keep returned %d, want the running %d", tk, ta)
		}
		p.Keep(2, req{N: 99})
		p.Sweep()
	}
	if got := g.ranList(); len(got) != 2 {
		t.Fatalf("a repeated declaration restarted the work: ran %v", got)
	}
	// Frame 4 declares only B: A is cancelled when that frame is swept, not before.
	p.Keep(2, req{})
	if g.cancels.Load() != 0 {
		t.Fatal("cancelled before the frame ended")
	}
	p.Sweep()
	waitFor(t, "A to be cancelled", func() bool { return g.cancels.Load() == 1 })
	rs := pollAll(t, p, 1)
	if len(rs) != 1 || rs[0].Key != 2 {
		t.Fatalf("results %+v, want only B's", rs)
	}
	// Declaring A again later starts it afresh.
	if tk, err := p.Keep(1, req{N: 3}); err != nil || tk == ta {
		t.Fatalf("redeclared: %v %v", tk, err)
	}
	awaitStart(t, g, 3)
	close(hold)
}

func TestKeepFullRetriesNextFrame(t *testing.T) {
	g := newGated()
	p := NewPool(Options{Workers: 1, Queue: 1}, g.fn)
	defer p.Close(time.Second)
	hold := make(chan struct{})
	p.Submit(req{N: 1, Hold: hold}, 1)
	awaitStart(t, g, 1)
	if _, err := p.Keep(2, req{N: 2}); !errors.Is(err, ErrFull) {
		t.Fatalf("Keep into a full pool: %v", err)
	}
	p.Sweep()
	close(hold)
	pollAll(t, p, 1)
	if _, err := p.Keep(2, req{N: 2}); err != nil {
		t.Fatalf("next frame: %v", err)
	}
	p.Sweep()
	pollAll(t, p, 1)
}

func TestCloseIsBounded(t *testing.T) {
	g := newGated()
	p := NewPool(Options{Workers: 1}, g.fn)
	stuck := make(chan struct{})
	p.Submit(req{N: 1, Hold: stuck}, 0)
	awaitStart(t, g, 1)
	p.Submit(req{N: 2}, 0)
	// This job honours its Token, so Close sees it return.
	t0 := time.Now()
	if !p.Close(5 * time.Second) {
		t.Fatal("Close timed out on a job that honours its Token")
	}
	if time.Since(t0) > 2*time.Second {
		t.Errorf("Close took %v", time.Since(t0))
	}
	if _, err := p.Submit(req{N: 3}, 0); !errors.Is(err, ErrClosed) {
		t.Errorf("Submit after Close: %v", err)
	}
	if rs := p.Poll(nil); len(rs) != 0 {
		t.Errorf("results after Close: %+v", rs)
	}
	if !p.Close(time.Millisecond) {
		t.Error("a second Close failed")
	}
	if got := g.ranList(); len(got) != 1 {
		t.Errorf("a queued job ran after Close: %v", got)
	}
}

func TestCloseDetachesStuckWork(t *testing.T) {
	var woke atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{})
	p := NewPool(Options{Workers: 1, Wake: func() { woke.Add(1) }}, func(Token, int) int {
		close(started)
		<-release // ignores its Token
		return 1
	})
	p.Submit(1, 0)
	<-started
	t0 := time.Now()
	if p.Close(30 * time.Millisecond) {
		t.Fatal("Close reported a clean stop with a worker stuck")
	}
	if d := time.Since(t0); d < 25*time.Millisecond || d > 2*time.Second {
		t.Errorf("Close waited %v for a 30ms bound", d)
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	if rs := p.Poll(nil); len(rs) != 0 || woke.Load() != 0 {
		t.Errorf("a detached job delivered: %+v, wakes %d", rs, woke.Load())
	}
}

// Many keys, many replacements, several workers: for every key the last
// submission's result arrives, and per key results never go back in time.
func TestStress(t *testing.T) {
	p := NewPool(Options{Workers: 4, Queue: 16}, func(tk Token, in int) int {
		if in%7 == 0 {
			runtime.Gosched()
		}
		return in
	})
	defer p.Close(time.Second)
	const keys = 8
	last := [keys + 1]Ticket{}
	seen := [keys + 1]Ticket{}
	var buf []Result[int]
	check := func() {
		buf = p.Poll(buf[:0])
		for _, r := range buf {
			if r.Ticket < seen[r.Key] {
				t.Fatalf("key %d: ticket %d after %d", r.Key, r.Ticket, seen[r.Key])
			}
			seen[r.Key] = r.Ticket
		}
	}
	for i := 0; i < 20000; i++ {
		k := uint64(i%keys + 1)
		tk, err := p.Submit(i, k)
		for errors.Is(err, ErrFull) {
			check()
			runtime.Gosched()
			tk, err = p.Submit(i, k)
		}
		if err != nil {
			t.Fatal(err)
		}
		last[k] = tk
		if i%3 == 0 {
			check()
		}
	}
	waitFor(t, "every key's last result", func() bool {
		check()
		for k := 1; k <= keys; k++ {
			if seen[k] != last[k] {
				return false
			}
		}
		return true
	})
	waitFor(t, "an empty pool", func() bool { check(); return p.Pending() == 0 })
}

// The owner's side of the pool allocates nothing once warm: an idle Poll, a
// Submit + Poll cycle (the workers park and wake through core.Parker), and a
// Keep/Sweep frame. The counters are process-wide, so the rule is the bench
// gate's (D27): some window after the first allocates nothing.
func TestOwnerZeroAlloc(t *testing.T) {
	woken := core.NewParker()
	defer woken.Close()
	p := NewPool(Options{Workers: 2, Wake: woken.Unpark}, func(tk Token, in int) int { return in + 1 })
	defer p.Close(time.Second)
	buf := make([]Result[int], 0, 16)
	cycle := func(i int) {
		if _, err := p.Submit(i, uint64(i%3+1)); err != nil {
			t.Fatal(err)
		}
		for {
			buf = p.Poll(buf[:0])
			if len(buf) > 0 {
				return
			}
			woken.Park()
		}
	}
	for i := 0; i < 200; i++ {
		cycle(i)
		p.Keep(uint64(i%4+1), i)
		p.Sweep()
	}
	if a := testing.AllocsPerRun(100, func() { buf = p.Poll(buf[:0]) }); a != 0 {
		t.Errorf("idle Poll allocates %v", a)
	}
	// Declarations that already exist cost nothing.
	p.Keep(50, 0)
	p.Sweep()
	pollN := func() { buf = p.Poll(buf[:0]) }
	for p.Pending() > 0 {
		pollN()
		time.Sleep(time.Millisecond)
	}
	if a := testing.AllocsPerRun(100, func() { p.Keep(50, 0); p.Sweep() }); a != 0 {
		t.Errorf("a repeated Keep + Sweep allocates %v", a)
	}

	const reps, windows = 500, 4
	var deltas [windows]uint64
	ok := false
	for attempt := 0; attempt < 5 && !ok; attempt++ {
		for w := range deltas {
			runtime.GC()
			time.Sleep(5 * time.Millisecond)
			var m0, m1 runtime.MemStats
			runtime.ReadMemStats(&m0)
			for i := 0; i < reps; i++ {
				cycle(i)
			}
			runtime.ReadMemStats(&m1)
			deltas[w] = m1.Mallocs - m0.Mallocs
		}
		for _, d := range deltas[1:] {
			ok = ok || d == 0
		}
		if !ok {
			t.Logf("attempt %d: mallocs per %d Submit/Poll cycles: %v", attempt, reps, deltas)
		}
	}
	if !ok {
		t.Errorf("mallocs per %d Submit/Poll cycles: %v, want a window after the first with 0", reps, deltas)
	}
}

// A declaration refused for want of room must not wait for a frame nobody asks
// for: freeing the room (here, Sweep cancelling the job that held it, whose
// discarded completion wakes nobody) wakes the loop once, and the retry works.
func TestRefusedKeepIsRetriedByAWakeWhenSweepFreesRoom(t *testing.T) {
	var wakes atomic.Int32
	g := newGated()
	p := NewPool(Options{Workers: 1, Queue: 1, Wake: func() { wakes.Add(1) }}, g.fn)
	defer p.Close(time.Second)
	hold := make(chan struct{})
	if _, err := p.Keep(1, req{N: 1, Hold: hold}); err != nil { // frame A
		t.Fatal(err)
	}
	p.Sweep()
	awaitStart(t, g, 1)
	if _, err := p.Keep(2, req{N: 2}); !errors.Is(err, ErrFull) { // frame B declares B, not A
		t.Fatalf("Keep(B): %v, want ErrFull", err)
	}
	if wakes.Load() != 0 {
		t.Fatal("woken before any room appeared")
	}
	p.Sweep() // cancels A
	waitFor(t, "the retry wake", func() bool { return wakes.Load() >= 1 })
	time.Sleep(20 * time.Millisecond) // the cancelled job's worker returning adds none
	if w := wakes.Load(); w != 1 {
		t.Errorf("%d wakes, want exactly 1", w)
	}
	// The woken frame declares B again and it is accepted.
	if _, err := p.Keep(2, req{N: 2}); err != nil {
		t.Fatalf("retry: %v", err)
	}
	p.Sweep()
	awaitStart(t, g, 2)
	pollAll(t, p, 1)
	// No refusal pending: freeing room again owes no wake.
	before := wakes.Load()
	p.Keep(2, req{N: 2})
	p.Sweep() // key 2 declared: stays
	p.Sweep() // not declared this frame: cancelled
	time.Sleep(10 * time.Millisecond)
	if wakes.Load() != before {
		t.Error("a wake without a refused declaration")
	}
}

func TestKeepAfterCloseIsRefused(t *testing.T) {
	p := NewPool(Options{Workers: 1}, func(Token, int) int { return 0 })
	if _, err := p.Keep(1, 0); err != nil {
		t.Fatal(err)
	}
	p.Sweep()
	p.Close(time.Second)
	if _, err := p.Keep(1, 0); !errors.Is(err, ErrClosed) { // the declaration is on record
		t.Errorf("Keep of a recorded key after Close: %v", err)
	}
	if _, err := p.Keep(2, 0); !errors.Is(err, ErrClosed) {
		t.Errorf("Keep of a new key after Close: %v", err)
	}
}
