//go:build !darwin && !windows

package platform

// Window commands (wincmd_darwin.go, wincmd_windows.go) on Linux, where the
// OS has one window per app so far (D30 and D68 are built for macOS and
// Windows): the window's own state stays as the user and the OS set it, and
// the commands do nothing.

// Retire does nothing here: no OS waits for a quit answer inside the pump.
func (w *Window) Retire() {}

// SetRepresentedFile does nothing here: only macOS has a proxy icon.
func (w *Window) SetRepresentedFile(path string) {}

// SetRepresentedURL does nothing here: only macOS has a proxy icon.
func (w *Window) SetRepresentedURL(url string) {}

// FrameRect reports no frame here yet (all zero).
func (w *Window) FrameRect() (x, y, width, height float64) { return 0, 0, 0, 0 }

// SetFrameRect does nothing here yet.
func (w *Window) SetFrameRect(x, y, width, height float64) {}

// ToggleFullScreen does nothing here yet.
func (w *Window) ToggleFullScreen() {}

// FullScreen reports false here.
func (w *Window) FullScreen() bool { return false }

// Miniaturize minimises the window where the OS lets the app do it
// (Minimize).
func (w *Window) Miniaturize() { w.Minimize() }

// Miniaturized reports false here.
func (w *Window) Miniaturized() bool { return false }

// Zoom does nothing here yet.
func (w *Window) Zoom() {}

// Zoomed reports false here.
func (w *Window) Zoomed() bool { return false }

// Activate does nothing here yet.
func (w *Window) Activate() {}

// Hide does nothing here yet.
func (w *Window) Hide() {}

// Visible reports true here.
func (w *Window) Visible() bool { return true }

// SetDocumentEdited does nothing here.
func (w *Window) SetDocumentEdited(edited bool) {}

// RequestAttention does nothing here and returns 0.
func RequestAttention(critical bool) int { return 0 }

// CancelAttention does nothing here.
func CancelAttention(id int) {}

// Wake wakes the loop for the window: with one window per app, Wake.
func (w *Window) Wake() { Wake() }

// WakeLoop makes the loop turn once: with one window per app, Wake.
func WakeLoop() { Wake() }
