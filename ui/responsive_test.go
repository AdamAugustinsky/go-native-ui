package ui

import (
	"math"
	"testing"
)

// frameDT is harness.frame with a chosen time step.
func (h *harness) frameDT(dt float32, build func(c *Context)) {
	h.c.Begin(&h.in, h.w, h.h, 1, dt)
	if build != nil {
		build(h.c)
	}
	h.c.End(&h.list)
	h.in.ClearFrame()
}

// idle runs frames until nothing animates (at most n).
func (h *harness) idle(n int, build func(c *Context)) int {
	for i := 0; i < n; i++ {
		h.frame(build)
		if !h.c.Animating() {
			return i + 1
		}
	}
	h.t.Fatalf("still animating after %d frames", n)
	return n
}

// A value first animated in a frame starts at its start value and moves by
// one display interval at most: the time before it, while nothing animated,
// belongs to no animation. Its first value is the same whatever the frame's
// dt (a key pressed 50 or 90 ms after the last frame).
func TestFreshAnimationIgnoresTheGapBeforeIt(t *testing.T) {
	key := HashString(0, "fresh")
	firstValue := func(interval, dt float32) float32 {
		h := newHarness(t, 200, 100)
		h.c.FrameInterval = interval
		h.frame(nil)
		var v float32
		h.frameDT(dt, func(c *Context) { v = c.AnimateValueFrom(key, 0, 1, 30) })
		return v
	}
	for _, iv := range []float32{0, 1.0 / 120} {
		dts := []float32{1.0 / 60, 0.05, 0.09}
		if iv != 0 {
			dts[0] = iv
		}
		want := firstValue(iv, dts[0])
		if want <= 0 || want >= 1 {
			t.Fatalf("interval %v: first value %v, want a step between 0 and 1", iv, want)
		}
		for _, dt := range dts[1:] {
			if got := firstValue(iv, dt); got != want {
				t.Errorf("interval %v: first value at dt %v is %v, at dt %v it is %v", iv, dt, got, dts[0], want)
			}
		}
	}

	// A value at rest that is given a new target starts afresh too.
	h := newHarness(t, 200, 100)
	var v float32
	for i := 0; i < 3; i++ {
		h.frame(func(c *Context) { v = c.AnimateValue(key, 0, 30) })
	}
	h.frameDT(0.09, func(c *Context) { v = c.AnimateValue(key, 1, 30) })
	if want := 1 - float32(math.Exp2(-30.0/60)); math.Abs(float64(v-want)) > 1e-4 {
		t.Errorf("retarget from rest after 90 ms: %v, want one 60 Hz step %v", v, want)
	}
	// A value already moving catches up with the whole of a late frame.
	prev := v
	h.frameDT(0.09, func(c *Context) { v = c.AnimateValue(key, 1, 30) })
	if want := 1 - (1-prev)*float32(math.Exp2(-30*0.09)); math.Abs(float64(v-want)) > 1e-4 {
		t.Errorf("a moving value did not take the late frame's time: %v, want %v", v, want)
	}
}

// The focus value of a box and a wheel's glide start the same way.
func TestFreshBoxAndScrollStepsIgnoreTheGap(t *testing.T) {
	focusFirst := func(dt float32) float32 {
		h := newHarness(t, 200, 100)
		key := HashString(0, "f")
		build := func(c *Context) {
			c.NextSize(Px(100), Px(30))
			c.BoxKeyed(Focusable|FocusAnimation, key, nil)
		}
		h.idle(10, build)
		h.c.SetFocus(key)
		h.frameDT(dt, build)
		return h.c.FocusTOf(key)
	}
	scrollFirst := func(dt float32) float64 {
		h := newHarness(t, 400, 300)
		var sc ID
		build := func(c *Context) {
			c.NextSize(Px(200), Px(100))
			sc = c.Box(ScrollY|Clip|Hoverable, "sc")
			c.Push(sc)
			c.NextSize(Pct(1), Px(1000))
			c.Box(0, "content")
			c.Pop()
		}
		h.move(50, 50)
		h.idle(10, build)
		h.in.AddScroll(0, -1, false)
		h.frameDT(dt, build)
		_, y := h.c.Scroll(sc)
		return y
	}
	f0, s0 := focusFirst(1.0/60), scrollFirst(1.0/60)
	if f0 <= 0 || f0 >= 1 || s0 <= 0 || s0 >= 48 {
		t.Fatalf("first steps: focus %v, scroll %v", f0, s0)
	}
	for _, dt := range []float32{0.05, 0.09} {
		if f := focusFirst(dt); f != f0 {
			t.Errorf("focus value after a %v s gap: %v, after 1/60: %v", dt, f, f0)
		}
		if s := scrollFirst(dt); s != s0 {
			t.Errorf("wheel glide after a %v s gap: %v, after 1/60: %v", dt, s, s0)
		}
	}
}

// pressScene is a button and its neighbour, the press value read by the
// build as a recipe reads it (ActiveTOf before the box is built).
type pressScene struct {
	a, b       Key
	downA      float32 // ActiveTOf(a) as the build read it
	hotA, hotB float32
	sigA       Signal
}

func (s *pressScene) build(c *Context) {
	s.downA, s.hotA, s.hotB = c.ActiveTOf(s.a), c.HotTOf(s.a), c.HotTOf(s.b)
	f := Clickable | HotAnimation | ActiveAnimation | FloatingX | FloatingY
	c.NextSize(Px(100), Px(40))
	c.NextPos(10, 10)
	id := c.BoxKeyed(f, s.a, nil)
	c.NextSize(Px(100), Px(40))
	c.NextPos(110, 10)
	c.BoxKeyed(f, s.b, nil)
	s.sigA = c.Signal(id)
}

func newPressScene() *pressScene {
	return &pressScene{a: HashString(0, "a"), b: HashString(0, "b")}
}

// A press shows in full in the frame of the mouse-down: the build of that
// frame reads it.
func TestPressShowsInItsOwnFrame(t *testing.T) {
	h := newHarness(t, 400, 200)
	s := newPressScene()
	h.move(50, 30)
	h.idle(20, s.build)
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(s.build)
	if !s.sigA.Pressed || s.downA != 1 {
		t.Fatalf("press frame: pressed %v, the build read Down %v, want 1", s.sigA.Pressed, s.downA)
	}
	// The release fades.
	h.in.SetButton(ButtonLeft, false, 0)
	h.frame(s.build)
	if !s.sigA.Clicked {
		t.Fatal("release inside did not click")
	}
	h.frame(s.build)
	if s.downA <= 0 || s.downA >= 1 {
		t.Errorf("release: Down %v the frame after, want a fade between 0 and 1", s.downA)
	}
	if n := h.idle(30, s.build); n > 12 {
		t.Errorf("release took %d frames to settle", n)
	}
	if s.downA != 0 {
		t.Errorf("settled Down %v", s.downA)
	}
}

// While the button is held the pressed look follows the pointer: off the
// control it lets go (a release there would not click), back on it presses
// again.
func TestPressFollowsThePointer(t *testing.T) {
	h := newHarness(t, 400, 200)
	s := newPressScene()
	h.move(50, 30)
	h.idle(20, s.build)
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(s.build)
	h.move(300, 150) // off it, button still down
	h.frame(s.build)
	h.idle(30, s.build)
	if s.downA != 0 {
		t.Fatalf("dragged off: Down %v, want 0", s.downA)
	}
	h.move(60, 30) // back on
	h.frame(s.build)
	if s.downA != 1 {
		t.Fatalf("dragged back: Down %v, want 1 in the same frame", s.downA)
	}
	h.move(300, 150)
	h.idle(30, s.build)
	h.in.SetButton(ButtonLeft, false, 0)
	h.frame(s.build)
	if s.sigA.Clicked {
		t.Error("released off the control and it clicked")
	}
}

// Hover lands at once and fades out: moving to a neighbour lights it fully
// in that frame while the one left fades.
func TestHoverInAtOnceOutFades(t *testing.T) {
	h := newHarness(t, 400, 200)
	s := newPressScene()
	h.idle(20, s.build)
	h.move(50, 30)
	h.frame(s.build)
	if s.hotA != 1 {
		t.Fatalf("hover in: the build read Hot %v, want 1", s.hotA)
	}
	h.idle(20, s.build)
	h.move(150, 30)
	h.frame(s.build)
	if s.hotB != 1 {
		t.Errorf("neighbour: Hot %v, want 1 at once", s.hotB)
	}
	h.frame(s.build)
	if s.hotA <= 0 || s.hotA >= 1 {
		t.Errorf("hover out: Hot %v the frame after, want a fade between 0 and 1", s.hotA)
	}
	if n := h.idle(30, s.build); n > 12 {
		t.Errorf("hover out took %d frames to settle", n)
	}
	if s.hotA != 0 || s.hotB != 1 {
		t.Errorf("settled: a %v b %v", s.hotA, s.hotB)
	}
}

// The rates of each direction are the context's: with every one at once
// (strict AppKit), a release and a hover out are gone the next frame.
func TestStateRatesEachWay(t *testing.T) {
	h := newHarness(t, 400, 200)
	h.c.Rates = Rates{Hot: 1e6, Active: 1e6, Focus: 1e6, Scroll: 40}
	s := newPressScene()
	h.move(50, 30)
	h.idle(5, s.build)
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(s.build)
	h.in.SetButton(ButtonLeft, false, 0)
	h.move(150, 30)
	h.frame(s.build)
	h.frame(s.build)
	if s.downA != 0 || s.hotA != 0 || h.c.Animating() {
		t.Errorf("instant rates: Down %v Hot %v animating %v", s.downA, s.hotA, h.c.Animating())
	}
	// A finite rate in: the value rises over frames.
	h.c.Rates.HotIn = 20
	h.move(50, 30)
	h.frame(s.build)
	if s.hotA <= 0 || s.hotA >= 1 {
		t.Errorf("finite hover-in rate: first Hot %v", s.hotA)
	}
}

// A scroll of the content under a held press cancels it: the release clicks
// nothing.
func TestScrollCancelsAHeldPress(t *testing.T) {
	h := newHarness(t, 400, 300)
	var sc ID
	row := HashString(0, "row")
	var sig Signal
	build := func(c *Context) {
		c.NextSize(Px(200), Px(100))
		sc = c.Box(ScrollY|Clip, "sc")
		c.Push(sc)
		c.NextSize(Pct(1), Px(400)) // one tall row: still under the pointer after the scroll
		id := c.BoxKeyed(Clickable|ActiveAnimation, row, nil)
		c.Pop()
		sig = c.Signal(id)
	}
	h.move(50, 50)
	h.idle(10, build)
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(build)
	h.in.AddScroll(0, -30, true)
	h.frame(build)
	if _, y := h.c.Scroll(sc); y != 30 {
		t.Fatalf("scrolled to %v", y)
	}
	if h.c.ActiveKey() != 0 {
		t.Error("the press is still held after the content scrolled under it")
	}
	h.in.SetButton(ButtonLeft, false, 0)
	h.frame(build)
	if sig.Clicked {
		t.Error("released after a scroll and it clicked")
	}
	// Without a scroll the same press clicks.
	h.idle(20, build)
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(build)
	h.in.SetButton(ButtonLeft, false, 0)
	h.frame(build)
	if !sig.Clicked {
		t.Error("an ordinary press and release did not click")
	}
}

// Rows carried under a pointer that stands still while their list scrolls do
// not light in turn: the hover stays where it was until the scroll stops,
// then goes to the row under the pointer.
func TestHoverHoldsWhileContentScrollsUnderAStillPointer(t *testing.T) {
	h := newHarness(t, 400, 300)
	var sc ID
	rows := [20]Key{}
	for i := range rows {
		rows[i] = KeyN(HashString(0, "r"), uint64(i))
	}
	build := func(c *Context) {
		c.NextSize(Px(200), Px(100))
		sc = c.Box(ScrollY|Clip, "sc")
		c.Push(sc)
		for i := range rows {
			c.NextSize(Pct(1), Px(20))
			c.BoxKeyed(Clickable|HotAnimation, rows[i], nil)
		}
		c.Pop()
	}
	h.frame(build)
	h.move(50, 30) // row 1
	h.idle(20, build)
	if h.c.HotKey() != rows[1] {
		t.Fatalf("hot %x, want row 1", h.c.HotKey())
	}
	for i := 0; i < 5; i++ {
		h.in.AddScroll(0, -20, true)
		h.frame(build)
		if h.c.HotKey() != rows[1] {
			t.Fatalf("scroll frame %d: hover moved to %x while the pointer stood still", i, h.c.HotKey())
		}
	}
	h.idle(20, build)
	if h.c.HotKey() != rows[6] {
		t.Errorf("after the scroll: hot %x, want row 6", h.c.HotKey())
	}
	if h.c.HotTOf(rows[1]) != 0 || h.c.HotTOf(rows[6]) != 1 {
		t.Errorf("after the scroll: row 1 %v, row 6 %v", h.c.HotTOf(rows[1]), h.c.HotTOf(rows[6]))
	}
	// A pointer that moves during a scroll is followed.
	h.in.AddScroll(0, -20, true)
	h.move(50, 50)
	h.frame(build)
	if h.c.HotKey() == rows[6] {
		t.Error("a moving pointer kept the old hover during a scroll")
	}
}

// Hover, press and their fades allocate nothing once warm.
func TestResponsiveStatesZeroAlloc(t *testing.T) {
	h := newHarness(t, 400, 300)
	s := newPressScene()
	var sc ID
	build := func(c *Context) {
		s.build(c)
		c.NextSize(Px(200), Px(100))
		c.NextPos(0, 100)
		sc = c.Box(ScrollY|Clip|FloatingX|FloatingY, "sc")
		c.Push(sc)
		for i := 0; i < 20; i++ {
			c.NextSize(Pct(1), Px(20))
			c.BoxKeyed(Clickable|HotAnimation|ActiveAnimation, KeyN(9, uint64(i)), nil)
		}
		c.Pop()
	}
	step := 0
	cycle := func() {
		switch step % 6 {
		case 0:
			h.move(50, 30)
		case 1:
			h.in.SetButton(ButtonLeft, true, 1)
		case 2:
			h.move(150, 30)
		case 3:
			h.in.SetButton(ButtonLeft, false, 0)
		case 4:
			h.move(50, 150)
			h.in.SetButton(ButtonLeft, true, 1)
		case 5:
			h.in.AddScroll(0, -10, true)
			h.in.SetButton(ButtonLeft, false, 0)
		}
		step++
		h.frame(build)
	}
	for i := 0; i < 60; i++ {
		cycle()
	}
	if n := testing.AllocsPerRun(120, cycle); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}

// Measures what the changes are for, logged with -v: frames from a mouse-down
// to a full pressed look, and the first value of a fresh animation at three
// gaps.
func TestMeasureResponsiveness(t *testing.T) {
	for _, hz := range []float32{60, 120} {
		h := newHarness(t, 400, 200)
		h.c.FrameInterval = 1 / hz
		s := newPressScene()
		h.move(50, 30)
		h.idle(20, s.build)
		h.in.SetButton(ButtonLeft, true, 1)
		n := 0
		for ; n < 60; n++ {
			h.frameDT(1/hz, s.build)
			if s.downA >= 0.95 {
				break
			}
		}
		t.Logf("%v Hz: the mouse-down's frame +%d frames to a pressed look of 0.95 (%.1f ms)", hz, n, float32(n)*1000/hz)
		if n != 0 {
			t.Errorf("%v Hz: pressed look %d frames after the mouse-down", hz, n)
		}
	}
	for _, dt := range []float32{1.0 / 120, 0.05, 0.09} {
		h := newHarness(t, 200, 100)
		h.c.FrameInterval = 1.0 / 120
		h.frame(nil)
		var v float32
		h.frameDT(dt, func(c *Context) { v = c.AnimateValueFrom(1, 0, 1, 28.8) }) // OverlayEnter's rate
		t.Logf("fresh overlay fade, first frame after a %.1f ms gap: %.3f", dt*1000, v)
	}
}
