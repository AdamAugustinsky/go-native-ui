package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// OverlayRecipe is the surface every floating panel shares: popovers, the
// pickers built in them, the drawn menus (DropdownMenu, ContextMenu off
// macOS), the palette and dialogs. It is T3 Code's popup (its app.asar,
// 2026-11-03: Base UI's popover, menu and select popups with the
// dropdown-glass class), drawn opaque: a raised fill, a 1 pt edge of the
// label colour at 10 %, a 10 pt radius, a long soft shadow under it and a
// fade in from its anchor (D98). Lengths are points.
type OverlayRecipe struct {
	// Radius is the corner radius (10: T3's rounded-lg, --radius 0.625rem).
	Radius float32
	// EdgeW is the edge's width and Edge its colour (T3's border: 1 px of
	// --contrast-foreground at 10 %).
	EdgeW float32
	Edge  Tint
	// Shadows are the layers under the surface: T3's long shadow (0 16 px
	// 40 px −18 px, black at 55 %, 80 % in dark: blur 40 is σ 20) and a
	// contact shadow that keeps the surface's edge read over a fill of its
	// own colour.
	Shadows [2]theme.ShadowLayer
	// Rise is how far the surface travels toward its place from the side of
	// its anchor as it comes in, and Enter how long it takes (T3: 150 ms
	// from 98 % scale and opacity 0; there is no subtree transform here, so
	// the scale's movement at the far edge becomes a rise of that size).
	// Reduce Motion keeps the fade and drops the rise (theme.Resolved.Fade,
	// Enter).
	Rise, Enter float32
}

// OverlayLook is the floating surfaces' recipe.
var OverlayLook = OverlayRecipe{
	Radius: 10, EdgeW: 1, Edge: OfAlpha(theme.Label, 0.10, 0.10),
	Shadows: [2]theme.ShadowLayer{
		{Y: 16, Blur: 20, Spread: -18, Alpha: [2]float32{0.55, 0.80}},
		{Y: 1, Blur: 2, Alpha: [2]float32{0.06, 0.30}},
	},
	Rise: 4, Enter: 0.15,
}

// shadows are the recipe's layers at the kit's scale and mode.
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

// NextOverlaySurface makes the next box a floating surface of fill (an
// opaque colour): OverlayLook's edge, radius and shadows, with radius in
// pixels (0: the recipe's). It returns the flags the box needs; build it
// with them.
//
//	c.NextBG(fill)
//	flags := rexui.NextOverlaySurface(k, 0)
//	c.BoxKeyed(flags|ui.DrawBackground, key, nil)
func NextOverlaySurface(k *base.Kit, radius float32) ui.Flags {
	c, r := k.Context(), k.Theme()
	rc := &OverlayLook
	if radius <= 0 {
		radius = pt(k.Scale(), rc.Radius)
	}
	c.NextRadius(radius)
	flags := ui.Flags(0)
	if a, b := rc.shadows(k); a.Sigma > 0 || b.Sigma > 0 {
		c.NextShadows(a, b)
		flags |= ui.DrawShadow
	}
	if rc.EdgeW > 0 {
		c.NextBorder(rc.Edge.RGBA(r), max(theme.Snap(rc.EdgeW*k.Scale()), 1))
		flags |= ui.DrawBorder
	}
	return flags
}

// OverlayEnter is how far the floating surface keyed key has come in, 0 to
// 1, over OverlayLook.Enter from the frame it is first built (its key not
// asked for in the frame before), and the offset in pixels, toward the side
// of its anchor given by place, that its rise leaves: the surface fades in
// at the returned opacity and starts that far from its place. Under the
// system's Reduce Motion it fades (theme.Resolved.Fade) without the rise; with
// the kit's ReduceMotion (still renders, tests) it is at rest at once.
//
//	t, dx, dy := rexui.OverlayEnter(k, key, ui.PlaceBelow)
//	c.PushOpacity(t)
//	c.NextOffset(dx, dy)
func OverlayEnter(k *base.Kit, key ui.Key, place ui.Placement) (t, dx, dy float32) {
	c, r := k.Context(), k.Theme()
	rc := &OverlayLook
	if k.ReduceMotion { // a still render (the kit's own switch): at rest at once
		return 1, 0, 0
	}
	t = c.AnimateValueFrom(ui.KeyN(key, 0x0E7E), 0, 1, r.Fade(rc.Enter))
	if t > 0.995 {
		return 1, 0, 0
	}
	if r.Enter(rc.Enter) >= theme.Instant { // Reduce Motion: a fade, no travel
		return t, 0, 0
	}
	d := theme.Snap((1 - t) * rc.Rise * k.Scale())
	switch place {
	case ui.PlaceBelow:
		dy = -d
	case ui.PlaceAbove:
		dy = d
	case ui.PlaceRight:
		dx = -d
	case ui.PlaceLeft:
		dx = d
	}
	return t, dx, dy
}
