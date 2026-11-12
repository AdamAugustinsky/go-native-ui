package theme

import (
	"runtime"

	"gonativeui/ui"
)

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
	// Spread grows (negative: shrinks) the shadow's rectangle before the
	// blur, in points (CSS box-shadow's spread).
	Spread float32
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
	ElevPane:   {Fill: Pane, Edge: 0.5, EdgeRole: PaneHairline, Shadows: [2]ShadowLayer{{1, 1.5, [2]float32{0.07, 0.40}, 0}, {3, 5, [2]float32{0.05, 0.30}, 0}}},
	ElevRaised: {Fill: Raised, Edge: 0.5, EdgeRole: Separator, Shadows: [2]ShadowLayer{{4, 12, [2]float32{0.20, 0.45}, 0}}},
	ElevModal:  {Fill: Raised, Edge: 0.5, EdgeRole: Separator, Shadows: [2]ShadowLayer{{8, 24, [2]float32{0.25, 0.55}, 0}}, Scrim: runtime.GOOS != "darwin"},
}

// Lift is how high a floating surface stands over the window in the default
// look (D108): one named step of a short scale that every menu, popover,
// tooltip, picker, dialog and toast takes, so that siblings are lifted
// alike. Under strict AppKit (Resolved.StrictAppKit) a surface draws its
// recipe's measured or historical shadows instead.
type Lift uint8

const (
	// LiftControl is a raised control: a segmented thumb, a chip, a pill
	// over content.
	LiftControl Lift = iota
	// LiftMenu is a menu, a popover, a picker, a select's list, the
	// completion list and a tooltip.
	LiftMenu
	// LiftDialog is a dialog, a sheet and the command palette.
	LiftDialog
	// LiftToast is a toast and a floating banner: lower than a menu, as it
	// sits by the content it reports on, and with no scrim.
	LiftToast

	// NumLifts is the number of lifts.
	NumLifts
)

// LiftSpec is one lift: a contact shadow, tight under the surface's edge,
// that draws its outline where the backdrop is the surface's own colour, an
// ambient one, long and soft, that says how high it stands, and its edge, a
// hairline of the label colour (a faint dark line in light mode, a faint
// light rim in dark). Lengths are points; ShadowLayer's alphas are light and
// dark.
type LiftSpec struct {
	// Contact and Ambient are the two shadows.
	Contact, Ambient ShadowLayer
	// Edge is the opacity of the label colour (the Label role's RGB) of the
	// edge, light and dark, and EdgeW its width in points (at least a
	// pixel).
	Edge  [2]float32
	EdgeW float32
}

// Lifts are the lifts of the default look, tuned by eye over a light, a dark
// and a backdrop of the surface's own colour (rexui's surface shots, D108).
// In dark mode a shadow hardly reads against a dark window, so its contact
// layer is darker and the edge is a light rim rather than a longer blur.
// Apps may change them before the first frame.
var Lifts = [NumLifts]LiftSpec{
	LiftControl: {
		Contact: ShadowLayer{Y: 0.5, Blur: 0.75, Alpha: [2]float32{0.14, 0.45}},
		Ambient: ShadowLayer{Y: 1, Blur: 2, Alpha: [2]float32{0.06, 0.25}},
		Edge:    [2]float32{0.06, 0.06}, EdgeW: 0.5,
	},
	LiftMenu: {
		Contact: ShadowLayer{Y: 1, Blur: 1.5, Alpha: [2]float32{0.12, 0.45}},
		Ambient: ShadowLayer{Y: 8, Blur: 12, Spread: -4, Alpha: [2]float32{0.16, 0.50}},
		Edge:    [2]float32{0.08, 0.07}, EdgeW: 1,
	},
	LiftDialog: {
		Contact: ShadowLayer{Y: 1, Blur: 1.5, Alpha: [2]float32{0.12, 0.45}},
		Ambient: ShadowLayer{Y: 16, Blur: 24, Spread: -8, Alpha: [2]float32{0.22, 0.60}},
		Edge:    [2]float32{0.08, 0.07}, EdgeW: 1,
	},
	LiftToast: {
		Contact: ShadowLayer{Y: 1, Blur: 1.5, Alpha: [2]float32{0.12, 0.45}},
		Ambient: ShadowLayer{Y: 4, Blur: 10, Spread: -2, Alpha: [2]float32{0.14, 0.45}},
		Edge:    [2]float32{0.08, 0.07}, EdgeW: 1,
	},
}

// Shadows are the lift's contact and ambient shadows at r's scale and mode,
// cast in the Shadow role's RGB.
func (l *LiftSpec) Shadows(r *Resolved) (contact, ambient ui.Shadow) {
	return l.Contact.shadow(r), l.Ambient.shadow(r)
}

// EdgeColor is the lift's edge at r's mode: the Label role's RGB at Edge's
// opacity.
func (l *LiftSpec) EdgeColor(r *Resolved) uint32 {
	return ColA(r.Colors[Label]>>8, Lerp(l.Edge[0], l.Edge[1], Clamp01(r.T)))
}

// EdgeWidth is the edge's width in pixels at r's scale: at least one pixel,
// on whole pixels; 0 for none.
func (l *LiftSpec) EdgeWidth(r *Resolved) float32 {
	if l.EdgeW <= 0 {
		return 0
	}
	return max(Snap(l.EdgeW*r.Scale), 1)
}

// shadow is the layer in pixels at r's scale, in its colour at r's mode; the
// zero Shadow for a layer with no blur.
func (l ShadowLayer) shadow(r *Resolved) ui.Shadow {
	if l.Blur <= 0 {
		return ui.Shadow{}
	}
	s := r.Scale
	return ui.Shadow{OffY: l.Y * s, Sigma: l.Blur * s, Spread: l.Spread * s, Color: l.Color(r.Colors[Shadow], r.T)}
}

func (e ElevationSpec) scaled(k float32) ElevationSpec {
	e.Edge *= k
	for i := range e.Shadows {
		e.Shadows[i].Y *= k
		e.Shadows[i].Blur *= k
		e.Shadows[i].Spread *= k
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
