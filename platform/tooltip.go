package platform

import (
	"slices"

	"gonativeui/internal/uitypes"
)

// Native tooltips (phase 6, N2, D41). The window registers rectangles of its
// content with the system (macOS: addToolTipRect:owner:userData:), each with a
// key, and the system asks for the text of a key only when it shows a tooltip:
// the delay, look and placement past the window edge are the system's. The
// text comes from the function given to SetTooltipText.
//
// Elsewhere (Windows, Linux so far) NativeTooltips is false and the calls do
// nothing: the widgets draw their own tooltips.

// TooltipRegion is a rectangle of the window content (x0, y0, x1, y1 in
// physical pixels, origin top left) that shows the tooltip of Key. Later
// entries are on top.
type TooltipRegion = uitypes.TooltipRegion

// MaxTooltipRegions bounds the regions a window registers; more are ignored.
const MaxTooltipRegions = 256

// tooltipMap is the window's tooltip regions as last given. set copies into
// storage the map owns and reports whether anything changed, so a steady
// stream of identical lists costs a compare and no allocation. flatten cuts
// the list into rectangles that do not overlap: AppKit does not say which of
// two overlapping tooltip rects wins, so none overlap when they reach it.
type tooltipMap struct {
	regs  []TooltipRegion
	flat  []TooltipRegion
	sweep rectSweep
}

func (m *tooltipMap) set(r []TooltipRegion) bool {
	r = r[:min(len(r), MaxTooltipRegions)]
	if slices.Equal(m.regs, r) {
		return false
	}
	m.regs = copyRegions(m.regs, r)
	return true
}

// flatten returns the regions as rectangles with no overlap and the same key
// at every point: each region keeps only what no later (higher) region covers.
// Empty pieces and keyless regions are dropped, and a keyless region cuts
// nothing out of the ones beneath it. The result is owned by the map
// and valid until the next call.
func (m *tooltipMap) flatten() []TooltipRegion {
	m.flat = flattenRects(&m.sweep, m.flat, m.regs, nil, 4*MaxTooltipRegions,
		func(r TooltipRegion) ([4]float32, uint64) {
			if r.Key == 0 {
				return [4]float32{}, 0
			}
			return r.Rect, r.Key
		},
		func(r [4]float32, v uint64) TooltipRegion { return TooltipRegion{Rect: r, Key: v} })
	return m.flat
}
