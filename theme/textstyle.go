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
	// Cell is the height in points of a single-line label of the style: an
	// NSTextField's intrinsic height, its cell's height rounded up to a half
	// point. It is Line for most styles; Large Title (31), Title 2 (20.5) and
	// Title 3 (19) are shorter than their line. 0 takes Line.
	Cell float32
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
	// Cell is the height in pixels of a one-line label of the style
	// (TextSpec.Cell): what rexui.Label is as tall as. It is Line when the
	// style has no cell height of its own.
	Cell float32
	// Baseline is the baseline's distance from the top of a Line-tall box,
	// in whole pixels.
	Baseline float32
}

// MacText are the macOS 27.2 text styles (NSFont.preferredFontForTextStyle,
// tools/appkitref) and the window chrome's fonts measured from live windows
// (docs/design/liquid-glass-reference.md). The Rex additions default to Body
// and its semibold.
var MacText = [NumTextStyles]TextSpec{
	StyleLargeTitle:    {Size: 26, Line: 32, Weight: WeightRegular, Cell: 31},
	StyleTitle1:        {Size: 22, Line: 26, Weight: WeightRegular},
	StyleTitle2:        {Size: 17, Line: 22, Weight: WeightRegular, Cell: 20.5},
	StyleTitle3:        {Size: 15, Line: 20, Weight: WeightRegular, Cell: 19},
	StyleHeadline:      {Size: 13, Line: 16, Weight: WeightBold},
	StyleBody:          {Size: 13, Line: 16, Weight: WeightRegular},
	StyleCallout:       {Size: 12, Line: 15, Weight: WeightRegular},
	StyleSubheadline:   {Size: 11, Line: 14, Weight: WeightRegular},
	StyleFootnote:      {Size: 10, Line: 13, Weight: WeightRegular},
	StyleCaption1:      {Size: 10, Line: 13, Weight: WeightRegular},
	StyleCaption2:      {Size: 10, Line: 13, Weight: WeightMedium},
	StyleMono:          {Size: 13, Line: 16, Weight: WeightRegular, Mono: true},
	StyleToolbarTitle:  {Size: 15, Line: 19, Weight: WeightBold},
	StyleWindowTitle:   {Size: 13, Line: 16, Weight: WeightBold},
	StyleSidebarHeader: {Size: 11, Line: 14, Weight: WeightBold},
	StyleBodySemibold:  {Size: 13, Line: 16, Weight: WeightSemibold},
	StyleMenu:          {Size: 14, Line: 17, Weight: WeightRegular},
	StyleTooltip:       {Size: 11, Line: 13, Weight: WeightRegular},
	StyleTabTitle:      {Size: 13, Line: 16, Weight: WeightRegular},
	StylePaneTitle:     {Size: 13, Line: 16, Weight: WeightSemibold},
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
	cell := line
	if spec.Cell > 0 {
		cell = min(Snap(spec.Cell*scale), line)
	}
	return StyleFont{Font: k, Line: line, Cell: cell, Baseline: Snap((line-(m.Ascent+m.Descent))/2 + m.Ascent)}
}
