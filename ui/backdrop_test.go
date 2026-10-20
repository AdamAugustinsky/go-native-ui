package ui

import (
	"testing"

	"gonativeui/gpu"
)

// A backdrop box is drawn as a backdrop quad where its background would be:
// after what was drawn before it, before its border, its children and what
// comes after it, clipped and rounded as the box, with its background (the
// fallback) as its colour and its recipe in the list. The style is one-shot,
// the boxes past the frame's sixteen draw their background instead, and
// none of it allocates.
func TestBackdropBox(t *testing.T) {
	h := newHarness(t, 400, 300)
	rec := gpu.Backdrop{Sigma: 12, Down: 4, Tint: 0xFFFFFFFF, Mix: 0.8245, Saturation: 1, Add: 0.03}
	extra := 0
	build := func(c *Context) {
		c.NextSize(Px(400), Px(300))
		c.NextBG(0x112233FF)
		c.Push(c.Box(DrawBackground|Clip, "content"))
		c.NextSize(Px(500), Px(40))
		c.NextBG(0xFCFCFCFF)
		c.NextBorder(0x00000040, 1)
		c.NextRadius(6)
		c.NextBackdrop(rec)
		c.Push(c.Box(DrawBackground|DrawBorder, "edge"))
		c.NextSize(Px(10), Px(10))
		c.NextBG(0xFF0000FF)
		c.Box(DrawBackground, "item")
		c.Pop()
		c.NextSize(Px(10), Px(10))
		c.NextBG(0x00FF00FF)
		c.Box(DrawBackground, "after")
		for i := 0; i < extra; i++ {
			c.NextSize(Px(1), Px(1))
			c.NextBG(0x0000FFFF)
			c.NextBackdrop(rec)
			c.Box(DrawBackground, "more"+string(rune('a'+i)))
		}
		c.Pop()
	}
	h.frame(build)
	qs, l := quadsOf(h), &h.list
	if len(qs) != 5 || len(l.Backdrops) != 1 {
		t.Fatalf("%d quads and %d backdrops, want content, the backdrop, its border, item, after and one recipe", len(qs), len(l.Backdrops))
	}
	bd := qs[1]
	if bd.Mode != gpu.ModeBackdrop || l.Backdrops[bd.Tex] != rec || bd.Rect != [4]float32{0, 0, 500, 40} ||
		bd.Clip != [4]float32{0, 0, 400, 300} || bd.Radii != [4]float32{6, 6, 6, 6} || bd.Color != [4]uint32{0xFCFCFCFF, 0xFCFCFCFF, 0xFCFCFCFF, 0xFCFCFCFF} {
		t.Errorf("the backdrop: %+v, recipe %+v", bd, l.Backdrops)
	}
	if border := qs[2]; border.Mode != gpu.ModeShape || border.Color != [4]uint32{} || border.Border != 1 {
		t.Errorf("the border over it: %+v", border)
	}
	if qs[3].Color[0] != 0xFF0000FF || qs[4].Color[0] != 0x00FF00FF {
		t.Error("its child and the box after it are not drawn over it")
	}
	extra = 20
	h.frame(build)
	if n := len(h.list.Backdrops); n != maxBackdrops {
		t.Errorf("%d backdrops in a frame, want %d", n, maxBackdrops)
	}
	fill := 0
	for _, q := range h.list.Quads {
		if q.Mode == gpu.ModeShape && q.Color[0] == 0x0000FFFF {
			fill++
		}
	}
	if fill != 21-maxBackdrops {
		t.Errorf("%d boxes past the frame's backdrops drew their fallback, want %d", fill, 21-maxBackdrops)
	}
	extra = 0
	h.frame(func(c *Context) {
		c.NextBackdrop(rec)
		c.NextSize(Px(10), Px(10))
		c.Box(0, "a") // no background: the fallback is clear
		c.NextSize(Px(10), Px(10))
		c.NextBG(0x0000FFFF)
		c.Box(DrawBackground, "b")
	})
	if qs := quadsOf(h); len(qs) != 2 || qs[0].Mode != gpu.ModeBackdrop || qs[0].Color != [4]uint32{} || qs[1].Mode != gpu.ModeShape {
		t.Errorf("one-shot: %+v", qs)
	}
	h.frame(build)
	if n := testing.AllocsPerRun(50, func() { h.frame(build) }); n != 0 {
		t.Errorf("%.1f allocations per frame", n)
	}
}
