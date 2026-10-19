package platform

// System appearance (phase 5, D32): light or dark mode and the accent colour,
// read when the app starts and followed while it runs.
//
// Each OS implements, in appearance_<os>.go:
//
//	SystemAppearance() Appearance
//	AppearanceSerial() uint32
//	(*Window).SetDarkChrome(dark bool)
//
// SystemAppearance returns the current appearance. It is cheap enough to
// call on every frame: a cached value, refreshed when the system reports a
// change. Its first call must not delay the first frame by more than a few
// milliseconds, because the first frame is drawn in the mode it returns
// (content-first: a dark-mode user should not see a light first frame). On
// Linux the first call waits at most 25 ms for the desktop portal; a portal
// slower than that (one started on demand) gives Known false, a light first
// frame, and a crossfade when the answer arrives (D32). Main thread.
//
// AppearanceSerial counts changes of the system appearance. When the system
// reports one, the serial changes and Wake is called, so an idle app wakes
// once, compares the serial and reads SystemAppearance again. Following the
// system costs no wakeups while nothing changes. Any thread.
//
// SetDarkChrome makes the parts of the window the OS draws (the Windows
// title bar, the macOS traffic lights and menus) match the app's mode, which
// can differ from the system's when the app forces one. A no-op where the OS
// draws nothing. Main thread.

// System values (phase 6, D39): the system's semantic colours in both
// appearances and its accessibility and scroller settings, for the colour
// roles of theme's tokens v2. Each OS implements:
//
//	ReadSystemValues(dst *SystemValues)
//
// ReadSystemValues fills dst. On macOS it reads the colours from NSColor in
// the light and the dark appearance (the high-contrast ones when Increase
// Contrast is on), and the settings from NSWorkspace and NSScroller. It
// reads only once the app follows the system appearance (SystemAppearance
// has been called); before that, and on Windows and Linux, dst.Known is false
// and the theme uses its own values. The app of a live window calls it on the
// main thread and hands the values to its ui.Context (app.Run does when
// AppearanceSerial changes); themes take them from there, never from here,
// so an offscreen context on any thread keeps its own values. The values are
// cached and read again after a change, which
// bumps AppearanceSerial and calls Wake like any appearance change: the
// colour, highlight, appearance, accessibility-display and scroller-style
// notifications. The startup read is about 90 colours and well under a
// millisecond; a re-read allocates nothing. Main thread.

// SystemColor indexes SystemValues.Colors. The order is that of theme's
// System roles (theme.Role), which take these values one to one.
type SystemColor uint8

// The system colours, each the NSColor of the same name in the comment.
const (
	ColorWindowBackground                      SystemColor = iota // windowBackgroundColor
	ColorUnderPageBackground                                      // underPageBackgroundColor
	ColorControlBackground                                        // controlBackgroundColor
	ColorTextBackground                                           // textBackgroundColor
	ColorLabel                                                    // labelColor
	ColorSecondaryLabel                                           // secondaryLabelColor
	ColorTertiaryLabel                                            // tertiaryLabelColor
	ColorQuaternaryLabel                                          // quaternaryLabelColor
	ColorQuinaryLabel                                             // quinaryLabelColor
	ColorText                                                     // textColor
	ColorPlaceholderText                                          // placeholderTextColor
	ColorDisabledControlText                                      // disabledControlTextColor
	ColorHeaderText                                               // headerTextColor
	ColorSystemFill                                               // systemFillColor
	ColorSecondarySystemFill                                      // secondarySystemFillColor
	ColorTertiarySystemFill                                       // tertiarySystemFillColor
	ColorQuaternarySystemFill                                     // quaternarySystemFillColor
	ColorQuinarySystemFill                                        // quinarySystemFillColor
	ColorSeparator                                                // separatorColor
	ColorGrid                                                     // gridColor
	ColorControlAccent                                            // controlAccentColor
	ColorSelectedContentBackground                                // selectedContentBackgroundColor
	ColorUnemphasizedSelectedContentBackground                    // unemphasizedSelectedContentBackgroundColor
	ColorAlternateSelectedControlText                             // alternateSelectedControlTextColor
	ColorSelectedTextBackground                                   // selectedTextBackgroundColor (the user's highlight colour)
	ColorUnemphasizedSelectedTextBackground                       // unemphasizedSelectedTextBackgroundColor
	ColorSelectedText                                             // selectedTextColor
	ColorKeyboardFocusIndicator                                   // keyboardFocusIndicatorColor
	ColorAlternatingContentBackground                             // alternatingContentBackgroundColors[1]
	ColorLink                                                     // linkColor
	ColorTextInsertionPoint                                       // textInsertionPointColor
	ColorFindHighlight                                            // findHighlightColor
	ColorSystemRed                                                // systemRedColor
	ColorSystemOrange                                             // systemOrangeColor
	ColorSystemYellow                                             // systemYellowColor
	ColorSystemGreen                                              // systemGreenColor
	ColorSystemMint                                               // systemMintColor
	ColorSystemTeal                                               // systemTealColor
	ColorSystemCyan                                               // systemCyanColor
	ColorSystemBlue                                               // systemBlueColor
	ColorSystemIndigo                                             // systemIndigoColor
	ColorSystemPurple                                             // systemPurpleColor
	ColorSystemPink                                               // systemPinkColor
	ColorSystemBrown                                              // systemBrownColor
	ColorSystemGray                                               // systemGrayColor
	ColorControlText                                              // controlTextColor
	// NumSystemColors is the number of system colours.
	NumSystemColors
)

// SystemColorNames are the names of the NSColor class methods the system
// colours are read from, indexed by SystemColor. ColorAlternatingContentBackground
// is the second colour of alternatingContentBackgroundColors.
var SystemColorNames = [NumSystemColors]string{
	"windowBackgroundColor", "underPageBackgroundColor", "controlBackgroundColor", "textBackgroundColor",
	"labelColor", "secondaryLabelColor", "tertiaryLabelColor", "quaternaryLabelColor", "quinaryLabelColor",
	"textColor", "placeholderTextColor", "disabledControlTextColor", "headerTextColor",
	"systemFillColor", "secondarySystemFillColor", "tertiarySystemFillColor", "quaternarySystemFillColor", "quinarySystemFillColor",
	"separatorColor", "gridColor", "controlAccentColor",
	"selectedContentBackgroundColor", "unemphasizedSelectedContentBackgroundColor", "alternateSelectedControlTextColor",
	"selectedTextBackgroundColor", "unemphasizedSelectedTextBackgroundColor", "selectedTextColor",
	"keyboardFocusIndicatorColor", "alternatingContentBackgroundColors", "linkColor", "textInsertionPointColor", "findHighlightColor",
	"systemRedColor", "systemOrangeColor", "systemYellowColor", "systemGreenColor", "systemMintColor", "systemTealColor",
	"systemCyanColor", "systemBlueColor", "systemIndigoColor", "systemPurpleColor", "systemPinkColor", "systemBrownColor", "systemGrayColor",
	"controlTextColor",
}

// SystemValues are the system's colours and settings (ReadSystemValues).
type SystemValues struct {
	// Known is false when nothing was read: not macOS, or the app does not
	// follow the system appearance (yet). The other fields are then zero.
	Known bool
	// Colors are the system colours in the light (0) and the dark (1)
	// appearance, sRGB 0xRRGGBBAA with the colour's own alpha. With
	// IncreaseContrast they are the high-contrast appearances' colours. A
	// colour the system does not have (an older macOS) is 0.
	Colors [2][NumSystemColors]uint32
	// ReduceMotion, ReduceTransparency and IncreaseContrast are the
	// accessibility display settings of the same names.
	ReduceMotion, ReduceTransparency, IncreaseContrast bool
	// LegacyScrollers is true when the preferred scroller style is legacy
	// (always-visible scroll bars that take space) rather than overlay.
	LegacyScrollers bool
}

// Appearance is the system's light or dark mode and its accent colour.
type Appearance struct {
	// Dark is true when the system is in dark mode.
	Dark bool
	// Accent is the user's accent colour, RGBA8 (0xRRGGBBAA); 0 when the
	// system has none or it is not known.
	Accent uint32
	// Known is false when the system could not be asked (Linux without a
	// desktop portal, or while the first answer is still on its way); Dark
	// is then false and the app shows light.
	Known bool
}
