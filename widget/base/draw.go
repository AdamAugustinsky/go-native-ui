package base

import (
	"gonativeui/icon"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
)

// The draw helpers. A recipe styles a component's own boxes with ui's Next
// calls; these build the extra boxes most components need, keyed from the
// component so that they keep their identity.

// FocusRingOutsetPt is how far outside a control's shape its keyboard focus
// ring reaches, in points: AppKit's ring ends 3 pt outside the frame
// (keyboardFocusIndicatorColor, measured on macOS 27 for push buttons, pop-up
// buttons, text and search fields, checkboxes and radio buttons).
const FocusRingOutsetPt = 3

// FocusRingWidthPt is the width of the ring's band, in points: it runs from the
// outset inwards and overlaps the shape's edge by half a point, as AppKit's
// does (measured 3.49 pt).
const FocusRingWidthPt = 3.5

// RingGeom is the geometry of a focus ring around a shape, in pixels.
type RingGeom struct {
	// Rect is the ring's outer edge (x0, y0, x1, y1), in the coordinates the
	// shape's rectangle was given in.
	Rect [4]float32
	// Radius is the outer edge's corner radius.
	Radius float32
	// Width is the band's width, from the outer edge inwards.
	Width float32
}

// FocusRingGeom is the ring around the shape rect (x0, y0, x1, y1) of corner
// radius radius, at scale pixels per point: the rectangle outset by
// FocusRingOutsetPt on every side, the radius grown by the same (so the ring
// follows the shape), and a band FocusRingWidthPt wide. Edges are snapped to
// whole pixels.
func FocusRingGeom(rect [4]float32, radius, scale float32) RingGeom {
	o := theme.Snap(FocusRingOutsetPt * scale)
	return RingGeom{
		Rect:   [4]float32{rect[0] - o, rect[1] - o, rect[2] + o, rect[3] + o},
		Radius: radius + o,
		Width:  theme.Snap(FocusRingWidthPt * scale),
	}
}

// FocusRing builds the keyboard focus ring of the component whose root is id,
// outside its shape (the root's rectangle of the previous frame, with the
// corner radius radius in pixels), in the FocusRing role at v.Ring. The
// geometry is FocusRingGeom's with the theme's Lines tokens (FocusOutset and
// FocusWidth). It draws nothing while v.Ring is 0 (the focus came from the
// pointer, or the component is not focused), and nothing in an inactive
// window, where AppKit hides the ring.
//
// Build it in the component's root, when the root does not clip its children,
// or after the root in the root's parent: either way the ring is not clipped by
// the component itself and reaches past its edges, and only a clipping
// ancestor (a scroll view, a pane) cuts it, as a clip view cuts AppKit's. Leave
// FocusRingOutsetPt of room around a control that can take the focus.
func (k *Kit) FocusRing(id ui.ID, v Visual, radius float32) {
	if v.Ring <= 0.004 || v.Inactive || id == 0 {
		return
	}
	c := k.c
	r := c.Rect(id)
	if r[2] <= r[0] || r[3] <= r[1] {
		return
	}
	p := c.Rect(c.Parent())
	rel := [4]float32{r[0] - p[0], r[1] - p[1], r[2] - p[0], r[3] - p[1]}
	o, w := theme.Snap(k.r.Lines.FocusOutset), theme.Snap(k.r.Lines.FocusWidth)
	g := RingGeom{Rect: [4]float32{rel[0] - o, rel[1] - o, rel[2] + o, rel[3] + o}, Radius: radius + o, Width: w}
	theme.At(c, g.Rect[0], g.Rect[1], g.Rect[2]-g.Rect[0], g.Rect[3]-g.Rect[1])
	c.NextBorder(theme.WithAlpha(k.r.Colors[theme.FocusRing], v.Ring), g.Width)
	c.NextRadius(g.Radius)
	c.BoxKeyed(ui.DrawBorder|Floating, ui.KeyN(c.KeyOf(id), 9004), nil)
}

// Fill builds a box of colour color (RGBA) with corner radius radius over the
// whole of the component whose root id is the current parent, sized by the
// root's rectangle of the previous frame: a tint over the component's own fill
// (a pressed or hovered look). n tells the fills of one component apart.
func (k *Kit) Fill(id ui.ID, n uint64, color uint32, radius float32) {
	if color&255 == 0 {
		return
	}
	c := k.c
	r := c.Rect(id)
	w, h := r[2]-r[0], r[3]-r[1]
	if w <= 0 || h <= 0 {
		return
	}
	theme.At(c, 0, 0, w, h)
	c.NextBG(color)
	c.NextRadius(radius)
	c.BoxKeyed(ui.DrawBackground|Floating, ui.KeyN(c.KeyOf(id), 9010+n), nil)
}

// Text builds one line of text s in font f and colour color (RGBA), as high
// as h pixels with the text centred in it and as wide as its text, aligned by
// align, in the flow of the current parent; it fades out where it does not fit.
// n tells the texts of one component apart. It records nothing for the
// accessibility tree: the component's root carries the name.
func (k *Kit) Text(n uint64, s string, f text.FontKey, color uint32, align ui.Align, h float32) ui.ID {
	if s == "" {
		return 0
	}
	c := k.c
	c.NextSize(ui.TextSize(LabelSlack), ui.Px(h))
	c.NextFont(f)
	c.NextTextColor(color)
	c.NextAlign(align)
	return c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(c.ParentKey(), 9020+n), StrBytes(s))
}

// LabelSlack is the pixel a text box is wider than its text: layout snaps the
// box's edges to whole pixels, which can leave it a fraction narrower than the
// text, and a text box narrower than its text fades out at the edge.
const LabelSlack = 1

// Icon builds the icon id of the kit's atlas in colour (RGBA; 0 takes the text
// colour) in the box the icon needs (NextHeight makes the box taller, with the
// glyph centred in it). An icon that cannot be drawn on this system builds an
// empty box.
func (k *Kit) Icon(id icon.ID, color uint32) ui.ID {
	if color == 0 {
		color = k.r.Pal.Text
	}
	c := k.c
	flags, disp := k.icons.Prepare(c, id, color)
	if k.hasH && flags != 0 { // a height set with NextHeight: the glyph stays centred in it
		w, _ := k.icons.Size(id)
		c.NextSize(ui.Px(w), k.nh)
	}
	k.hasH = false
	c.NextRole(ui.RoleImage)
	return c.BoxKeyed(flags, ui.KeyN(c.ParentKey(), 7000+uint64(id)), disp)
}

// IconSize is the size in pixels of the box Icon builds for id.
func (k *Kit) IconSize(id icon.ID) (w, h float32) { return k.icons.Size(id) }
