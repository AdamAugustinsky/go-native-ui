package ui

// Cursor is a pointer shape. The values mirror platform.Cursor, which the app
// package converts to.
type Cursor uint8

const (
	CursorArrow    Cursor = iota // the default arrow: a box with it marks no region
	CursorPointer                // pointing hand (links, tabs, buttons)
	CursorText                   // I-beam
	CursorResizeEW               // horizontal resize (left-right arrows)
	CursorResizeNS               // vertical resize (up-down arrows)
)

// CursorRegion is a screen rectangle (x0, y0, x1, y1 in pixels, origin top
// left) in which the pointer shows Cursor.
type CursorRegion struct {
	// Rect is x0, y0, x1, y1 in pixels.
	Rect [4]float32
	// Cursor is the shape shown inside Rect.
	Cursor Cursor
}

// DragRegion is a rectangle of the window's drag map. Rects with Block false
// are WindowDrag areas; Block true is a box that takes input and sits above
// them. The topmost (last) rectangle containing a point decides: a point is
// in the titlebar drag area when that rectangle is not a Block.
type DragRegion struct {
	// Rect is x0, y0, x1, y1 in pixels.
	Rect [4]float32
	// Block true makes the rectangle keep its presses; false makes it a window-drag area.
	Block bool
}

// regionWalk is the state of one pass over the finished tree.
type regionWalk struct {
	cursors    []CursorRegion
	drags      []DragRegion
	wantCursor bool
	wantDrag   bool
}

// CursorRegions appends to dst the boxes of the last finished frame (after
// End) that have a cursor other than the arrow, as pixel rectangles clipped by
// their ancestors' clip rectangles and the window, in draw order: where
// regions overlap, the last one is the topmost. Empty rectangles are left out.
// It allocates nothing when dst has room, and visits no box when the frame
// has no cursor boxes.
func (c *Context) CursorRegions(dst []CursorRegion) []CursorRegion {
	cr, _ := c.regions(dst, nil, true, false)
	return cr
}

// DragRegions appends to dst the window drag map of the last finished frame
// (after End), in draw order: the WindowDrag boxes (Block false) and, after
// or between them, the boxes that take input (Clickable, Hoverable or
// Focusable) and overlap a WindowDrag area (Block true), all clipped like
// CursorRegions. A press at a point is a window drag when the last rectangle
// containing the point is not a Block; buttons and tabs drawn over a titlebar
// are therefore excluded without any subtraction. It returns dst empty when no
// box has WindowDrag.
func (c *Context) DragRegions(dst []DragRegion) []DragRegion {
	_, dr := c.regions(nil, dst, false, true)
	return dr
}

// PointerRegions is CursorRegions and DragRegions in one pass over the boxes
// (both are wanted after every frame).
func (c *Context) PointerRegions(cursors []CursorRegion, drags []DragRegion) ([]CursorRegion, []DragRegion) {
	return c.regions(cursors, drags, true, true)
}

func (c *Context) regions(cursors []CursorRegion, drags []DragRegion, wantCursor, wantDrag bool) ([]CursorRegion, []DragRegion) {
	w := regionWalk{cursors: cursors[:0], drags: drags[:0], wantCursor: wantCursor && c.nCursorBoxes > 0, wantDrag: wantDrag && c.nDragBoxes > 0}
	if c.root == 0 || !(w.wantCursor || w.wantDrag) {
		return w.cursors, w.drags
	}
	win := [4]float32{0, 0, float32(c.w), float32(c.h)}
	m := -1 // the topmost modal hides everything beneath it
	for i := 0; i < c.nOvs; i++ {
		if c.ovs[i].kind == OverlayModal {
			m = i
		}
	}
	if m < 0 {
		c.walkRegions(c.root, win, &w)
	}
	for i := max(m, 0); i < c.nOvs; i++ {
		if c.ovs[i].kind != OverlayTooltip {
			c.walkRegions(c.ovs[i].id, win, &w)
		}
	}
	if w.wantDrag {
		w.drags = trimBlocks(w.drags)
	}
	return w.cursors, w.drags
}

// ActiveCursor is the cursor of the box that holds the left mouse button (a
// drag in progress), CursorArrow if there is none or it has no cursor. A drag
// keeps its cursor wherever the pointer goes, so the app shows this one over
// everything while it is not the arrow. Valid after End.
func (c *Context) ActiveCursor() Cursor {
	k := c.active[0]
	if k == 0 {
		return CursorArrow
	}
	v, ok := c.table.Get(uint64(k))
	if !ok || c.boxes[v].lastFrame != c.frame {
		return CursorArrow
	}
	return c.boxes[v].cursor
}

// trimBlocks drops the Block rectangles that touch no drag area (the walk
// collects every input box, the drag areas are only known once it is over),
// keeping the order.
func trimBlocks(d []DragRegion) []DragRegion {
	var band [4]float32
	have := false
	for i := range d {
		if r := d[i].Rect; !d[i].Block {
			if !have {
				band, have = r, true
			} else {
				band = [4]float32{min(band[0], r[0]), min(band[1], r[1]), max(band[2], r[2]), max(band[3], r[3])}
			}
		}
	}
	if !have {
		return d[:0]
	}
	out := d[:0]
	for _, r := range d {
		if !r.Block || !empty(intersect(r.Rect, band)) {
			out = append(out, r)
		}
	}
	return out
}

// walkRegions visits the tree in draw order (a box, then its children, the
// floating ones after the others, as emit does) with the clip rectangle.
func (c *Context) walkRegions(id ID, clip [4]float32, w *regionWalk) {
	b := &c.boxes[id]
	r := intersect(b.rect, clip)
	if !empty(r) {
		if w.wantCursor && b.cursor != CursorArrow {
			w.cursors = append(w.cursors, CursorRegion{Rect: r, Cursor: b.cursor})
		}
		if w.wantCursor && b.rich != 0 {
			w.cursors = c.richCursors(w.cursors, b, intersect(clip, b.rect))
		}
		if w.wantDrag {
			switch {
			case b.flags&WindowDrag != 0:
				w.drags = append(w.drags, DragRegion{Rect: r})
			case b.flags&hitMask != 0:
				w.drags = append(w.drags, DragRegion{Rect: r, Block: true})
			}
		}
	}
	cclip := clip
	if b.flags&Clip != 0 {
		cclip = intersect(clip, b.rect)
		if empty(cclip) {
			return
		}
	}
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if c.boxes[ch].flags&floatingMask == 0 {
			c.walkRegions(ch, cclip, w)
		}
	}
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if c.boxes[ch].flags&floatingMask != 0 {
			c.walkRegions(ch, cclip, w)
		}
	}
}
