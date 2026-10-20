package ui

import (
	"strings"
	"testing"
)

// zoomRig is the gesture rig with a layout in points (c.Pt), so that a scale
// change scales the box and its content as a zoom does: 100×100 pt over a
// 100×1000 pt content, the pointer over the box.
func newZoomRig(t *testing.T) *gestureRig {
	r := &gestureRig{harness: newHarness(t, 600, 600), now: 1e9}
	r.build = func(c *Context) {
		c.NextSize(Px(c.Pt(100)), Px(c.Pt(100)))
		r.box = c.Open(ScrollY|Clip, "box")
		c.NextSize(Px(c.Pt(100)), Px(c.Pt(1000)))
		c.Box(0, "content")
		c.Close()
	}
	r.step()
	r.step()
	r.move(50, 50)
	return r
}

// A precise scroll in the frame the scale doubles waits for the next frame, and
// is then clamped against the layout at the new scale (the bottom is 1800 px),
// not the old (900), which would throw a view at the bottom back by half a
// content.
func TestScrollInTheZoomFrameIsClampedAtTheNewScale(t *testing.T) {
	r := newZoomRig(t)
	r.c.SetScroll(r.box, 0, 900) // the bottom at scale 1
	r.step()
	r.step()
	r.scale = 2
	r.in.AddScrollEvent(ScrollEvent{DY: 50, Precise: true, Time: r.now + frameNs/2}) // up by 50 px
	r.step()
	if y := r.y(r.box); !near64(y, 1800, 0.01) || !r.c.Animating() {
		t.Errorf("the zoom frame: view at %v px (want 1800, the offset scaled), animating=%v (the held scroll needs a frame)", y, r.c.Animating())
	}
	r.step()
	if y := r.y(r.box); !near64(y, 1750, 0.01) {
		t.Errorf("a scroll up by 50 px in the zoom frame left the view at %v px, want 1750", y)
	}
	r.in.AddScrollEvent(ScrollEvent{DY: -100, Precise: true, Time: r.now + frameNs/2}) // down past the end
	r.step()
	if y := r.y(r.box); !near64(y, 1800, 0.01) {
		t.Errorf("the bottom at scale 2 is %v px, want 1800", y)
	}
}

// Momentum that carries on across the zoom frame is held for a frame, with its
// phase, and then goes on from where it was: it is not taken for a hit on the
// old bottom, and does not bounce or stop.
func TestMomentumAcrossTheZoomFrame(t *testing.T) {
	r := newZoomRig(t)
	r.c.SetScroll(r.box, 0, 700)
	r.step()
	r.ev(PhaseBegan, PhaseNone, 0, -1)
	r.step()
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.step()
	r.ev(PhaseNone, PhaseBegan, 0, -10) // 10 px toward the bottom
	r.step()
	before := r.y(r.box)
	r.scale = 2
	r.ev(PhaseNone, PhaseChanged, 0, -10)
	r.step()
	if !near64(r.y(r.box), 2*before, 0.01) {
		t.Errorf("the zoom frame: offset %v, want the old one scaled (%v)", r.y(r.box), 2*before)
	}
	r.step()
	if want := 2*before + 10; !near64(r.y(r.box), want, 0.01) || r.stretch(r.box) != 0 {
		t.Errorf("the held momentum: offset %v stretch %v, want %v and none", r.y(r.box), r.stretch(r.box), want)
	}
	if r.c.sp.g.ignoreMom {
		t.Error("the momentum was dropped as if it had hit the edge")
	}
	r.ev(PhaseNone, PhaseChanged, 0, -10)
	r.step()
	if want := 2*before + 20; !near64(r.y(r.box), want, 0.01) {
		t.Errorf("the momentum went on to %v, want %v", r.y(r.box), want)
	}
}

// A viewport that fills the window keeps its size in pixels when the zoom
// grows (here 100 px, as the window is), while its content grows: scaling the
// stored view size would make the bottom of the content look 100 px nearer than
// it is, and momentum would bounce off a false end. Held for a frame, momentum
// goes to the true end.
func TestMomentumNearTheBottomOfAFillSizedViewportAcrossAZoom(t *testing.T) {
	r := &gestureRig{harness: newHarness(t, 100, 100), now: 1e9}
	r.build = func(c *Context) {
		c.NextSize(Pct(1), Pct(1)) // the window, 100×100 px at every scale
		r.box = c.Open(ScrollY|Clip, "box")
		c.NextSize(Px(100), Px(c.Pt(1000)))
		c.Box(0, "content")
		c.Close()
	}
	r.step()
	r.step()
	r.move(50, 50)
	r.c.SetScroll(r.box, 0, 880) // 20 px from the bottom (900) at scale 1
	r.step()
	r.ev(PhaseBegan, PhaseNone, 0, -1)
	r.step()
	r.ev(PhaseEnded, PhaseNone, 0, 0)
	r.step()
	r.ev(PhaseNone, PhaseBegan, 0, -5)
	r.step()
	before := r.y(r.box)
	r.scale = 2
	r.ev(PhaseNone, PhaseChanged, 0, -30)
	r.step() // the zoom frame: held
	r.step() // applied: 2×before + 30
	r.ev(PhaseNone, PhaseChanged, 0, -40)
	r.step()
	r.ev(PhaseNone, PhaseChanged, 0, -40) // past 1800, where a view size scaled with the zoom would end the content
	r.step()
	if y, want := r.y(r.box), 2*before+110; !near64(y, want, 0.01) || want < 1800 || want > 1900 {
		t.Errorf("momentum ended at %v px (stretch %v), want %v: the true end is 1900, a false one 1800 (before the zoom %v)", y, r.stretch(r.box), want, before)
	}
	if s := r.stretch(r.box); s != 0 {
		t.Errorf("a false bounce: the view stretches by %v px", s)
	}
}

// A text field scrolled to the right keeps the same text in view across a scale
// change, whether or not it has the focus.
func TestTextFieldScrollFollowsTheScale(t *testing.T) {
	h := newHarness(t, 800, 200)
	var buf TextBuffer
	buf.SetString(strings.Repeat("0123456789", 4))
	build := func(c *Context) {
		c.NextSize(Px(c.Pt(100)), Px(c.Pt(24)))
		c.TextField("f", &buf)
	}
	h.frame(build)
	buf.scroll = 50 // scrolled; the field has no focus (the fake font's 320 px text: the largest scroll is 221 px, then 122)
	h.frame(build)
	if buf.scroll != 50 {
		t.Fatalf("scroll %v at the same scale", buf.scroll)
	}
	h.scale = 2
	h.frame(build)
	if buf.scroll != 100 {
		t.Errorf("scroll %v px after the scale doubled, want 100", buf.scroll)
	}
	h.frame(build)
	if buf.scroll != 100 {
		t.Errorf("scroll %v px a frame later: rescaled again", buf.scroll)
	}
	h.scale = 1
	h.frame(build)
	if buf.scroll != 50 {
		t.Errorf("scroll %v px back at scale 1, want 50", buf.scroll)
	}
}
