package platform

import "slices"

// CursorRegion is a rectangle of the window content (x0, y0, x1, y1 in
// physical pixels, origin top left) in which the pointer shows Cursor.
type CursorRegion struct {
	// Rect is x0, y0, x1, y1 in physical pixels.
	Rect [4]float32
	// Cursor is the shape shown inside Rect.
	Cursor Cursor
}

// DragRegion is a rectangle of the window's drag map (pixels, origin top
// left). Block false is an area where pressing the left button moves the
// window (a custom titlebar); Block true is a rectangle above it that keeps
// its presses for the app (a tab, a button).
type DragRegion struct {
	// Rect is x0, y0, x1, y1 in physical pixels.
	Rect [4]float32
	// Block true makes the rectangle keep its presses; false makes it a window-drag area.
	Block bool
}

// copyRegions copies src into dst's storage. Storage that is too small is
// replaced by one twice the size: a copy made by append is exactly as large
// as src, and a list one entry longer the next frame would allocate again
// (vlistbench's allocation gate caught it in a measured window).
func copyRegions[T any](dst, src []T) []T {
	if cap(dst) < len(src) {
		dst = make([]T, 0, 2*len(src))
	}
	return append(dst[:0], src...)
}

func inRect(r [4]float32, x, y float32) bool {
	return x >= r[0] && x < r[2] && y >= r[1] && y < r[3]
}

// cursorMap is the window's cursor state: the region list, whose last
// matching entry wins (it is the topmost box), and the override that beats
// the regions while it is not the arrow (a drag in progress). Setters copy
// into storage owned by the map and report whether anything changed, so a
// steady stream of identical lists costs a compare and no allocation.
type cursorMap struct {
	regs     []CursorRegion
	override Cursor
	flat     []CursorRegion // flatten's output, reused
	holes    [][4]float32   // rectangles left out of every region: hosted native views, which have their own cursors
}

// setHoles sets the rectangles flatten leaves out of the regions (copied) and
// reports whether they changed.
func (m *cursorMap) setHoles(h [][4]float32) bool {
	if slices.Equal(m.holes, h) {
		return false
	}
	m.holes = copyRegions(m.holes, h)
	return true
}

func (m *cursorMap) setRegions(r []CursorRegion) bool {
	if slices.Equal(m.regs, r) {
		return false
	}
	m.regs = copyRegions(m.regs, r)
	return true
}

func (m *cursorMap) setOverride(c Cursor) bool {
	if c >= numCursors {
		c = CursorArrow
	}
	if c == m.override {
		return false
	}
	m.override = c
	return true
}

// at is the cursor at pixel (x, y): the override if there is one, else the
// topmost region containing the point, else the arrow.
func (m *cursorMap) at(x, y float32) Cursor {
	if m.override != CursorArrow {
		return m.override
	}
	for i := len(m.regs) - 1; i >= 0; i-- {
		if inRect(m.regs[i].Rect, x, y) {
			return m.regs[i].Cursor
		}
	}
	return CursorArrow
}

// dragMap is the window's drag map (see DragRegion).
type dragMap struct{ regs []DragRegion }

func (m *dragMap) set(r []DragRegion) bool {
	if slices.Equal(m.regs, r) {
		return false
	}
	m.regs = copyRegions(m.regs, r)
	return true
}

// at reports whether a press at pixel (x, y) moves the window: the last
// rectangle containing the point is a drag area, not a block.
func (m *dragMap) at(x, y float32) bool {
	for i := len(m.regs) - 1; i >= 0; i-- {
		if inRect(m.regs[i].Rect, x, y) {
			return !m.regs[i].Block
		}
	}
	return false
}

// maxFlat bounds the pieces flatten produces.
const maxFlat = 512

// flatten returns the regions as rectangles that do not overlap, with the same
// cursor at every point: each region keeps only what no later (higher) region
// covers. AppKit's behaviour for overlapping cursor rects of one view is not
// specified, so none are handed to it. Empty results are dropped; if the pieces
// would exceed maxFlat the remaining regions are added whole, except that no
// piece is ever left over a hole: one that would have to be split is dropped.
// The result is owned by the map and valid until the next call.
func (m *cursorMap) flatten() []CursorRegion {
	m.flat = m.flat[:0]
	for i, r := range m.regs {
		start := len(m.flat)
		m.flat = append(m.flat, r)
		for _, hi := range m.regs[i+1:] {
			m.cut(start, r.Cursor, hi.Rect, false)
		}
		for _, h := range m.holes {
			m.cut(start, r.Cursor, h, true)
		}
	}
	// Drop what was covered completely.
	out := m.flat[:0]
	for _, r := range m.flat {
		if r.Rect[0] < r.Rect[2] && r.Rect[1] < r.Rect[3] {
			out = append(out, r)
		}
	}
	m.flat = out
	return out
}

// cut removes rectangle cut from the pieces flat[start:] (of one region, with
// cursor c): each piece it overlaps is replaced by what is left of it above,
// below, left and right of cut. Past maxFlat pieces it stops, leaving the
// rest overlapping cut, unless the cut is mandatory (a hole): then a piece
// that overlaps it is dropped.
func (m *cursorMap) cut(start int, c Cursor, cut [4]float32, mandatory bool) {
	for p := start; p < len(m.flat); p++ {
		piece := m.flat[p].Rect
		if !overlaps(piece, cut) {
			continue
		}
		if len(m.flat) >= maxFlat {
			if !mandatory {
				break
			}
			m.flat[p].Rect = [4]float32{} // no room to split it
			continue
		}
		var keep [4][4]float32
		n := 0
		if piece[1] < cut[1] { // above
			keep[n] = [4]float32{piece[0], piece[1], piece[2], cut[1]}
			n++
		}
		if piece[3] > cut[3] { // below
			keep[n] = [4]float32{piece[0], cut[3], piece[2], piece[3]}
			n++
		}
		y0, y1 := max(piece[1], cut[1]), min(piece[3], cut[3])
		if piece[0] < cut[0] { // left of the cut, between its top and bottom
			keep[n] = [4]float32{piece[0], y0, cut[0], y1}
			n++
		}
		if piece[2] > cut[2] { // right
			keep[n] = [4]float32{cut[2], y0, piece[2], y1}
			n++
		}
		if n == 0 {
			m.flat[p].Rect = [4]float32{} // fully covered
			continue
		}
		m.flat[p].Rect = keep[0]
		for _, k := range keep[1:n] {
			m.flat = append(m.flat, CursorRegion{Rect: k, Cursor: c})
		}
	}
}

func overlaps(a, b [4]float32) bool {
	return a[0] < b[2] && b[0] < a[2] && a[1] < b[3] && b[1] < a[3]
}

// pointerShape is the cursor WM_SETCURSOR shows. Windows sends that message
// before the WM_MOUSEMOVE of the same movement, so the last position the
// window saw is the one the pointer just left: the shape is looked up at the
// pointer's current position (cur, when the OS could give it) and only falls
// back to the last known one.
func (m *cursorMap) pointerShape(curX, curY float32, curOK bool, lastX, lastY float32) Cursor {
	if curOK {
		return m.at(curX, curY)
	}
	return m.at(lastX, lastY)
}
