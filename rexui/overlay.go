package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// OverlayRecipe is the surface every floating panel shares: popovers, the
// pickers built in them, the drawn menus (DropdownMenu, ContextMenu off
// macOS), the completion list, the palette and dialogs. Its shape is T3
// Code's popup (its app.asar, 2026-11-03: Base UI's popover, menu and select
// popups with the dropdown-glass class), drawn opaque: a raised fill, a 10
// pt radius, a 1 pt edge and a fade in from its anchor (D98). Its edge and
// shadows are a step of the theme's lift scale (theme.Lifts, D108); under
// strict AppKit (theme.Resolved.StrictAppKit) they are what Rex drew before,
// T3's: Edge and Shadows. Lengths are points.
type OverlayRecipe struct {
	// Radius is the corner radius (10: T3's rounded-lg, --radius 0.625rem).
	Radius float32
	// Lift is the surface's step of the lift scale (theme.LiftMenu): its
	// contact and ambient shadows and its edge. A dialog takes
	// theme.LiftDialog (NextSurface).
	Lift theme.Lift
	// EdgeW is the edge's width and Edge its colour under strict AppKit (T3's
	// border: 1 px of --contrast-foreground at 10 %).
	EdgeW float32
	Edge  Tint
	// Shadows are the layers under the surface under strict AppKit: T3's
	// long shadow (0 16 px 40 px −18 px, black at 55 %, 80 % in dark: blur
	// 40 is σ 20) and a contact shadow that keeps the surface's edge read
	// over a fill of its own colour.
	Shadows [2]theme.ShadowLayer
	// Rise is how far a popover travels toward its place from the side of
	// its anchor as it comes in (T3: 150 ms from 98 % scale and opacity 0;
	// there is no subtree transform here, so the scale's movement at the far
	// edge becomes a rise of that size). Enter is how long a popover's fade
	// takes, in seconds; 0 is the theme's Short duration (theme.Motion). A
	// menu, a list and a tooltip fade over the theme's Micro, without the
	// rise. The system's Reduce Motion keeps the fades and drops the rise
	// (D98); under strict AppKit every surface is in place at once.
	Rise, Enter float32
}

// OverlayLook is the floating surfaces' recipe.
var OverlayLook = OverlayRecipe{
	Radius: 10, Lift: theme.LiftMenu,
	EdgeW: 1, Edge: OfAlpha(theme.Label, 0.10, 0.10),
	Shadows: [2]theme.ShadowLayer{
		{Y: 16, Blur: 20, Spread: -18, Alpha: [2]float32{0.55, 0.80}},
		{Y: 1, Blur: 2, Alpha: [2]float32{0.06, 0.30}},
	},
	Rise: 4,
}

// shadows are the recipe's strict layers at the kit's scale and mode.
func (rc *OverlayRecipe) shadows(k *base.Kit) (a, b ui.Shadow) {
	r := k.Theme()
	sc := k.Scale()
	var out [2]ui.Shadow
	for i, l := range rc.Shadows {
		if l.Blur > 0 {
			out[i] = ui.Shadow{OffY: l.Y * sc, Sigma: l.Blur * sc, Spread: l.Spread * sc, Color: l.Color(r.Colors[theme.Shadow], r.T)}
		}
	}
	return out[0], out[1]
}

// surfaceShadows are the shadows of a floating surface at lift l: the
// lift's contact and ambient layers, or OverlayLook's strict ones under
// strict AppKit.
func surfaceShadows(k *base.Kit, l theme.Lift) (a, b ui.Shadow) {
	r := k.Theme()
	if r.StrictAppKit {
		return OverlayLook.shadows(k)
	}
	return theme.Lifts[l%theme.NumLifts].Shadows(r)
}

// NextOverlaySurface makes the next box a floating surface of fill (an
// opaque colour): OverlayLook's radius and its lift's edge and shadows, with
// radius in pixels (0: the recipe's). It returns the flags the box needs;
// build it with them.
//
//	c.NextBG(fill)
//	flags := rexui.NextOverlaySurface(k, 0)
//	c.BoxKeyed(flags|ui.DrawBackground, key, nil)
func NextOverlaySurface(k *base.Kit, radius float32) ui.Flags {
	return NextSurface(k, radius, OverlayLook.Lift)
}

// NextSurface is NextOverlaySurface at lift l: a dialog's sheet is
// theme.LiftDialog, a toast theme.LiftToast. Under strict AppKit every lift
// draws OverlayLook's strict edge and shadows, as Rex's surfaces did before
// the lift scale (D108).
func NextSurface(k *base.Kit, radius float32, l theme.Lift) ui.Flags {
	c, r := k.Context(), k.Theme()
	rc := &OverlayLook
	if radius <= 0 {
		radius = pt(k.Scale(), rc.Radius)
	}
	c.NextRadius(radius)
	sa, sb := surfaceShadows(k, l)
	var edge uint32
	var w float32
	if r.StrictAppKit {
		if rc.EdgeW > 0 {
			edge, w = rc.Edge.RGBA(r), max(theme.Snap(rc.EdgeW*k.Scale()), 1)
		}
	} else {
		ls := &theme.Lifts[l%theme.NumLifts]
		edge, w = ls.EdgeColor(r), ls.EdgeWidth(r)
	}
	return nextLifted(c, sa, sb, edge, w)
}

// nextLifted gives the next box shadows a and b and an edge of width w
// (none for 0) and returns the flags that draw them.
func nextLifted(c *ui.Context, a, b ui.Shadow, edge uint32, w float32) ui.Flags {
	flags := ui.Flags(0)
	if a.Sigma > 0 || b.Sigma > 0 {
		c.NextShadows(a, b)
		flags |= ui.DrawShadow
	}
	if w > 0 && edge&0xFF != 0 {
		c.NextBorder(edge, w)
		flags |= ui.DrawBorder
	}
	return flags
}

// overlayStill reports whether floating surfaces open at once: strict AppKit,
// or the kit's ReduceMotion (still renders, tests). The system's Reduce Motion
// keeps the fade and drops the travel (D98; overlayTravels).
func overlayStill(k *base.Kit) bool {
	return k.ReduceMotion || k.Theme().StrictAppKit
}

// overlayTravels reports whether a surface that fades in also moves into
// place: not under the system's Reduce Motion, which keeps only the fade.
func overlayTravels(k *base.Kit) bool {
	return k.Theme().Enter(theme.DefaultDurations.Short) < theme.Instant
}

// overlayDur is the theme's duration d (theme.Motion's Micro or Short), or the
// default ladder's when the theme has none.
func overlayDur(d, def float32) float32 {
	if d > 0 {
		return d
	}
	return def
}

// overlayFade is how far the surface keyed key has faded in, 0 to 1, over d
// seconds from the frame it is first built (its value starts at 0, and its
// first frame steps by one display interval at most: D103); 1 at once when
// surfaces open still. The system's Reduce Motion keeps the fade
// (theme.Resolved.Fade).
func overlayFade(k *base.Kit, key ui.Key, d float32) float32 {
	if overlayStill(k) {
		return 1
	}
	t := k.Context().AnimateValueFrom(ui.KeyN(key, 0x0E7E), 0, 1, k.Theme().Fade(d))
	if t > 0.995 {
		return 1
	}
	return t
}

// menuEnter is how far a menu, a list or a tooltip keyed key has faded in:
// over the theme's Micro duration, without travel.
func menuEnter(k *base.Kit, key ui.Key) float32 {
	return overlayFade(k, key, overlayDur(k.Theme().Motion.Micro, theme.DefaultDurations.Micro))
}

// dialogEnter is how long a dialog's or the palette's fade and slide take:
// the theme's Short duration, as a popover's.
func dialogEnter(k *base.Kit) float32 {
	return overlayDur(k.Theme().Motion.Short, theme.DefaultDurations.Short)
}

// OverlayEnter is how far the popover keyed key has come in, 0 to 1, over
// OverlayLook.Enter (the theme's Short duration) from the frame it is first
// built (its key not asked for in the frame before), and the offset in
// pixels, toward the side of its anchor given by place, that its rise
// leaves: the surface fades in at the returned opacity and starts that far
// from its place. Under the system's Reduce Motion it fades without the rise
// (D98); with the kit's ReduceMotion (still renders, tests) and under strict
// AppKit it is at rest at once.
//
//	t, dx, dy := rexui.OverlayEnter(k, key, ui.PlaceBelow)
//	c.PushOpacity(t)
//	c.NextOffset(dx, dy)
func OverlayEnter(k *base.Kit, key ui.Key, place ui.Placement) (t, dx, dy float32) {
	rc := &OverlayLook
	d := rc.Enter
	if d <= 0 {
		d = overlayDur(k.Theme().Motion.Short, theme.DefaultDurations.Short)
	}
	t = overlayFade(k, key, d)
	if t >= 1 {
		return 1, 0, 0
	}
	if !overlayTravels(k) {
		return t, 0, 0
	}
	off := theme.Snap((1 - t) * rc.Rise * k.Scale())
	switch place {
	case ui.PlaceBelow:
		dy = -off
	case ui.PlaceAbove:
		dy = off
	case ui.PlaceRight:
		dx = -off
	case ui.PlaceLeft:
		dx = off
	}
	return t, dx, dy
}

// nextToastLift gives the next box a toast's edge and shadows: the lift
// theme.LiftToast, or under strict AppKit what Rex drew before the lift
// scale (D108): theme.ElevRaised's one shadow and a Separator hairline. It
// returns the flags that draw them.
func nextToastLift(k *base.Kit) ui.Flags {
	c, r := k.Context(), k.Theme()
	if r.StrictAppKit {
		var sa ui.Shadow
		if sh := r.Elevations[theme.ElevRaised].Shadows[0]; sh.Blur > 0 {
			sa = ui.Shadow{OffY: sh.Y, Sigma: sh.Blur, Color: sh.Color(r.Colors[theme.Shadow], r.T)}
		}
		return nextLifted(c, sa, ui.Shadow{}, r.Colors[theme.Separator], r.Lines.Hairline)
	}
	ls := &theme.Lifts[theme.LiftToast]
	sa, sb := ls.Shadows(r)
	return nextLifted(c, sa, sb, ls.EdgeColor(r), ls.EdgeWidth(r))
}

// toastEnter is how far the toast keyed key has faded in: over the theme's
// Short duration from the frame it is first built, in a window that is
// showing (a hidden one schedules nothing), at once under Reduce Motion and
// strict AppKit.
func toastEnter(k *base.Kit, key ui.Key) float32 {
	if k.Context().Input().Hidden {
		return 1
	}
	return overlayFade(k, ui.KeyN(key, 71), overlayDur(k.Theme().Motion.Short, theme.DefaultDurations.Short))
}
