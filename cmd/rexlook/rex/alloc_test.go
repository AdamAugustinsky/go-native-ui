package rex

import (
	"math"
	"runtime"
	"testing"
	"time"

	"gonativeui/ui"
)

// The exact allocation gate: a warmed-up, scripted frame sequence allocates
// nothing (runtime.ReadMemStats deltas, as for ui's scene). The scripts move
// the pointer across the tabs, switch the focus between the panes by clicking,
// drag a divider back and forth, and — in the second script — split, maximise,
// restore, close and switch tabs, with animations running most of the time.

func readMem(run func(), frames int) (mallocs, bytes uint64) {
	runtime.GC()
	time.Sleep(20 * time.Millisecond)
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	for i := 0; i < frames; i++ {
		run()
	}
	runtime.ReadMemStats(&m1)
	return m1.Mallocs - m0.Mallocs, m1.TotalAlloc - m0.TotalAlloc
}

// zeroAlloc warms the script up and requires one clean measurement window.
func zeroAlloc(t *testing.T, name string, run func(), animating func() bool, boxes func() int, warm, frames int) {
	t.Helper()
	for i := 0; i < warm; i++ {
		run()
	}
	runtime.GC()
	const attempts = 6
	var mallocs, bytes uint64
	for a := 0; a < attempts; a++ {
		mallocs, bytes = readMem(run, frames)
		t.Logf("%s: window %d: %d frames (%d boxes): %d mallocs, %d bytes", name, a+1, frames, boxes(), mallocs, bytes)
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Errorf("%s: allocated in all %d windows (last: %d mallocs, %d bytes over %d frames)", name, attempts, mallocs, bytes, frames)
}

func TestZeroAllocFake(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	s := &tscript{script: script{x: h.x, in: &h.in}, frame: h.frame}
	zeroAlloc(t, "pointer script, fake text", s.step, h.c.Animating, h.c.Boxes, 1800, 900)

	h2 := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	s2 := &tscript{script: script{x: h2.x, in: &h2.in}, frame: h2.frame}
	zeroAlloc(t, "layout script, fake text", s2.structural, h2.c.Animating, h2.c.Boxes, 1920, 960)

	// Live resize: the window changes size every frame.
	h3 := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	n := 0
	resize := func() {
		n++
		h3.w = 1200 + int(600*math.Sin(float64(n)/17))
		h3.h = 800 + int(300*math.Sin(float64(n)/23))
		h3.frame()
	}
	zeroAlloc(t, "live resize, fake text", resize, h3.c.Animating, h3.c.Boxes, 600, 600)
}

// The planted allocation must read.
func TestAllocMeterSees(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	s := &tscript{script: script{x: h.x, in: &h.in}, frame: h.frame}
	var sink *[16]byte
	m, b := readMem(func() { s.step(); sink = new([16]byte) }, 600)
	_ = sink
	if m < 600 || b < 600*16 {
		t.Fatalf("planted allocation read as %d mallocs, %d bytes", m, b)
	}
}

// With the real text system and renderer (every frame also syncs the atlas).
func TestZeroAllocRealText(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the exact allocation gate runs against CoreText; elsewhere the fake text system stands in (TestZeroAllocFake)")
	}
	g, x := newRig(t, WindowWidthPt, WindowHeightPt, 2)
	s := &tscript{script: script{x: x, in: &g.In}, frame: func() { g.Frame(x) }}
	zeroAlloc(t, "pointer script, real text", s.step, g.C.Animating, g.C.Boxes, 1800, 900)
	s2 := &tscript{script: script{x: x, in: &g.In}, frame: func() { g.Frame(x) }}
	zeroAlloc(t, "layout script, real text", s2.structural, g.C.Animating, g.C.Boxes, 1920, 960)
}

// The scripts do what they claim: they hover tabs, move the focus, drag both
// kinds of divider, split, maximise, close and switch tabs, with animations
// running a good part of the time.
func TestScriptsExercise(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	var (
		animating, hovered, focusChanges, tabChanges, maxSeen, minSeen, maximised int
		ratios                                                                    = map[float32]bool{}
		lastFocus                                                                 uint32
		lastTab                                                                   = -1
	)
	minSeen = 99
	track := func() {
		h.frame()
		x := h.x
		if h.c.Animating() {
			animating++
		}
		if h.cursor() == ui.CursorPointer {
			hovered++
		}
		tb := x.cur()
		if tb.focus != lastFocus {
			focusChanges++
			lastFocus = tb.focus
		}
		if x.active != lastTab {
			tabChanges++
			lastTab = x.active
		}
		maxSeen = max(maxSeen, tb.numPanes())
		minSeen = min(minSeen, tb.numPanes())
		if tb.max != 0 {
			maximised++
		}
		for i := range tb.nodes {
			if n := &tb.nodes[i]; n.used && !n.leaf {
				ratios[n.ratio] = true
			}
		}
	}
	s := &tscript{script: script{x: h.x, in: &h.in}, frame: track}
	for i := 0; i < 900; i++ {
		s.step()
	}
	if focusChanges < 3 || hovered < 100 || len(ratios) < 40 || animating < 150 {
		t.Errorf("pointer script: %d focus changes, %d frames over a tab, %d divider positions, %d animating frames", focusChanges, hovered, len(ratios), animating)
	}
	s2 := &tscript{script: script{x: h.x, in: &h.in}, frame: track}
	animating = 0
	for i := 0; i < 480; i++ {
		s2.structural()
	}
	if maxSeen < 5 || minSeen > 3 || maximised < 30 || tabChanges < 3 || animating < 100 {
		t.Errorf("layout script: panes %d..%d, %d maximised frames, %d tab changes, %d animating frames", minSeen, maxSeen, maximised, tabChanges, animating)
	}
}
