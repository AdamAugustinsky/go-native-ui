package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The window chrome's glass: the sidebar and inspector panes, the toolbar's
// capsules and the titlebar band sit on the system's materials (macOS 27:
// Liquid Glass, DECISIONS D52). Where a live material shows behind a region
// of the window's layer the chrome leaves the region clear; where it does not
// (other systems, Reduce Transparency, and until the materials are hosted)
// the chrome draws the material's measured fallback colour.
//
// Every piece of chrome asks for its material here, in one place, so that the
// native materials (N3: ui material regions and app.Config.Materials) take
// over from the drawn fallbacks by changing GlassFill alone.

// GlassArea is the part of the window chrome a material is for.
type GlassArea uint8

const (
	// GlassSidebar is a sidebar or inspector pane: flush, full height.
	GlassSidebar GlassArea = iota
	// GlassToolbarCapsule is a toolbar item's capsule (radius h/2).
	GlassToolbarCapsule
	// GlassTitlebar is the toolbar band: shown only while content is under
	// the toolbar or the window is inactive (theme.ScrollEdgeVisible).
	GlassTitlebar
)

// glassMaterial is the theme material of each area.
var glassMaterial = [...]theme.Material{
	GlassSidebar:        theme.MatSidebar,
	GlassToolbarCapsule: theme.MatToolbar,
	GlassTitlebar:       theme.MatScrollEdge,
}

// GlassFill is the background the box being built for area takes: the
// material's fallback in an active or inactive window (its opaque Solid
// under Reduce Transparency, Resolved.Material). It is the one place the
// chrome meets the materials: with hosted materials it declares the region
// and returns the clear colour where the system's material shows.
func GlassFill(k *base.Kit, area GlassArea) uint32 {
	r := k.Theme()
	m := r.Material(glassMaterial[area])
	if k.Inactive() {
		return r.Colors[m.FillInactive]
	}
	return r.Colors[m.Fill]
}

// glassBox builds a floating box of area's material at (x, y, w, h) in the
// current parent with the radius and the key, and returns it (not pushed).
func glassBox(k *base.Kit, area GlassArea, key ui.Key, x, y, w, h, radius float32, flags ui.Flags) ui.ID {
	c := k.Context()
	theme.At(c, x, y, w, h)
	c.NextBG(GlassFill(k, area))
	c.NextRadius(radius)
	return c.BoxKeyed(ui.DrawBackground|base.Floating|flags, key, nil)
}
