package ui

import "gonativeui/gpu"

// Backdrops (D54): a box that blurs what is drawn under it, as AppKit's
// scroll edge blurs the content scrolled under a toolbar. The box is drawn
// as a gpu.ModeBackdrop quad where its background would be, so what the
// boxes before it in draw order drew under it is blurred and tinted, and its
// border, text and children draw over that.

// maxBackdrops is the most backdrops a frame has; the boxes past that draw
// their fallback colour.
const maxBackdrops = 16

// backdrops are the frame's backdrop recipes, and the next box's.
type backdrops struct {
	recs    [maxBackdrops]gpu.Backdrop
	n       int
	next    gpu.Backdrop
	hasNext bool
}

// NextBackdrop makes the next box a backdrop of recipe b: what the boxes
// drawn before it drew under its rectangle (inside its ancestors' clips) is
// blurred and tinted by b, opaque and cut by the box's corner radii, and its
// border, text and children draw over that. Its background colour (NextBG
// with DrawBackground) is the fallback: a renderer that cannot blur
// (gpu.Renderer.Backdrops) fills the box with it instead, so make it the
// blur's mean over typical content. The box's opacity and tint transform
// apply to the fallback only. A frame has at most 16 backdrops; the boxes
// past them draw their fallback.
//
// A recipe with a Fade fades out at an edge instead of ending there, as
// macOS 26's soft scroll edge does under a toolbar: one box over the bar and
// the tail below it, its blur and tint easing off over the tail into the
// content under it,
//
//	b.Fade, b.FadeEdge = tailPx, gpu.FadeBottom
//	c.NextBackdrop(b)
func (c *Context) NextBackdrop(b gpu.Backdrop) { c.bd.next, c.bd.hasNext = b, true }

// takeBackdrop gives the box being built (b) the next backdrop, if one was
// set.
func (c *Context) takeBackdrop(b *box) {
	if !c.bd.hasNext {
		return
	}
	c.bd.hasNext = false
	if c.bd.n >= maxBackdrops {
		return
	}
	c.bd.recs[c.bd.n] = c.bd.next
	c.bd.n++
	b.bdrop = uint8(c.bd.n)
}
