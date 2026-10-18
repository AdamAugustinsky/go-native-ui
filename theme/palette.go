package theme

// Palette is the set of semantic colours of one mode (light or dark), RGBA8
// 0xRRGGBBAA. Widgets and chrome ask for a role, never for a literal colour.
//
// "Text" roles are meant to sit on Surface and SurfaceRaised and on the
// window Background around them; a theme keeps Text at a contrast of at least
// 7:1 and TextMuted at least 4.5:1 against all of those (theme/rex tests this
// for Rex).
type Palette struct {
	// Background is the window's base colour: the solid colour the window
	// background averages to. Chrome may paint a richer field over it.
	Background uint32
	// Surface is the opaque colour of a content surface (a focused pane, the
	// body of a table or text field's container).
	Surface uint32
	// SurfaceRaised is the opaque colour of what floats above a surface: menus,
	// dropdowns, tooltips and dialogs.
	SurfaceRaised uint32

	// Text is the colour of body text.
	Text uint32
	// TextMuted is the colour of secondary text (captions, hints, units).
	TextMuted uint32
	// TextDisabled is the colour of text and icons that do not take input. It
	// is exempt from the contrast minimums.
	TextDisabled uint32

	// Accent fills primary buttons, checked boxes and switches, the filled
	// part of sliders and progress bars, and marks links and the sort arrow.
	Accent uint32
	// OnAccent is text and icons drawn on Accent.
	OnAccent uint32

	// Line is the colour of hairline separators (between rows, under headers,
	// round surfaces). It is translucent so that it works on any surface.
	Line uint32
	// Selection is the background of selected rows, text and menu items; it is
	// translucent, so that what is selected stays visible.
	Selection uint32
	// SelectionText is the text colour on Selection.
	SelectionText uint32
	// FocusRing is the colour of the keyboard focus ring.
	FocusRing uint32

	// Danger marks destructive actions and errors; Warning, Success and Info
	// mark the matching states (banners, icons, validation). They are
	// indicator colours: use Text for the words next to them.
	Danger, Warning, Success, Info uint32

	// Scrim dims what lies beneath a modal dialog.
	Scrim uint32
	// Shadow is the colour drop shadows are cast in; its alpha is the strength
	// of the standard shadow of a raised surface, which widgets scale.
	Shadow uint32

	// HoverOverlay and PressOverlay are laid over a control that is hovered or
	// pressed (translucent, so that they work on any fill).
	HoverOverlay, PressOverlay uint32

	// ControlFill and ControlBorder paint the body and the outline of buttons,
	// text fields, checkboxes and selects at rest.
	ControlFill, ControlBorder uint32
	// TrackFill is the unfilled track of sliders, progress bars and switches
	// that are off; ThumbFill is the knob that rides on a track.
	TrackFill, ThumbFill uint32
	// ScrollThumb is the thumb of a scrollbar.
	ScrollThumb uint32
}

// MixPalettes returns the palette between a (t = 0) and b (t = 1), channel by channel.
func MixPalettes(a, b *Palette, t float32) Palette {
	m := func(x, y uint32) uint32 { return MixRGBA(x, y, t) }
	return Palette{
		Background: m(a.Background, b.Background), Surface: m(a.Surface, b.Surface), SurfaceRaised: m(a.SurfaceRaised, b.SurfaceRaised),
		Text: m(a.Text, b.Text), TextMuted: m(a.TextMuted, b.TextMuted), TextDisabled: m(a.TextDisabled, b.TextDisabled),
		Accent: m(a.Accent, b.Accent), OnAccent: m(a.OnAccent, b.OnAccent),
		Line: m(a.Line, b.Line), Selection: m(a.Selection, b.Selection), SelectionText: m(a.SelectionText, b.SelectionText),
		FocusRing: m(a.FocusRing, b.FocusRing),
		Danger:    m(a.Danger, b.Danger), Warning: m(a.Warning, b.Warning), Success: m(a.Success, b.Success), Info: m(a.Info, b.Info),
		Scrim: m(a.Scrim, b.Scrim), Shadow: m(a.Shadow, b.Shadow),
		HoverOverlay: m(a.HoverOverlay, b.HoverOverlay), PressOverlay: m(a.PressOverlay, b.PressOverlay),
		ControlFill: m(a.ControlFill, b.ControlFill), ControlBorder: m(a.ControlBorder, b.ControlBorder),
		TrackFill: m(a.TrackFill, b.TrackFill), ThumbFill: m(a.ThumbFill, b.ThumbFill),
		ScrollThumb: m(a.ScrollThumb, b.ScrollThumb),
	}
}

// WithAccent returns p recoloured to a system accent colour (0xRRGGBBAA, the
// alpha is ignored): Accent becomes the colour, OnAccent the better of white
// and black on it, and FocusRing and Selection take its hue and keep their
// own alpha.
func (p Palette) WithAccent(accent uint32) Palette {
	rgb := accent &^ 0xFF
	p.Accent = rgb | 0xFF
	p.OnAccent = 0xFFFFFFFF
	if Contrast(p.Accent, 0x000000FF) > Contrast(p.Accent, 0xFFFFFFFF) {
		p.OnAccent = 0x000000FF
	}
	p.FocusRing = rgb | p.FocusRing&0xFF
	p.Selection = rgb | p.Selection&0xFF
	return p
}
