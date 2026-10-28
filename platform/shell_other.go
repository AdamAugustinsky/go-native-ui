//go:build !darwin

package platform

// The native shell (Options.Backdrop, Options.Buttons, material regions and
// hosts) is macOS only so far: elsewhere the window has none of it, these do
// nothing, and the app draws everything itself.

// Backdrop is the material the window got (Options.Backdrop): always
// BackdropNone here. Experimental.
func (w *Window) Backdrop() Backdrop { return BackdropNone }

// HasButtons reports whether the window has native buttons
// (Options.Buttons): never here. Experimental.
func (w *Window) HasButtons() bool { return false }

// SetButtonEnabled enables or disables a native button: a no-op here.
// Experimental.
func (w *Window) SetButtonEnabled(id uint16, enabled bool) {}

// HasMaterials reports whether the window shows material regions
// (Options.Materials): never here.
func (w *Window) HasMaterials() bool { return false }

// SetMaterials sets the window's material regions: a no-op here.
func (w *Window) SetMaterials(r []MaterialRegion) {}

// SetPanelOrigin moves a child panel: a no-op here, which has none.
func (w *Window) SetPanelOrigin(x, y float32) {}

// SetPanelMask shapes a panel's material: ErrNoPanels here.
func (w *Window) SetPanelMask(png []byte) error { return ErrNoPanels }

// NewHost creates a hosted native view: ErrNoHosts here.
func (w *Window) NewHost(kind HostKind, o HostOptions) (HostID, error) { return 0, ErrNoHosts }

// ReleaseHost releases a host: a no-op here.
func (w *Window) ReleaseHost(id HostID) {}

// HostView is the NSView of a host: 0 here.
func (w *Window) HostView(id HostID) uintptr { return 0 }

// SetHosts sets where the hosts are: a no-op here.
func (w *Window) SetHosts(r []HostRegion) {}

// FocusHost gives the keyboard focus to a host: a no-op here.
func (w *Window) FocusHost(id HostID) {}

// FlushShell applies staged shell changes: a no-op here.
func (w *Window) FlushShell() {}

// ShellPending reports staged shell changes: never here.
func ShellPending() bool { return false }

// ApplyShell applies staged shell changes: a no-op here.
func ApplyShell() {}

// ShellObjects counts the native shell objects alive: none here.
func ShellObjects() ShellCounts { return ShellCounts{} }
