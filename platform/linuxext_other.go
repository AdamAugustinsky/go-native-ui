//go:build !linux

package platform

// Linux-only window API (see decor_wl_linux.go and export_linux.go), present
// here as no-ops so portable code compiles everywhere. On these systems the OS
// decorates the window (ClientDecorations is false), so the calls that serve
// client-side decorations have nothing to do.

// ResizeEdges are the window edges a ResizeRegion drags.
type ResizeEdges uint8

const (
	EdgeTop ResizeEdges = 1 << iota
	EdgeBottom
	EdgeLeft
	EdgeRight
)

// ResizeRegion is a rectangle where a press starts a resize by Edges
// (Linux client-side decorations).
type ResizeRegion struct {
	Rect  [4]float32
	Edges ResizeEdges
}

// SetResizeRegions does nothing off Linux.
func (w *Window) SetResizeRegions([]ResizeRegion) {}

// ClientDecorations is always false off Linux: the OS draws the decorations.
func (w *Window) ClientDecorations() bool { return false }

// Maximized is not tracked off Linux and reports false.
func (w *Window) Maximized() bool { return false }

// Minimize does nothing off Linux.
func (w *Window) Minimize() {}

// ToggleMaximize does nothing off Linux.
func (w *Window) ToggleMaximize() {}

// RequestClose does nothing off Linux.
func (w *Window) RequestClose() {}

// SetShadowInsets does nothing off Linux.
func (w *Window) SetShadowInsets(left, top, right, bottom float32) {}

// ExternalHandle reports no handle off Linux.
func (w *Window) ExternalHandle() (string, bool) { return "", false }
