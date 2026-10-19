package icon

// TextStyle is the text style a symbol is sized for, as
// NSImage.SymbolConfiguration(textStyle:scale:) does: the symbol takes the
// point size and the weight of the style's font, so an icon next to a
// headline is as heavy as the headline, and one in a caption as light as a
// caption. The zero value is body text, the size of a regular control.
type TextStyle uint8

const (
	// StyleBody is 13 pt regular: regular-size controls and body text.
	StyleBody TextStyle = iota
	// StyleCallout is 12 pt regular.
	StyleCallout
	// StyleSubheadline is 11 pt regular: small controls and table headers.
	StyleSubheadline
	// StyleFootnote is 10 pt regular.
	StyleFootnote
	// StyleCaption1 is 10 pt regular.
	StyleCaption1
	// StyleCaption2 is 10 pt medium.
	StyleCaption2
	// StyleHeadline is 13 pt bold.
	StyleHeadline
	// StyleTitle3 is 15 pt regular.
	StyleTitle3
	// StyleTitle2 is 17 pt regular.
	StyleTitle2
	// StyleTitle1 is 22 pt regular.
	StyleTitle1
	// StyleLargeTitle is 26 pt regular.
	StyleLargeTitle

	// NumStyles is the number of text styles.
	NumStyles
)

// Scale is a symbol's scale within its text style (NSImageSymbolScale): the
// size of the symbol's glyph relative to the style's cap height, which
// changes the symbol's drawn size and not the text style's point size.
type Scale uint8

const (
	// ScaleMedium is the default scale.
	ScaleMedium Scale = iota
	// ScaleSmall draws the symbol smaller than the text beside it.
	ScaleSmall
	// ScaleLarge draws it larger.
	ScaleLarge
)

// styleSpec is a text style's point size and weight (NSFontWeight): the
// sizes and weights of theme.MacText, which macOS 27.2's
// preferredFontForTextStyle gives (a test checks both).
var styleSpec = [NumStyles]struct{ Pt, Weight float32 }{
	StyleBody:        {13, 0},
	StyleCallout:     {12, 0},
	StyleSubheadline: {11, 0},
	StyleFootnote:    {10, 0},
	StyleCaption1:    {10, 0},
	StyleCaption2:    {10, 0.23},
	StyleHeadline:    {13, 0.4},
	StyleTitle3:      {15, 0},
	StyleTitle2:      {17, 0},
	StyleTitle1:      {22, 0},
	StyleLargeTitle:  {26, 0},
}

// Pt is the point size of text style s (body's for one out of range).
func (s TextStyle) Pt() float32 { return s.spec().Pt }

// Weight is the NSFontWeight of text style s: 0 regular, 0.23 medium, 0.4
// bold.
func (s TextStyle) Weight() float32 { return s.spec().Weight }

func (s TextStyle) spec() struct{ Pt, Weight float32 } {
	if s >= NumStyles {
		s = StyleBody
	}
	return styleSpec[s]
}

// In returns the spec sized for text style st at scale sc: its Pt and Weight
// are the style's, and its Scale is sc. The symbol's name and its Segoe and
// fallback glyphs are kept: the drawn icon on Windows and Linux (a glyph of
// the symbol font or of the UI font, which have one weight) takes the style's
// point size and the scale's factor, and ignores the weight.
//
//	icon.Spec{Symbol: "plus", Segoe: 0xE710, Fallback: '+'}.In(icon.StyleHeadline, icon.ScaleLarge)
func (s Spec) In(st TextStyle, sc Scale) Spec {
	s.Pt, s.Weight, s.Scale = st.Pt(), st.Weight(), sc
	return s
}

// scaleFactor is the size of a symbol at scale s relative to medium: what
// AppKit's small and large scales do to a symbol's drawn size (the mean of the heights AppKit's
// symbols take on macOS 27.2 over 13 to 104 px, TestScaleFactors), which the drawn
// glyphs of Windows and Linux follow.
func scaleFactor(s Scale) float32 {
	switch s {
	case ScaleSmall:
		return 0.80
	case ScaleLarge:
		return 1.27
	}
	return 1
}
