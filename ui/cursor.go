package ui

import "gonativeui/internal/uitypes"

// Cursor is a pointer shape shared with the platform bridge.
type Cursor = uitypes.Cursor

const (
	CursorArrow      Cursor = iota // the default arrow: a box with it marks no region
	CursorPointer                  // pointing hand (links, tabs, buttons)
	CursorText                     // I-beam
	CursorResizeEW                 // horizontal resize (left-right arrows)
	CursorResizeNS                 // vertical resize (up-down arrows)
	CursorResizeNWSE               // diagonal resize, top-left to bottom-right
	CursorResizeNESW               // diagonal resize, top-right to bottom-left
	CursorGrab                     // open hand: something can be dragged (Windows: the pointing hand)
	CursorGrabbing                 // closed hand: it is being dragged (Windows: the four-way move arrows)
)

// CursorRegion is a screen rectangle (x0, y0, x1, y1 in pixels, origin top
// left) in which the pointer shows Cursor.
type CursorRegion = uitypes.CursorRegion

// DragRegion is a rectangle of the window's drag map. Rects with Block false
// are WindowDrag areas; Block true is a box that takes input and sits above
// them. The topmost (last) rectangle containing a point decides: a point is
// in the titlebar drag area when that rectangle is not a Block.
type DragRegion = uitypes.DragRegion

// frameRegions are collected by emit, using the same order and clips as drawing.
type frameRegions struct {
	cursors []CursorRegion
	drags   []DragRegion

	modal, popup int
	collecting   bool
}

// FramePointerRegions borrows the last finished frame's cursor and drag maps.
// The slices are read-only and valid until the next End.
func (c *Context) FramePointerRegions() ([]CursorRegion, []DragRegion) {
	return c.regions.cursors, c.regions.drags
}

// CursorRegions copies the last finished frame's cursor map into dst's storage.
// Regions are clipped by their ancestors and the window, in draw order.
func (c *Context) CursorRegions(dst []CursorRegion) []CursorRegion {
	return append(dst[:0], c.regions.cursors...)
}

// DragRegions copies the last finished frame's window drag map into dst's
// storage. The last matching rectangle decides whether a press moves the window.
func (c *Context) DragRegions(dst []DragRegion) []DragRegion {
	return append(dst[:0], c.regions.drags...)
}

// PointerRegions copies both finished maps into the caller's storage.
// FramePointerRegions borrows them without a copy.
func (c *Context) PointerRegions(cursors []CursorRegion, drags []DragRegion) ([]CursorRegion, []DragRegion) {
	return c.CursorRegions(cursors), c.DragRegions(drags)
}

func (c *Context) beginRegions() {
	r := &c.regions
	r.cursors, r.drags = r.cursors[:0], r.drags[:0]
	r.modal, r.popup = -1, -1
	for i := 0; i < c.nOvs; i++ {
		if c.ovs[i].kind != OverlayTooltip {
			r.popup = i
		}
		if c.ovs[i].kind == OverlayModal {
			r.modal = i
		}
	}
	c.beginTooltips()
	r.collecting = c.nCursorBoxes > 0 || c.nDragBoxes > 0 || c.nt.collecting
}

func (c *Context) endRegions() {
	c.regions.collecting = false
	c.regions.drags = trimBlocks(c.regions.drags)
}

func (c *Context) collectRegions(id ID, r [4]float32) {
	w := &c.regions
	if !w.collecting {
		return
	}
	b := &c.boxes[id]
	ov := int(b.ovl) - 1
	if ov >= w.popup {
		c.collectTooltips(id, r)
	}
	if ov < w.modal || ov >= 0 && c.ovs[ov].kind == OverlayTooltip {
		return
	}
	if c.nCursorBoxes > 0 {
		if b.cursor != CursorArrow {
			w.cursors = append(w.cursors, CursorRegion{Rect: r, Cursor: b.cursor})
		}
		if b.rich != 0 {
			w.cursors = c.richCursors(w.cursors, b, r)
		}
	}
	if c.nDragBoxes > 0 {
		switch {
		case b.flags&WindowDrag != 0:
			w.drags = append(w.drags, DragRegion{Rect: r})
		case b.flags&hitMask != 0:
			w.drags = append(w.drags, DragRegion{Rect: r, Block: true})
		}
	}
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
	if !ok || c.boxes[v].lastFrame != c.frame || c.boxes[v].hid {
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
