// Package a3lab is A3 Lab's web look: the tokens of the A3 web app's
// stylesheet (a3-app ui/src/styles.css, :root and its dark block), for the
// apps that draw natively what the web app draws in a browser. Paper and
// ink are the brand's neutrals, rust its one colour: the accent, the links,
// the focus ring and the attention a row asks for.
//
// The web app is the reference (D98): a flat sidebar on paper with a
// hairline at its edge, the main column a step lighter, white cards, and
// greys that are the ink at an opacity rather than greys of their own.
// Apply puts the tokens into a theme's roles; rexui.FlatPane draws the flat
// regions.
package a3lab

import "gonativeui/theme"

// The brand's colours, 0xRRGGBB: paper, ink, rust and carbon.
const (
	Paper  = 0xEEEEEF
	Ink    = 0x0C0C12
	Rust   = 0xAD5424
	Carbon = 0x626276
)

// Tokens are one mode's colours, 0xRRGGBB, with the text's and the lines'
// opacities over Fg (the stylesheet's rgba(ink, a) and rgba(paper, a)).
type Tokens struct {
	// Bg is the main column's colour, Sidebar the sidebar's, Card a card's
	// (the composer, a menu, a dialog), CodeBg a code block's.
	Bg, Sidebar, Card, CodeBg uint32
	// Fg is the text's colour; Fg70, Fg50 and Fg35 its secondary, muted
	// and faint opacities.
	Fg                 uint32
	Fg70, Fg50, Fg35   float32
	Line, LineStrong   float32 // a hairline's and a control's outline
	Hover, Active      float32 // a row under the pointer, a chosen row
	RustText           uint32  // rust that reads as text (links)
	RustSoft, RustLine float32 // rust's wash (attention) and outline (focus)
	OK, Danger         uint32
	DangerSoft         float32
}

// Light and Dark are the stylesheet's two modes.
var (
	Light = Tokens{
		Bg: 0xF7F7F8, Sidebar: Paper, Card: 0xFFFFFF, CodeBg: 0xF1F1F3,
		Fg: Ink, Fg70: 0.72, Fg50: 0.52, Fg35: 0.36,
		Line: 0.09, LineStrong: 0.16, Hover: 0.045, Active: 0.08,
		RustText: 0x964818, RustSoft: 0.09, RustLine: 0.45,
		OK: 0x2F8F57, Danger: 0xB4372F, DangerSoft: 0.08,
	}
	Dark = Tokens{
		Bg: 0x111117, Sidebar: Ink, Card: 0x1A1A21, CodeBg: Ink,
		Fg: Paper, Fg70: 0.74, Fg50: 0.52, Fg35: 0.36,
		Line: 0.08, LineStrong: 0.15, Hover: 0.05, Active: 0.09,
		RustText: 0xD98E57, RustSoft: 0.16, RustLine: 0.5,
		OK: 0x5CC68A, Danger: 0xE0756C, DangerSoft: 0.1,
	}
)

// Mode is the tokens of light (false) or dark (true).
func Mode(dark bool) *Tokens {
	if dark {
		return &Dark
	}
	return &Light
}

// Apply gives th A3 Lab's web colours, owned whatever the system's are: the
// field (what a flat sidebar shows) paper, the panes the main column's
// colour, what is raised the cards' white; the labels the ink at the
// stylesheet's opacities, the lines and the rows' states the ink's too;
// rust as the accent, the selection and the focus ring, rust's text colour
// on the links; the stylesheet's green and red.
func Apply(th *theme.Theme) {
	th.SetAccent(Rust<<8|0xFF, Rust<<8|0xFF)
	pals := [2]*theme.Palette{&th.Light, &th.Dark}
	for m, t := range [2]*Tokens{&Light, &Dark} {
		op := func(c uint32) uint32 { return c<<8 | 0xFF }
		fg := func(a float32) uint32 { return theme.ColA(t.Fg, a) }
		p := pals[m]
		p.Background, p.Surface, p.SurfaceRaised = op(t.Sidebar), op(t.Bg), op(t.Card)
		c := &th.Roles[m]
		set := func(r theme.Role, v uint32) { c[r], th.Own[r] = v, true }
		set(theme.Field, op(t.Sidebar))
		set(theme.Pane, op(t.Bg))
		set(theme.Raised, op(t.Card))
		set(theme.Label, op(t.Fg))
		set(theme.Text, op(t.Fg))
		set(theme.Label2, fg(t.Fg70))
		set(theme.Label3, fg(t.Fg50))
		set(theme.Label4, fg(t.Fg35))
		set(theme.Placeholder, fg(t.Fg35))
		set(theme.Separator, fg(t.Line))
		set(theme.Grid, fg(t.Line))
		set(theme.PaneHairline, fg(t.Line))
		set(theme.RowHover, fg(t.Hover))
		set(theme.RowPressed, fg(t.Active))
		set(theme.RowSelected, fg(t.Active))
		set(theme.RowSelectedInactive, fg(t.Hover))
		set(theme.RowSelectedRing, 0)
		set(theme.Link, op(t.RustText))
		set(theme.FocusRing, theme.ColA(Rust, t.RustLine))
		set(theme.TextSelection, theme.ColA(Rust, t.RustSoft*2))
		set(theme.Green, op(t.OK))
		set(theme.Red, op(t.Danger))
	}
}
