package app

import (
	"gonativeui/platform"
	"gonativeui/ui"
)

// syncAppearance hands the system appearance to the UI for the frame about to
// be built (D32). platform.SystemAppearance is a cached load, so this runs
// every frame; a change wakes the loop (the platform calls Wake), and the
// frame that follows reads it. The first frame reads it too, so it is drawn
// in the system's mode.
func (a *App) syncAppearance() {
	s := platform.SystemAppearance()
	a.UI.SetSystemAppearance(ui.Appearance{Dark: s.Dark, Accent: s.Accent, Known: s.Known})
}

// chromeTarget is the part of platform.Window that syncChrome drives.
type chromeTarget interface{ SetDarkChrome(dark bool) }

// syncChrome makes the parts of the window the OS draws follow the mode the
// UI declared (ui.Context.SetChromeDark), once per change: setting it is an
// OS call (NSWindow appearance, a DWM attribute). Until the UI declares a
// mode the window keeps what the OS gives it.
func (a *App) syncChrome(win chromeTarget) {
	dark, ok := a.UI.ChromeDark()
	if !ok || (a.chromeSet && dark == a.chromeDark) {
		return
	}
	a.chromeDark, a.chromeSet = dark, true
	win.SetDarkChrome(dark)
}
