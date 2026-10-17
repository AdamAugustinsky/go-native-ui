package ui

import (
	"runtime"
	"testing"
)

func cursorBox(c *Context, x, y, w, h float32, cur Cursor, name string) ID {
	c.NextSize(Px(w), Px(h))
	c.NextPos(x, y)
	c.NextCursor(cur)
	return c.Box(Hoverable|FloatingX|FloatingY, name)
}

func TestCursorRegions(t *testing.T) {
	h := newHarness(t, 400, 300)
	var regs []CursorRegion
	h.frame(func(c *Context) {
		cursorBox(c, 10, 10, 100, 40, CursorPointer, "a")
		cursorBox(c, 50, 20, 100, 40, CursorResizeEW, "b") // overlaps a, drawn after: on top
		c.NextSize(Px(60), Px(60))
		c.NextPos(200, 100)
		c.Box(Hoverable|FloatingX|FloatingY, "plain") // no cursor: not a region
		// Clipped by its parent: only 20 px of the 100 px box show.
		c.NextSize(Px(40), Px(40))
		c.NextPos(300, 200)
		cl := c.Box(Clip|FloatingX|FloatingY, "clip")
		c.Push(cl)
		cursorBox(c, 20, 0, 100, 40, CursorText, "in-clip")
		cursorBox(c, 60, 0, 10, 10, CursorPointer, "out-of-clip") // entirely outside the clip: dropped
		c.Pop()
		cursorBox(c, 380, 280, 100, 100, CursorPointer, "edge") // clipped by the window
	})
	regs = h.c.CursorRegions(regs)
	want := []CursorRegion{
		{[4]float32{10, 10, 110, 50}, CursorPointer},
		{[4]float32{50, 20, 150, 60}, CursorResizeEW},
		{[4]float32{320, 200, 340, 240}, CursorText},
		{[4]float32{380, 280, 400, 300}, CursorPointer},
	}
	if len(regs) != len(want) {
		t.Fatalf("%d regions %+v, want %d", len(regs), regs, len(want))
	}
	for i := range want {
		if regs[i] != want[i] {
			t.Errorf("region %d: %+v, want %+v", i, regs[i], want[i])
		}
	}
	// The slice is reused.
	if again := h.c.CursorRegions(regs[:0]); len(again) != len(want) || &again[0] != &regs[0] {
		t.Errorf("not appended in place")
	}
}

func TestCursorRegionsDrawOrder(t *testing.T) {
	// A floating box is drawn after its non-floating siblings, whatever the
	// order they were built in: it must come last in the list.
	h := newHarness(t, 400, 300)
	h.frame(func(c *Context) {
		cursorBox(c, 0, 0, 50, 50, CursorResizeNS, "floating-first")
		c.NextSize(Px(50), Px(50))
		c.NextCursor(CursorText)
		c.Box(Hoverable, "flow")
	})
	regs := h.c.CursorRegions(nil)
	if len(regs) != 2 || regs[0].Cursor != CursorText || regs[1].Cursor != CursorResizeNS {
		t.Errorf("%+v", regs)
	}
}

func TestActiveCursor(t *testing.T) {
	h := newHarness(t, 400, 300)
	build := func(c *Context) {
		c.NextSize(Px(100), Px(40))
		c.NextPos(10, 10)
		c.NextCursor(CursorResizeEW)
		c.Box(Clickable|FloatingX|FloatingY, "div")
		c.NextSize(Px(100), Px(40))
		c.NextPos(10, 100)
		c.Box(Clickable|FloatingX|FloatingY, "plain")
	}
	h.frame(build)
	if got := h.c.ActiveCursor(); got != CursorArrow {
		t.Errorf("idle: %v", got)
	}
	h.move(50, 30)
	h.frame(build)
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(build)
	if got := h.c.ActiveCursor(); got != CursorResizeEW {
		t.Errorf("pressed on the divider: %v", got)
	}
	h.move(300, 250) // a drag keeps its cursor wherever the pointer goes
	h.frame(build)
	if got := h.c.ActiveCursor(); got != CursorResizeEW {
		t.Errorf("dragging away: %v", got)
	}
	h.in.SetButton(ButtonLeft, false, 0)
	h.frame(build)
	if got := h.c.ActiveCursor(); got != CursorArrow {
		t.Errorf("released: %v", got)
	}
	h.move(50, 120)
	h.frame(build)
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(build)
	if got := h.c.ActiveCursor(); got != CursorArrow {
		t.Errorf("pressed on a box without cursor: %v", got)
	}
}

func TestDragRegions(t *testing.T) {
	h := newHarness(t, 400, 300)
	h.frame(func(c *Context) {
		// A page below with an input box that is not near the titlebar.
		c.NextSize(Px(400), Px(250))
		c.NextPos(0, 50)
		c.Box(Hoverable|FloatingX|FloatingY, "page")
		// The titlebar band, a drag area; a tab and a button above it.
		c.NextSize(Px(400), Px(46))
		c.NextPos(0, 0)
		c.Box(WindowDrag|FloatingX|FloatingY, "bar")
		c.NextSize(Px(100), Px(30))
		c.NextPos(100, 8)
		c.Box(Clickable|FloatingX|FloatingY, "tab")
		c.NextSize(Px(30), Px(30))
		c.NextPos(300, 8)
		c.Box(Hoverable|FloatingX|FloatingY, "btn")
	})
	regs := h.c.DragRegions(nil)
	// bar (drag), tab, btn (blocks); the page lies outside the band's union... it overlaps
	// only below y=46, so it intersects the band's rectangle by 0 rows: left out.
	want := []DragRegion{
		{[4]float32{0, 0, 400, 46}, false},
		{[4]float32{100, 8, 200, 38}, true},
		{[4]float32{300, 8, 330, 38}, true},
	}
	if len(regs) != len(want) {
		t.Fatalf("%+v", regs)
	}
	for i := range want {
		if regs[i] != want[i] {
			t.Errorf("%d: %+v want %+v", i, regs[i], want[i])
		}
	}
	// A hit-testable box drawn before (under) the drag area is not a blocker
	// that matters, but one built over it is; none without WindowDrag.
	h2 := newHarness(t, 100, 100)
	h2.frame(func(c *Context) { c.Box(Hoverable, "x") })
	if r := h2.c.DragRegions(nil); len(r) != 0 {
		t.Errorf("no WindowDrag: %+v", r)
	}
}

func TestPoolReserveNoGrowth(t *testing.T) {
	build := func(n int) func(c *Context) {
		return func(c *Context) {
			for i := 0; i < n; i++ {
				c.NextSize(Px(1), Px(1))
				c.NextPos(float32(i%300), float32(i/300))
				c.BoxKeyed(FloatingX|FloatingY, KeyN(1, uint64(i)), nil)
			}
		}
	}
	// A jump from 40 to 2500 boxes after warm-up must not allocate once
	// reserved. The allocation count is process wide, so a stray allocation of
	// another goroutine can show in a window: it passes if any of a few fresh
	// contexts has a clean jump (without Reserve every one allocates).
	var last uint64
	for attempt := 0; attempt < 5; attempt++ {
		h := newHarness(t, 400, 300)
		h.c.Reserve(3000)
		for i := 0; i < 5; i++ {
			h.frame(build(40))
		}
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		h.frame(build(2500))
		runtime.ReadMemStats(&m1)
		if last = m1.Mallocs - m0.Mallocs; last == 0 {
			return
		}
	}
	t.Errorf("%d allocations on the box-count jump in every attempt", last)
}

func TestRegionsZeroAlloc(t *testing.T) {
	h := newHarness(t, 400, 300)
	var cr []CursorRegion
	var dr []DragRegion
	build := func(c *Context) {
		cursorBox(c, 10, 10, 100, 40, CursorPointer, "a")
		c.NextSize(Px(400), Px(46))
		c.Box(WindowDrag|FloatingX|FloatingY, "bar")
		c.NextSize(Px(40), Px(30))
		c.NextPos(100, 8)
		c.Box(Clickable|FloatingX|FloatingY, "tab")
	}
	for i := 0; i < 20; i++ {
		h.frame(build)
		cr, dr = h.c.PointerRegions(cr, dr)
	}
	runtime.GC()
	if n := testing.AllocsPerRun(100, func() {
		h.frame(build)
		cr, dr = h.c.PointerRegions(cr, dr)
		_ = h.c.ActiveCursor()
	}); n != 0 {
		t.Errorf("%v allocs per frame", n)
	}
}
