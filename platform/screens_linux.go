//go:build linux

package platform

// Screens returns nil on Linux: the displays are not read yet (X11 RandR and the
// Wayland outputs are not wired), and Wayland clients cannot place their windows
// anyway. Callers then keep a saved window size and leave the position to the
// compositor (winstate does).
func Screens() []ScreenInfo { return nil }

// ScreensInto returns dst unchanged on Linux (Screens).
func ScreensInto(dst []ScreenInfo) []ScreenInfo { return dst }
