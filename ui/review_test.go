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
	quiet      bool // stop resetting
}

func (r *alwaysResetShaper) Glyph(f text.FaceID, gid uint16, sub uint8) text.GlyphEntry {
	r.glyphCalls++
	if r.quiet {
		return r.fakeShaper.Glyph(f, gid, sub)
	}
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

// Glyph ink is not confined to its advance box. A glyph looked up is drawn
// whenever its real bounds meet the clip: one that overhangs far to the
// right of its origin, and one from a fallback face that reaches far to the
// left of its pen position, both with the pen outside the clip.
type inkShaper struct {
	*fakeShaper
	faces [fakeMaxGlyphs]text.FaceID
}

func (r *inkShaper) Run(ref text.RunRef) text.RunView {
	v := r.fakeShaper.Run(ref)
	for i := range v.Faces {
		r.faces[i] = 0
		if i > 0 && i == len(v.Faces)-1 {
			r.faces[i] = 1 // the last glyph comes from a fallback face
		}
	}
	v.Faces = r.faces[:len(v.Faces)]
	return v
}

func (r *inkShaper) Glyph(f text.FaceID, gid uint16, sub uint8) text.GlyphEntry {
	e := r.fakeShaper.Glyph(f, gid, sub)
	switch {
	case f == 1:
		e.W, e.BearingX = 90, -80 // reaches 80 px left of the pen
	case gid == 'a':
		e.W, e.BearingX = 40, 0 // 40 px wide at an 8 px advance
	}
	return e
}

func inkQuads(t *testing.T, text string, textX, clipX, clipW float32) []gpu.Quad {
	c := New(&inkShaper{fakeShaper: newFake()})
	var in Input
	var list gpu.DrawList
	c.Begin(&in, 400, 200, 1, 0.016)
	c.NextSize(Px(clipW), Px(30))
	c.NextPos(clipX, 10)
	cl := c.Box(Clip|FloatingX|FloatingY, "clip")
	c.Push(cl)
	c.NextSize(Px(200), Px(30))
	c.NextPos(textX-clipX, 0)
	c.Box(DrawText|FloatingX|FloatingY, text)
	c.Pop()
	c.End(&list)
	var out []gpu.Quad
	for _, q := range list.Quads {
		if q.Mode == gpu.ModeMaskR8 {
			out = append(out, q)
		}
	}
	return out
}

func TestOverhangingGlyphsReachTheClip(t *testing.T) {
	// "ab" at x=0: glyph a is [0,40), the pen of b (fallback, ink [-72,18))
	// is at 8. The clip [30,50) shows only a.
	q := inkQuads(t, "ab", 0, 30, 20)
	if len(q) != 1 || q[0].Rect[0] != 0 || q[0].Rect[2] != 40 {
		t.Errorf("right overhang: quads %+v, want glyph a (ink 0..40) in the clip 30..50", q)
	}
	// Eight x then b, at x=40: b's pen is at 104, right of the clip, and its
	// ink [24,114) meets [30,50).
	q = inkQuads(t, "xxxxxxxxb", 40, 30, 20)
	found := false
	for _, g := range q {
		if g.Rect[0] == 24 {
			found = true
		}
	}
	if !found {
		t.Errorf("left overhang from a fallback face: quads %+v, want the glyph with ink 24..114", q)
	}
}

// ---- re-review: release metadata, ScrollTo and growth, overflow bound ----

// A release and the next press in one frame: Clicked, DoubleClicked and
// DragDelta describe the press that ended, not the one that began.
func TestReleaseKeepsItsOwnClickCountAndOrigin(t *testing.T) {
	for _, c := range []struct {
		oldClicks, newClicks uint8
		wantDouble           bool
	}{{1, 2, false}, {2, 1, true}} {
		h := newHarness(t, 400, 100)
		var ia ID
		build := func(c *Context) { ia, _ = twoButtons(c) }
		h.frame(build)
		h.move(50, 30)
		h.frame(build)
		h.in.SetButton(ButtonLeft, true, c.oldClicks)
		h.frame(build)
		h.move(80, 30)
		h.in.Released[ButtonLeft], h.in.Pressed[ButtonLeft], h.in.Clicks[ButtonLeft] = true, true, c.newClicks
		h.in.Down[ButtonLeft] = true
		var s Signal
		h.frame(func(cx *Context) { ia, _ = twoButtons(cx); s = cx.Signal(ia) })
		if !s.Clicked || !s.Pressed {
			t.Errorf("old %d new %d: %+v", c.oldClicks, c.newClicks, s)
		}
		if s.DoubleClicked != c.wantDouble {
			t.Errorf("old %d new %d: DoubleClicked %v, want %v (the release ends the first press)", c.oldClicks, c.newClicks, s.DoubleClicked, c.wantDouble)
		}
		if s.DragDelta != [2]float32{30, 0} {
			t.Errorf("old %d new %d: DragDelta %v, want {30 0} from the first press's origin", c.oldClicks, c.newClicks, s.DragDelta)
		}
	}
}

// The target becomes valid only through content built in the same frame: an
// empty box, then 300 px of children and ScrollTo(150). A loop that draws
// only while Animating must reach it.
func TestScrollToWithContentBuiltThisFrame(t *testing.T) {
	h := newHarness(t, 400, 300)
	var sc ID
	build := func(rows int) func(*Context) {
		return func(c *Context) {
			c.NextSize(Px(200), Px(100))
			sc = c.Box(ScrollY|Clip|Hoverable, "sc")
			c.Push(sc)
			for i := 0; i < rows; i++ {
				c.NextSize(Pct(1), Px(30))
				c.Box(Hoverable, string(rune('a'+i)))
			}
			c.Pop()
		}
	}
	h.frame(build(0))
	h.frame(build(0))
	if h.c.Animating() {
		t.Fatal("a still scene animates")
	}
	h.frame(func(c *Context) { build(10)(c); c.ScrollTo(sc, 0, 150) })
	if !h.c.Animating() {
		t.Fatal("ScrollTo made valid by this frame's content did not keep the loop animating")
	}
	n := 0
	for ; h.c.Animating() && n < 1000; n++ {
		h.frame(build(10))
	}
	if _, y := h.c.Scroll(sc); y != 150 {
		t.Errorf("after %d frames the offset is %v, want 150", n, y)
	}
	// A target beyond what the content allows clamps; at the end, quiet.
	h.frame(func(c *Context) { build(10)(c); c.ScrollTo(sc, 0, 9999) })
	for i := 0; h.c.Animating() && i < 1000; i++ {
		h.frame(build(10))
	}
	h.frame(func(c *Context) { build(10)(c); c.ScrollTo(sc, 0, 9999) })
	if h.c.Animating() {
		t.Error("ScrollTo past the end while at the end animates")
	}
}

// A glyph set that never fits the atlas must not keep a settled app drawing:
// the frame loop (which draws only while Animating) stops after a bounded
// number of frames, the result is flagged degraded and counted.
func TestAtlasOverflowRepaintsAreBounded(t *testing.T) {
	rs := &alwaysResetShaper{fakeShaper: newFake()}
	c := New(rs)
	c.AtlasSync(&fakeUploader{})
	var in Input
	var list gpu.DrawList
	frame := func() {
		c.Begin(&in, 200, 100, 1, 0.016)
		c.NextSize(Px(100), Px(30))
		c.Box(DrawText, "abcdef")
		c.End(&list)
	}
	frame()
	n := 1
	for ; c.Animating() && n < 100; n++ {
		frame()
	}
	if n > maxOverflowRepaints+1 {
		t.Errorf("%d frames drawn for one overflow, want at most %d", n, maxOverflowRepaints+1)
	}
	if c.Animating() || !c.TextDegraded() || c.AtlasOverflowFrames() != uint64(n) {
		t.Errorf("animating %v degraded %v overflow frames %d (drawn %d)", c.Animating(), c.TextDegraded(), c.AtlasOverflowFrames(), n)
	}
	// Further frames (input) stay degraded and still do not ask for more.
	frame()
	if c.Animating() || !c.TextDegraded() {
		t.Error("a later overflow frame asks for repaints")
	}
	// Once the atlas stops overflowing the next frame is whole and clean.
	rs.quiet = true
	frame()
	if c.TextDegraded() || c.Animating() || len(list.Quads) != 6 {
		t.Errorf("a quiet atlas: degraded %v animating %v quads %d", c.TextDegraded(), c.Animating(), len(list.Quads))
	}
}
