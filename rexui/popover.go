package rexui

import (
	"math"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// PopoverEdge is the side of its anchor a popover opens on (NSPopover's
// preferredEdge); it flips to the other side when there is no room.
type PopoverEdge uint8

const (
	// PopoverBelow opens under the anchor, the arrow pointing up at it.
	PopoverBelow PopoverEdge = iota
	// PopoverAbove opens over the anchor.
	PopoverAbove
	// PopoverRight opens to the right of the anchor.
	PopoverRight
	// PopoverLeft opens to the left of the anchor.
	PopoverLeft
)

// PopoverBehavior is when a popover closes (NSPopoverBehavior).
type PopoverBehavior uint8

const (
	// PopoverTransient closes on Escape, on a press outside it and when the
	// window stops being the key window (the app is switched away from): the
	// signal's Dismissed is set and the app stops building it. A press
	// outside reaches what it landed on, except on the anchor, which it
	// closes the popover on and does not reach, so the button that opens a
	// popover does not reopen it. It is the zero value, as AppKit's default
	// (all measured on macOS 27.2, tools/appkitref/popover.sh behavior).
	PopoverTransient PopoverBehavior = iota
	// PopoverSemitransient closes on Escape and on a press in the window
	// outside the popover, which reaches what it landed on, the anchor
	// included, and does not close it there; it stays when the window stops
	// being key.
	PopoverSemitransient
	// PopoverApplicationDefined never closes by itself, on Escape or on a
	// press: the app decides. Presses outside reach what they land on.
	PopoverApplicationDefined
)

// PopoverOpts are a popover's options, passed by value. The zero value is a
// transient popover below its anchor, PopoverLook.Width wide, with the
// content padded by PopoverLook.Pad.
type PopoverOpts struct {
	// Edge is the preferred side of the anchor.
	Edge PopoverEdge
	// Behavior is when it closes.
	Behavior PopoverBehavior
	// Width is the popover's width in points (0: PopoverLook.Width): its
	// height is its content's.
	Width float32
	// NoPad leaves the content flush with the edges, for a content that has
	// its own insets (a list that runs to the edge).
	NoPad bool
	// Label is the accessible name of the popover.
	Label string
	// Arrow draws NSPopover's look: an arrow pointing at the anchor, the
	// 20 pt radius and the shaded fill. Without it the popover is the
	// floating surface the menus and pickers share (OverlayLook, T3 Code's
	// popup), PopoverRecipe.Offset from its anchor.
	Arrow bool
}

// PopoverRecipe is the look of popovers: an NSPopover on macOS 27.2, measured
// live on the Mac mini (tools/appkitref/popover.sh: a real popover on a window
// of the window background colour, captured by GnuiShot, light and dark).
//
// The rexui popover is drawn, not hosted. Its content is built by us in the
// window's own layer (text selection, lists, fields and the zero-allocation
// frame all depend on that), and a hosted NSPopover can only carry a native
// view; a native vibrancy region cannot take the arrow's shape either (an
// NSVisualEffectView is a rounded rectangle, and the arrow joins the body in
// one outline). So the fill is opaque, the colour the real popover's material
// has over the window background.
type PopoverRecipe struct {
	// Width is the default width, in points, and Pad the content's padding.
	Width, Pad float32
	// Radius is the body's corner radius (20, measured).
	Radius float32
	// ArrowH is the arrow's height from the body's edge to its tip (11), and
	// Gap the tip's distance from the anchor (2), in points; so the body's
	// edge is ArrowH+Gap from the anchor.
	ArrowH, Gap float32
	// TipR is the radius that rounds the tip (the 90° apex measured 4 pt
	// wide at its first pixel row), and Flare the radius of the concave
	// shoulder where the arrow meets the body (the base measured 5 pt
	// wider than the 45° sides give).
	TipR, Flare float32
	// ArrowInset is the nearest the arrow's centre comes to the body's end
	// along its edge, in points: the arrow stays on the straight part of the
	// edge, clear of the corner.
	ArrowInset float32
	// Offset is how far a popover without an arrow is from its anchor, in
	// points (T3 Code's popups' side offset).
	Offset float32
	// Fill is the body: the raised surface (Raised) with Shade laid over it
	// (#F2F2F2 over white, #2D2D2D over the dark window, measured: a popover's
	// material is a little off the surface it floats above). Edge is the 1 px
	// ring round the body (#ECECEC and #2A2A2A measured): black at 2.5 % in
	// light and 8 % in dark, in the Shadow role's colour.
	Fill, Shade, Edge Tint
}

// PopoverLook is the popovers' recipe.
var PopoverLook = PopoverRecipe{
	Width: 280, Pad: 16, Radius: 20,
	ArrowH: 11, Gap: 2, TipR: 2, Flare: 2.5, ArrowInset: 36, Offset: 6,
	Fill: Of(theme.Raised), Shade: OfAlpha(theme.Label, 0.051, 0.015), Edge: OfAlpha(theme.Shadow, 0.025, 0.08),
}

// PopoverSignal is what a popover reports.
type PopoverSignal struct {
	// ID is the popover's body (the box the content is built in).
	ID ui.ID
	// Dismissed: the popover's behaviour asks the app to close it; the app
	// stops building it.
	Dismissed bool
	// Edge is the side of the anchor it is on, which can differ from the
	// preferred one when the window left no room.
	Edge PopoverEdge
}

// Popover is a popover being built: BeginPopover opens it and EndPopover
// closes it.
type Popover struct {
	PopoverSignal
}

// popOver is the fill the body and the arrow share.
func (rc *PopoverRecipe) fill(r *theme.Resolved) uint32 {
	return theme.Over(rc.Shade.RGBA(r), rc.Fill.RGBA(r)|0xFF)
}

// half is the arrow's half-width at distance d from its tip, in points, along
// the 45° sides (half = d) with the tip rounded by a circle of radius TipR
// and the base flared into the body.
func (rc *PopoverRecipe) half(d float32) float32 {
	rt, fl, h := float64(rc.TipR), float64(rc.Flare), float64(rc.ArrowH)
	dd := float64(d)
	var w float64
	if tc := rt * (1 - math.Sqrt2/2); dd < tc { // on the tip's circle
		w = math.Sqrt(max(rt*rt-(rt-dd)*(rt-dd), 0))
	} else { // the side, continuing the circle's tangent
		w = rt*math.Sqrt2/2 + (dd - tc)
	}
	if x := dd - (h - fl); x > 0 { // the concave shoulder
		w += fl - math.Sqrt(max(fl*fl-x*x, 0))
	}
	return float32(w)
}

// BeginPopover opens a popover on the side o.Edge of anchor (a rectangle in
// window pixels, such as ui.Context.Rect of the component that opens it), with
// an arrow pointing at the anchor's centre, and makes its body the parent
// of what is built next, until EndPopover. It is an overlay: it draws above
// the window, is centred on the anchor, kept in the window's usable area by
// ui's positioner (flipped to the other side when its own has no room,
// shifted along the anchor, its length bounded to the room with its content
// scrolling: ui.OverlayOpts.Fit) while the arrow still points at the anchor,
// moves the keyboard focus into it when it opens and gives it back when the
// app stops building it. Against a box built in this frame, BeginPopoverOn
// places it where that box is now, not where it was in the last frame.
//
// The app owns whether it is open: it builds the popover while it is, and
// stops when the signal says Dismissed (see PopoverBehavior for what closes
// each kind and what becomes of the press).
//
//	b := rexui.Button(k, "Info", rexui.ButtonOpts{})
//	if b.Clicked { v.open = !v.open }
//	if v.open {
//		p := rexui.BeginPopoverOn(k, "info", b.ID, rexui.PopoverOpts{})
//		rexui.Label(k, "Details", rexui.LabelOpts{Style: rexui.TextHeadline})
//		rexui.EndPopover(k, &p)
//		v.open = !p.Dismissed
//	}
//
// The popover fades in from its anchor's side over the theme's Short
// duration (OverlayEnter; at once under Reduce Motion and strict AppKit),
// then draws no frames while idle; it goes at once when the app stops
// building it.
func BeginPopover(k *base.Kit, label string, anchor [4]float32, o PopoverOpts) Popover {
	return beginPopover(k, label, anchor, 0, o)
}

// BeginPopoverOn is BeginPopover against the box anchor, built before it in
// this frame (the button that opens it): the popover is placed where layout
// puts that box in this frame (ui.Context.BeginOverlayOn), so it never lags a
// frame behind an anchor that moves, as one in a list being scrolled.
func BeginPopoverOn(k *base.Kit, label string, anchor ui.ID, o PopoverOpts) Popover {
	return beginPopover(k, label, k.Context().Rect(anchor), anchor, o)
}

// popoverPlace is the placement of a popover on edge e.
func popoverPlace(e PopoverEdge) ui.Placement {
	switch e {
	case PopoverAbove:
		return ui.PlaceAbove
	case PopoverRight:
		return ui.PlaceRight
	case PopoverLeft:
		return ui.PlaceLeft
	}
	return ui.PlaceBelow
}

// popoverFlip is the edge across the anchor from e.
func popoverFlip(e PopoverEdge) PopoverEdge { return e ^ 1 }

// beginPopover is BeginPopover against rect, or against box on of this frame
// when it is not 0 (rect is then its last frame's rectangle).
func beginPopover(k *base.Kit, label string, rect [4]float32, on ui.ID, o PopoverOpts) Popover {
	c, r := k.Context(), k.Theme()
	rc := &PopoverLook
	sc := k.Scale()
	display, ident := ui.SplitLabel(label)
	if display == "" {
		display = o.Label
	}
	okey := ui.HashString(ui.HashString(c.ParentKey(), "popover"), ident)

	width := rc.Width
	if o.Width > 0 {
		width = o.Width
	}
	surfW := pt(sc, width)
	arrowRoom := pt(sc, rc.ArrowH+rc.Gap)
	radius := pt(sc, rc.Radius)
	if !o.Arrow { // the shared floating surface, Offset from the anchor
		arrowRoom, radius = pt(sc, rc.Offset), pt(sc, OverlayLook.Radius)
	}
	hair := r.Lines.Hairline

	// the overlay: centred on the anchor, the arrow's room from it, kept in
	// the usable area by ui (flip, shift, a bounded length)
	dismiss := ui.DismissTransient
	switch o.Behavior {
	case PopoverSemitransient:
		dismiss = ui.DismissSemitransient
	case PopoverApplicationDefined:
		dismiss = ui.DismissApplication
	}
	c.NextOverlay(ui.OverlayOpts{Dismiss: dismiss, Align: ui.OverlayCenter, Gap: arrowRoom, Fit: true})
	c.NextSize(ui.Px(surfW), ui.Children(0))
	c.NextRole(ui.RoleDialog)
	c.NextSemName(display)
	var os ui.OverlaySignal
	if on != 0 {
		os = c.BeginOverlayOn(okey, ui.OverlayPopup, on, popoverPlace(o.Edge))
	} else {
		os = c.BeginOverlay(okey, ui.OverlayPopup, rect, popoverPlace(o.Edge))
	}
	edge := o.Edge
	if os.Flipped {
		edge = popoverFlip(edge)
	}
	pop := Popover{PopoverSignal: PopoverSignal{Edge: edge}}
	switch o.Behavior {
	case PopoverTransient:
		pop.Dismissed = os.Dismissed || !c.Input().Focused
	case PopoverSemitransient:
		pop.Dismissed = os.Dismissed
	}

	t, dx, dy := OverlayEnter(k, okey, popoverPlace(edge))
	c.PushOpacity(t)
	fill := rc.fill(r)
	flags := ui.DrawBackground
	if o.Arrow {
		flags |= nextArrowSurface(k, theme.Over(rc.Edge.RGBA(r), fill), hair, radius) // opaque: the ring is a shade of the fill
	} else {
		fill = rc.Fill.RGBA(r) | 0xFF
		flags |= NextOverlaySurface(k, radius)
	}
	c.NextBG(fill)
	pad := pt(sc, rc.Pad)
	if o.NoPad {
		pad = 0
	}
	c.NextPad(pad)
	c.NextSize(ui.Pct(1), ui.Children(0))
	c.NextOffset(dx, dy)
	c.NextAuditExempt(ui.AuditOverflow.Mask()) // the arrow lies outside the body
	pop.ID = c.BoxKeyed(flags, ui.KeyN(okey, 1), nil)
	c.Push(pop.ID)
	if o.Arrow {
		popoverArrow(k, okey, rect, on, os.Rect, edge, arrowRoom, surfW, fill, hair)
	}
	return pop
}

// popoverArrow builds the arrow of the popover okey on edge, from the body's
// rectangle in the last frame (prev, the overlay's: zero in the frame it
// opens) and where its anchor was then: the anchor's centre stands as far
// along the body's edge as it did, so the arrow is exact when the popover
// moves with its anchor (BeginPopoverOn moves both in the same frame), and
// one frame behind only when ui shifted or flipped it anew; a frame is asked
// for then. In the frame it opens, the arrow is in the middle of the edge
// (where the centred overlay puts the anchor unless the window's edge
// shifts it), and one more frame aims it.
func popoverArrow(k *base.Kit, okey ui.Key, rect [4]float32, on ui.ID, prev [4]float32, edge PopoverEdge, room, surfW float32, fill uint32, hair float32) {
	c := k.Context()
	rc := &PopoverLook
	vertical := edge == PopoverBelow || edge == PopoverAbove // the arrow is on a horizontal edge of the body
	ax := 0
	if !vertical {
		ax = 1
	}
	// the anchor's centre as the last frame placed the popover against it:
	// a box's last rectangle, or the rectangle passed then (kept here)
	cur := (rect[ax] + rect[ax+2]) / 2
	last := c.KeepValue(ui.KeyN(okey, 2), cur, cur)
	if on != 0 {
		last = cur // rect is the box's last rectangle already
	} else if last != cur {
		c.RequestFrame() // the anchor moved: aim again from where this frame puts it
	}
	bw, bh := prev[2]-prev[0], prev[3]-prev[1]
	var along float32
	if bw <= 0 || bh <= 0 { // the frame it opens
		c.RequestFrame()
		if edge != PopoverBelow {
			return // it hangs off an edge whose place is not known yet
		}
		bw, along = surfW, surfW/2
	} else {
		along = last - prev[ax]
	}
	bodyLen := bw
	if !vertical {
		bodyLen = bh
	}
	inset := min(pt(k.Scale(), rc.ArrowInset), bodyLen/2)
	along = max(min(along, bodyLen-inset), inset)
	popArrow(k, ui.KeyN(okey, 1), edge, along, room, fill, hair, bw, bh)
}

// nextArrowSurface edges and lifts the body of a popover with an arrow
// (NSPopover's look): an opaque ring of edge, radius, and the menus' lift, or
// under strict AppKit the shadows Rex drew before D108.
func nextArrowSurface(k *base.Kit, edge uint32, hair, radius float32) ui.Flags {
	c := k.Context()
	c.NextBorder(edge, hair)
	c.NextRadius(radius)
	flags := ui.DrawBorder
	sa, sb := surfaceShadows(k, OverlayLook.Lift)
	if sa.Sigma > 0 || sb.Sigma > 0 {
		c.NextShadows(sa, sb)
		flags |= ui.DrawShadow
	}
	return flags
}

// popArrow builds the arrow of the popover whose body (the current parent)
// has the key key: rows (columns, for a side edge) one pixel thick from the
// tip, in fill, with the arrow's centre along from the body's start along
// its edge; bw and bh are the body's size. The rows run on into the body by
// a hairline, over its ring.
func popArrow(k *base.Kit, key ui.Key, edge PopoverEdge, along, room float32, fill uint32, hair, bw, bh float32) {
	c := k.Context()
	rc := &PopoverLook
	sc := k.Scale()
	gap := pt(sc, rc.Gap)
	n := int(math.Round(float64(pt(sc, rc.ArrowH)))) + int(hair)
	maxD := rc.ArrowH
	for i := 0; i < n; i++ {
		d := min((float32(i)+0.5)/sc, maxD)
		hw := rc.half(d) * sc
		if hw < 0.5 {
			continue
		}
		off := float32(i) // pixels from the tip along the arrow's axis
		var x, y, w, h float32
		switch edge {
		case PopoverBelow: // above the body, pointing up: the tip is at the body's top less the room
			x, y, w, h = along-hw, -room+gap+off, 2*hw, 1
		case PopoverAbove: // below the body, pointing down
			x, y, w, h = along-hw, bh+room-gap-off-1, 2*hw, 1
		case PopoverRight:
			x, y, w, h = -room+gap+off, along-hw, 1, 2*hw
		default:
			x, y, w, h = bw+room-gap-off-1, along-hw, 1, 2*hw
		}
		theme.At(c, x, y, w, h)
		c.NextBG(fill)
		c.NextRadius(0.5)
		c.NextAuditExempt(ui.AuditOverflow.Mask())
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 9100+uint64(i)), nil)
	}
}

// EndPopover closes the popover BeginPopover opened.
func EndPopover(k *base.Kit, p *Popover) {
	c := k.Context()
	c.Pop() // the body
	c.EndOverlay()
	c.PopOpacity()
}
