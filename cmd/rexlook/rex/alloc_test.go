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

// script feeds input to a Rex and runs its frames.
type script struct {
	x     *Rex
	in    *ui.Input
	frame func()
	f     int
	dragX float32
	dragY float32
}

func (s *script) move(x, y float32) { s.in.MouseX, s.in.MouseY = x, y }
func (s *script) down()             { s.in.SetButton(ui.ButtonLeft, true, 1) }
func (s *script) up()               { s.in.SetButton(ui.ButtonLeft, false, 0) }

func (s *script) pane(i int) [4]float32 {
	t := s.x.cur()
	n := 0
	for slot := range t.panes {
		if !t.panes[slot].used {
			continue
		}
		if n == i%t.numPanes() {
			return s.x.disp[slot]
		}
		n++
	}
	return [4]float32{}
}

func (s *script) focused() [4]float32 {
	t := s.x.cur()
	return s.x.disp[t.slotOf(t.focus)]
}

// step runs one frame of the pointer/focus/drag script.
func (s *script) step() {
	f := s.f % 900
	s.f++
	x := s.x
	switch {
	case f < 200: // hover across the tabs and back
		a, _ := centre(x.tabRects[0])
		b, _ := centre(x.tabRects[x.nTabs-1])
		_, y := centre(x.tabRects[0])
		s.move(a+(b-a)*float32(0.5-0.5*math.Cos(float64(f)*math.Pi/100)), y)
	case f >= 200 && f < 203, f >= 330 && f < 333, f >= 460 && f < 463: // focus another pane by clicking it
		r := s.pane((f - 200) / 130)
		s.move(r[2]-30*x.k, r[3]-30*x.k)
		switch f % 130 {
		case 71:
			s.down()
		case 72:
			s.up()
		}
	case f >= 520 && f < 700: // drag the divider between the columns right and left
		t := x.cur()
		g := t.nodes[t.root].gut
		switch {
		case f == 520:
			s.dragX, s.dragY = (g[0]+g[2])/2, (g[1]+g[3])/2
			s.move(s.dragX, s.dragY)
		case f == 524:
			s.down()
		case f < 690:
			s.move(s.dragX+90*float32(math.Sin(float64(f-524)*math.Pi/40)), s.dragY+10)
		case f == 690:
			s.up()
		}
	case f >= 700 && f < 780: // the horizontal divider
		t := x.cur()
		var g [4]float32
		for i := range t.nodes {
			if n := &t.nodes[i]; n.used && !n.leaf && n.axis == ui.AxisY {
				g = n.gut
			}
		}
		switch {
		case f == 700:
			s.dragX, s.dragY = (g[0]+g[2])/2, (g[1]+g[3])/2
			s.move(s.dragX, s.dragY)
		case f == 703:
			s.down()
		case f < 770:
			s.move(s.dragX, s.dragY+50*float32(math.Sin(float64(f-703)*math.Pi/20)))
		case f == 770:
			s.up()
		}
	default:
		s.move(4, 4)
	}
	s.frame()
}

// structural runs one frame of the layout script: split, split, maximise,
// restore, close, close and tab switches, each followed by animation.
func (s *script) structural() {
	f := s.f % 480
	s.f++
	x := s.x
	click := func(b int, which uint) {
		r := s.focused()
		cx, cy := x.btnCentre(r, b)
		s.move(cx, cy)
		switch which {
		case 0:
			s.down()
		case 1:
			s.up()
		}
	}
	switch f {
	case 10, 11:
		click(btnSplitX, uint(f-10))
	case 50, 51:
		click(btnSplitY, uint(f-50))
	case 90, 91:
		click(btnMax, uint(f-90))
	case 140, 141:
		click(btnMax, uint(f-140))
	case 190, 191:
		click(btnClose, uint(f-190))
	case 230, 231:
		click(btnClose, uint(f-230))
	case 280, 281, 330, 331, 380, 381, 430, 431:
		i := (f - 280) / 50 % 3
		switch {
		case f >= 380 && f < 430:
			i = 2
		case f >= 430:
			i = 0
		}
		cx, cy := centre(x.tabRects[i%x.nTabs])
		s.move(cx, cy)
		if f%2 == 0 {
			s.down()
		} else {
			s.up()
		}
	}
	s.frame()
}

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
	s := &script{x: h.x, in: &h.in, frame: h.frame}
	zeroAlloc(t, "pointer script, fake text", s.step, h.c.Animating, h.c.Boxes, 1800, 900)

	h2 := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	s2 := &script{x: h2.x, in: &h2.in, frame: h2.frame}
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
	s := &script{x: h.x, in: &h.in, frame: h.frame}
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
	s := &script{x: x, in: &g.In, frame: func() { g.Frame(x) }}
	zeroAlloc(t, "pointer script, real text", s.step, g.C.Animating, g.C.Boxes, 1800, 900)
	s2 := &script{x: x, in: &g.In, frame: func() { g.Frame(x) }}
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
		if x.Cursor() == CursorPointer {
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
	s := &script{x: h.x, in: &h.in, frame: track}
	for i := 0; i < 900; i++ {
		s.step()
	}
	if focusChanges < 3 || hovered < 100 || len(ratios) < 40 || animating < 150 {
		t.Errorf("pointer script: %d focus changes, %d frames over a tab, %d divider positions, %d animating frames", focusChanges, hovered, len(ratios), animating)
	}
	s2 := &script{x: h.x, in: &h.in, frame: track}
	animating = 0
	for i := 0; i < 480; i++ {
		s2.structural()
	}
	if maxSeen < 5 || minSeen > 3 || maximised < 30 || tabChanges < 3 || animating < 100 {
		t.Errorf("layout script: panes %d..%d, %d maximised frames, %d tab changes, %d animating frames", minSeen, maxSeen, maximised, tabChanges, animating)
	}
}
