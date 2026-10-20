package rexui

import (
	"math"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// SkeletonShape is what a skeleton stands in for.
type SkeletonShape uint8

const (
	// SkeletonLine is a line of text: a capsule 12 pt high that fills the
	// parent's width.
	SkeletonLine SkeletonShape = iota
	// SkeletonBlock is an image or a card: a rounded rectangle 80 pt high.
	SkeletonBlock
	// SkeletonAvatar is a circle of an avatar's size.
	SkeletonAvatar
)

// SkeletonOpts are a skeleton's options, passed by value.
type SkeletonOpts struct {
	// Shape is what it stands in for.
	Shape SkeletonShape
	// Name is the accessible name ("Loading messages"); empty makes the
	// skeleton decorative: one of many placeholder lines should not make
	// assistive technologies announce each, so name only the group's first
	// or put the name on a parent.
	Name string
	// Still draws the sheen at Phase and asks for no frames, whatever the
	// motion settings: screenshots and comparisons use it.
	Still bool
	// Phase is where a Still skeleton's sheen stands, 0 (entering at the
	// start) to 1 (gone past the end).
	Phase float32
}

// SkeletonRecipe is the look of skeletons: the tertiary fill with a lighter
// band that sweeps across it while the placeholder is on screen. Designed;
// no AppKit control is a skeleton.
type SkeletonRecipe struct {
	// H and Radius are the shapes' default heights and corner radii, in
	// points (a line and an avatar are capsules and circles: Radius 0 means
	// half the height); AvatarSize is the circle's side.
	H, Radius  [3]float32
	AvatarSize float32
	// Track is the placeholder's fill and Sheen the band that crosses it
	// (the label colour at a small opacity, light and dark).
	Track, Sheen Tint
	// Band is the sheen's width as a share of the shape's, Period the time
	// it takes to cross, in seconds, and RestPhase where it stands under
	// Reduce Motion (none: the track alone).
	Band, Period float32
	// Pulse is how far an avatar's fill moves toward the sheen's colour at
	// the top of its pulse (a circle has no straight part for a sweep).
	Pulse float32
}

// SkeletonLook is the skeletons' recipe.
var SkeletonLook = SkeletonRecipe{
	H:      [3]float32{12, 80, 0},
	Radius: [3]float32{0, 8, 0}, AvatarSize: 36,
	Track: Of(theme.Fill2), Sheen: OfAlpha(theme.Label, 0.045, 0.07),
	Band: 0.4, Period: 1.6, Pulse: 1,
}

// Skeleton builds a placeholder for content that is loading. A line or a block
// fills the parent's width (the kit's NextSize says otherwise); its sheen
// crosses it, once per SkeletonRecipe.Period, only while the skeleton is on
// screen and the window showing, and asks for no frames otherwise: an idle
// window, a hidden one and a skeleton scrolled out of view draw none. Under
// Reduce Motion it is the plain track, still. An avatar-shaped skeleton pulses
// instead of sweeping.
//
//	rexui.Skeleton(k, "msg-1", rexui.SkeletonOpts{Name: "Loading messages"})
//
// The label tells the skeletons of one parent apart (as every label does).
func Skeleton(k *base.Kit, label string, o SkeletonOpts) ui.ID {
	c, r := k.Context(), k.Theme()
	rc := &SkeletonLook
	sc := k.Scale()
	shape := o.Shape
	if shape > SkeletonAvatar {
		shape = SkeletonLine
	}
	_, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)

	h := pt(sc, rc.H[shape])
	w := ui.Pct(1)
	if shape == SkeletonAvatar {
		h = pt(sc, rc.AvatarSize)
		w = ui.Px(h)
	}
	radius := pt(sc, rc.Radius[shape])
	if ph, ok := k.PendingHeight(); ok && ph.Kind == ui.SizePixels {
		h = ph.Value
	}
	k.ApplySize(w, ui.Px(h))
	if radius == 0 || radius > h/2 {
		radius = h / 2
	}
	c.NextBG(rc.Track.RGBA(r))
	c.NextRadius(radius)
	if o.Name != "" {
		c.NextRole(ui.RoleProgressBar)
		c.NextSemName(o.Name)
	}
	id := c.BoxKeyed(ui.DrawBackground|disabledFlag(k), key, nil)

	phase := float32(-1) // no sheen
	switch {
	case o.Still:
		phase = o.Phase
	case !reduced(k) && !k.Disabled():
		phase = cycle(k, int64(rc.Period*1e9))
		if loops(k, id) {
			c.RequestFrame()
		}
	}
	if phase < 0 || k.Disabled() {
		return id
	}
	rr := c.Rect(id)
	bw := rr[2] - rr[0]
	if bw <= 0 {
		return id
	}
	sheen := rc.Sheen.RGBA(r)
	c.Push(id)
	defer c.Pop()
	if shape == SkeletonAvatar { // the whole disc pulses
		a := 0.5 - 0.5*float32(math.Cos(2*math.Pi*float64(phase)))
		theme.At(c, 0, 0, bw, bw)
		c.NextBG(theme.WithAlpha(sheen, a*rc.Pulse))
		c.NextRadius(bw / 2)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 1), nil)
		return id
	}
	// the sheen runs in the straight part of the shape: between the corner
	// radii, clipped there so that it never leaves the rounded ends
	inner := bw - 2*radius
	if inner <= 0 {
		return id
	}
	theme.At(c, radius, 0, inner, h)
	c.NextAuditExempt(ui.AuditOverflow.Mask())
	clip := c.BoxKeyed(ui.Clip|base.Floating, ui.KeyN(key, 2), nil)
	c.Push(clip)
	band := theme.Snap(bw * rc.Band)
	x := phase*(inner+band) - band // the band's left edge in the clip
	half := theme.Snap(band / 2)
	clear0 := theme.WithAlpha(sheen, 0)
	theme.At(c, x, 0, half, h)
	c.NextBG4(clear0, sheen, clear0, sheen)
	c.NextAuditExempt(ui.AuditOverflow.Mask())
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 3), nil)
	theme.At(c, x+half, 0, band-half, h)
	c.NextBG4(sheen, clear0, sheen, clear0)
	c.NextAuditExempt(ui.AuditOverflow.Mask())
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 4), nil)
	c.Pop()
	return id
}
