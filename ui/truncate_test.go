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

func TestEllipsisCutInvalidatesOnWidthTextAndGeneration(t *testing.T) {
	sh := newEditShaper()
	h := &harness{t: t, c: New(sh), sh: sh.fakeShaper, w: 400, h: 100}
	w, body := float32(60), "hello world###stable"
	var id ID
	build := func(c *Context) {
		c.NextSize(Px(w), Px(20))
		id = c.Box(DrawText|EllipsisOverflowX, body)
	}
	h.frame(build)
	cut := *h.c.truncationCache(&h.c.boxes[id])
	h.frame(build)
	if got := *h.c.truncationCache(&h.c.boxes[id]); got != cut || len(quadsOf(h)) != 6 {
		t.Fatal("warm ellipsis cut changed")
	}
	w = 40
	h.frame(build)
	if len(quadsOf(h)) != 5 || h.c.truncationCache(&h.c.boxes[id]).w != w {
		t.Fatal("resized cut was not recomputed")
	}
	body = "x y abcdefg###stable"
	h.frame(build)
	if len(quadsOf(h)) != 3 {
		t.Fatalf("changed text has %d glyphs", len(quadsOf(h)))
	}
	sh.gen++
	h.frame(build)
	if h.c.truncationCache(&h.c.boxes[id]).gen != sh.gen {
		t.Fatal("eviction did not invalidate ellipsis cut")
	}
}
