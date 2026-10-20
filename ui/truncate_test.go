package ui

import "testing"

// A line too wide for its box ends with an ellipsis after the last glyph that
// leaves room for it, the spaces before the cut dropped; a line that fits is
// drawn whole.
func TestEllipsisOverflow(t *testing.T) {
	sh := newEditShaper()
	h := &harness{t: t, c: New(sh), sh: sh.fakeShaper, w: 400, h: 100}
	h.in.Inside, h.in.Focused = true, true
	h.frame(func(c *Context) {
		c.NextSize(Px(60), Px(20))
		c.Box(DrawText|EllipsisOverflowX, "hello world")
	})
	qs := quadsOf(h)
	// 8 px a rune: "hello " ends at 48 and the ellipsis needs 8 more, so
	// the cut is after the space, which is dropped: "hello…"
	if len(qs) != 6 {
		t.Fatalf("%d glyph quads, want 6 (hello…)", len(qs))
	}
	if last := qs[5]; last.Rect[0] != 40+1 || last.Clip[2] != 60 {
		t.Errorf("ellipsis quad %+v, want it at the pen 40", last)
	}
	h.frame(func(c *Context) {
		c.NextSize(Px(100), Px(20))
		c.Box(DrawText|EllipsisOverflowX, "hello world")
	})
	if n := len(quadsOf(h)); n != 10 {
		t.Errorf("%d glyph quads for a line that fits, want 10", n)
	}
}
