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
	// Motion holds the animation rates.
	Motion Motion
	// Chrome draws the window-level elements of this window.
	Chrome Chrome
	// Backdrop is set by the app when a system material shows behind the
	// window's content (app.Config.Backdrop, the window got it): Chrome then
	// leaves the background unpainted, and surfaces that blend with the
	// background are translucent over the material instead of mixed with a
	// painted one. Experimental.
	Backdrop bool

	pal          [2]Palette // light and dark, with the accent applied
	base         [2]Palette // as the theme defines them
	followAccent bool
	met          [6]struct {
		key text.FontKey
		met FontMet
	}
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
	r.Mix(mode.T())
	return r
}

// Mix sets the palette to the mix of light (t = 0) and dark (t = 1) and
// records t. Call it each frame of a mode switch with the animated t; it
// allocates nothing.
func (r *Resolved) Mix(t float32) {
	t = Clamp01(t)
	r.T = t
	switch t {
	case 0:
		r.Pal = r.pal[0]
	case 1:
		r.Pal = r.pal[1]
	default:
		r.Pal = MixPalettes(&r.pal[0], &r.pal[1], t)
	}
}

// Dark reports whether the current mix is closer to dark.
func (r *Resolved) Dark() bool { return r.T >= 0.5 }

// SetAccent applies the system accent colour (0xRRGGBBAA) to both palettes if
// the theme follows it; 0 returns to the theme's own accent. It does nothing
// for a theme that ignores the system accent.
func (r *Resolved) SetAccent(accent uint32) {
	if !r.followAccent {
		return
	}
	r.pal = r.base
	if accent != 0 {
		r.pal[0] = r.base[0].WithAccent(accent)
		r.pal[1] = r.base[1].WithAccent(accent)
	}
	r.Mix(r.T)
}

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
