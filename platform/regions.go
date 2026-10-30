package platform

import (
	"cmp"
	"slices"

	"gonativeui/internal/uitypes"
)

// CursorRegion is a rectangle of the window content (x0, y0, x1, y1 in
// physical pixels, origin top left) in which the pointer shows Cursor.
type CursorRegion = uitypes.CursorRegion

// DragRegion is a rectangle of the window's drag map (pixels, origin top
// left). Block false is an area where pressing the left button moves the
// window (a custom titlebar); Block true is a rectangle above it that keeps
// its presses for the app (a tab, a button).
type DragRegion = uitypes.DragRegion

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
	sweep    rectSweep
	holes    [][4]float32 // rectangles left out of every region: hosted native views, which have their own cursors
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

// maxFlat is the minimum piece budget; a longer input gets one slot per region.
const maxFlat = 512

// flatten returns disjoint pieces with the topmost cursor at each point,
// excluding hosted views. On overflow lower pieces are dropped, never left
// overlapping. The output and sweep storage are reused.
func (m *cursorMap) flatten() []CursorRegion {
	m.flat = flattenRects(&m.sweep, m.flat, m.regs, m.holes, max(maxFlat, len(m.regs)),
		func(r CursorRegion) ([4]float32, Cursor) { return r.Rect, r.Cursor },
		func(r [4]float32, v Cursor) CursorRegion { return CursorRegion{Rect: r, Cursor: v} })
	return m.flat
}

// rectSweep indexes rectangles by their top edge. Subtree maxima skip all
// rectangles ending above a query or below it in draw order. Querying each
// region visits only intersecting y intervals (and the search paths), instead
// of every later region. Storage is O(n), including heavily overlapping input.
type rectSweep struct {
	order     []sweepRect
	maxY      []float32
	maxZ      []int
	cur, next [][4]float32
}

type sweepRect struct {
	rect [4]float32
	z    int
}

func validRect(r [4]float32) bool { return r[0] < r[2] && r[1] < r[3] }

// flattenRects is shared by cursors and tooltips. It sorts and indexes in
// O(n log n), then visits overlapping y intervals and their search paths.
// Exact subtraction visits surviving pieces, bounded by limit. Higher regions
// are kept first when the output budget is exhausted.
func flattenRects[R any, V ~uint8 | ~uint64](s *rectSweep, dst, src []R, holes [][4]float32, limit int,
	get func(R) ([4]float32, V), put func([4]float32, V) R) []R {
	s.order = s.order[:0]
	for i, r := range src {
		rect, _ := get(r)
		if validRect(rect) {
			s.order = append(s.order, sweepRect{rect, i})
		}
	}
	for i, r := range holes {
		if validRect(r) {
			s.order = append(s.order, sweepRect{r, len(src) + i})
		}
	}
	slices.SortFunc(s.order, func(a, b sweepRect) int { return cmp.Compare(a.rect[1], b.rect[1]) })
	n := len(s.order)
	dst = dst[:0]
	if n == 0 {
		return dst
	}
	// The common scrolling-list case has no y overlap. Its sorted rows need
	// neither an interval index nor subtraction, and only neighbours can merge.
	separated := true
	bottom := s.order[0].rect[3]
	for _, r := range s.order[1:] {
		if r.rect[1] < bottom {
			separated = false
			break
		}
		bottom = r.rect[3]
	}
	if separated {
		for _, item := range s.order {
			if item.z >= len(src) {
				continue
			}
			r, v := get(src[item.z])
			if len(dst) > 0 {
				prev, pv := get(dst[len(dst)-1])
				if pv == v && prev[0] == r[0] && prev[2] == r[2] && prev[3] == r[1] {
					prev[3] = r[3]
					dst[len(dst)-1] = put(prev, v)
					continue
				}
			}
			if len(dst) < limit {
				dst = append(dst, put(r, v))
			}
		}
		return dst
	}
	size := 4 * n
	if cap(s.maxY) < size {
		s.maxY = make([]float32, size, 4*cap(s.order))
		s.maxZ = make([]int, size, 4*cap(s.order))
	}
	s.maxY, s.maxZ = s.maxY[:size], s.maxZ[:size]
	s.index(1, 0, n)
	for i := len(src) - 1; i >= 0 && len(dst) < limit; i-- {
		r, v := get(src[i])
		if !validRect(r) {
			continue
		}
		s.cur = append(s.cur[:0], r)
		s.cutAbove(1, 0, n, i, r, limit-len(dst))
		for _, p := range s.cur {
			dst = append(dst, put(p, v))
		}
	}
	// Equal values and x extents are adjacent; vertically touching pieces can
	// share one AppKit rectangle. All pieces were already disjoint.
	slices.SortFunc(dst, func(a, b R) int {
		ar, av := get(a)
		br, bv := get(b)
		if c := cmp.Compare(av, bv); c != 0 {
			return c
		}
		for _, axis := range [4]int{0, 2, 1, 3} {
			if c := cmp.Compare(ar[axis], br[axis]); c != 0 {
				return c
			}
		}
		return 0
	})
	out := dst[:0]
	for _, p := range dst {
		r, v := get(p)
		if len(out) > 0 {
			prev, pv := get(out[len(out)-1])
			if v == pv && prev[0] == r[0] && prev[2] == r[2] && prev[3] == r[1] {
				prev[3] = r[3]
				out[len(out)-1] = put(prev, v)
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

func (s *rectSweep) index(node, lo, hi int) {
	if hi-lo == 1 {
		s.maxY[node], s.maxZ[node] = s.order[lo].rect[3], s.order[lo].z
		return
	}
	mid := (lo + hi) / 2
	s.index(2*node, lo, mid)
	s.index(2*node+1, mid, hi)
	s.maxY[node] = max(s.maxY[2*node], s.maxY[2*node+1])
	s.maxZ[node] = max(s.maxZ[2*node], s.maxZ[2*node+1])
}

func (s *rectSweep) cutAbove(node, lo, hi, z int, r [4]float32, limit int) {
	if len(s.cur) == 0 || s.maxZ[node] <= z || s.maxY[node] <= r[1] || s.order[lo].rect[1] >= r[3] {
		return
	}
	if hi-lo > 1 {
		mid := (lo + hi) / 2
		s.cutAbove(2*node, lo, mid, z, r, limit)
		s.cutAbove(2*node+1, mid, hi, z, r, limit)
		return
	}
	cut := s.order[lo].rect
	if !overlaps(r, cut) {
		return
	}
	s.next = s.next[:0]
	for _, p := range s.cur {
		s.next = appendCut(s.next, p, cut)
		if len(s.next) >= limit {
			s.next = s.next[:limit]
			break
		}
	}
	s.cur, s.next = s.next, s.cur
}

// appendCut appends the up to four pieces of p outside cut.
func appendCut(dst [][4]float32, p, cut [4]float32) [][4]float32 {
	if !overlaps(p, cut) {
		return append(dst, p)
	}
	if p[1] < cut[1] {
		dst = append(dst, [4]float32{p[0], p[1], p[2], cut[1]})
	}
	if p[3] > cut[3] {
		dst = append(dst, [4]float32{p[0], cut[3], p[2], p[3]})
	}
	y0, y1 := max(p[1], cut[1]), min(p[3], cut[3])
	if p[0] < cut[0] {
		dst = append(dst, [4]float32{p[0], y0, cut[0], y1})
	}
	if p[2] > cut[2] {
		dst = append(dst, [4]float32{cut[2], y0, p[2], y1})
	}
	return dst
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

// regionRefresh coalesces native registrations while scrolling under a still
// pointer. A move, scroll end or unchanged frame flushes the current maps.
type regionRefresh struct {
	scrolling, changed        bool
	cursorDirty, tooltipDirty bool
}

func (r *regionRefresh) scroll(phase, momentum ScrollPhase) {
	p := phase
	if momentum != PhaseNone {
		p = momentum
	}
	r.scrolling = p != PhaseEnded && p != PhaseCancelled && p != PhaseStationary
}
