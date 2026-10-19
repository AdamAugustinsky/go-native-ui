// Package theme holds the design tokens of an interface and the window-level
// drawing a look owns: colours (a light and a dark Palette), sizes in points
// (Metrics), fonts, animation rates (Motion), and the Chrome that paints the
// window background, surfaces, dividers and the titlebar.
//
// A Theme is data plus a Chrome constructor. It is shared by every window; a
// window calls Resolve once (and again when its scale changes) and draws from
// the Resolved result, which holds pixel metrics, font keys and the palette
// for the current light/dark mix. The default look is theme/rex.
//
// # Tokens v2
//
// Next to the Palette and Metrics of phase 5, which keep their values, a
// theme has the tokens of D39, measured from macOS 27.2: colour roles
// (Role, Resolved.Colors) whose System roles are the system's own colours
// read live on macOS, text styles (Resolved.Styles), control sizes
// (Resolved.Sizes, indexed by Size), space, radii, lines, elevations,
// materials, motion that follows Reduce Motion, and the window-level
// geometry of D52 (Resolved.Window). Every table is a plain array indexed by
// a small enum, so a recipe keeps indices, not colours or pointers, and
// reading a token costs an index:
//
//	s := &r.Sizes[size]
//	c.NextBG(r.Colors[theme.Fill2])
//	c.NextRadius(s.Radius)
//	h, pad, font := s.Height, s.PadX, s.Font.Font
//
// # Threading
//
// A Resolved and its Chrome belong to one window and are used from the main
// thread, like the ui.Context they draw into.
package theme

import (
	"gonativeui/text"
	"gonativeui/ui"
)

// Mode is the light or dark appearance.
type Mode uint8

const (
	// Light is the light appearance.
	Light Mode = iota
	// Dark is the dark appearance.
	Dark
)

// T is the mix value of m: 0 for Light, 1 for Dark (see Resolved.Mix).
func (m Mode) T() float32 {
	if m == Dark {
		return 1
	}
	return 0
}

// Theme is a complete look. Copy it and change fields to derive a variant.
type Theme struct {
	// Name identifies the theme.
	Name string
	// Light and Dark are the palettes of the two modes.
	Light, Dark Palette
	// Metrics are the sizes, in points.
	Metrics Metrics
	// Fonts are the font descriptions.
	Fonts Fonts
	// Motion holds the animation rates.
	Motion Motion
	// SystemAccent says the theme takes the system accent colour when the
	// platform supplies one (Resolved.SetAccent); otherwise it ignores it.
	SystemAccent bool

	// Roles are the colour roles of the light (0) and the dark (1) mode,
	// indexed by Role (tokens v2, D39); zero takes Measured. The System roles'
	// values are the fallbacks used until the system's are read, and on
	// systems that have none.
	Roles [2]Colors
	// SystemColors says the System roles take the system's colours when the
	// platform reads them (macOS, through the Follower). The system's
	// Settings apply either way.
	SystemColors bool
	// Text are the text styles in points; zero takes MacText.
	Text [NumTextStyles]TextSpec
	// Sizes are the control sizes in points; zero takes MacSizes.
	Sizes [NumSizes]SizeSpec
	// Space is the space scale in points; zero takes MacSpacing.
	Space Spacing
	// Radii are the shapes' radii in points; zero takes MacRadii.
	Radii Radii
	// Lines are strokes and list lengths in points; zero takes MacLines.
	Lines Lines
	// Elevations are the elevations; zero takes MacElevations.
	Elevations [NumElevations]ElevationSpec
	// Materials are the materials; zero takes MacMaterials.
	Materials [NumMaterials]MaterialSpec
	// Window is the window-level geometry in points; zero takes MacWindow.
	Window WindowSpec
	// NewChrome makes the Chrome of one window; it is called by Resolve.
	NewChrome func() Chrome
}

// Resolved is a Theme for one window: metrics in pixels at the window's scale,
// fonts created in its text system, and the palette for the current mix of
// light and dark.
type Resolved struct {
	// Name is the theme's name.
	Name string
	// Scale is pixels per point.
	Scale float32
	// T is the current mix between Light (0) and Dark (1); see Mix.
	T float32
	// Pal is the palette at T.
	Pal Palette
	// Px holds the theme's metrics in pixels.
	Px Metrics
	// Font holds the fonts, created at Scale.
	Font FontKeys

	// Colors holds the colour roles at T, indexed by Role (D39). On macOS
	// the System roles are the system's colours once the Follower has read
	// them (SetSystem), and the theme's fallbacks until then.
	Colors Colors
	// Styles holds the text styles' fonts, indexed by TextStyle.
	Styles [NumTextStyles]StyleFont
	// Sizes holds the control sizes in pixels, indexed by Size.
	Sizes [NumSizes]SizeSpec
	// Space is the space scale in pixels.
	Space Spacing
	// Radii are the shapes' radii in pixels.
	Radii Radii
	// Lines are strokes and list lengths in pixels.
	Lines Lines
	// Elevations are the elevations, lengths in pixels.
	Elevations [NumElevations]ElevationSpec
	// Window is the window-level geometry in pixels.
	Window WindowSpec
	// Settings are the system's accessibility and scroller settings (the
	// zero value until the system is read, and where it is not).
	Settings Settings
	// Motion holds the animation rates.
	Motion Motion
	// Chrome draws the window-level elements of this window.
	Chrome Chrome

	pal          [2]Palette // light and dark, with the accent applied
	base         [2]Palette // as the theme defines them
	followAccent bool
	met          [6]struct {
		key text.FontKey
		met FontMet
	}

	roles     [2]Colors    // the light and dark roles, with the system's colours and the accent
	rolesBase [2]Colors    // as the theme defines them
	sys       SystemValues // what SetSystem last gave
	acc       uint32       // what SetAccent last gave
	followSys bool
	motion    Motion // the theme's, before Reduce Motion
	materials [NumMaterials]MaterialSpec
}

// Resolve makes the theme ready for a window at scale pixels per point,
// starting at mode. It creates the fonts, so call it on the thread that owns
// ts, when the window is created and again when its scale changes; it is the
// only place a theme creates fonts and allocates.
func (t *Theme) Resolve(ts TextSystem, scale float32, mode Mode) Resolved {
	r := Resolved{
		Name: t.Name, Scale: scale, Px: t.Metrics.scaled(scale), Motion: t.Motion,
		base: [2]Palette{t.Light, t.Dark}, pal: [2]Palette{t.Light, t.Dark}, followAccent: t.SystemAccent,
	}
	if t.NewChrome != nil {
		r.Chrome = t.NewChrome()
	}
	mk := func(d text.FontDesc) text.FontKey {
		d.Scale = scale
		return ts.Font(d)
	}
	f := &t.Fonts
	r.Font = FontKeys{UI: mk(f.UI), UISemi: mk(f.UISemi), Small: mk(f.Small), Mono: mk(f.Mono), MonoBold: mk(f.MonoBold), Symbols: mk(f.Symbols)}
	for i, k := range [...]text.FontKey{r.Font.UI, r.Font.UISemi, r.Font.Small, r.Font.Mono, r.Font.MonoBold, r.Font.Symbols} {
		r.met[i].key, r.met[i].met = k, LineBox(ts.Metrics(k))
	}
	t.resolveTokens(&r, ts, scale)
	r.Mix(mode.T())
	return r
}

// resolveTokens fills the tokens v2 of r: the zero groups of t take the
// macOS ones, lengths are scaled to pixels and the fonts created.
func (t *Theme) resolveTokens(r *Resolved, ts TextSystem, scale float32) {
	r.rolesBase, r.followSys, r.motion = t.Roles, t.SystemColors, t.Motion
	if r.rolesBase[0] == (Colors{}) && r.rolesBase[1] == (Colors{}) {
		r.rolesBase = Measured
	}
	text := orDefault(t.Text, MacText)
	for i := range r.Styles {
		r.Styles[i] = styleFont(ts, text[i], scale)
	}
	sizes := orDefault(t.Sizes, MacSizes)
	for i := range r.Sizes {
		r.Sizes[i] = sizes[i].scaled(scale)
		r.Sizes[i].Font = styleFont(ts, TextSpec{Size: sizes[i].FontSize, Line: sizes[i].FontLine, Weight: WeightRegular}, scale)
	}
	r.Space = orDefault(t.Space, MacSpacing).scaled(scale)
	r.Radii = orDefault(t.Radii, MacRadii).scaled(scale)
	r.Lines = orDefault(t.Lines, MacLines).scaled(scale)
	for i, e := range orDefault(t.Elevations, MacElevations) {
		r.Elevations[i] = e.scaled(scale)
	}
	r.materials = orDefault(t.Materials, MacMaterials)
	r.Window = orDefault(t.Window, MacWindow).scaled(scale)
	r.applySystem()
}

// orDefault is v, or def when v is the zero value.
func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// Mix sets the palette to the mix of light (t = 0) and dark (t = 1) and
// records t. Call it each frame of a mode switch with the animated t; it
// allocates nothing.
func (r *Resolved) Mix(t float32) {
	t = Clamp01(t)
	r.T = t
	switch t {
	case 0:
		r.Pal, r.Colors = r.pal[0], r.roles[0]
	case 1:
		r.Pal, r.Colors = r.pal[1], r.roles[1]
	default:
		r.Pal = MixPalettes(&r.pal[0], &r.pal[1], t)
		MixColors(&r.Colors, &r.roles[0], &r.roles[1], t)
	}
}

// Dark reports whether the current mix is closer to dark.
func (r *Resolved) Dark() bool { return r.T >= 0.5 }

// SetAccent applies the system accent colour (0xRRGGBBAA) to both palettes if
// the theme follows it; 0 returns to the theme's own accent. It does nothing
// for a theme that ignores the system accent.
//
// The roles follow too: Accent becomes the colour, and FocusRing (keeping its
// alpha), Selection and SidebarSel take it, unless it is the accent the
// system's colours were read with (they are then exactly the system's).
func (r *Resolved) SetAccent(accent uint32) {
	if !r.followAccent {
		return
	}
	r.pal = r.base
	if accent != 0 {
		r.pal[0] = r.base[0].WithAccent(accent)
		r.pal[1] = r.base[1].WithAccent(accent)
	}
	r.acc = accent
	r.applyRoles()
	r.Mix(r.T)
}

// SetSystem applies what the system says (the Follower calls it when that
// changes): the System roles take its colours if the theme follows them
// (Theme.SystemColors), and Settings, Motion and the materials its settings.
// SystemValues that are not Known returns to the theme's values. It allocates
// nothing.
func (r *Resolved) SetSystem(s *SystemValues) {
	r.sys = *s
	r.applySystem()
	r.Mix(r.T)
}

// SystemValues are what SetSystem last gave.
func (r *Resolved) SystemValues() *SystemValues { return &r.sys }

// applySystem applies r.sys and r.acc to the roles, settings and motion.
func (r *Resolved) applySystem() {
	r.Settings = Settings{}
	if r.sys.Known {
		r.Settings = r.sys.Settings
	}
	r.Motion = r.motion
	if r.Settings.ReduceMotion {
		r.Motion = r.motion.Reduced()
	}
	r.applyRoles()
}

// applyRoles computes the light and dark roles from the theme's, the
// system's colours and the accent.
func (r *Resolved) applyRoles() {
	live := r.followSys && r.sys.Known
	for m := range r.roles {
		c := r.rolesBase[m]
		if live {
			for i, v := range r.sys.Colors[m] {
				if v != 0 {
					c[i] = v
				}
			}
		}
		if a := r.acc | 0xFF; r.followAccent && r.acc != 0 && a != c[Accent]|0xFF {
			c[Accent] = a
			c[FocusRing] = a&^0xFF | c[FocusRing]&0xFF
			c[Selection] = a
		}
		derive(&c, m, r.rolesBase[m][SidebarSel])
		r.roles[m] = c
	}
}

// Material is how material m shows: Native is false under Reduce
// Transparency and on systems without the material.
func (r *Resolved) Material(m Material) MaterialSpec {
	s := r.materials[m]
	if s.Native && (r.Settings.ReduceTransparency || !nativeMaterials) {
		s.Native = false
	}
	return s
}

// Role returns the colour of role c at the current mix: r.Colors[c].
func (r *Resolved) Role(c Role) uint32 { return r.Colors[c] }

// Met returns the line box of font f, one of r.Font; zero for any other.
func (r *Resolved) Met(f text.FontKey) FontMet {
	for i := range r.met {
		if r.met[i].key == f {
			return r.met[i].met
		}
	}
	return FontMet{}
}

// UITheme returns the hover and press overlay colours for ui.Context.Theme.
func (r *Resolved) UITheme() ui.Theme {
	return ui.Theme{HotOverlay: r.Pal.HoverOverlay, ActiveOverlay: r.Pal.PressOverlay}
}
