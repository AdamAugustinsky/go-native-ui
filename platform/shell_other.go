//go:build !darwin

package platform

// The native shell (Options.Backdrop, Options.Toolbar) is macOS only so far:
// elsewhere the window has neither and these do nothing.

// Backdrop is the material the window got (Options.Backdrop): always
// BackdropNone here. Experimental.
func (w *Window) Backdrop() Backdrop { return BackdropNone }

// HasToolbar reports whether the window has a native toolbar
// (Options.Toolbar): never here. Experimental.
func (w *Window) HasToolbar() bool { return false }

// SetToolbarTabs replaces the tabs of a ToolbarTabs item: a no-op here.
// Experimental.
func (w *Window) SetToolbarTabs(id uint16, tabs []ToolbarTab, selected int) {}

// SetToolbarEnabled enables or disables a toolbar item: a no-op here.
// Experimental.
func (w *Window) SetToolbarEnabled(id uint16, enabled bool) {}
