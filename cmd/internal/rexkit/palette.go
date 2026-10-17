package rexkit

// Palette is the terminal palette of a pane: Catppuccin-Latte-like when the
// pane is focused, dimmed toward the pane fill otherwise. Colours are
// 0xRRGGBB.
type Palette struct {
	FG, Dim, Blue, Teal, Red, Green, Bullet, Op, Purple, Orange, Line uint32
}

var (
	// PaletteFocused and PaletteDimmed are the two ends of a pane palette.
	PaletteFocused = Palette{
		FG: 0x1E2228, Dim: 0x757679, Blue: 0x1E66F5, Teal: 0x218A91, Red: 0xCF3E4B,
		Green: 0x378925, Bullet: 0x106329, Op: 0x4C4F69, Purple: 0x8839EF, Orange: 0xB87B2E, Line: 0x1E2228,
	}
	PaletteDimmed = Palette{
		FG: 0x5E6161, Dim: 0x9B9B97, Blue: 0x4F92D9, Teal: 0x5CA0A3, Red: 0xDA6066,
		Green: 0x558E62, Bullet: 0x558E62, Op: 0x5E6161, Purple: 0xA481DF, Orange: 0xB78942, Line: 0x5E6162,
	}
)

// MixPalette is the palette of a pane whose focus animation stands at t
// (0 = fully dimmed, 1 = focused).
func MixPalette(t float32) Palette {
	a, b := &PaletteDimmed, &PaletteFocused
	return Palette{
		FG: MixRGB(a.FG, b.FG, t), Dim: MixRGB(a.Dim, b.Dim, t), Blue: MixRGB(a.Blue, b.Blue, t),
		Teal: MixRGB(a.Teal, b.Teal, t), Red: MixRGB(a.Red, b.Red, t), Green: MixRGB(a.Green, b.Green, t),
		Bullet: MixRGB(a.Bullet, b.Bullet, t), Op: MixRGB(a.Op, b.Op, t), Purple: MixRGB(a.Purple, b.Purple, t),
		Orange: MixRGB(a.Orange, b.Orange, t), Line: MixRGB(a.Line, b.Line, t),
	}
}
