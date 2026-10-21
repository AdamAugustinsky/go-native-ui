package platform

import "slices"

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
type TooltipRegion struct {
	// Rect is x0, y0, x1, y1 in physical pixels.
	Rect [4]float32
	// Key identifies the tooltip; it is what the text function is asked for.
	// 0 is not a key: such a region is ignored.
	Key uint64
}

// MaxTooltipRegions bounds the regions a window registers; more are ignored.
const MaxTooltipRegions = 256

// tooltipMap is the window's tooltip regions as last given. set copies into
// storage the map owns and reports whether anything changed, so a steady
// stream of identical lists costs a compare and no allocation. flatten cuts
// the list into rectangles that do not overlap: AppKit does not say which of
// two overlapping tooltip rects wins, so none overlap when they reach it.
type tooltipMap struct {
	regs     []TooltipRegion
	flat     []TooltipRegion
	cur, nxt [][4]float32
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
	m.flat = m.flat[:0]
	for i, r := range m.regs {
		if r.Key == 0 || r.Rect[0] >= r.Rect[2] || r.Rect[1] >= r.Rect[3] {
			continue
		}
		m.cur = append(m.cur[:0], r.Rect)
		for _, hi := range m.regs[i+1:] {
			if hi.Key == 0 || hi.Rect[0] >= hi.Rect[2] || hi.Rect[1] >= hi.Rect[3] {
				continue // empty or keyless: it shows no tooltip, so it hides none
			}
			m.nxt = m.nxt[:0]
			for _, p := range m.cur {
				m.nxt = appendCut(m.nxt, p, hi.Rect)
			}
			m.cur, m.nxt = m.nxt, m.cur
			if len(m.cur) == 0 {
				break
			}
		}
		for _, p := range m.cur {
			if len(m.flat) >= 4*MaxTooltipRegions {
				return m.flat
			}
			m.flat = append(m.flat, TooltipRegion{Rect: p, Key: r.Key})
		}
	}
	return m.flat
}

// appendCut appends to dst what is left of p when cut is taken out of it: up
// to four rectangles (above, below, left and right of the cut).
func appendCut(dst [][4]float32, p, cut [4]float32) [][4]float32 {
	if !overlaps(p, cut) {
		return append(dst, p)
	}
	if p[1] < cut[1] { // above
		dst = append(dst, [4]float32{p[0], p[1], p[2], cut[1]})
	}
	if p[3] > cut[3] { // below
		dst = append(dst, [4]float32{p[0], cut[3], p[2], p[3]})
	}
	y0, y1 := max(p[1], cut[1]), min(p[3], cut[3])
	if p[0] < cut[0] { // left of the cut, between its top and bottom
		dst = append(dst, [4]float32{p[0], y0, cut[0], y1})
	}
	if p[2] > cut[2] { // right
		dst = append(dst, [4]float32{cut[2], y0, p[2], y1})
	}
	return dst
}
