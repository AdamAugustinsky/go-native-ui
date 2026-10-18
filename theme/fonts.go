package theme

import (
	"math"

	"gonativeui/text"
)

// Fonts are the font descriptions of a theme. Scale is left zero: Resolve sets
// it for the window.
type Fonts struct {
	// UI is the font of body text and controls; UISemi the emphasised variant
	// (titles, headers); Small the caption size.
	UI, UISemi, Small text.FontDesc
	// Mono and MonoBold are the monospace pair.
	Mono, MonoBold text.FontDesc
	// Symbols is the font that text glyphs standing in for icons are set in.
	Symbols text.FontDesc
}

// FontKeys are the fonts of a Resolved theme, created in a text system.
type FontKeys struct {
	UI, UISemi, Small, Mono, MonoBold, Symbols text.FontKey
}

// FontMet places the baseline in a text box of one whole-pixel line: H is the
// box height, Off the baseline's distance from its top.
type FontMet struct{ H, Off float32 }

// TextSystem is what a theme needs from the text system to resolve fonts;
// *text.System satisfies it.
type TextSystem interface {
	Font(d text.FontDesc) text.FontKey
	Metrics(f text.FontKey) text.Metrics
}

var _ TextSystem = (*text.System)(nil)

// LineBox is the box of one line of a font with metrics m, and where the
// baseline sits in it the way ui places text.
func LineBox(m text.Metrics) FontMet {
	lh := m.LineHeight
	if lh <= 0 {
		lh = m.Ascent + m.Descent + m.LineGap
	}
	h := float32(math.Ceil(float64(max(lh, m.Ascent+m.Descent))))
	return FontMet{H: h, Off: Snap((h-(m.Ascent+m.Descent))/2 + m.Ascent)}
}
