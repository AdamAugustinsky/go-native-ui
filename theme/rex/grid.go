package rex

import (
	"math"

	"gonativeui/text"
	"gonativeui/theme"
)

// GridText is what NewGrid needs from the text system; *text.System satisfies it.
type GridText interface {
	theme.TextSystem
	Shape(f text.FontKey, utf8 []byte) text.RunRef
	Run(r text.RunRef) text.RunView
}

var _ GridText = (*text.System)(nil)

// Grid is the monospace cell grid of the terminal-style content of the
// reference: a system monospace face sized so that a cell is 7.5 pt wide, on
// a row pitch of 16 pt.
type Grid struct {
	// Mono and MonoBold are the fonts, with the line boxes Met and BoldMet.
	Mono, MonoBold text.FontKey
	Met, BoldMet   theme.FontMet
	// Adv is the width of a cell and RowPitch the height of a row, in pixels.
	Adv, RowPitch float32
	// Face names the face in use and SizePt its size in points.
	Face   string
	SizePt float32
	scale  float32
}

// AdvPt is the width of a cell in points.
func (g *Grid) AdvPt() float64 { return float64(g.Adv) / float64(g.scale) }

// RowPitchPt is the height of a row in points.
func (g *Grid) RowPitchPt() float64 { return float64(g.RowPitch) / float64(g.scale) }

func width(ts GridText, f text.FontKey, s string) float32 {
	return ts.Run(ts.Shape(f, strBytes(s))).Advance
}

// NewGrid creates the grid fonts for a window at scale pixels per point. The
// first family that really is monospaced wins (Menlo on macOS, Consolas on
// Windows, DejaVu Sans Mono or Liberation Mono on Linux); otherwise the
// system's monospace design is used.
func NewGrid(ts GridText, scale float32) Grid {
	g := Grid{scale: scale}
	var probe text.FontKey
	family := ""
	found := false
	for _, fam := range [...]string{"Menlo", "Consolas", "DejaVu Sans Mono", "Liberation Mono"} {
		p := ts.Font(text.FontDesc{Family: fam, SizePt: 12.5, Weight: 400, Scale: scale})
		narrow, wide := width(ts, p, "i"), width(ts, p, "m")
		if math.Abs(float64(narrow-wide)) <= 0.01*float64(wide) {
			family, probe, found = fam, p, true
			break
		}
	}
	if !found {
		probe = ts.Font(text.FontDesc{Mono: true, SizePt: 12.5, Weight: 400, Scale: scale})
	}
	g.Face = family
	if family == "" {
		g.Face = "system monospaced"
	}
	adv := ts.Metrics(probe).MonoAdvance
	if adv <= 0 {
		adv = width(ts, probe, "0")
	}
	if adv <= 0 {
		adv = 7.5 * scale
	}
	size := 12.5 * (7.5 * scale) / adv
	g.SizePt = size
	mk := func(weight int) text.FontKey {
		return ts.Font(text.FontDesc{Family: family, Mono: family == "", SizePt: size, Weight: weight, Scale: scale})
	}
	g.Mono, g.MonoBold = mk(400), mk(700)
	g.Adv = ts.Metrics(g.Mono).MonoAdvance
	if g.Adv <= 0 {
		g.Adv = width(ts, g.Mono, "0")
	}
	g.RowPitch = 16 * scale
	g.Met, g.BoldMet = theme.LineBox(ts.Metrics(g.Mono)), theme.LineBox(ts.Metrics(g.MonoBold))
	return g
}
