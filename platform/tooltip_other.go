//go:build !darwin

package platform

// NativeTooltips reports whether the window system shows tooltips for the
// regions given to SetTooltipRegions: false here, so widgets draw their own.
const NativeTooltips = false

// SetTooltipRegions does nothing here: tooltips are drawn by the widgets.
func (w *Window) SetTooltipRegions([]TooltipRegion) {}

// SetTooltipText does nothing here: tooltips are drawn by the widgets.
func (w *Window) SetTooltipText(func(key uint64, dst []byte) []byte) {}

// FinishRegionFrame does nothing where native rects do not need registration.
func (w *Window) FinishRegionFrame() {}
