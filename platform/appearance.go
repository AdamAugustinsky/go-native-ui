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
