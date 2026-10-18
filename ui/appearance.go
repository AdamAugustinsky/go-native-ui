package ui

// Appearance is the system's light or dark mode and accent colour, as the app
// hands them to the UI before each frame (platform.SystemAppearance; D32).
type Appearance struct {
	// Dark is true when the system is in dark mode.
	Dark   bool
	Accent uint32 // RGBA8, 0xRRGGBBAA; 0 when the system has none or it is not known
	// Known is false when the system could not be asked (yet); show light.
	Known bool
}

// appearance is the Context's part of D32: what the system says, and what the
// app shows (for the parts of the window the OS draws).
type appearance struct {
	sys            Appearance
	chromeDark     bool // the mode the app shows (SetChromeDark)
	chromeDeclared bool // SetChromeDark was called
}

// SetSystemAppearance gives the UI the system appearance for the next frame.
// The app calls it before Begin (app.Run does, every frame: the platform's
// value is a cached load, and a change wakes the loop).
func (c *Context) SetSystemAppearance(a Appearance) { c.ap.sys = a }

// SystemAppearance is the system's light or dark mode and accent colour. Apps
// that follow the system read it each frame; the first frame already has it,
// so it is drawn in the right mode (no light flash in dark mode), except on
// Linux when the desktop portal has not answered within 25 ms (Known false;
// see platform.SystemAppearance).
func (c *Context) SystemAppearance() Appearance { return c.ap.sys }

// SetChromeDark declares the mode the app is showing, so that the parts of the
// window the OS draws (the Windows title bar, the macOS traffic lights and
// menus) match it, also when the app forces a mode other than the system's.
// Call it in any frame, typically every frame from the theme layer; an app
// that never calls it leaves those parts as the OS has them.
func (c *Context) SetChromeDark(dark bool) {
	c.ap.chromeDark, c.ap.chromeDeclared = dark, true
}

// ChromeDark reports the mode last declared with SetChromeDark; ok is false
// when none was. The app forwards it to the window after the frame.
func (c *Context) ChromeDark() (dark, ok bool) { return c.ap.chromeDark, c.ap.chromeDeclared }
