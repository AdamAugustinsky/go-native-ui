package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The window chrome's glass: the sidebar and inspector panes, the toolbar's
// capsules and the titlebar band sit on the system's materials (macOS 27:
// Liquid Glass, DECISIONS D52). In a window that hosts the materials
// (base.Kit.Materials, from app.Config.Materials) the chrome marks those
// boxes as material regions (ui.Context.Material, N3) and leaves them clear,
// so that the system's glass shows behind the window's layer; elsewhere (other
// systems, offscreen renders, Reduce Transparency) it draws the material's
// measured fallback colour. Every piece of chrome asks for its material here.

// GlassArea is the part of the window chrome a material is for.
type GlassArea uint8

const (
	// GlassSidebar is a sidebar pane: flush, full height.
	GlassSidebar GlassArea = iota
	// GlassToolbarCapsule is a toolbar item's capsule (radius h/2).
	GlassToolbarCapsule
	// GlassTitlebar is the toolbar band's scroll-edge bar over the content
	// pane. With hosted materials it is an opaque bar below the capsules (the
	// layer cannot show them over its own pixels), so the band over the
	// content is always one: the bar while content is under the toolbar or
	// the window is inactive (theme.ScrollEdgeVisible), the content's
	// background otherwise (ToolbarOpts.BandFill).
	GlassTitlebar
	// GlassInspector is an inspector pane: flush, full height, the
	// sidebar's material.
	GlassInspector
)

// glassMaterial is the theme material of each area, and glassArea its ui
// material area.
var (
	glassMaterial = [...]theme.Material{
		GlassSidebar:        theme.MatSidebar,
		GlassToolbarCapsule: theme.MatToolbar,
		GlassTitlebar:       theme.MatScrollEdge,
		GlassInspector:      theme.MatSidebar,
	}
	glassArea = [...]ui.MaterialArea{
		GlassSidebar:        ui.AreaSidebar,
		GlassToolbarCapsule: ui.AreaToolbarCapsule,
		GlassTitlebar:       ui.AreaTitlebar,
		GlassInspector:      ui.AreaInspector,
	}
)

// GlassNative reports whether area shows the system's material in this
// window: the window hosts the materials and the theme's material is native
// there (not under Reduce Transparency). The titlebar band is always a
// region then, an opaque one.
func GlassNative(k *base.Kit, area GlassArea) bool {
	if !k.Materials || int(area) >= len(glassMaterial) {
		return false
	}
	return area == GlassTitlebar || k.Theme().Material(glassMaterial[area]).Native
}

// glassFallback is the colour the material of area draws as when the system's
// does not show: its fallback in an active or inactive window (its opaque
// Solid under Reduce Transparency, Resolved.Material).
func glassFallback(k *base.Kit, area GlassArea) uint32 {
	r := k.Theme()
	m := r.Material(glassMaterial[area])
	if k.Inactive() {
		return r.Colors[m.FillInactive]
	}
	return r.Colors[m.Fill]
}

// GlassFill is the background the box being built for area takes: clear where
// the system's material shows (GlassNative), else the fallback colour. Mark
// the box with GlassRegion once it is built.
func GlassFill(k *base.Kit, area GlassArea) uint32 {
	if GlassNative(k, area) {
		return 0
	}
	return glassFallback(k, area)
}

// GlassRegion marks the box id, just built with GlassFill's background, as
// area's material region where the system's material shows; elsewhere it does
// nothing. solid is the opaque colour the region shows under Reduce
// Transparency, and always for the titlebar band (0: the fallback colour).
func GlassRegion(k *base.Kit, id ui.ID, area GlassArea, solid uint32) {
	if !GlassNative(k, area) {
		return
	}
	if solid == 0 {
		solid = glassFallback(k, area)
	}
	k.Context().Material(id, glassArea[area], uint8(glassMaterial[area]), solid|0xFF)
}
