package ui

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
)

// ---- button ordering and cancellation ----

func twoButtons(c *Context) (a, b ID) {
	c.NextSize(Px(100), Px(40))
	c.NextPos(10, 10)
	a = c.Box(Clickable|FloatingX|FloatingY, "A")
	c.NextSize(Px(100), Px(40))
	c.NextPos(200, 10)
	b = c.Box(Clickable|FloatingX|FloatingY, "B")
	return
}

// A release followed by a new press in one frame (both edges, Down true): the
// old capture ends with a Released and the new press stays captured. Before,
// the capture was made and then cleared in the same frame.
func TestReleaseThenPressInOneFrame(t *testing.T) {
	h := newHarness(t, 400, 100)
	var ia, ib ID
	build := func(c *Context) { ia, ib = twoButtons(c) }
	h.frame(build)
	h.move(50, 30)
	h.frame(build)
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(build)
	// Release and press again before the next frame: both edges, Down true.
	h.in.Down[ButtonLeft] = true
	h.in.Released[ButtonLeft], h.in.Pressed[ButtonLeft], h.in.Clicks[ButtonLeft] = true, true, 1
	var sa Signal
	h.frame(func(c *Context) { ia, ib = twoButtons(c); sa = c.Signal(ia) })
	if !sa.Released || !sa.Clicked {
		t.Errorf("the first press must end as a click: %+v", sa)
	}
	if !sa.Pressed || !sa.Dragging {
		t.Errorf("the new press must be captured and held: %+v", sa)
	}
	_ = ib
	h.in.SetButton(ButtonLeft, false, 0)
	var s2 Signal
	h.frame(func(c *Context) { ia, ib = twoButtons(c); s2 = c.Signal(ia) })
	if !s2.Clicked {
		t.Errorf("the second press must click on release: %+v", s2)
	}
}

// A cancelled press (focus loss) drops the capture without a release, a click
// or a double click, even with the pointer still over the box.
func TestCancelDropsCaptureWithoutClick(t *testing.T) {
	h := newHarness(t, 400, 100)
	var ia ID
	build := func(c *Context) { ia, _ = twoButtons(c) }
	h.frame(build)
	h.move(50, 30)
	h.frame(build)
	h.in.SetButton(ButtonLeft, true, 2)
	h.frame(build)
	if h.c.ActiveKey() == 0 {
		t.Fatal("the press did not capture")
	}
	h.in.Cancel(ButtonLeft)
	if h.in.Down[ButtonLeft] || !h.in.Cancelled[ButtonLeft] {
		t.Fatalf("Cancel: down %v cancelled %v", h.in.Down, h.in.Cancelled)
	}
	var s Signal
	h.frame(func(c *Context) { ia, _ = twoButtons(c); s = c.Signal(ia) })
	if s.Released || s.Clicked || s.DoubleClicked || s.Dragging {
		t.Errorf("a cancelled press reported %+v", s)
	}
	if h.c.ActiveKey() != 0 {
		t.Error("the capture survived the cancel")
	}
	if h.in.Cancelled[ButtonLeft] {
		t.Error("ClearFrame must clear Cancelled")
	}
	// Cancel on a button that is not held does nothing.
	h.in.Cancel(ButtonLeft)
	if h.in.Cancelled[ButtonLeft] {
		t.Error("Cancel of a button that is up")
	}
}

// ---- ScrollTo animates ----

// A frame loop that draws only while Animating reaches a ScrollTo target.
func TestScrollToKeepsAnimating(t *testing.T) {
	h := newHarness(t, 400, 300)
	var sc ID
	build := func(c *Context) {
		c.NextSize(Px(200), Px(100))
		sc = c.Box(ScrollY|Clip|Hoverable, "sc")
		c.Push(sc)
		for i := 0; i < 10; i++ {
			c.NextSize(Pct(1), Px(30))
			c.Box(Hoverable, string(rune('a'+i)))
		}
		c.Pop()
	}
	h.frame(build)
	h.frame(build)
	if h.c.Animating() {
		t.Fatal("a still scene animates")
	}
	h.frame(func(c *Context) { build(c); c.ScrollTo(sc, 0, 150) })
	if !h.c.Animating() {
		t.Fatal("ScrollTo to another offset did not mark the frame as animating")
	}
	n := 0
	for ; h.c.Animating() && n < 1000; n++ { // draws only while Animating
		h.frame(build)
	}
	if _, y := h.c.Scroll(sc); y != 150 {
		t.Errorf("after %d frames the offset is %v, want 150", n, y)
	}
	// A target that is where the offset already is, or beyond the end the
	// offset is at, does not keep the loop busy.
	h.frame(func(c *Context) { build(c); c.ScrollTo(sc, 0, 150) })
	if h.c.Animating() {
		t.Error("ScrollTo the current offset animates")
	}
	h.frame(func(c *Context) { build(c); c.ScrollTo(sc, 0, 5000) }) // clamps to 200
	if !h.c.Animating() {
		t.Error("ScrollTo past the end, which clamps to a different offset, does not animate")
	}
	for i := 0; h.c.Animating() && i < 1000; i++ {
		h.frame(build)
	}
	h.frame(func(c *Context) { build(c); c.ScrollTo(sc, 0, 5000) })
	if h.c.Animating() {
		t.Error("ScrollTo past the end while already at the end animates")
	}
}

// ---- atlas reset on every attempt ----

// alwaysResetShaper resets the atlas during every glyph lookup. Entries carry
// the generation they were made in (as X), so a quad is valid exactly when its
// UV x equals the current generation.
type alwaysResetShaper struct {
	*fakeShaper
	glyphCalls int
}

func (r *alwaysResetShaper) Glyph(f text.FaceID, gid uint16, sub uint8) text.GlyphEntry {
	r.glyphCalls++
	r.atlasGen++ // the lookup resets the atlas, then rasterizes into the fresh one
	e := r.fakeShaper.Glyph(f, gid, sub)
	if e.W != 0 {
		e.X = uint16(r.atlasGen)
	}
	return e
}

func TestAtlasResetOnEveryAttemptSubmitsNoStaleQuads(t *testing.T) {
	rs := &alwaysResetShaper{fakeShaper: newFake()}
	c := New(rs)
	c.AtlasSync(&fakeUploader{})
	var in Input
	var list gpu.DrawList
	c.Begin(&in, 200, 100, 1, 0.016)
	c.NextSize(Px(100), Px(30))
	c.NextBG(0xFF0000FF)
	c.Box(DrawText|DrawBackground, "abcdef")
	c.End(&list)
	cur := float32(rs.atlasGen)
	glyphs := 0
	for _, q := range list.Quads {
		if q.Mode != gpu.ModeMaskR8 {
			continue
		}
		glyphs++
		if q.UV[0] != cur {
			t.Errorf("a glyph quad from atlas generation %v was accepted in generation %v", q.UV[0], cur)
		}
	}
	if glyphs != 1 { // only the lookup after the last reset is valid
		t.Errorf("%d glyph quads, want the 1 that is valid", glyphs)
	}
	if len(list.Quads) < 1 || list.Quads[0].Mode == gpu.ModeMaskR8 {
		t.Errorf("the background must survive: %+v", list.Quads)
	}
	if !c.Animating() {
		t.Error("a frame that dropped text must ask for another")
	}
}

// Text clipped away is not looked up: nothing is rasterized for glyphs the
// clip hides, from whole boxes down to single glyphs of a long run.
func TestClippedGlyphsAreNotLooked(t *testing.T) {
	rs := &countingShaper{fakeShaper: newFake()}
	c := New(rs)
	var in Input
	var list gpu.DrawList
	c.Begin(&in, 400, 200, 1, 0.016)
	// A 600 px run in a 100 px clip, 150 px in from the run's left end.
	c.NextSize(Px(100), Px(30))
	c.NextPos(10, 10)
	cl := c.Box(Clip|FloatingX|FloatingY, "clip")
	c.Push(cl)
	c.NextSize(Px(600), Px(30))
	c.NextPos(-150, 0)
	c.Box(DrawText|FloatingX|FloatingY, "0123456789012345678901234567890123456789"[:40])
	c.Pop()
	c.End(&list)
	// 40 glyphs of 8 px; the clip shows x in [150, 250) of the run: about 13
	// glyphs plus the margin of a line (13 px) on each side.
	if rs.glyphCalls >= 25 || rs.glyphCalls < 12 {
		t.Errorf("%d glyph lookups for 12 visible glyphs in a 40 glyph run", rs.glyphCalls)
	}
	n := 0
	for _, q := range list.Quads {
		if q.Mode == gpu.ModeMaskR8 {
			n++
		}
	}
	if n < 12 || n > 14 {
		t.Errorf("%d glyph quads drawn", n)
	}
}

type countingShaper struct {
	*fakeShaper
	glyphCalls int
}

func (r *countingShaper) Glyph(f text.FaceID, gid uint16, sub uint8) text.GlyphEntry {
	r.glyphCalls++
	return r.fakeShaper.Glyph(f, gid, sub)
}
