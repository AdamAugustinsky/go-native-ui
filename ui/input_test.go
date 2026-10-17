package ui

import (
	"math"
	"testing"
)

type scene2 struct{ back, front, pop, inner ID }

func TestHitTestingOrderAndClip(t *testing.T) {
	h := newHarness(t, 400, 300)
	var s scene2
	build := func(c *Context) {
		c.NextSize(Px(100), Px(100))
		c.NextPos(0, 0)
		s.back = c.Box(Clickable|FloatingX|FloatingY, "back")
		c.NextSize(Px(100), Px(100))
		c.NextPos(50, 50)
		s.front = c.Box(Clickable|FloatingX|FloatingY, "front") // overlaps back, drawn later
		// A clipped container holding a child that sticks out of it.
		c.NextSize(Px(60), Px(60))
		c.NextPos(200, 0)
		cl := c.Box(Clip|FloatingX|FloatingY, "clipper")
		c.Push(cl)
		c.NextSize(Px(200), Px(200))
		s.inner = c.Box(Clickable, "inner")
		c.Pop()
		// A floating popup declared before nothing but still drawn after its siblings.
		c.NextSize(Px(40), Px(40))
		c.NextPos(10, 150)
		s.pop = c.Box(Hoverable|FloatingX|FloatingY, "pop")
	}
	h.frame(build) // layout for the next frame's hit test
	hov := func(id ID) bool { return h.c.Signal(id).Hovering }
	probe := func(x, y float32) (back, front, inner, pop bool) {
		h.move(x, y)
		h.frame(build)
		return hov(s.back), hov(s.front), hov(s.inner), hov(s.pop)
	}
	if b, f, _, _ := probe(20, 20); !b || f {
		t.Errorf("only back at (20,20): back=%v front=%v", b, f)
	}
	if b, f, _, _ := probe(70, 70); b || !f {
		t.Errorf("overlap goes to the box drawn last: back=%v front=%v", b, f)
	}
	if _, _, in, _ := probe(230, 30); !in {
		t.Error("inner not hovered inside the clip")
	}
	if _, _, in, _ := probe(280, 100); in {
		t.Error("inner hovered outside its parent's clip")
	}
	if _, _, _, p := probe(20, 160); !p {
		t.Error("popup not hovered")
	}
	// Not inside the window: nothing is hot.
	h.in.Inside = false
	if b, f, in, p := probe(20, 20); b || f || in || p {
		t.Error("hover while pointer outside the window")
	}
}

func TestSignalsClickDragDouble(t *testing.T) {
	h := newHarness(t, 400, 300)
	var box ID
	build := func(c *Context) {
		c.NextSize(Px(100), Px(50))
		c.NextPos(10, 10)
		box = c.Box(Clickable|FloatingX|FloatingY, "btn")
	}
	h.frame(build)
	frame := func() Signal {
		var s Signal
		h.frame(func(c *Context) { build(c); s = c.Signal(box) })
		return s
	}

	h.move(50, 30)
	if s := frame(); !s.Hovering || s.Pressed || s.Clicked {
		t.Fatalf("hover: %+v", s)
	}

	// Click.
	h.in.SetButton(ButtonLeft, true, 1)
	if s := frame(); !s.Pressed || !s.Dragging || s.Clicked {
		t.Fatalf("press: %+v", s)
	}
	h.in.SetButton(ButtonLeft, false, 0)
	if s := frame(); !s.Released || !s.Clicked || s.DoubleClicked || s.Dragging {
		t.Fatalf("release: %+v", s)
	}
	if s := frame(); s.Released || s.Clicked || s.Pressed {
		t.Fatalf("edges must last one frame: %+v", s)
	}

	// Double click.
	h.in.SetButton(ButtonLeft, true, 2)
	frame()
	h.in.SetButton(ButtonLeft, false, 0)
	if s := frame(); !s.DoubleClicked {
		t.Fatalf("double click: %+v", s)
	}

	// Drag out of the box: capture holds, delta accumulates, release outside is not a click.
	h.in.SetButton(ButtonLeft, true, 1)
	frame()
	h.move(300, 200)
	s := frame()
	if !s.Dragging || s.Hovering || s.DragDelta != [2]float32{250, 170} {
		t.Fatalf("drag outside: %+v", s)
	}
	if h.c.ActiveKey() != h.c.KeyOf(box) {
		t.Fatal("active key lost while dragging outside")
	}
	h.in.SetButton(ButtonLeft, false, 0)
	s = frame()
	if !s.Released || s.Clicked || s.Dragging {
		t.Fatalf("release outside: %+v", s)
	}
	if h.c.ActiveKey() != 0 {
		t.Fatal("active key not cleared")
	}

	// Another box under the pointer while dragging must not get hover.
	// Right click.
	h.move(50, 30)
	frame()
	h.in.SetButton(ButtonRight, true, 1)
	frame()
	h.in.SetButton(ButtonRight, false, 0)
	if s := frame(); !s.RightClicked {
		t.Fatalf("right click: %+v", s)
	}

	// A press and release inside one frame still clicks.
	h.in.SetButton(ButtonLeft, true, 1)
	h.in.SetButton(ButtonLeft, false, 0)
	if s := frame(); !s.Pressed || !s.Clicked {
		t.Fatalf("fast click: %+v", s)
	}
}

func TestDragCaptureBlocksOtherHover(t *testing.T) {
	h := newHarness(t, 400, 300)
	var a, b ID
	build := func(c *Context) {
		c.NextSize(Px(50), Px(50))
		c.NextPos(0, 0)
		a = c.Box(Clickable|FloatingX|FloatingY, "a")
		c.NextSize(Px(50), Px(50))
		c.NextPos(100, 0)
		b = c.Box(Clickable|FloatingX|FloatingY, "b")
	}
	h.frame(build)
	h.move(10, 10)
	h.frame(build)
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(build)
	h.move(110, 10)
	h.frame(func(c *Context) {
		build(c)
		if c.Signal(b).Hovering {
			t.Error("box under the pointer is hovered while another holds the mouse")
		}
		if s := c.Signal(a); !s.Dragging {
			t.Error("dragging lost")
		}
	})
}

func TestFocus(t *testing.T) {
	h := newHarness(t, 400, 300)
	var pane, btn, other ID
	build := func(c *Context) {
		c.NextSize(Px(200), Px(200))
		pane = c.Box(Focusable|Clickable|FocusAnimation, "pane")
		c.Push(pane)
		c.NextSize(Px(50), Px(50))
		btn = c.Box(Clickable, "btn") // a press here focuses the pane
		c.Pop()
		c.NextSize(Px(50), Px(50))
		c.NextPos(300, 0)
		other = c.Box(Focusable|FloatingX|FloatingY, "other")
	}
	click := func(x, y float32) {
		h.move(x, y)
		h.frame(build)
		h.in.SetButton(ButtonLeft, true, 1)
		h.frame(build)
		h.in.SetButton(ButtonLeft, false, 0)
		h.frame(build)
	}
	h.frame(build)
	click(10, 10)
	if h.c.FocusKey() != h.c.KeyOf(pane) {
		t.Fatal("press on a child did not focus the focusable ancestor")
	}
	click(320, 10)
	if h.c.FocusKey() != h.c.KeyOf(other) {
		t.Fatal("focus did not move")
	}
	click(250, 250) // nothing hit-testable
	if h.c.FocusKey() != 0 {
		t.Fatal("press on nothing kept the focus")
	}
	_ = btn

	// Keyboard activation needs focus.
	h.c.SetFocus(h.c.KeyOf(pane))
	h.in.PushKey(KeyEvent{Code: KeyEnter, Down: true})
	var s Signal
	h.frame(func(c *Context) { build(c); s = c.Signal(pane) })
	if !s.Activated {
		t.Error("Enter did not activate the focused box")
	}
	h.in.PushKey(KeyEvent{Code: KeyEnter, Down: true})
	h.frame(func(c *Context) { build(c); s = c.Signal(other) })
	if s.Activated {
		t.Error("unfocused box activated")
	}
}

func TestAnimationConvergence(t *testing.T) {
	h := newHarness(t, 400, 300)
	var box ID
	build := func(c *Context) {
		c.NextSize(Px(100), Px(100))
		box = c.Box(Clickable|HotAnimation|ActiveAnimation|DrawHotEffects, "b")
	}
	h.frame(build)
	if h.c.Animating() {
		t.Fatal("animating with nothing hot")
	}
	h.move(50, 50)
	h.frame(build) // hot now; hot_t starts moving
	if !h.c.Animating() {
		t.Fatal("not animating after hover")
	}
	prev := float32(0)
	frames := 1
	for h.c.Animating() {
		if v := h.c.HotT(box); v < prev {
			t.Fatalf("hot_t went backwards: %v -> %v", prev, v)
		} else {
			prev = v
		}
		h.frame(build)
		frames++
		if frames > 200 {
			t.Fatal("hot_t never converged")
		}
	}
	if h.c.HotT(box) != 1 {
		t.Fatalf("hot_t = %v after convergence", h.c.HotT(box))
	}
	// rate 1-2^(-50/60) ≈ 0.44/frame: 0.995 within ~10 frames.
	if frames > 14 || frames < 5 {
		t.Errorf("converged in %d frames", frames)
	}
	// Leaving the box animates back down to 0.
	h.move(300, 250)
	for i := 0; i < 100 && (i == 0 || h.c.Animating()); i++ {
		h.frame(build)
	}
	if h.c.HotT(box) != 0 || h.c.Animating() {
		t.Fatalf("hot_t = %v animating=%v", h.c.HotT(box), h.c.Animating())
	}
}

func TestAnimateValue(t *testing.T) {
	h := newHarness(t, 400, 300)
	key := HashString(0, "val")
	target := float32(100)
	var got float32
	step := func() { h.frame(func(c *Context) { got = c.AnimateValue(key, target, 30) }) }
	step()
	if got != 100 || h.c.Animating() {
		t.Fatalf("first call should start at the target: %v", got)
	}
	target = 0
	step()
	if got >= 100 || got <= 0 || !h.c.Animating() {
		t.Fatalf("not moving: %v", got)
	}
	// Per-frame rate matches 1-2^(-k dt).
	want := 100 * math.Exp2(-30.0/60)
	if math.Abs(float64(got)-want) > 0.01 {
		t.Errorf("after one frame %v, want %v", got, want)
	}
	for i := 0; i < 200 && h.c.Animating(); i++ {
		step()
	}
	if got != 0 || h.c.Animating() {
		t.Fatalf("did not settle: %v", got)
	}
	// Asking twice in a frame steps once.
	target = 50
	h.frame(func(c *Context) {
		a := c.AnimateValue(key, target, 30)
		b := c.AnimateValue(key, target, 30)
		if a != b {
			t.Errorf("value stepped twice in a frame: %v then %v", a, b)
		}
	})
	// Dropped when not asked for.
	h.frame(nil)
	if len(h.c.av) > 1 && h.c.avPool.Len() > 1 {
		t.Errorf("unused animation value kept (%d live)", h.c.avPool.Len()-1)
	}
	h.frame(func(c *Context) { got = c.AnimateValueFrom(key, 7, 7, 30) })
	if got != 7 {
		t.Errorf("AnimateValueFrom start %v", got)
	}
}

func TestScrollClampAndApproach(t *testing.T) {
	h := newHarness(t, 400, 300)
	var sc ID
	build := func(c *Context) {
		c.NextSize(Px(200), Px(100))
		sc = c.Box(ScrollY|Clip|Hoverable, "sc")
		c.Push(sc)
		for i := 0; i < 10; i++ { // 10 × 30 = 300 of content in 100 px
			c.NextSize(Pct(1), Px(30))
			c.Box(Hoverable, string(rune('a'+i)))
		}
		c.Pop()
	}
	h.frame(build)
	h.move(50, 50)
	h.frame(build)

	// A precise trackpad delta applies directly (and clamps at the end).
	h.in.AddScroll(0, -60, true)
	var s Signal
	h.frame(func(c *Context) { build(c); s = c.Signal(sc) })
	if _, y := h.c.Scroll(sc); y != 60 || s.Scroll[1] != 60 {
		t.Fatalf("precise scroll: offset %v signal %v", y, s.Scroll)
	}
	h.in.AddScroll(0, -1000, true)
	h.frame(build)
	if _, y := h.c.Scroll(sc); y != 200 {
		t.Fatalf("scroll must clamp to content-view = 200, got %v", y)
	}
	h.in.AddScroll(0, 5000, true)
	h.frame(build)
	if _, y := h.c.Scroll(sc); y != 0 {
		t.Fatalf("scroll must clamp at 0, got %v", y)
	}
	// Children move with the offset.
	h.in.AddScroll(0, -45, true)
	h.frame(build)
	first := h.rect(h.c.boxes[sc].first)
	if first[1] != -45 {
		t.Errorf("first child at y=%v after scrolling 45", first[1])
	}

	// A wheel notch sets a target and the offset approaches it.
	h.c.SetScroll(sc, 0, 0)
	h.frame(build)
	h.in.AddScroll(0, -1, false)
	h.frame(build)
	tgt := h.c.boxes[sc].scrollTgt[1]
	if tgt != 48 {
		t.Fatalf("wheel target %v, want 48", tgt)
	}
	if _, y := h.c.Scroll(sc); y >= tgt {
		t.Fatalf("wheel scroll jumped to %v", y)
	}
	n := 0
	for h.c.Animating() || n == 0 {
		h.frame(build)
		n++
		if n > 200 {
			t.Fatal("scroll never settled")
		}
	}
	if _, y := h.c.Scroll(sc); y != tgt {
		t.Fatalf("settled at %v, want %v", y, tgt)
	}

	// Scroll not over the box does nothing.
	h.move(350, 250)
	h.frame(build)
	h.in.AddScroll(0, -100, true)
	h.frame(build)
	if _, y := h.c.Scroll(sc); y != tgt {
		t.Fatalf("scrolled while the pointer was elsewhere: %v", y)
	}

	// Shrinking the content re-clamps.
	h.move(50, 50)
	h.frame(build)
	h.frame(func(c *Context) {
		c.NextSize(Px(200), Px(100))
		sc = c.Box(ScrollY|Clip|Hoverable, "sc")
	})
	if _, y := h.c.Scroll(sc); y != 0 {
		t.Fatalf("offset %v with no content", y)
	}
}

func TestListRange(t *testing.T) {
	h := newHarness(t, 400, 600)
	const rows, rowH = 1_000_000, 24
	var list ID
	var first, last int
	build := func(c *Context) {
		c.NextSize(Pct(1), Pct(1))
		c.NextPad(0)
		list = c.Box(ScrollY|Clip|Hoverable, "list")
		first, last = c.ListRange(list, rows, rowH, 2)
		c.Push(list)
		for r := first; r < last; r++ {
			c.NextSize(Pct(1), Px(rowH))
			c.BoxKeyed(Hoverable, KeyN(c.ParentKey(), uint64(r)), nil)
		}
		c.Pop()
	}
	h.frame(build) // first frame: unknown size, falls back to the window height
	if first != 0 || last != 600/rowH+2 {
		t.Fatalf("initial range [%d,%d)", first, last)
	}
	h.frame(build)
	if first != 0 || last != 25+2 {
		t.Fatalf("range [%d,%d), want [0,27)", first, last)
	}
	if h.c.boxes[list].content[1] != rows*rowH {
		t.Fatalf("content height %v", h.c.boxes[list].content[1])
	}

	h.c.SetScroll(list, 0, 12_345_678)
	h.frame(build) // scroll applied in this frame's build...
	h.frame(build)
	sy := 12_345_678.0
	wantFirst := int(sy/rowH) - 2
	wantLast := int(math.Ceil((sy+600)/rowH)) + 2
	if first != wantFirst || last != wantLast {
		t.Fatalf("range [%d,%d), want [%d,%d)", first, last, wantFirst, wantLast)
	}
	// The row that straddles the top edge is at the right place, to the pixel.
	firstRow := h.rect(h.c.boxes[list].first)
	wantY := float32(float64(first)*rowH - sy)
	if firstRow[1] != snap(wantY) {
		t.Fatalf("first built row at y=%v, want %v", firstRow[1], snap(wantY))
	}
	if n := last - first; h.c.Boxes() != n+2 {
		t.Errorf("built %d boxes for %d rows", h.c.Boxes(), n)
	}

	// The very end: clamped to the content, last row bottom aligned.
	h.c.SetScroll(list, 0, 1e12)
	h.frame(build)
	h.frame(build)
	if last != rows {
		t.Fatalf("end of list: last=%d", last)
	}
	lastRow := h.rect(h.c.boxes[list].last)
	if lastRow[3] != 600 {
		t.Fatalf("last row bottom at %v, want 600", lastRow[3])
	}

	// Degenerate lists.
	h.frame(func(c *Context) {
		l := c.Box(ScrollY, "l2")
		if f, e := c.ListRange(l, 0, 24, 3); f != 0 || e != 0 {
			t.Errorf("empty list [%d,%d)", f, e)
		}
		if f, e := c.ListRange(l, 5, 24, 100); f != 0 || e != 5 {
			t.Errorf("overscan past the end [%d,%d)", f, e)
		}
	})
}
