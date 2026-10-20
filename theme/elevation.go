package theme

import "runtime"

// Elevation is how far a surface floats above the content (D39).
type Elevation uint8

const (
	// ElevFlat is content: no shadow.
	ElevFlat Elevation = iota
	// ElevPane is a Rex pane or a card: two soft layers and a hairline.
	ElevPane
	// ElevRaised is a drawn popover, a menu drawn on other systems, a tooltip.
	ElevRaised
	// ElevModal is a sheet or a drawn dialog.
	ElevModal

	// NumElevations is the number of elevations.
	NumElevations
)

// ShadowLayer is one drop shadow: offset down and Gaussian sigma in points
// (pixels once resolved), and its final opacity in light and dark. The
// shadow is cast in the Shadow role's RGB at that opacity; the role's own
// alpha is not applied on top (Color).
type ShadowLayer struct {
	// Y is the downward offset and Blur the sigma.
	Y, Blur float32
	// Alpha is the shadow's final opacity in light (0) and dark (1).
	Alpha [2]float32
}

// Color is the layer's colour at the light/dark mix t: the RGB of shadow
// (Resolved.Colors[Shadow]) at the layer's opacity mixed by t.
func (l ShadowLayer) Color(shadow uint32, t float32) uint32 {
	return ColA(shadow>>8, Lerp(l.Alpha[0], l.Alpha[1], Clamp01(t)))
}

// ElevationSpec is one elevation.
type ElevationSpec struct {
	// Fill is the role of the surface's fill.
	Fill Role
	// Edge is the width of the hairline edge round the surface in points (0:
	// none), drawn in EdgeRole.
	Edge     float32
	EdgeRole Role
	// Shadows are the shadow layers; a zero layer is none.
	Shadows [2]ShadowLayer
	// Scrim says the surface dims what lies under it. macOS sheets do not.
	Scrim bool
}

// MacElevations are the elevations: Pane is Rex's two layers (tight 1/1.5,
// ambient 3/5, at the focused pane's alphas); Raised and Modal are designed
// (y 4 / σ 12 at 20 %, y 8 / σ 24), without a scrim on macOS.
var MacElevations = [NumElevations]ElevationSpec{
	ElevFlat:   {Fill: Pane},
	ElevPane:   {Fill: Pane, Edge: 0.5, EdgeRole: PaneHairline, Shadows: [2]ShadowLayer{{1, 1.5, [2]float32{0.07, 0.40}}, {3, 5, [2]float32{0.05, 0.30}}}},
	ElevRaised: {Fill: Raised, Edge: 0.5, EdgeRole: Separator, Shadows: [2]ShadowLayer{{4, 12, [2]float32{0.20, 0.45}}}},
	ElevModal:  {Fill: Raised, Edge: 0.5, EdgeRole: Separator, Shadows: [2]ShadowLayer{{8, 24, [2]float32{0.25, 0.55}}}, Scrim: runtime.GOOS != "darwin"},
}

func (e ElevationSpec) scaled(k float32) ElevationSpec {
	e.Edge *= k
	for i := range e.Shadows {
		e.Shadows[i].Y *= k
		e.Shadows[i].Blur *= k
	}
	return e
}

// Material is what a region of the window is made of (D39, D52): a flat
// colour, Rex's field, or a live system material behind a transparent region
// of the window's layer.
type Material uint8

const (
	// MatWindow is the window's flat background.
	MatWindow Material = iota
	// MatField is Rex's background field (drawn).
	MatField
	// MatGlass is Rex's translucent glass of unfocused panes (drawn).
	MatGlass
	// MatSidebar is the sidebar and the inspector: Liquid Glass on macOS 27,
	// flush and full height.
	MatSidebar
	// MatToolbar is a toolbar item's glass capsule.
	MatToolbar
	// MatMenu is a menu's material.
	MatMenu
	// MatPopover is a popover's material.
	MatPopover
	// MatHUD is a heads-up window's dark material.
	MatHUD
	// MatScrollEdge is the toolbar's scroll-edge bar (a blur of the app's
	// own content, which it can draw).
	MatScrollEdge

	// NumMaterials is the number of materials.
	NumMaterials
)

// nativeMaterials says the system has the live materials (macOS).
const nativeMaterials = runtime.GOOS == "darwin"

// MaterialSpec is how a material shows.
type MaterialSpec struct {
	// Native says a system material shows (NSGlassEffectView or
	// NSVisualEffectView behind the region); the app leaves the region clear.
	// False on other systems, for drawn materials, and under Reduce
	// Transparency.
	Native bool
	// Fill and FillInactive are the roles of the fallback, in an active and
	// an inactive window: what the app draws when Native is false. A drawn
	// material may be translucent (Rex's glass); under Reduce Transparency
	// Resolved.Material gives Solid instead of a translucent fill.
	Fill, FillInactive Role
	// Solid is the opaque fill that replaces a translucent Fill or
	// FillInactive under Reduce Transparency.
	Solid Role
}

// MacMaterials are the materials as macOS 27.2 shows them, with the measured
// fallbacks (docs/design/liquid-glass-reference.md §6).
var MacMaterials = [NumMaterials]MaterialSpec{
	MatWindow:     {Fill: Window, FillInactive: Window},
	MatField:      {Fill: Field, FillInactive: Field},
	MatGlass:      {Fill: GlassTint, FillInactive: GlassTint, Solid: Pane},
	MatSidebar:    {Native: true, Fill: SidebarFill, FillInactive: SidebarFillInactive},
	MatToolbar:    {Native: true, Fill: CapsuleFill, FillInactive: CapsuleFillInactive, Solid: Raised},
	MatMenu:       {Native: true, Fill: Raised, FillInactive: Raised},
	MatPopover:    {Native: true, Fill: Raised, FillInactive: Raised},
	MatHUD:        {Native: true, Fill: Raised, FillInactive: Raised},
	MatScrollEdge: {Fill: ScrollEdgeBar, FillInactive: ScrollEdgeBarInactive},
}
