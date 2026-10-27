package rex

import "gonativeui/theme"

// Terminal is the palette of the terminal-style content of the reference: the
// Catppuccin-Latte-like colours of a focused pane in light, dimmed toward the
// fill when it is not, and a dark pair designed for the dark surfaces.
// Colours are 0xRRGGBB (see theme.Col).
type Terminal struct {
	// FG is the foreground (text); Dim the secondary text; Line the colour of
	// rules and box-drawing.
	FG, Dim, Line uint32
	// Blue, Teal, Red, Green, Purple and Orange are the accents of syntax and
	// status; Bullet is the green of list bullets; Op is the colour of operators.
	Blue, Teal, Red, Green, Bullet, Op, Purple, Orange uint32
}

var (
	// The light pair is sampled from the reference (docs/design/rex-benchmark.md).
	termLightFocused = Terminal{
		FG: 0x1E2228, Dim: 0x757679, Blue: 0x1E66F5, Teal: 0x218A91, Red: 0xCF3E4B,
		Green: 0x378925, Bullet: 0x106329, Op: 0x4C4F69, Purple: 0x8839EF, Orange: 0xB87B2E, Line: 0x1E2228,
	}
	termLightDimmed = Terminal{
		FG: 0x5E6161, Dim: 0x9B9B97, Blue: 0x4F92D9, Teal: 0x5CA0A3, Red: 0xDA6066,
		Green: 0x558E62, Bullet: 0x558E62, Op: 0x5E6161, Purple: 0xA481DF, Orange: 0xB78942, Line: 0x5E6162,
	}
	// The dark pair keeps each colour's hue, lightened to read on ink: at least
	// 4.5:1 on the focused surface and on the unfocused glass (palette_test.go).
	termDarkFocused = Terminal{
		FG: 0xEAE6E7, Dim: 0xA69EA1, Blue: 0x7FA8FF, Teal: 0x5FD0D6, Red: 0xFF8791,
		Green: 0x86DB8E, Bullet: 0x6FCF7C, Op: 0xBEB6B9, Purple: 0xC39BFF, Orange: 0xF0B66A, Line: 0xEAE6E7,
	}
	termDarkDimmed = Terminal{
		FG: 0xA8A0A3, Dim: 0xA39B9E, Blue: 0x80A0E0, Teal: 0x5FA9AD, Red: 0xE17F88,
		Green: 0x6FAF7A, Bullet: 0x6FAF7A, Op: 0xA8A0A3, Purple: 0xB496E2, Orange: 0xC49A5B, Line: 0xA8A0A3,
	}
)

func mixTerminal(a, b *Terminal, t float32) Terminal {
	m := func(x, y uint32) uint32 { return theme.MixRGB(x, y, t) }
	return Terminal{
		FG: m(a.FG, b.FG), Dim: m(a.Dim, b.Dim), Blue: m(a.Blue, b.Blue), Teal: m(a.Teal, b.Teal), Red: m(a.Red, b.Red),
		Green: m(a.Green, b.Green), Bullet: m(a.Bullet, b.Bullet), Op: m(a.Op, b.Op), Purple: m(a.Purple, b.Purple),
		Orange: m(a.Orange, b.Orange), Line: m(a.Line, b.Line),
	}
}

// TerminalAt is the palette of a pane whose focus animation stands at focus
// (0 = fully dimmed, 1 = focused), in the light/dark mix t (theme.Resolved.T).
func TerminalAt(t, focus float32) Terminal {
	l := mixTerminal(&termLightDimmed, &termLightFocused, focus)
	if t == 0 {
		return l
	}
	d := mixTerminal(&termDarkDimmed, &termDarkFocused, focus)
	if t == 1 {
		return d
	}
	return mixTerminal(&l, &d, t)
}
