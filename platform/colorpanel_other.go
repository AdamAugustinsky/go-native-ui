//go:build !darwin

package platform

// NativeColorPanel reports whether the OS has a colour panel the app can
// drive: false here.
const NativeColorPanel = false

// ShowColorPanel is not available here: it returns ErrUnsupported.
func (w *Window) ShowColorPanel(r, g, b, a float32) error { return ErrUnsupported }

// SetColorPanelColor is not available here: it returns ErrUnsupported.
func (w *Window) SetColorPanelColor(r, g, b, a float32) error { return ErrUnsupported }

// CloseColorPanel does nothing here.
func (w *Window) CloseColorPanel() {}
