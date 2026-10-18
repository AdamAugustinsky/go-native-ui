package ui

import "testing"

// scrollRig is a 100×60 box scrolling a 300×400 content (a 10 px padding
// included: 280×380 of children).
type scrollRig struct {
	*harness
	box ID
}

func newScrollRig(t *testing.T) *scrollRig {
	r := &scrollRig{harness: newHarness(t, 300, 200)}
	r.frame(r.build)
	return r
}

func (r *scrollRig) build(c *Context) {
	c.NextSize(Px(100), Px(60))
	c.NextPad(10)
	r.box = c.Open(ScrollX|ScrollY|Clip, "box")
	c.NextSize(Px(280), Px(380))
	c.Box(0, "content")
	c.Close()
}

func (r *scrollRig) settle() {
	for i := 0; i < 200; i++ {
		r.frame(r.build)
		if !r.c.Animating() {
			return
		}
	}
	r.t.Fatal("the scroll never settles")
}

func TestScrollExtent(t *testing.T) {
	h := newHarness(t, 300, 200)
	var content, view [2]float32
	build := func(c *Context) {
		c.NextSize(Px(100), Px(60))
		c.NextPad(10)
		b := c.Open(ScrollY|Clip, "box")
		content, view = c.ScrollExtent(b)
		c.NextSize(Px(80), Px(380))
		c.Box(0, "content")
		c.Close()
	}
	h.frame(build)
	if content != [2]float32{} || view != [2]float32{} {
		t.Fatalf("extent before the first layout: %v %v", content, view)
	}
	h.frame(build)
	if content[1] != 400 || view != [2]float32{100, 60} {
		t.Fatalf("extent %v in view %v: the content is 380 plus 20 of padding", content, view)
	}
}

func TestScrollIntoViewMakesTheSmallestMove(t *testing.T) {
	r := newScrollRig(t)
	r.frame(r.build)
	scrollY := func() float64 { _, y := r.c.Scroll(r.box); return y }
	do := func(rect [4]float32) {
		r.frame(func(c *Context) {
			r.build(c)
			c.ScrollIntoView(r.box, rect)
		})
		r.settle()
	}
	// Already in view: nothing moves.
	do([4]float32{10, 10, 50, 40})
	if scrollY() != 0 || !near(float32(r.scrollX()), 0) {
		t.Fatalf("a visible rectangle moved the view to %v", scrollY())
	}
	// Below the view: its bottom edge lands on the bottom of the view.
	do([4]float32{10, 200, 50, 220})
	if scrollY() != 160 {
		t.Fatalf("scrolled to %v, want 160", scrollY())
	}
	// Still visible: no move. Partly out above: its top edge lands on the top.
	do([4]float32{10, 180, 50, 200})
	if scrollY() != 160 {
		t.Fatalf("a visible rectangle moved the view to %v", scrollY())
	}
	do([4]float32{10, 150, 50, 170})
	if scrollY() != 150 {
		t.Fatalf("scrolled to %v, want 150", scrollY())
	}
	// Larger than the view: aligned at its start.
	do([4]float32{10, 20, 50, 120})
	if scrollY() != 20 {
		t.Fatalf("tall rectangle: scrolled to %v, want 20", scrollY())
	}
	// Beyond the end of the content: clamped by the layout.
	do([4]float32{10, 390, 50, 500})
	if scrollY() != 340 {
		t.Fatalf("scrolled to %v, want the end (340)", scrollY())
	}
	// Both axes at once; the horizontal move is as small as the vertical.
	do([4]float32{200, 0, 220, 10})
	if scrollY() != 0 || r.scrollX() != 120 {
		t.Fatalf("scrolled to %v, %v, want 120, 0", r.scrollX(), scrollY())
	}
}

func (r *scrollRig) scrollX() float64 { x, _ := r.c.Scroll(r.box); return x }

func TestScrollIntoViewIsAnimatedAndAccumulates(t *testing.T) {
	r := newScrollRig(t)
	r.frame(r.build)
	r.frame(func(c *Context) {
		r.build(c)
		c.ScrollIntoView(r.box, [4]float32{10, 100, 50, 120})
		c.ScrollIntoView(r.box, [4]float32{10, 150, 50, 170}) // a second call builds on the first target
	})
	if !r.c.Animating() {
		t.Fatal("a scroll in progress must keep the frames coming")
	}
	if _, y := r.c.Scroll(r.box); y >= 110 {
		t.Fatalf("jumped to %v instead of animating", y)
	}
	r.settle()
	if _, y := r.c.Scroll(r.box); y != 110 { // 170 - 60
		t.Fatalf("settled at %v, want 110", y)
	}
}

func TestScrollIntoViewIgnoresAxesThatDoNotScroll(t *testing.T) {
	h := newHarness(t, 300, 200)
	var box ID
	build := func(c *Context) {
		c.NextSize(Px(100), Px(60))
		box = c.Open(ScrollY|Clip, "box")
		c.NextSize(Px(300), Px(380))
		c.Box(0, "content")
		c.Close()
	}
	h.frame(build)
	h.frame(func(c *Context) {
		build(c)
		c.ScrollIntoView(box, [4]float32{250, 200, 280, 220})
	})
	for i := 0; i < 100; i++ {
		h.frame(build)
	}
	if x, y := h.c.Scroll(box); x != 0 || y != 160 {
		t.Fatalf("scrolled to %v, %v", x, y)
	}
}
