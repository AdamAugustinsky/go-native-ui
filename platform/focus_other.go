//go:build !darwin

package platform

// Focused reports whether the window has the keyboard focus. Elsewhere the
// window is assumed to have it from the start, and EvFocus corrects it.
func (w *Window) Focused() bool { return true }

// DebugAssumeKey does nothing here: the window is assumed to have the focus
// from the start (Focused). For tests only.
func DebugAssumeKey(w *Window) (restore func()) { return func() {} }
