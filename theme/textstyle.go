package theme

import "gonativeui/text"

// TextStyle names a text style (D39): the macOS text styles, the window
// chrome's fonts, and the theme's additions. A Resolved theme holds each
// style's font in Resolved.Styles, created at the point size (D38).
type TextStyle uint8

const (
	// StyleLargeTitle is 26/32 regular: onboarding and empty-state titles.
	StyleLargeTitle TextStyle = iota
	// StyleTitle1 is 22/26 regular: window-level titles.
	StyleTitle1
	// StyleTitle2 is 17/22 regular: section titles.
	StyleTitle2
	// StyleTitle3 is 15/20 regular: group titles.
	StyleTitle3
	// StyleHeadline is 13/16 bold: emphasised body, list titles.
	StyleHeadline
	// StyleBody is 13/16 regular: text and regular-size controls.
	StyleBody
	// StyleCallout is 12/15 regular: help text.
	StyleCallout
	// StyleSubheadline is 11/14 regular: table headers, small controls.
	StyleSubheadline
	// StyleFootnote is 10/13 regular: footers.
	StyleFootnote
	// StyleCaption1 is 10/13 regular: captions.
	StyleCaption1
	// StyleCaption2 is 10/13 medium: badges, small labels.
	StyleCaption2
	// StyleMono is 13/16 SF Mono: code and terminal text.
	StyleMono
	// StyleToolbarTitle is the unified toolbar's title: 15/19 bold.
	StyleToolbarTitle
	// StyleWindowTitle is the title of a plain titlebar or a compact
	// toolbar: 13/16 bold.
	StyleWindowTitle
	// StyleSidebarHeader is a sidebar group header: 11/14 bold (AppKit sets
	// 11 pt whose stems render bold).
	StyleSidebarHeader
	// StyleBodySemibold is 13/16 semibold: the selected sidebar row.
	StyleBodySemibold
	// StyleMenu is a menu item's text: 14/17.
	StyleMenu
	// StyleTooltip is a tooltip's text: 11/13.
	StyleTooltip
	// StyleTabTitle is a Rex tab's title.
	StyleTabTitle
	// StylePaneTitle is a Rex pane's title.
	StylePaneTitle

	// NumTextStyles is the number of text styles.
	NumTextStyles
)

// The weights tokens use, as text.FontDesc.Weight. CoreText maps them to
// NSFontWeight's medium (0.23), semibold (0.3) and bold (0.4): SF's 510, 590
// and 700.
const (
	// WeightRegular is the regular weight.
	WeightRegular = 400
	// WeightMedium is SF's medium (510).
	WeightMedium = 500
	// WeightSemibold is SF's semibold (590).
	WeightSemibold = 600
	// WeightBold is bold (700).
	WeightBold = 700
)

// TextSpec is a text style in points.
type TextSpec struct {
	// Size is the point size, Line the line height in points (AppKit's
	// default line height for the style).
	Size, Line float32
	// Weight is the font weight (WeightRegular …).
	Weight int
	// Mono selects the system's monospaced design (SF Mono).
	Mono bool
}

// Desc is the font description of s (Scale left zero for Resolve to set).
func (s TextSpec) Desc() text.FontDesc {
	return text.FontDesc{SizePt: s.Size, Weight: s.Weight, Mono: s.Mono}
}

// StyleFont is a resolved text style: its font and where the baseline sits in
// a line of the style's height.
type StyleFont struct {
	// Font is the font, at the point size and the window's scale.
	Font text.FontKey
	// Line is the line height in pixels (the style's, or the font's own if
	// that is taller).
	Line float32
	// Baseline is the baseline's distance from the top of a Line-tall box,
	// in whole pixels.
	Baseline float32
}

// MacText are the macOS 27.2 text styles (NSFont.preferredFontForTextStyle,
// tools/appkitref) and the window chrome's fonts measured from live windows
// (docs/design/liquid-glass-reference.md). The Rex additions default to Body
// and its semibold.
var MacText = [NumTextStyles]TextSpec{
	StyleLargeTitle:    {26, 32, WeightRegular, false},
	StyleTitle1:        {22, 26, WeightRegular, false},
	StyleTitle2:        {17, 22, WeightRegular, false},
	StyleTitle3:        {15, 20, WeightRegular, false},
	StyleHeadline:      {13, 16, WeightBold, false},
	StyleBody:          {13, 16, WeightRegular, false},
	StyleCallout:       {12, 15, WeightRegular, false},
	StyleSubheadline:   {11, 14, WeightRegular, false},
	StyleFootnote:      {10, 13, WeightRegular, false},
	StyleCaption1:      {10, 13, WeightRegular, false},
	StyleCaption2:      {10, 13, WeightMedium, false},
	StyleMono:          {13, 16, WeightRegular, true},
	StyleToolbarTitle:  {15, 19, WeightBold, false},
	StyleWindowTitle:   {13, 16, WeightBold, false},
	StyleSidebarHeader: {11, 14, WeightBold, false},
	StyleBodySemibold:  {13, 16, WeightSemibold, false},
	StyleMenu:          {14, 17, WeightRegular, false},
	StyleTooltip:       {11, 13, WeightRegular, false},
	StyleTabTitle:      {13, 16, WeightRegular, false},
	StylePaneTitle:     {13, 16, WeightSemibold, false},
}

// styleFont resolves spec at scale in ts.
func styleFont(ts TextSystem, spec TextSpec, scale float32) StyleFont {
	d := spec.Desc()
	d.Scale = scale
	k := ts.Font(d)
	m := ts.Metrics(k)
	line := spec.Line * scale
	if nat := m.Ascent + m.Descent; nat > line {
		line = nat
	}
	line = Snap(line)
	return StyleFont{Font: k, Line: line, Baseline: Snap((line-(m.Ascent+m.Descent))/2 + m.Ascent)}
}
