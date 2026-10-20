//go:build !darwin

package platform

// Native menus are macOS only: elsewhere the app draws its menus.

// HasNativeMenus reports whether the OS has native menus: false here.
func HasNativeMenus() bool { return false }

// SetMenuBar installs a native menu bar: a no-op here.
func (w *Window) SetMenuBar(spec *MenuSpec, state MenuState) {}

// UseMenuBar does nothing here (no native menu bar).
func (w *Window) UseMenuBar(state MenuState) {}

// SetFieldKeys sets the focused field's claim on key equivalents: a no-op
// here, where no native menu takes keys.
func (w *Window) SetFieldKeys(f FieldKeys) {}

// PopUpMenu shows a native menu: there is none here, so nothing is chosen.
func (w *Window) PopUpMenu(p *Popup) int { return -1 }

// DebugOpenBarMenu opens a menu of the native menu bar (a screenshot hook):
// there is none here.
func (w *Window) DebugOpenBarMenu(i int) bool { return false }
