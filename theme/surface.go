package theme

// Surface profiles: what each kind of surface is made of and how it is
// shaped, resolved for the window's appearance, its active state, the
// system's Reduce Transparency and Increase Contrast, the backdrop and the
// app's density. They gather what the material, elevation, radius and colour
// tokens already say into one value per surface, so that a recipe (a pane's
// glass, the sidebar, a toolbar capsule) reads one place instead of
// repeating the fallbacks. Resolved.Surface reads them; they are computed
// again only after something they depend on changed, not every frame.

// SurfaceRole names a kind of surface. Roles are append-only.
type SurfaceRole uint8

const (
	// SurfacePane is a workspace pane: Rex's glass, opaque while focused
	// (theme.Chrome's surfaces).
	SurfacePane SurfaceRole = iota
	// SurfaceSidebar is a sidebar: flush, full height, the system's glass.
	SurfaceSidebar
	// SurfaceInspector is an inspector: the sidebar's material at the other
	// edge.
	SurfaceInspector
	// SurfaceToolbarCapsule is a toolbar item's glass capsule.
	SurfaceToolbarCapsule
	// SurfaceTitlebar is the toolbar band's scroll-edge bar.
	SurfaceTitlebar
	// SurfacePopover is a drawn popover.
	SurfacePopover

	// NumSurfaceRoles is the number of surface roles.
	NumSurfaceRoles
)

// Density is how tightly an app packs its surfaces.
type Density uint8

const (
	// DensityComfortable is the default: the theme's insets, gutters and
	// radii.
	DensityComfortable Density = iota
	// DensityCompact packs panes closer, with smaller corners and title
	// rows, for apps that show many panes (a terminal's splits).
	DensityCompact
)

// FillKind is how a surface's background is made.
type FillKind uint8

const (
	// FillOpaque is an opaque colour the app draws.
	FillOpaque FillKind = iota
	// FillDrawn is a translucent colour the app draws over its own
	// background (Rex's glass over the field, or over the window's
	// backdrop).
	FillDrawn
	// FillNative is the system's material: the app leaves the region clear
	// (ui.Context.Material) and draws only the tint, if any.
	FillNative
)

// SurfaceSpec is a surface role as a theme defines it, in points.
type SurfaceSpec struct {
	// Material is what the surface is made of.
	Material Material
	// Elevation is how far it floats; the profile's shadows are its.
	Elevation Elevation
	// Radius is the corner radius at the comfortable density; 0 takes the
	// role's token (SurfacePane: Metrics.RadiusLarge; a capsule: half the
	// toolbar's capsule height; a popover: Radii.Popover; flush surfaces
	// none). Capsule makes it half the toolbar capsule's height.
	Radius  float32
	Capsule bool
	// Flush says the surface touches the window's edges (no corners, no
	// stroke but the one it shares with its neighbour).
	Flush bool
	// Stroke is the role of the hairline round the surface, StrokeW its
	// width (0: none, except under Increase Contrast).
	Stroke  Role
	StrokeW float32
}

// MacSurfaces are the surface roles as macOS 27.2 and Rex make them.
var MacSurfaces = [NumSurfaceRoles]SurfaceSpec{
	SurfacePane:           {Material: MatGlass, Elevation: ElevPane, Stroke: PaneHairline, StrokeW: 0.5},
	SurfaceSidebar:        {Material: MatSidebar, Flush: true},
	SurfaceInspector:      {Material: MatSidebar, Flush: true},
	SurfaceToolbarCapsule: {Material: MatToolbar, Capsule: true, Stroke: CapsuleRim, StrokeW: 0.5},
	SurfaceTitlebar:       {Material: MatScrollEdge, Flush: true},
	SurfacePopover:        {Material: MatPopover, Elevation: ElevRaised, Stroke: Separator, StrokeW: 0.5},
}

// SurfaceProfile is a surface role resolved for one window state, lengths in
// pixels and colours at the current light/dark mix.
type SurfaceProfile struct {
	// Material is the surface's material, and Fill how it shows now: the
	// system's (FillNative), a translucent colour the app draws (FillDrawn)
	// or an opaque one (FillOpaque: Reduce Transparency, other systems).
	Material Material
	Fill     FillKind
	// Color is what the app draws: the opaque or translucent fill, or for
	// FillNative the fallback a region shows where the material does not
	// (offscreen renders).
	Color uint32
	// Opacity is Color's alpha, 0..1.
	Opacity float32
	// Solid is the opaque colour that stands for the surface wherever
	// nothing may show through: a native region's colour under Reduce
	// Transparency (ui.Context.Material's solid).
	Solid uint32
	// Tint is laid over the system's material so that text on it keeps its
	// contrast (the field's scrim over Rex's backdrop); 0 is none.
	Tint uint32
	// Radius is the corner radius; Continuous says the corners are macOS's
	// squircle.
	Radius     float32
	Continuous bool
	// Stroke and StrokeW are the hairline round the surface; StrokeW 0 is
	// none.
	Stroke  uint32
	StrokeW float32
	// Elevation is the surface's elevation, Shadows its layers' colours
	// (offsets and blur in Resolved.Elevations).
	Elevation Elevation
	Shadows   [2]uint32
	// Inset is the room between the window's edge and a surface of this role,
	// Gutter between two neighbours, Header the height of its title row.
	Inset, Gutter, Header float32
}

// surfaceKey is what the profiles were last resolved for: everything they
// read that may change while the Resolved lives. Mix counts the changes of
// the colours and the settings (SetAccent and SetSystem end in it).
type surfaceKey struct {
	mix      uint32
	backdrop bool
	density  Density
}

// SetDensity sets the density the surface profiles are resolved for. Call
// it each frame with the app's setting, like SetBackdrop; a change makes the
// next Surface resolve the profiles again.
func (r *Resolved) SetDensity(d Density) { r.density = d }

// Density is the density set with SetDensity.
func (r *Resolved) Density() Density { return r.density }

// Surface is the profile of role in an active (inactive false) or an
// inactive window, for this frame's appearance, settings, backdrop and
// density. The profiles are resolved together the first time one is asked
// for after any of those changed, and read from the cache otherwise: a
// comparison and an index. A change made to Colors directly, not through
// Mix, SetAccent or SetSystem, is seen after InvalidateSurfaces.
func (r *Resolved) Surface(role SurfaceRole, inactive bool) SurfaceProfile {
	k := surfaceKey{mix: r.mixes, backdrop: r.Backdrop, density: r.density}
	if !r.surfOK || k != r.surfKey {
		r.surfKey, r.surfOK = k, true
		for i := range r.surfaces {
			r.surfaces[i][0] = r.resolveSurface(SurfaceRole(i), false)
			r.surfaces[i][1] = r.resolveSurface(SurfaceRole(i), true)
		}
	}
	w := 0
	if inactive {
		w = 1
	}
	return r.surfaces[role][w]
}

// InvalidateSurfaces makes the next Surface resolve the profiles again.
func (r *Resolved) InvalidateSurfaces() { r.surfOK = false }

// resolveSurface computes one profile.
func (r *Resolved) resolveSurface(role SurfaceRole, inactive bool) SurfaceProfile {
	s := r.surfSpecs[role]
	m := r.Material(s.Material) // Reduce Transparency and the system's materials applied
	fill := m.Fill
	if inactive {
		fill = m.FillInactive
	}
	p := SurfaceProfile{Material: s.Material, Color: r.Colors[fill], Elevation: s.Elevation, Continuous: r.Continuous}
	p.Solid = p.Color | 0xFF
	if m.Solid != 0 {
		p.Solid = r.Colors[m.Solid] | 0xFF
	}
	switch {
	case m.Native:
		p.Fill = FillNative
	case p.Color&0xFF != 0xFF:
		p.Fill = FillDrawn
	default:
		p.Fill = FillOpaque
	}
	// Rex's glass over the window's backdrop is a translucent fill over the
	// system's material: the drawn glass's colour is the tint that keeps
	// text on it readable.
	if s.Material == MatGlass && r.Backdrop {
		p.Tint = p.Color
	}
	p.Opacity = float32(p.Color&0xFF) / 255

	compact := r.density == DensityCompact
	switch {
	case s.Flush:
	case s.Capsule:
		p.Radius = r.Window.Toolbars[ToolbarCompact].CapsuleH / 2
		if !compact {
			p.Radius = r.Window.Toolbars[ToolbarUnified].CapsuleH / 2
		}
	case s.Radius != 0:
		p.Radius = s.Radius * r.Scale
	case role == SurfacePane:
		p.Radius = r.Px.RadiusLarge
	default:
		p.Radius = r.Radii.Popover
	}
	if compact && !s.Capsule {
		// Smaller corners on panes packed closer: no larger than an inset
		// selection's, the next radius in from the window's.
		p.Radius = min(p.Radius, r.Radii.Selection)
	}

	if s.StrokeW > 0 {
		p.Stroke, p.StrokeW = r.Colors[s.Stroke], max(r.Lines.Hairline, s.StrokeW*r.Scale)
	}
	if r.Settings.IncreaseContrast && !s.Flush {
		// AppKit outlines what floats under Increase Contrast: a hairline at
		// least, in the secondary label's colour, which holds AppKit's
		// contrast against the window in both modes (D39).
		p.Stroke, p.StrokeW = r.Colors[Label2], max(p.StrokeW, r.Lines.Hairline)
	}

	e := &r.Elevations[s.Elevation]
	for i, l := range e.Shadows {
		if l.Blur > 0 || l.Y > 0 {
			p.Shadows[i] = l.Color(r.Colors[Shadow], r.T)
		}
	}

	p.Inset, p.Gutter, p.Header = r.Px.Inset, r.Px.Gutter, r.Window.Toolbars[ToolbarCompact].Height
	if compact {
		p.Inset, p.Gutter, p.Header = r.Space.S4, r.Space.S4, r.Lines.HeaderH
	}
	return p
}
