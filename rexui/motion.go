package rexui

import (
	"math"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Looping motion and drawn strokes: what the indeterminate bar, the spinner,
// the disclosure chevron and the stepper's arrows share. Components that use
// them copy this file with them (tools/rexui add).

// reduced reports whether decorative motion stands still: the kit's
// ReduceMotion, or the system's Reduce Motion setting.
func reduced(k *base.Kit) bool {
	return k.ReduceMotion || k.Theme().Settings.ReduceMotion
}

// loops reports whether a looping animation in box id may run now: motion is
// not reduced, the window is showing (not minimised, hidden or covered:
// ui.Input.Hidden), and the box is in view as the previous frame laid it
// out, not hidden by a flow's MaxLines. Before the box has been laid out
// once its rectangle is empty and the answer is yes, so that the frame that
// lays it out is not the last. A loop that is not running asks for no
// frames: an idle window draws none.
func loops(k *base.Kit, id ui.ID) bool {
	c := k.Context()
	if reduced(k) || c.Input().Hidden || c.WasHidden(id) {
		return false
	}
	r := c.Rect(id)
	if r[2] <= r[0] || r[3] <= r[1] {
		return true
	}
	return k.Visible(r)
}

// shown reports whether the box keyed key showed in the previous frame: the
// window is showing (not ui.Input.Hidden), and the box was laid out in view
// and not hidden by a flow's MaxLines. A transition of a box that did not
// show jumps to its end: it would ask for frames nobody sees.
func shown(k *base.Kit, key ui.Key) bool {
	c := k.Context()
	if c.Input().Hidden {
		return false
	}
	id, ok := c.BoxOf(key)
	return ok && !c.WasHidden(id) && k.Visible(c.Rect(id))
}

// disabledFlag is ui.Disabled in a disabled region (base.Kit.BeginDisabled):
// what an indicator's root box carries so that it reports SemDisabled.
func disabledFlag(k *base.Kit) ui.Flags {
	if k.Disabled() {
		return ui.Disabled
	}
	return 0
}

// cycle is where the frame's clock is in a cycle of period ns, 0 to 1.
func cycle(k *base.Kit, period int64) float32 {
	return float32(k.Context().Input().Time%period) / float32(period)
}

// untilNext is how long from the frame's clock to the next time a cycle of
// period ns starts a step of its n steps, in ns (at least 1): what a stepped
// animation asks ui.Context.WakeIn for, to draw once per step and not once per
// frame.
func untilNext(k *base.Kit, period int64, n int) int64 {
	step := period / int64(n)
	return step - k.Context().Input().Time%step
}

// stroke builds a straight line from (x0, y0) to (x1, y1) of width w with
// round caps, in the opaque colour col, in the current parent's coordinates
// (pixels): a chain of discs, because boxes are axis-aligned. col must be
// opaque (overlapping discs of a translucent colour would show their
// overlaps): ink gives a translucent colour composited over its backdrop.
// The boxes' keys are KeyN(key, n), KeyN(key, n+1) … and the next free n is
// returned.
func stroke(c *ui.Context, key ui.Key, n uint64, x0, y0, x1, y1, w float32, col uint32) uint64 {
	dx, dy := float64(x1-x0), float64(y1-y0)
	length := math.Hypot(dx, dy)
	step := float64(max(w/4, 0.75))
	segs := int(math.Ceil(length / step))
	if segs < 1 {
		segs = 1
	}
	for i := 0; i <= segs; i++ {
		t := float64(i) / float64(segs)
		theme.At(c, x0+float32(dx*t)-w/2, y0+float32(dy*t)-w/2, w, w)
		c.NextBG(col)
		c.NextRadius(w / 2)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, n), nil)
		n++
	}
	return n
}

// Chevron is a chevron's centreline: the end of its first arm, its apex and
// the end of its second arm, in points from the top left of the box it is
// drawn in, and the point it turns about.
type Chevron struct {
	A, Apex, B [2]float32 // the first arm's end, the apex, the second arm's end
	Centre     [2]float32 // the point it turns about
}

// chevron builds ch turned deg degrees clockwise about its centre, as two
// strokes of width wPt points in col (opaque, see stroke), in the current
// parent at scale pixels per point, with the keys of stroke from n.
func chevron(c *ui.Context, key ui.Key, n uint64, ch *Chevron, deg, wPt, scale float32, col uint32) uint64 {
	var p [3][2]float32
	for i, q := range [3][2]float32{ch.A, ch.Apex, ch.B} {
		x, y := rotate(q[0], q[1], ch.Centre[0], ch.Centre[1], deg)
		p[i] = [2]float32{x * scale, y * scale}
	}
	w := max(wPt*scale, 1)
	n = stroke(c, key, n, p[0][0], p[0][1], p[1][0], p[1][1], w, col)
	return stroke(c, key, n, p[1][0], p[1][1], p[2][0], p[2][1], w, col)
}

// ink is the colour a translucent glyph colour top has over the backdrop
// colours under it, opaque: strokes and spokes are drawn in it.
func ink(top uint32, under ...uint32) uint32 {
	bg := uint32(0xFFFFFFFF)
	for _, u := range under {
		bg = theme.Over(u, bg)
	}
	return theme.Over(top, bg)
}

// rotate turns the point (x, y) about (cx, cy) by deg degrees clockwise (the
// y axis points down).
func rotate(x, y, cx, cy, deg float32) (float32, float32) {
	s, c := math.Sincos(float64(deg) * math.Pi / 180)
	dx, dy := float64(x-cx), float64(y-cy)
	return cx + float32(dx*c-dy*s), cy + float32(dx*s+dy*c)
}
