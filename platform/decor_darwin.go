package platform

// The calls that serve a titlebar the app draws (Linux's client-side
// decorations, Windows' drawn titlebar: decor_wl_linux.go, chrome_windows.go)
// have nothing to do on macOS, where AppKit draws the decorations and the
// traffic lights.

// ClientDecorations is always false on macOS: the OS draws the decorations.
func (w *Window) ClientDecorations() bool { return false }

// Maximized is not tracked on macOS and reports false (Zoomed reports the
// zoom).
func (w *Window) Maximized() bool { return false }

// Minimize does nothing on macOS (Miniaturize does).
func (w *Window) Minimize() {}

// ToggleMaximize does nothing on macOS (Zoom does).
func (w *Window) ToggleMaximize() {}

// RequestClose does nothing on macOS.
func (w *Window) RequestClose() {}
