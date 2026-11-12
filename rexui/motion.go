package rexui

import (
	"math"

	"gonativeui/gpu"
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

// strict reports whether the kit's theme asks for AppKit's static controls
// (theme.Theme.StrictAppKit): a recipe that departs from what AppKit draws
// for the sake of response or contrast (a rim, a pressed knob, an eased mark)
// draws AppKit's instead.
func strict(k *base.Kit) bool { return k.Theme().StrictAppKit }

// stateEase is how far a state the user changed has shown, 0 to 1: a check
// mark or a radio dot fading in, keyed by key (the box it is drawn in). It
// eases toward on over d seconds (the theme's Micro or Short), and is at once
// under strict AppKit, Reduce Motion, and for a box that did not show.
func stateEase(k *base.Kit, key ui.Key, on bool, d float32) float32 {
	rate := RateFor(d)
	if strict(k) || reduced(k) || !shown(k, key) {
		rate = theme.Instant
	}
	return k.Context().AnimateValue(ui.KeyN(key, 0x5EA5E), b2f(on), rate)
}

// hoverT is how hovered a part keyed key is, 0 to 1, from whether the
// pointer is over it this frame (hovering): at once in, fading out at the
// theme's hover rate (Motion.Hot), and always 0 when the theme is strict
// AppKit for the default policy (base.Kit.Hovers with h). For parts that are
// not boxes of their own with a hover animation: a list's rows, a field's
// outline.
func hoverT(k *base.Kit, key ui.Key, hovering bool, h base.Hover) float32 {
	if !k.Hovers(h) {
		return 0
	}
	rate := k.Theme().Motion.Hot
	if hovering {
		rate = theme.Instant
	}
	return k.Context().AnimateValue(ui.KeyN(key, 0x40E2), b2f(hovering), rate)
}

// stateFill is a control's fill at rest, laid toward its hover fill by the
// hover and then toward its pressed fill by the press (base.Visual's Hot and
// Down), so a press is never lighter than the hover it follows.
func stateFill(rest, hover, pressed uint32, v base.Visual) uint32 {
	return tintToward(tintToward(rest, hover, v.Hot), pressed, v.Down)
}

// loops reports whether a looping animation in box id may run now: motion is
// not reduced, the window is showing (not minimised, hidden or covered:
// ui.Input.Hidden), and some of the box is in view as the previous frame laid
// it out: inside the window and the clips and scroll areas around it, not
// hidden by a flow's MaxLines. Before the box has been laid out
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
	s := c.ShownRect(id) // clipped by the scroll areas and clips around it, not only by the window
	return s[2] > s[0] && s[3] > s[1]
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
	if !ok {
		return false
	}
	r := c.ShownRect(id)
	return r[2] > r[0] && r[3] > r[1]
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

// stroke builds one round-capped segment in the current parent's pixel
// coordinates. Its single SDF quad works at every angle without a texture.
// It uses KeyN(key, n) and returns the next free n.
func stroke(c *ui.Context, key ui.Key, n uint64, x0, y0, x1, y1, w float32, col uint32) uint64 {
	if w <= 0 {
		return n
	}
	r := w / 2
	x, y := min(x0, x1)-r, min(y0, y1)-r
	width, height := max(x0, x1)+r-x, max(y0, y1)+r-y
	// The box is pixel-snapped by layout; keep the segment's fractional
	// position in the quad so a rotating chevron moves continuously.
	sx, sy := theme.Snap(x), theme.Snap(y)
	theme.At(c, sx, sy, width, height)
	c.Quad(base.Floating, ui.KeyN(key, n), gpu.Quad{
		Rect:  [4]float32{x - sx, y - sy, x - sx + width, y - sy + height},
		UV:    [4]float32{x0 - x, y0 - y, x1 - x, y1 - y},
		Color: [4]uint32{col, col, col, col}, Radii: [4]float32{r}, Mode: gpu.ModeCapsule,
	})
	return n + 1
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
