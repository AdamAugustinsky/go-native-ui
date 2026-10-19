package app

import (
	"strings"
	"testing"
)

// cycler is a CycleAligned session of n frames.
type cycler struct{ n, frame int }

func (c *cycler) AtCycleStart() bool { return c.frame%c.n == 0 }

var benchSink []byte

// drive plays frames through the bench the way present does and returns once
// it is done. allocIn(window, frame) says where the "script" allocates.
func driveBench(t *testing.T, bn *benchRun, a *App, cy *cycler, allocIn func(window, frame int) bool) {
	t.Helper()
	for i := 0; i < 10000 && !a.done; i++ {
		bn.before(a)
		w := len(bn.windows)
		if bn.measuring && allocIn(w, cy.frame%cy.n) {
			benchSink = make([]byte, 64)
		}
		cy.frame++
		a.fm.start = int64(i+1) * 1e6
		bn.after(a)
	}
	if !a.done {
		t.Fatal("the bench did not end")
	}
}

func newBenchRig(t *testing.T, o BenchOptions, cy *cycler) (*benchRun, *App) {
	t.Helper()
	off, err := NewOffscreen(Config{}, 100, 100, 1, &tiny{})
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	t.Cleanup(off.Close)
	a := off.App
	a.p = newPacer()
	bn := &benchRun{o: o, cyc: cy}
	bn.frameCPU, bn.intervals, bn.lat = make([]float64, 0, 64), make([]float64, 0, 64), make([]float64, 0, 64)
	bn.scratch = make([]float64, 0, 64)
	bn.windows = make([]BenchWindow, 0, o.Windows)
	a.bench = bn
	return bn, a
}

// Windows are whole sessions; a one-off allocation in one of them does not fail
// the run, an allocation of every session does.
func TestBenchWindowsAreWholeSessions(t *testing.T) {
	t.Run("one-off", func(t *testing.T) {
		cy := &cycler{n: 10}
		bn, a := newBenchRig(t, BenchOptions{Warm: 7, PostGCWarm: 3, Windows: 6}, cy)
		driveBench(t, bn, a, cy, func(w, f int) bool { return w == 0 && f == 4 })
		// The counters are process-wide, so runtime noise can dirty a further
		// window; what matters is that the run stops at the first clean one.
		ws := bn.report.Windows
		if len(ws) < 2 {
			t.Fatalf("%d windows, want at least 2 (the injected allocation, then a clean one): %+v", len(ws), ws)
		}
		for i, w := range ws {
			clean := w.Mallocs == 0 && w.TotalAlloc == 0
			if clean != (i == len(ws)-1) {
				t.Fatalf("window %d clean=%v: the run must stop at the first clean window: %+v", i, clean, ws)
			}
		}
		for _, w := range bn.report.Windows {
			if w.Frames != 10 {
				t.Errorf("a window of %d frames, want the whole session of 10", w.Frames)
			}
		}
		if w := bn.report.Windows[0]; w.Mallocs == 0 || w.TotalAlloc < 64 {
			t.Errorf("first window %+v lacks the allocation", w)
		}
		if bn.report.Mallocs != 0 || bn.report.TotalAlloc != 0 || bn.report.Frames != 10 {
			t.Errorf("report is of the clean window: %+v", bn.report)
		}
		// The clean window does not excuse it: the allocation was ours.
		if bn.report.OwnAllocObjects == 0 || bn.report.OwnAllocFailure() == "" {
			t.Errorf("the allocation of ours in window 1 was not attributed: %d objects, sites %+v", bn.report.OwnAllocObjects, bn.report.AllocSites)
		}
	})
	t.Run("every session", func(t *testing.T) {
		cy := &cycler{n: 10}
		bn, a := newBenchRig(t, BenchOptions{Warm: 0, Windows: 4}, cy)
		driveBench(t, bn, a, cy, func(w, f int) bool { return f == 9 })
		if got := len(bn.report.Windows); got != 4 {
			t.Fatalf("%d windows, want all 4", got)
		}
		for i, w := range bn.report.Windows {
			if w.Mallocs == 0 {
				t.Errorf("window %d saw nothing", i)
			}
		}
		if bn.report.Mallocs == 0 {
			t.Error("the report must show the allocation")
		}
	})
	t.Run("starts at a session boundary", func(t *testing.T) {
		cy := &cycler{n: 10}
		bn, a := newBenchRig(t, BenchOptions{Warm: 4, PostGCWarm: 2, Windows: 1}, cy)
		starts := -1
		for i := 0; i < 100 && !a.done; i++ {
			bn.before(a)
			if bn.measuring && starts < 0 {
				starts = cy.frame
			}
			cy.frame++
			bn.after(a)
		}
		if starts != 10 {
			t.Errorf("the window began at frame %d, want the next boundary (10)", starts)
		}
	})
}

// D27's amendment: an allocation of ours that misses some windows (here every
// other one, as rexlook's 900-frame cycles against TestAllocEvery 1800) fails
// the gate although a window is clean.
func TestBenchSparseAllocationOfOursFails(t *testing.T) {
	cy := &cycler{n: 10}
	bn, a := newBenchRig(t, BenchOptions{Warm: 5, PostGCWarm: 5, Windows: 6, TestAllocEvery: 20}, cy)
	driveBench(t, bn, a, cy, func(w, f int) bool { return false })
	r := bn.report
	if len(r.Windows) < 2 || r.Mallocs != 0 {
		t.Fatalf("want a dirty window, then a clean one: %+v", r.Windows)
	}
	msg := r.OwnAllocFailure()
	if msg == "" || !strings.Contains(msg, "gonativeui/app.(*benchRun).after") {
		t.Fatalf("gate message %q; sites %+v", msg, r.AllocSites)
	}
	t.Log(msg)
}

// A display stall (no frames while the loop keeps coming round) extends the
// deadline, up to maxStallExtension; a main thread that blocks gets nothing,
// nor does a loop that draws.
func TestBenchDeadlineOverDisplayStalls(t *testing.T) {
	const ms = int64(1e6)
	turns := func(b *benchRun, from, to, step, lastDraw int64) {
		for now := from; now <= to; now += step {
			b.loopTurn(now, lastDraw)
		}
	}
	b := &benchRun{deadline: 60_000 * ms}
	turns(b, 1000*ms, 21_000*ms, 50*ms, 0) // 20 s without a frame, the loop turning every 50 ms
	if got := b.deadline - 60_000*ms; got < 18_900*ms || got > 20_000*ms {
		t.Errorf("a 20 s display stall extended the deadline by %d ms", got/ms)
	}
	b = &benchRun{deadline: 60_000 * ms}
	turns(b, 1000*ms, 21_000*ms, 1500*ms, 0) // the main thread out for 1.5 s at a time
	if b.deadline != 60_000*ms {
		t.Errorf("a blocked main thread extended the deadline by %d ms", (b.deadline-60_000*ms)/ms)
	}
	b = &benchRun{deadline: 60_000 * ms}
	for now := int64(1000 * ms); now <= 21_000*ms; now += 16 * ms {
		b.loopTurn(now, now) // drawing
	}
	if b.deadline != 60_000*ms {
		t.Errorf("a loop that draws extended the deadline")
	}
	b = &benchRun{deadline: 60_000 * ms}
	turns(b, 1000*ms, 400_000*ms, 100*ms, 0)
	if got := b.deadline - 60_000*ms; got != maxStallExtension {
		t.Errorf("extension %d ms, want the cap %d", got/ms, maxStallExtension/ms)
	}
}
