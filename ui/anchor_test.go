package ui

import "testing"

// A box anchored in its parent sits where its anchor and insets say once its
// size is known, as CSS's absolute positioning: the corners and edges take
// the insets inward, the centre splits the free room (the parent's padding
// does not count), and the box floats without moving its flow siblings.
func TestAnchorPlacesAFloatingBox(t *testing.T) {
	for _, tc := range []struct {
		a      Anchor
		dx, dy float32
		want   [2]float32
	}{
		{AnchorTopLeft, 4, 6, [2]float32{4, 6}},
		{AnchorTop, 0, 6, [2]float32{80, 6}},
		{AnchorTopRight, 4, 6, [2]float32{156, 6}},
		{AnchorLeft, 4, 0, [2]float32{4, 45}},
		{AnchorCenter, 0, 0, [2]float32{80, 45}},
		{AnchorCenter, 2, -3, [2]float32{82, 42}},
		{AnchorRight, 4, 0, [2]float32{156, 45}},
		{AnchorBottomLeft, 4, 6, [2]float32{4, 84}},
		{AnchorBottom, 0, 6, [2]float32{80, 84}},
		{AnchorBottomRight, 4, 6, [2]float32{156, 84}},
	} {
		h := newHarness(t, 800, 600)
		var p, b, flow ID
		h.frame(func(c *Context) {
			c.NextSize(Px(200), Px(120))
			c.NextAxis(AxisX)
			c.NextPadding(10, 10, 10, 10) // ignored, as an absolute box's parent padding is
			p = c.Open(0, "p")
			c.NextAnchor(tc.a, tc.dx, tc.dy)
			c.NextSize(Px(40), Px(30))
			b = c.Box(0, "anchored")
			c.NextSize(Px(20), Px(20))
			flow = c.Box(0, "flow")
			c.Close()
		})
		if got := [2]float32{h.rel(b, p, 0), h.rel(b, p, 1)}; got != tc.want {
			t.Errorf("anchor %d inset %v,%v: at %v, want %v", tc.a, tc.dx, tc.dy, got, tc.want)
		}
		if got := h.rel(flow, p, 0); got != 10 {
			t.Errorf("anchor %d: the flow child is at %v, want 10: the anchored box took room", tc.a, got)
		}
	}
}

// NextCenter centres a box sized by its own content, whose size the caller
// never knows (an icon's image): the arrow in a round button.
func TestCenterCentresContentOfAnySize(t *testing.T) {
	for _, side := range []float32{12, 17, 23} {
		h := newHarness(t, 800, 600)
		var p, icon ID
		h.frame(func(c *Context) {
			c.NextSize(Px(40), Px(40))
			p = c.Open(0, "button")
			c.NextCenter()
			c.NextAxis(AxisX)
			c.Open(0, "wrap")
			c.NextSize(Px(side), Px(side))
			icon = c.Box(0, "icon")
			c.Close()
			c.Close()
		})
		x, y := h.rel(icon, p, 0)+side/2, h.rel(icon, p, 1)+side/2
		if d := x - 20; d > 0.5 || d < -0.5 {
			t.Errorf("a %v px icon's centre is %v px off across", side, d)
		}
		if d := y - 20; d > 0.5 || d < -0.5 {
			t.Errorf("a %v px icon's centre is %v px off down", side, d)
		}
	}
}

// NextOffset moves a flow box and its children by the offset and leaves its
// siblings where they were, as position: relative.
func TestOffsetMovesOnlyTheBox(t *testing.T) {
	h := newHarness(t, 800, 600)
	var p, a, kid, b ID
	h.frame(func(c *Context) {
		c.NextSize(Px(200), Px(100))
		c.NextAxis(AxisX)
		p = c.Open(0, "p")
		c.NextOffset(3, -2)
		c.NextSize(Px(40), Px(40))
		a = c.Open(0, "a")
		c.NextSize(Px(10), Px(10))
		kid = c.Box(0, "kid")
		c.Close()
		c.NextSize(Px(40), Px(40))
		b = c.Box(0, "b")
		c.Close()
	})
	if x, y := h.rel(a, p, 0), h.rel(a, p, 1); x != 3 || y != -2 {
		t.Errorf("the offset box is at %v,%v, want 3,-2", x, y)
	}
	if x, y := h.rel(kid, p, 0), h.rel(kid, p, 1); x != 3 || y != -2 {
		t.Errorf("its child is at %v,%v, want 3,-2 with it", x, y)
	}
	if x := h.rel(b, p, 0); x != 40 {
		t.Errorf("the sibling is at %v, want 40: the offset moved it", x)
	}
}
