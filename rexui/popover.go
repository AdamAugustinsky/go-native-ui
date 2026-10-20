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
	ArrowH: 11, Gap: 2, TipR: 2, Flare: 2.5, ArrowInset: 36,
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

// popoverSide is the side of the anchor a the popover is on: the preferred
// one, or the other when the window has no room for need pixels there and
// has them on the other side (the rule of ui's placement of an overlay, so
// that the arrow is on the side the overlay chose).
func popoverSide(want PopoverEdge, a [4]float32, need, W, H float32) PopoverEdge {
	switch want {
	case PopoverBelow:
		if a[3]+need > H && a[1]-need >= 0 {
			return PopoverAbove
		}
	case PopoverAbove:
		if a[1]-need < 0 && a[3]+need <= H {
			return PopoverBelow
		}
	case PopoverRight:
		if a[2]+need > W && a[0]-need >= 0 {
			return PopoverLeft
		}
	case PopoverLeft:
		if a[0]-need < 0 && a[2]+need <= W {
			return PopoverRight
		}
	}
	return want
}

// BeginPopover opens a popover on the side o.Edge of anchor (a rectangle in
// window pixels, such as ui.Context.Rect of the component that opens it), with
// an arrow pointing at the anchor's centre, and makes its body the parent
// of what is built next, until EndPopover. It is an overlay: it draws above
// the window, is placed against the anchor and kept inside the window (the
// arrow still points at the anchor), moves the keyboard focus into it when it
// opens and gives it back when the app stops building it.
//
// The app owns whether it is open: it builds the popover while it is, and
// stops when the signal says Dismissed (see PopoverBehavior for what closes
// each kind and what becomes of the press).
//
//	if rexui.Button(k, "Info", rexui.ButtonOpts{}).Clicked { v.open = !v.open }
//	if v.open {
//		p := rexui.BeginPopover(k, "info", c.RectOf(infoKey), rexui.PopoverOpts{})
//		rexui.Label(k, "Details", rexui.LabelOpts{Style: rexui.TextHeadline})
//		rexui.EndPopover(k, &p)
//		v.open = !p.Dismissed
//	}
//
// The popover is not animated (it appears and goes at once), so Reduce Motion
// changes nothing and an idle window draws no frames.
func BeginPopover(k *base.Kit, label string, anchor [4]float32, o PopoverOpts) Popover {
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
	hair := r.Lines.Hairline
	W, H := c.Size()

	// the previous frame's body says which side it is on and how tall it is
	bodyKey := ui.KeyN(okey, 1)
	var prev [4]float32
	if c.OverlayOpen(okey) {
		prev = c.RectOf(bodyKey)
	}
	bodyH := max(prev[3]-prev[1], 0)
	need := bodyH + arrowRoom
	if o.Edge == PopoverRight || o.Edge == PopoverLeft {
		need = surfW + arrowRoom
	}
	edge := popoverSide(o.Edge, anchor, need, float32(W), float32(H))
	vertical := edge == PopoverBelow || edge == PopoverAbove // the arrow is on a horizontal edge of the body
	wrapW, wrapH := surfW, bodyH
	if !vertical {
		wrapW = surfW + arrowRoom
	} else {
		wrapH = bodyH + arrowRoom
	}
	cx, cy := (anchor[0]+anchor[2])/2, (anchor[1]+anchor[3])/2

	// the anchor the overlay is placed against: centred on the anchor's
	// centre, so that the arrow can be in the middle of the body's edge
	place := ui.PlaceBelow
	a := anchor
	switch edge {
	case PopoverBelow, PopoverAbove:
		a[0], a[2] = cx-wrapW/2, cx+wrapW/2
		if edge == PopoverAbove {
			place = ui.PlaceAbove
		}
	default:
		a[1], a[3] = cy-wrapH/2, cy+wrapH/2
		place = ui.PlaceRight
		if edge == PopoverLeft {
			place = ui.PlaceLeft
		}
	}

	// where the body is: the overlay's origin once placed and kept inside the window
	var along, bodyLen float32 // the arrow's offset along the body's edge
	if vertical {
		x0 := max(min(a[0], float32(W)-wrapW), 0)
		along, bodyLen = cx-x0, surfW
	} else {
		y0 := max(min(a[1], float32(H)-wrapH), 0)
		along, bodyLen = cy-y0, bodyH
	}
	inset := min(pt(sc, rc.ArrowInset), bodyLen/2)
	along = max(min(along, bodyLen-inset), inset)

	pad := pt(sc, rc.Pad)
	if o.NoPad {
		pad = 0
	}
	// the wrapper: transparent, with the arrow's room on the anchor's side
	var pl, pt_, pr, pb float32
	switch edge {
	case PopoverBelow:
		pt_ = arrowRoom
	case PopoverAbove:
		pb = arrowRoom
	case PopoverRight:
		pl = arrowRoom
	case PopoverLeft:
		pr = arrowRoom
	}
	c.NextPadding(pl, pt_, pr, pb)
	c.NextSize(ui.Px(wrapW), ui.Children(1))
	c.NextRole(ui.RoleDialog)
	c.NextSemName(display)
	dismiss := ui.DismissTransient
	switch o.Behavior {
	case PopoverSemitransient:
		dismiss = ui.DismissSemitransient
	case PopoverApplicationDefined:
		dismiss = ui.DismissApplication
	}
	c.NextOverlay(ui.OverlayOpts{Dismiss: dismiss})
	os := c.BeginOverlay(okey, ui.OverlayPopup, a, place)
	pop := Popover{PopoverSignal{Edge: edge}}
	switch o.Behavior {
	case PopoverTransient:
		pop.Dismissed = os.Dismissed || !c.Input().Focused
	case PopoverSemitransient:
		pop.Dismissed = os.Dismissed
	}
	if prev == ([4]float32{}) { // a second frame puts it, and its arrow, where the window leaves room
		c.RequestFrame()
	}

	fill := rc.fill(r)
	c.NextBG(fill)
	c.NextBorder(theme.Over(rc.Edge.RGBA(r), fill), hair) // opaque: the ring is a shade of the fill
	c.NextRadius(radius)
	c.NextPad(pad)
	c.NextSize(ui.Px(surfW), ui.Children(1))
	c.NextAuditExempt(ui.AuditOverflow.Mask()) // the arrow lies outside the body
	pop.ID = c.BoxKeyed(ui.DrawBackground|ui.DrawBorder, bodyKey, nil)
	c.Push(pop.ID)
	// the arrow is built first, floating in the body's parent's room: it is
	// positioned in the wrapper's coordinates through the body's own, so it
	// belongs to the body and is drawn over its edge
	popArrow(k, bodyKey, edge, along, arrowRoom, fill, hair)
	return pop
}

// popArrow builds the arrow of the popover whose body (the current parent)
// has the key key: rows (columns, for a side edge) one pixel thick from the
// tip, in fill, with the arrow's centre along from the body's start along
// its edge. The rows run on into the body by a hairline, over its ring.
func popArrow(k *base.Kit, key ui.Key, edge PopoverEdge, along, room float32, fill uint32, hair float32) {
	c := k.Context()
	br := c.RectOf(key) // the body as the previous frame laid it out
	bw, bh := br[2]-br[0], br[3]-br[1]
	if (edge == PopoverAbove && bh <= 0) || (edge == PopoverLeft && bw <= 0) {
		return // the arrow hangs off the body's far edge: from the next frame
	}
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
}
