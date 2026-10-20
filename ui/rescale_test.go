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

// The scroll offsets and the extents that limit them change scale together:
// a precise scroll in the frame the scale doubles is clamped against the new
// bounds (1800 px), not the old (900), which would throw a view at the bottom
// back by half a content.
func TestScrollInTheZoomFrameIsClampedAtTheNewScale(t *testing.T) {
	r := newZoomRig(t)
	r.c.SetScroll(r.box, 0, 900) // the bottom at scale 1
	r.step()
	r.step()
	r.scale = 2
	r.in.AddScrollEvent(ScrollEvent{DY: 50, Precise: true, Time: r.now + frameNs/2}) // up by 50 px
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

// Momentum that carries on across the zoom frame is not taken for a hit on the
// old bottom: it neither bounces nor stops.
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
	if want := 2*before + 10; !near64(r.y(r.box), want, 0.01) || r.stretch(r.box) != 0 {
		t.Errorf("momentum in the zoom frame: offset %v stretch %v, want %v and none", r.y(r.box), r.stretch(r.box), want)
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
