//go:build !darwin

package platform

// SetAXSource returns ErrAXUnsupported: this OS has no bridge yet (UI
// Automation on Windows and AT-SPI on Linux are later work).
func (w *Window) SetAXSource(AXSource) error { return ErrAXUnsupported }

// AXActive reports false: there is no bridge on this OS.
func (w *Window) AXActive() bool { return false }

// SyncAX does nothing on this OS.
func (w *Window) SyncAX() {}
