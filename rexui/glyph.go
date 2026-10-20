package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
)

// Glyph is an optional symbol: the zero value is none, Sym(id) is the icon
// id of the kit's atlas.
type Glyph uint16

// Sym is the Glyph of icon id.
func Sym(id icon.ID) Glyph { return Glyph(id) + 1 }

// ID is the icon of g, and whether there is one.
func (g Glyph) ID() (icon.ID, bool) { return icon.ID(g - 1), g != 0 }

// Pair is a recipe colour that no role names (Rex's identity colours): a
// light and a dark 0xRRGGBB colour with an alpha in each, mixed by the
// theme's light/dark mix.
type Pair struct {
	// Light and Dark are the colours (0xRRGGBB).
	Light, Dark uint32
	// Alpha is the opacity in light and in dark.
	Alpha [2]float32
}

// Solid is the opaque Pair of light and dark (0xRRGGBB).
func Solid(light, dark uint32) Pair { return Pair{light, dark, [2]float32{1, 1}} }

// PairA is the Pair of light and dark (0xRRGGBB) at alphas la and da.
func PairA(light, dark uint32, la, da float32) Pair { return Pair{light, dark, [2]float32{la, da}} }

// At is the colour at the light/dark mix t (0 light, 1 dark), 0xRRGGBBAA.
func (p Pair) At(t float32) uint32 {
	return theme.ColA(theme.MixRGB(p.Light, p.Dark, t), theme.Lerp(p.Alpha[0], p.Alpha[1], t))
}
