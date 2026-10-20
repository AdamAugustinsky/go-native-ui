package theme

// Size is a control size (NSControlSize, D39). Recipes index
// Resolved.Sizes with it; Regular is the default.
type Size uint8

const (
	// SizeRegular is the default: 24 pt controls in 13 pt type.
	SizeRegular Size = iota
	// SizeMini is 16 pt controls in 9 pt type.
	SizeMini
	// SizeSmall is 20 pt controls in 11 pt type.
	SizeSmall
	// SizeLarge is 28 pt capsule controls in 13 pt type.
	SizeLarge
	// SizeXLarge is 36 pt capsule controls in 13 pt type.
	SizeXLarge

	// NumSizes is the number of sizes.
	NumSizes
)

// SizeSpec is one control size, in points (a Resolved theme holds it in
// pixels, with Font set).
type SizeSpec struct {
	// Height is the height of push buttons, pop-ups, segmented controls and
	// steppers.
	Height float32
	// FieldHeight is the height of text and search fields.
	FieldHeight float32
	// PadX is the horizontal padding between a control's edge and its label.
	PadX float32
	// Radius is the corner radius of push buttons and pop-ups; Height/2 when
	// Capsule.
	Radius float32
	// FieldRadius is the corner radius of text fields (search fields are
	// capsules: FieldHeight/2).
	FieldRadius float32
	// Capsule says the size's buttons are capsules (large and above).
	Capsule bool
	// Icon is the side of the box an icon sits in, inside a control.
	Icon float32
	// Gap is the space between an icon or a box (checkbox, radio) and its
	// label.
	Gap float32
	// Check is the side of a checkbox or a radio button.
	Check float32
	// SwitchW and SwitchH are a switch's size.
	SwitchW, SwitchH float32
	// FontSize is the control font's point size and FontLine its line
	// height (AppKit's systemFontOfSize: line); Resolve makes Font of them.
	FontSize, FontLine float32
	// Font is the control font, set by Resolve.
	Font StyleFont
}

// MacSizes are AppKit's control sizes on macOS 27.2 (tools/appkitref:
// push buttons, fields, switches and checkboxes per NSControlSize). PadX is
// derived from the measured button widths ((width − label) ÷ 2, the label
// scaled from the regular one by the font's advance); mini and x-large field
// heights, and Icon, are designed.
var MacSizes = [NumSizes]SizeSpec{
	SizeRegular: {Height: 24, FieldHeight: 24, PadX: 12, Radius: 6, FieldRadius: 6, Icon: 16, Gap: 6, Check: 14, SwitchW: 54, SwitchH: 24, FontSize: 13, FontLine: 16},
	SizeMini:    {Height: 16, FieldHeight: 16, PadX: 8, Radius: 4, FieldRadius: 4, Icon: 12, Gap: 4, Check: 10, SwitchW: 36, SwitchH: 16, FontSize: 9, FontLine: 11},
	SizeSmall:   {Height: 20, FieldHeight: 22, PadX: 10, Radius: 5, FieldRadius: 5, Icon: 14, Gap: 5, Check: 12, SwitchW: 44, SwitchH: 20, FontSize: 11, FontLine: 13},
	SizeLarge:   {Height: 28, FieldHeight: 24, PadX: 14, Radius: 14, FieldRadius: 7, Capsule: true, Icon: 18, Gap: 6, Check: 16, SwitchW: 64, SwitchH: 28, FontSize: 13, FontLine: 16},
	SizeXLarge:  {Height: 36, FieldHeight: 28, PadX: 18, Radius: 18, FieldRadius: 8, Capsule: true, Icon: 20, Gap: 8, Check: 16, SwitchW: 64, SwitchH: 28, FontSize: 13, FontLine: 16},
}

func (s SizeSpec) scaled(k float32) SizeSpec {
	s.Height *= k
	s.FieldHeight *= k
	s.PadX *= k
	s.Radius *= k
	s.FieldRadius *= k
	s.Icon *= k
	s.Gap *= k
	s.Check *= k
	s.SwitchW *= k
	s.SwitchH *= k
	if s.Capsule {
		s.Radius = s.Height / 2
	}
	return s
}

// Spacing is the space scale (the HIG's grid) and the named gaps, in points;
// pixels in a Resolved theme.
type Spacing struct {
	// S2 … S32 are the steps of the scale, named by their value in points.
	S2, S4, S6, S8, S12, S16, S20, S24, S32 float32
	// ControlGap is the space between neighbouring controls (8).
	ControlGap float32
	// LabelGap is the space between a checkbox or radio and its label (6).
	LabelGap float32
	// WindowMargin is the margin of a window's content (20).
	WindowMargin float32
}

// MacSpacing is the space scale of the HIG.
var MacSpacing = Spacing{S2: 2, S4: 4, S6: 6, S8: 8, S12: 12, S16: 16, S20: 20, S24: 24, S32: 32, ControlGap: 8, LabelGap: 6, WindowMargin: 20}

func (s Spacing) scaled(k float32) Spacing {
	for _, p := range [...]*float32{&s.S2, &s.S4, &s.S6, &s.S8, &s.S12, &s.S16, &s.S20, &s.S24, &s.S32, &s.ControlGap, &s.LabelGap, &s.WindowMargin} {
		*p *= k
	}
	return s
}

// Radii are the corner radii of shapes that are not controls, in points;
// pixels in a Resolved theme. Controls carry theirs in SizeSpec. macOS 27
// keeps radii concentric locally, from the window's 16 inward
// (Concentric), and a capsule's radius is half its height.
type Radii struct {
	// Window is the window's corner radius (16 for every style on 27.2; the
	// system draws it).
	Window float32
	// Group is a group box's radius (12).
	Group float32
	// Selection is the radius of an inset selection: sidebar rows and inset
	// tables (8).
	Selection float32
	// Pane is a Rex pane's radius (10.5).
	Pane float32
	// Popover is a drawn popover's or menu's radius (designed).
	Popover float32
	// Checkbox is a checkbox's radius (3.5).
	Checkbox float32
}

// MacRadii are the radii measured on macOS 27.2, and Rex's pane.
var MacRadii = Radii{Window: 16, Group: 12, Selection: 8, Pane: 10.5, Popover: 10, Checkbox: 3.5}

func (r Radii) scaled(k float32) Radii {
	r.Window *= k
	r.Group *= k
	r.Selection *= k
	r.Pane *= k
	r.Popover *= k
	r.Checkbox *= k
	return r
}

// Concentric is the radius of a shape inset by inset inside one of radius
// outer, so that their corners share a centre: outer − inset, at least 0.
func Concentric(outer, inset float32) float32 { return max(0, outer-inset) }

// Lines are stroke widths and the lengths of lists, in points; pixels in a
// Resolved theme.
type Lines struct {
	// Hairline is one device pixel (0.5 pt at 2×): separators, the scroll
	// edge's line. It resolves to 1 pixel at every scale.
	Hairline float32
	// FocusOutset is how far the focus ring's outer edge lies outside the
	// control (3), and FocusWidth the ring's visible width (3.5).
	FocusOutset, FocusWidth float32
	// RowH is a table or list row (24); HeaderH a table header (28).
	RowH, HeaderH float32
	// Indent is an outline's indentation per level (13).
	Indent float32
	// MenuItemH is a menu item (24); MenuPadY a menu's top and bottom padding
	// (5); MenuSeparatorH a separator item (11). For drawn menus.
	MenuItemH, MenuPadY, MenuSeparatorH float32
	// Scroller is a regular scroller's width (17 overlay and legacy);
	// ScrollerSmall a small overlay one's (15); ScrollerSmallLegacy a small
	// or mini legacy one's (13).
	Scroller, ScrollerSmall, ScrollerSmallLegacy float32
	// ScrollerKnobInset is the room between a legacy scroller's knob (and
	// the slot it moves in) and its track's sides and ends (3 at every
	// size: an 11-pt knob in a 17-pt scroller, 7 in 13).
	ScrollerKnobInset float32
}

// MacLines are AppKit's on macOS 27.2.
var MacLines = Lines{
	Hairline: 0.5, FocusOutset: 3, FocusWidth: 3.5,
	RowH: 24, HeaderH: 28, Indent: 13,
	MenuItemH: 24, MenuPadY: 5, MenuSeparatorH: 11,
	Scroller: 17, ScrollerSmall: 15, ScrollerSmallLegacy: 13, ScrollerKnobInset: 3,
}

func (l Lines) scaled(k float32) Lines {
	for _, p := range [...]*float32{&l.FocusOutset, &l.FocusWidth, &l.RowH, &l.HeaderH, &l.Indent, &l.MenuItemH, &l.MenuPadY, &l.MenuSeparatorH, &l.Scroller, &l.ScrollerSmall, &l.ScrollerSmallLegacy, &l.ScrollerKnobInset} {
		*p *= k
	}
	l.Hairline = 1
	return l
}
