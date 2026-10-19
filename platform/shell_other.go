//go:build !darwin

package platform

// The native shell (Options.Backdrop, Options.Buttons) is macOS only so far:
// elsewhere the window has neither and these do nothing.

// Backdrop is the material the window got (Options.Backdrop): always
// BackdropNone here. Experimental.
func (w *Window) Backdrop() Backdrop { return BackdropNone }

// HasButtons reports whether the window has native buttons
// (Options.Buttons): never here. Experimental.
func (w *Window) HasButtons() bool { return false }

// SetButtonEnabled enables or disables a native button: a no-op here.
// Experimental.
func (w *Window) SetButtonEnabled(id uint16, enabled bool) {}
