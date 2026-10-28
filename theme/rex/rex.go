// Package rex is the default theme: the Rex look (DECISIONS D17,
// docs/design/rex-benchmark.md), with a light variant fitted to the reference
// and a dark one fitted to its dark screenshot.
//
// The look is a smooth background field (pink-lavender over sand in light;
// warm plum over umber in dark), glass panes with soft layered shadows and
// hairline edges that dim and turn translucent when unfocused, a slim divider
// handle, and a titlebar band the panes start under. Fonts are the system's.
//
// Theme returns the tokens and the Chrome constructor; Grid and Terminal are
// the monospace cell grid and the colours the terminal-style content of the
// reference is set in.
package rex

import (
	"gonativeui/theme"
)

// Theme returns the Rex theme. Each call returns a new copy, which the caller
// may change before use.
func Theme() *theme.Theme {
	return &theme.Theme{
		Name:         "Rex",
		Light:        light,
		Dark:         dark,
		Metrics:      metrics,
		Fonts:        fonts,
		Motion:       motion,
		SystemAccent: true,
		// Rex's panes are circular arcs, not macOS's squircle: the corner of
		// the reference screenshot's pane fits a circle of 10 pt to 0.27 px
		// and a continuous corner only to 0.56 px (normal distance of the
		// edge highlight, 104 points of the top left corner of the right pane).
		Corners:      theme.CornersCircular,
		Roles:        roles,
		SystemColors: true,
		NewChrome:    func() theme.Chrome { return &chrome{} },
	}
}

// ThemeWithField is Theme with its window background, and the glass of its
// unfocused surfaces, in other colours (FieldColors).
func ThemeWithField(fc FieldColors) *theme.Theme {
	th := Theme()
	lk := newLook(fc)
	th.NewChrome = func() theme.Chrome { return &chrome{fld: Field{look: lk}} }
	if fc.DarkGlass != 0 {
		th.Roles[theme.Dark][theme.GlassTint] = theme.ColA(fc.DarkGlass, darkAlpha)
	}
	return th
}

// The palettes. Light is sampled from the reference (sRGB), and dark from
// its dark screenshot: warm plum and umber inks with the same roles. Text is
// at least 7:1 and TextMuted at least 4.5:1 against every surface and the
// background in both (palette_test.go).
var (
	light = theme.Palette{
		Background: 0xE7E3E7FF, Surface: 0xFFFFFFFF, SurfaceRaised: 0xFFFFFFFF,
		Text: 0x1E2228FF, TextMuted: 0x5A585EFF, TextDisabled: 0x9A9AA0FF,
		Accent: 0x1E66F5FF, OnAccent: 0xFFFFFFFF,
		Line: 0x5C5E6040, Selection: 0x1E66F52B, SelectionText: 0x1E2228FF, FocusRing: 0x1E66F5E6,
		Danger: 0xCF3E4BFF, Warning: 0xB87B2EFF, Success: 0x378925FF, Info: 0x218A91FF,
		Scrim: 0x1E222859, Shadow: 0x2A203012,
		HoverOverlay: 0x2A203014, PressOverlay: 0x2A203026,
		ControlFill: 0xFFFFFFB3, ControlBorder: 0x5C5E6059,
		TrackFill: 0xD9D6DCFF, ThumbFill: 0xFFFFFFFF,
		ScrollThumb: 0x6E637873,
	}
	dark = theme.Palette{
		Background: 0x221E1FFF, Surface: 0x2B2527FF, SurfaceRaised: 0x372F32FF,
		Text: 0xEEEAEBFF, TextMuted: 0xADA4A7FF, TextDisabled: 0x736B6EFF,
		Accent: 0x7AA2FFFF, OnAccent: 0x0B1226FF,
		Line: 0xFFFFFF1F, Selection: 0x7AA2FF40, SelectionText: 0xEEEAEBFF, FocusRing: 0x7AA2FFE6,
		Danger: 0xFF7B86FF, Warning: 0xE9B25AFF, Success: 0x7BD88FFF, Info: 0x6CCDD4FF,
		Scrim: 0x000000A6, Shadow: 0x00000052,
		HoverOverlay: 0xFFFFFF14, PressOverlay: 0xFFFFFF26,
		ControlFill: 0xFFFFFF14, ControlBorder: 0xFFFFFF33,
		TrackFill: 0x4A4245FF, ThumbFill: 0xF7F3F4FF,
		ScrollThumb: 0xC4B6BB80,
	}
)

var metrics = theme.Metrics{
	RadiusSmall: 4, RadiusMedium: 6, RadiusLarge: 10.5,
	Pad: 8, Gap: 8, GapSmall: 4,
	ControlH: 28, RowH: 24, IconSize: 16,
	BorderW: 0.5, FocusRingW: 2,
	ScrollbarW: 12, ScrollThumbW: 4,
	TitlebarH: 46,
	Inset:     7.5, Gutter: 7,
	DividerGrab:   13,
	WindowButtonW: 40,
}
