package platform

// The starting point (see appearance.go): the system appearance is not read
// yet, and the app shows light.

// SystemAppearance returns the current system appearance (see appearance.go).
func SystemAppearance() Appearance { return Appearance{} }

// AppearanceSerial counts system appearance changes (see appearance.go).
func AppearanceSerial() uint32 { return 0 }

// SetDarkChrome makes the OS-drawn parts of the window match the app's mode
// (see appearance.go).
func (w *Window) SetDarkChrome(dark bool) {}
