package theme

import "gonativeui/platform"

// Role names a colour role of tokens v2 (D39). A Resolved theme holds every
// role's colour in Resolved.Colors, indexed by Role, so a recipe names roles
// in its tables and reads r.Colors[role]; the light/dark crossfade is one
// loop over the array.
//
// Each role has a Source. The System roles take a platform.SystemColor each
// (systemColorRole): on macOS they are the system's colours, alpha included,
// read live from NSColor (Follower); elsewhere, and before the system is
// read, they are the theme's values, which for Rex are the colours measured
// on macOS 27.2 (Measured). The other roles are the theme's own
// (SourceTheme): Rex's identity, and the window-chrome colours measured from
// live macOS 27.2 windows (docs/design/liquid-glass-reference.md), which no
// NSColor names. Roles are append-only; their numbers are API.
type Role uint8

// The roles; for a System role the comment names the NSColor.
const (
	// Window is the window's background (windowBackgroundColor).
	Window Role = iota
	// UnderPage is the background behind documents (underPageBackgroundColor).
	UnderPage
	// ControlBackground is the background of lists, tables and other large
	// controls (controlBackgroundColor).
	ControlBackground
	// TextBackground is the background of text fields and text views
	// (textBackgroundColor).
	TextBackground
	// Label is primary text (labelColor).
	Label
	// Label2 is secondary text (secondaryLabelColor).
	Label2
	// Label3 is tertiary text: disabled glyphs, icons' placeholders
	// (tertiaryLabelColor).
	Label3
	// Label4 is quaternary text: watermarks (quaternaryLabelColor).
	Label4
	// Label5 is quinary text (quinaryLabelColor).
	Label5
	// Text is the text of documents and fields (textColor).
	Text
	// Placeholder is a field's placeholder (placeholderTextColor).
	Placeholder
	// TextDisabled is the label of a disabled control (disabledControlTextColor).
	TextDisabled
	// HeaderText is the text of table headers (headerTextColor).
	HeaderText
	// Fill1 is the system fill: a checkbox's body (systemFillColor).
	Fill1
	// Fill2 is the secondary fill: a push button's body (secondarySystemFillColor).
	Fill2
	// Fill3 is the tertiary fill: tracks (tertiarySystemFillColor).
	Fill3
	// Fill4 is the quaternary fill (quaternarySystemFillColor).
	Fill4
	// Fill5 is the quinary fill (quinarySystemFillColor).
	Fill5
	// Separator is the colour of dividers and hairlines (separatorColor).
	Separator
	// Grid is the colour of table grids (gridColor).
	Grid
	// Accent is the user's accent (controlAccentColor): default buttons,
	// checks, switches, sliders.
	Accent
	// Selection is the emphasised selection of rows and menu items
	// (selectedContentBackgroundColor).
	Selection
	// SelectionUnemph is the selection without focus or in an inactive
	// window (unemphasizedSelectedContentBackgroundColor).
	SelectionUnemph
	// OnSelection is text on Selection (alternateSelectedControlTextColor).
	OnSelection
	// TextSelection is the background of selected text, the user's highlight
	// colour (selectedTextBackgroundColor).
	TextSelection
	// TextSelectionUnemph is selected text's background without focus
	// (unemphasizedSelectedTextBackgroundColor).
	TextSelectionUnemph
	// SelectedText is the colour of selected text (selectedTextColor).
	SelectedText
	// FocusRing is the keyboard focus ring (keyboardFocusIndicatorColor).
	FocusRing
	// AltRow is the second colour of striped rows
	// (alternatingContentBackgroundColors[1]).
	AltRow
	// Link is the colour of links (linkColor).
	Link
	// Caret is the text insertion point (textInsertionPointColor).
	Caret
	// FindHighlight marks find results (findHighlightColor).
	FindHighlight
	// Red is systemRedColor.
	Red
	// Orange is systemOrangeColor.
	Orange
	// Yellow is systemYellowColor.
	Yellow
	// Green is systemGreenColor.
	Green
	// Mint is systemMintColor.
	Mint
	// Teal is systemTealColor.
	Teal
	// Cyan is systemCyanColor.
	Cyan
	// Blue is systemBlueColor.
	Blue
	// Indigo is systemIndigoColor.
	Indigo
	// Purple is systemPurpleColor.
	Purple
	// Pink is systemPinkColor.
	Pink
	// Brown is systemBrownColor.
	Brown
	// Gray is systemGrayColor.
	Gray

	// The theme's own roles (SourceTheme) follow, then the roles added since
	// (the end of the list).

	// Field is the average colour of the window's background field (Rex's
	// four-corner gradient, which Chrome paints).
	Field
	// Pane is the opaque fill of a focused pane, a card or a content area.
	// Dark is neutral.
	Pane
	// Raised is the fill of what floats over a pane: popovers, sheets, menus
	// drawn on other systems.
	Raised
	// OnAccent is text and glyphs on Accent: white.
	OnAccent
	// AccentPressed is Accent while pressed (AppKit darkens it by about 10 %
	// in light and lightens it in dark); derived from Accent.
	AccentPressed
	// FillPressed is the body of a pressed push button (measured #0000002B).
	FillPressed
	// FillDisabled is the body of a disabled push button (measured #0000000A).
	FillDisabled
	// InputFill is the body of a text field (textBackgroundColor at 98 %;
	// dark measured #171717F9).
	InputFill
	// PaneHairline is the hairline round a Rex pane.
	PaneHairline
	// PaneHighlight is the light inner rim of an unfocused Rex pane.
	PaneHighlight
	// GlassTint is the translucent fill of an unfocused Rex pane over the
	// field.
	GlassTint
	// Shadow is the colour drop shadows are cast in: elevations use its RGB
	// at their layers' own opacity (ShadowLayer.Color). Its alpha is the
	// phase-5 Palette.Shadow strength, which elevations do not multiply in.
	Shadow
	// Scrim dims what lies under a modal dialog on systems without sheets.
	Scrim
	// SidebarFill is the sidebar and inspector glass at rest, active window
	// (a flat stand-in for the live material).
	SidebarFill
	// SidebarFillInactive is the sidebar in an inactive window.
	SidebarFillInactive
	// SidebarLabel is a sidebar row's text (vibrancy turns labelColor black).
	SidebarLabel
	// SidebarLabelInactive is a sidebar row's text in an inactive window.
	SidebarLabelInactive
	// SidebarHeader is a sidebar group header's text.
	SidebarHeader
	// SidebarHeaderInactive is a sidebar group header's text in an inactive
	// window.
	SidebarHeaderInactive
	// SidebarSel is the emphasised sidebar selection: lighter than Selection,
	// the accent drawn on the glass (it follows the accent).
	SidebarSel
	// SidebarSelUnemph is the sidebar selection while another view has focus.
	SidebarSelUnemph
	// SidebarSelInactive is the sidebar selection in an inactive window.
	SidebarSelInactive
	// SelectionDivider is the line between merged rows of an emphasised
	// selection: 10 % white over Selection (derived).
	SelectionDivider
	// TitleText is the window title in the toolbar.
	TitleText
	// TitleTextInactive is the window title in an inactive window.
	TitleTextInactive
	// CapsuleFill is a toolbar capsule's resting fill (the glass over the
	// content colour).
	CapsuleFill
	// CapsuleFillInactive is a toolbar capsule in an inactive window.
	CapsuleFillInactive
	// CapsuleRim is a toolbar capsule's outermost pixel.
	CapsuleRim
	// CapsuleRimInactive is the rim in an inactive window.
	CapsuleRimInactive
	// CapsuleHighlight is the bright inner rim at a capsule's bottom edge.
	CapsuleHighlight
	// ScrollEdgeBar is the bar under the toolbar once content scrolls under
	// it (and always in an inactive window).
	ScrollEdgeBar
	// ScrollEdgeBarInactive is that bar in an inactive window.
	ScrollEdgeBarInactive
	// ScrollEdgeLine is the one-device-pixel hairline under the bar.
	ScrollEdgeLine
	// AccessoryLine is the line over a bottom accessory's band (a composer).
	AccessoryLine
	// GroupBox is a group box's fill (NSBox primary, untitled; no border).
	GroupBox

	// Roles are append-only: a role's number is part of the API (a theme
	// indexes its colours by it), so new roles go below, in the order they
	// were added (TestRoleNumbersAreStable).

	// ControlText is the text of controls: the labels of checkboxes, radio
	// buttons, switches and buttons, and an editable field's text
	// (controlTextColor, a System role). It equals Label on macOS 27.2.
	ControlText
	// Knob is the knob of a switch or slider: white in both modes (AppKit
	// draws it, no NSColor names it; GnuiShot and AppKitRef measured it).
	Knob
	// FieldOutline is the one-point outline just outside a text field's
	// frame: black at 15 % in light, white at 9 % in dark (#D9D9D9 over
	// white, #323232 over #1E1E1E measured; no NSColor names it).
	FieldOutline
	// ToolbarSymbolInactive is a toolbar symbol in an inactive window
	// (measured #A6A6A6 / #666666: lighter than TitleTextInactive). No
	// NSColor gives it: the nearest, tertiaryLabelColor over the inactive
	// bar, is #BDBDBD / #5C5C5C.
	ToolbarSymbolInactive
	// SelectedRowTextInactive is the text of a selected sidebar row in an
	// inactive window (measured #676767 / #999999: darker in light, lighter
	// in dark than the other rows'). unemphasizedSelectedTextColor is black /
	// white, and secondaryLabelColor over SidebarSelInactive is #747474 /
	// #A3A3A3, so the measured value stays the theme's.
	SelectedRowTextInactive
	// SidebarDetail is a sidebar row's second line over the glass: black at
	// 43 % in light, white at 36 % in dark (#868686 on #ECECEC and #747474 on
	// #262626 measured). It is secondaryLabelColor as the sidebar's vibrancy
	// draws it; the NSColor itself (50 % / 55 %) over the same glass is
	// #767676 / #9D9D9D, off by more than the tolerance, so it is not read.
	SidebarDetail
	// SidebarSearchFill is the fill of a search field on the sidebar's glass
	// (measured #EAEAEA on #ECECEC, #4E4E4E on dark's glass): a capsule.
	SidebarSearchFill
	// SidebarSearchFillInactive is that fill in an inactive window (measured
	// #E5E5E5 / #464646).
	SidebarSearchFillInactive
	// PaneButton is a borderless icon button's symbol (rexui.IconBorderless;
	// Rex's pane-header buttons): secondaryLabelColor's value here, in Rex a
	// quiet grey (#96999B in light, #8A8EA1 in dark) that darkens under the
	// pointer.
	PaneButton
	// PaneButtonHover is that symbol under the pointer and pressed: labelColor
	// here, #3F4246 / #ECEDF3 in Rex.
	PaneButtonHover
	// PaneButtonChip is the colour of the chip a borderless icon button lights
	// up behind its symbol under the pointer (rexui.IconButtonChip gives the
	// strength): the label's black or white here, #2A2030 / white in Rex.
	PaneButtonChip

	// NumRoles is the number of roles.
	NumRoles
)

// The status roles are the system colours under their uses.
const (
	// Danger marks destructive actions and errors.
	Danger = Red
	// Warning marks warnings.
	Warning = Orange
	// Success marks success.
	Success = Green
	// Info marks information.
	Info = Blue
)

// systemColorRole maps each platform.SystemColor to the System role that
// takes its colour. The System roles are not contiguous: the ones added
// after the first release sit at the end of the roles, like every other new
// role (TestSystemColorRoles).
var systemColorRole = [platform.NumSystemColors]Role{
	platform.ColorWindowBackground:                      Window,
	platform.ColorUnderPageBackground:                   UnderPage,
	platform.ColorControlBackground:                     ControlBackground,
	platform.ColorTextBackground:                        TextBackground,
	platform.ColorLabel:                                 Label,
	platform.ColorSecondaryLabel:                        Label2,
	platform.ColorTertiaryLabel:                         Label3,
	platform.ColorQuaternaryLabel:                       Label4,
	platform.ColorQuinaryLabel:                          Label5,
	platform.ColorText:                                  Text,
	platform.ColorPlaceholderText:                       Placeholder,
	platform.ColorDisabledControlText:                   TextDisabled,
	platform.ColorHeaderText:                            HeaderText,
	platform.ColorSystemFill:                            Fill1,
	platform.ColorSecondarySystemFill:                   Fill2,
	platform.ColorTertiarySystemFill:                    Fill3,
	platform.ColorQuaternarySystemFill:                  Fill4,
	platform.ColorQuinarySystemFill:                     Fill5,
	platform.ColorSeparator:                             Separator,
	platform.ColorGrid:                                  Grid,
	platform.ColorControlAccent:                         Accent,
	platform.ColorSelectedContentBackground:             Selection,
	platform.ColorUnemphasizedSelectedContentBackground: SelectionUnemph,
	platform.ColorAlternateSelectedControlText:          OnSelection,
	platform.ColorSelectedTextBackground:                TextSelection,
	platform.ColorUnemphasizedSelectedTextBackground:    TextSelectionUnemph,
	platform.ColorSelectedText:                          SelectedText,
	platform.ColorKeyboardFocusIndicator:                FocusRing,
	platform.ColorAlternatingContentBackground:          AltRow,
	platform.ColorLink:                                  Link,
	platform.ColorTextInsertionPoint:                    Caret,
	platform.ColorFindHighlight:                         FindHighlight,
	platform.ColorSystemRed:                             Red,
	platform.ColorSystemOrange:                          Orange,
	platform.ColorSystemYellow:                          Yellow,
	platform.ColorSystemGreen:                           Green,
	platform.ColorSystemMint:                            Mint,
	platform.ColorSystemTeal:                            Teal,
	platform.ColorSystemCyan:                            Cyan,
	platform.ColorSystemBlue:                            Blue,
	platform.ColorSystemIndigo:                          Indigo,
	platform.ColorSystemPurple:                          Purple,
	platform.ColorSystemPink:                            Pink,
	platform.ColorSystemBrown:                           Brown,
	platform.ColorSystemGray:                            Gray,
	platform.ColorControlText:                           ControlText,
}

// SystemRole is the System role that takes the colour of c.
func SystemRole(c platform.SystemColor) Role { return systemColorRole[c] }

// isSystemRole is true for the roles systemColorRole names.
var isSystemRole = func() (a [NumRoles]bool) {
	for _, r := range systemColorRole {
		a[r] = true
	}
	return
}()

// Source says where a role's colour comes from.
type Source uint8

const (
	// SourceTheme is the theme's own value.
	SourceTheme Source = iota
	// SourceSystem is the system's colour when the platform reads one (macOS),
	// else the theme's value.
	SourceSystem
)

// Source is where r's colour comes from.
func (r Role) Source() Source {
	if r < NumRoles && isSystemRole[r] {
		return SourceSystem
	}
	return SourceTheme
}

// String is the role's name, as in Go.
func (r Role) String() string {
	if r < NumRoles {
		return roleNames[r]
	}
	return "Role(?)"
}

var roleNames = [NumRoles]string{
	"Window", "UnderPage", "ControlBackground", "TextBackground", "Label", "Label2", "Label3", "Label4", "Label5",
	"Text", "Placeholder", "TextDisabled", "HeaderText", "Fill1", "Fill2", "Fill3", "Fill4", "Fill5", "Separator", "Grid",
	"Accent", "Selection", "SelectionUnemph", "OnSelection", "TextSelection", "TextSelectionUnemph", "SelectedText",
	"FocusRing", "AltRow", "Link", "Caret", "FindHighlight",
	"Red", "Orange", "Yellow", "Green", "Mint", "Teal", "Cyan", "Blue", "Indigo", "Purple", "Pink", "Brown", "Gray",
	"Field", "Pane", "Raised", "OnAccent", "AccentPressed", "FillPressed", "FillDisabled", "InputFill",
	"PaneHairline", "PaneHighlight", "GlassTint", "Shadow", "Scrim",
	"SidebarFill", "SidebarFillInactive", "SidebarLabel", "SidebarLabelInactive", "SidebarHeader", "SidebarHeaderInactive",
	"SidebarSel", "SidebarSelUnemph", "SidebarSelInactive", "SelectionDivider",
	"TitleText", "TitleTextInactive", "CapsuleFill", "CapsuleFillInactive", "CapsuleRim", "CapsuleRimInactive", "CapsuleHighlight",
	"ScrollEdgeBar", "ScrollEdgeBarInactive", "ScrollEdgeLine", "AccessoryLine", "GroupBox",
	"ControlText", "Knob", "FieldOutline", "ToolbarSymbolInactive", "SelectedRowTextInactive", "SidebarDetail", "SidebarSearchFill", "SidebarSearchFillInactive",
	"PaneButton", "PaneButtonHover", "PaneButtonChip",
}

// Colors holds a colour per role, 0xRRGGBBAA, indexed by Role.
type Colors [NumRoles]uint32

// MixColors sets dst to the mix of a (t = 0) and b (t = 1), channel by
// channel, alpha included. It allocates nothing.
func MixColors(dst, a, b *Colors, t float32) {
	for i := range dst {
		dst[i] = MixRGBA(a[i], b[i], t)
	}
}

// Measured are the roles as macOS 27.2 draws them, light and dark, indexed by
// Mode: the System roles are NSColor's values (tools/appkitref on the
// MacBook, build 26B5091g, blue accent; the text selection is that user's
// graphite highlight), and the window-chrome roles are the pixels of live
// windows (tools/appkitscenes, docs/design/liquid-glass-reference.md). The
// identity roles (Field, Pane, Raised, PaneHairline, PaneHighlight,
// GlassTint, Shadow, Scrim) are neutral stand-ins that a theme replaces. The
// derived roles (AccentPressed, SelectionDivider) hold what derive makes of
// the blue accent, within 1/255 of the pixels.
// theme/testdata/appkit-ref.json holds the reference they are tested
// against. Treat it as read-only; copy it to derive a theme's roles.
var Measured = [2]Colors{
	{
		Window: 0xFFFFFFFF, UnderPage: 0xF6F6F6FF, ControlBackground: 0xFFFFFFFF, TextBackground: 0xFFFFFFFF,
		Label: 0x000000D8, Label2: 0x0000007F, Label3: 0x00000042, Label4: 0x00000019, Label5: 0x0000000C,
		Text: 0x000000FF, Placeholder: 0x0000007F, TextDisabled: 0x0000003F, HeaderText: 0x000000D8,
		Fill1: 0x00000019, Fill2: 0x00000014, Fill3: 0x0000000C, Fill4: 0x00000007, Fill5: 0x00000002,
		Separator: 0x00000019, Grid: 0xE6E6E6FF, Accent: 0x007AFFFF,
		Selection: 0x0064E1FF, SelectionUnemph: 0xDCDCDCFF, OnSelection: 0xFFFFFFFF,
		TextSelection: 0xE0E0E0FF, TextSelectionUnemph: 0xDCDCDCFF, SelectedText: 0x000000FF,
		FocusRing: 0x0067F47F, AltRow: 0xF4F5F5FF, Link: 0x0068DAFF, Caret: 0x989898FF, FindHighlight: 0xFFFF00FF,
		Red: 0xFF383CFF, Orange: 0xFF8D28FF, Yellow: 0xFFCC00FF, Green: 0x34C759FF, Mint: 0x00C8B3FF, Teal: 0x00C3D0FF,
		Cyan: 0x00C0E8FF, Blue: 0x0088FFFF, Indigo: 0x6155F5FF, Purple: 0xCB30E0FF, Pink: 0xFF2D55FF, Brown: 0xAC7F5EFF, Gray: 0x8E8E93FF,
		ControlText: 0x000000D8,

		Field: 0xFFFFFFFF, Pane: 0xFFFFFFFF, Raised: 0xFFFFFFFF, OnAccent: 0xFFFFFFFF, AccentPressed: 0x006EE5FF,
		FillPressed: 0x0000002B, FillDisabled: 0x0000000A, InputFill: 0xFFFFFFF9, Knob: 0xFFFFFFFF, FieldOutline: 0x00000026,
		PaneHairline: 0x00000019, PaneHighlight: 0xFFFFFF00, GlassTint: 0xFFFFFF00, Shadow: 0x00000033, Scrim: 0x00000000,
		SidebarFill: 0xECECECFF, SidebarFillInactive: 0xF2F2F2FF, SidebarLabel: 0x000000FF, SidebarLabelInactive: 0x727272FF,
		SidebarHeader: 0x878787FF, SidebarHeaderInactive: 0xA6A6A6FF,
		SidebarSel: 0x0070F5FF, SidebarSelUnemph: 0xD7D7D7FF, SidebarSelInactive: 0xE7E7E7FF, SelectionDivider: 0x1A74E4FF,
		TitleText: 0x000000B2, TitleTextInactive: 0xB8B8B8FF,
		CapsuleFill: 0xF9F9F9FF, CapsuleFillInactive: 0xF2F2F2FF, CapsuleRim: 0x00000040, CapsuleRimInactive: 0xC1C1C1FF, CapsuleHighlight: 0xFFFFFFFF,
		ScrollEdgeBar: 0xFFFFFFFF, ScrollEdgeBarInactive: 0xFFFFFFFF, ScrollEdgeLine: 0x0000001A, AccessoryLine: 0x0000000D,
		GroupBox:              0xF7F7F7FF,
		ToolbarSymbolInactive: 0xA6A6A6FF, SelectedRowTextInactive: 0x676767FF, SidebarDetail: 0x0000006E,
		SidebarSearchFill: 0xEAEAEAFF, SidebarSearchFillInactive: 0xE5E5E5FF,
		PaneButton: 0x0000007F, PaneButtonHover: 0x000000D8, PaneButtonChip: 0x000000FF,
	},
	{
		Window: 0x1E1E1EFF, UnderPage: 0x282828FF, ControlBackground: 0x1E1E1EFF, TextBackground: 0x1E1E1EFF,
		Label: 0xFFFFFFD8, Label2: 0xFFFFFF8C, Label3: 0xFFFFFF3F, Label4: 0xFFFFFF19, Label5: 0xFFFFFF0C,
		Text: 0xFFFFFFFF, Placeholder: 0xFFFFFF8C, TextDisabled: 0xFFFFFF3F, HeaderText: 0xFFFFFFFF,
		Fill1: 0xFFFFFF19, Fill2: 0xFFFFFF14, Fill3: 0xFFFFFF0C, Fill4: 0xFFFFFF07, Fill5: 0xFFFFFF02,
		Separator: 0xFFFFFF19, Grid: 0x1A1A1AFF, Accent: 0x007AFFFF,
		Selection: 0x0059D1FF, SelectionUnemph: 0x464646FF, OnSelection: 0xFFFFFFFF,
		TextSelection: 0xFFFFFF3F, TextSelectionUnemph: 0x464646FF, SelectedText: 0xFFFFFFFF,
		FocusRing: 0x1AA9FF7F, AltRow: 0xFFFFFF0C, Link: 0x419CFFFF, Caret: 0x8C8C8CFF, FindHighlight: 0xFFFF00FF,
		Red: 0xFF4245FF, Orange: 0xFF9230FF, Yellow: 0xFFD600FF, Green: 0x30D158FF, Mint: 0x00DAC3FF, Teal: 0x00D2E0FF,
		Cyan: 0x3CD3FEFF, Blue: 0x0091FFFF, Indigo: 0x6D7CFFFF, Purple: 0xDB34F2FF, Pink: 0xFF375FFF, Brown: 0xB78A66FF, Gray: 0x98989DFF,
		ControlText: 0xFFFFFFD8,

		Field: 0x1E1E1EFF, Pane: 0x1E1E1EFF, Raised: 0x2A2A2AFF, OnAccent: 0xFFFFFFFF, AccentPressed: 0x1A88FFFF,
		FillPressed: 0xFFFFFF2B, FillDisabled: 0xFFFFFF0A, InputFill: 0x171717F9, Knob: 0xFFFFFFFF, FieldOutline: 0xFFFFFF17,
		PaneHairline: 0xFFFFFF19, PaneHighlight: 0xFFFFFF00, GlassTint: 0x1E1E1E00, Shadow: 0x00000066, Scrim: 0x00000000,
		SidebarFill: 0x2A2A2AFF, SidebarFillInactive: 0x252525FF, SidebarLabel: 0xFFFFFFFF, SidebarLabelInactive: 0x929292FF,
		SidebarHeader: 0x737373FF, SidebarHeaderInactive: 0x585858FF,
		SidebarSel: 0x007AFFFF, SidebarSelUnemph: 0x434343FF, SidebarSelInactive: 0x333333FF, SelectionDivider: 0x1A6AD6FF,
		TitleText: 0xFFFFFFE6, TitleTextInactive: 0x5C5C5CFF,
		CapsuleFill: 0x3A3A3AFF, CapsuleFillInactive: 0x333333FF, CapsuleRim: 0x0D0D0DFF, CapsuleRimInactive: 0x131313FF, CapsuleHighlight: 0x787878FF,
		ScrollEdgeBar: 0x292929FF, ScrollEdgeBarInactive: 0x262626FF, ScrollEdgeLine: 0xFFFFFF12, AccessoryLine: 0xFFFFFF0D,
		GroupBox:              0x252525FF,
		ToolbarSymbolInactive: 0x666666FF, SelectedRowTextInactive: 0x999999FF, SidebarDetail: 0xFFFFFF5C,
		SidebarSearchFill: 0x4E4E4EFF, SidebarSearchFillInactive: 0x464646FF,
		PaneButton: 0xFFFFFF8C, PaneButtonHover: 0xFFFFFFD8, PaneButtonChip: 0xFFFFFFFF,
	},
}

// defaultAccent is the accent the measured window-chrome colours were taken
// with (the blue, AppleAccentColor 4).
const defaultAccent = 0x007AFFFF

// derive sets the roles that follow others in mode m (0 light, 1 dark):
// AccentPressed and SidebarSel from Accent, SelectionDivider from Selection.
// sidebarSel is the measured SidebarSel, kept while the accent is the blue it
// was measured with.
func derive(c *Colors, m int, sidebarSel uint32) {
	acc := c[Accent] | 0xFF
	if m == 0 {
		c[AccentPressed] = Over(0x0000001A, acc)
	} else {
		c[AccentPressed] = Over(0xFFFFFF1A, acc)
	}
	c[SelectionDivider] = Over(0xFFFFFF1A, c[Selection]|0xFF)
	if acc == defaultAccent {
		c[SidebarSel] = sidebarSel
	} else {
		c[SidebarSel] = acc
	}
}
