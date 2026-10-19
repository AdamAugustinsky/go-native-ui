package ui

import "slices"

// Native tooltips (phase 6, N2, D41). Where the OS draws tooltips (macOS), a
// widget does not draw one: it reports the text of a box for the frame
// (NativeTooltip), the app hands the finished frame's regions to the window
// (TooltipRegions, like CursorRegions), and the window registers them with
// the system, which owns the delay, the look and the placement. The text is
// not copied to the OS: the system asks for it when it shows a tooltip, and
// the app answers from the last finished frame (TooltipText).

// TooltipRegion is a rectangle of the window (x0, y0, x1, y1 in pixels,
// origin top left) with the key of the box whose tooltip it shows.
type TooltipRegion struct {
	// Rect is x0, y0, x1, y1 in pixels.
	Rect [4]float32
	// Key identifies the box; TooltipText returns its text.
	Key uint64
}

// tipRec is one tooltip declared in a frame: the box and its text, which
// lives in nativeTips.buf at [off, off+n).
type tipRec struct {
	id     ID
	off, n int
}

// nativeTips is the context's native-tooltip state. The records and the text
// belong to the frame that declared them (frame); the first declaration of a
// newer frame starts them over, and the storage is reused, so declaring
// allocates nothing once it has grown to the frame's size.
type nativeTips struct {
	enabled bool
	frame   uint32
	recs    []tipRec
	buf     []byte
	ents    []tipEnt // TooltipRegions' scratch
	pathBuf []uint32
}

// SetNativeTooltips says whether the window shows native tooltips
// (platform.NativeTooltips and a window to show them). Widgets ask
// NativeTooltips and draw their own tooltips when it is false.
func (c *Context) SetNativeTooltips(on bool) { c.nt.enabled = on }

// NativeTooltips reports whether tooltips are native: a widget then declares
// its tooltip with NativeTooltip instead of drawing one.
func (c *Context) NativeTooltips() bool { return c.nt.enabled }

// NativeTooltip declares text as the native tooltip of box id for this
// frame. id must have been built in this frame; call it every frame the
// tooltip should exist. Empty text declares nothing.
func (c *Context) NativeTooltip(id ID, text string) {
	if text == "" || id == 0 {
		return
	}
	nt := &c.nt
	if nt.frame != c.frame {
		nt.frame, nt.recs, nt.buf = c.frame, nt.recs[:0], nt.buf[:0]
	}
	nt.recs = append(nt.recs, tipRec{id: id, off: len(nt.buf), n: len(text)})
	nt.buf = append(nt.buf, text...)
}

// tipEnt is a region on its way out: its place in the draw order is the path
// pathBuf[off:off+n].
type tipEnt struct {
	reg    TooltipRegion
	off, n int
}

// TooltipRegions appends to dst the tooltips declared in the last finished
// frame (after End), as pixel rectangles clipped by their ancestors' clip
// rectangles and the window, in draw order, as CursorRegions does: where
// regions overlap, the last one is the topmost, whatever order they were
// declared in (a floating box is drawn after its siblings, an overlay after
// the tree). A box hidden by a flow, and its descendants, is left out; so are
// the boxes under an overlay that takes input (a popup or a modal: the
// pointer is theirs, as the drawn tooltip was not shown while one was open),
// and empty rectangles. It allocates nothing when dst has room.
func (c *Context) TooltipRegions(dst []TooltipRegion) []TooltipRegion {
	dst = dst[:0]
	nt := &c.nt
	if nt.frame != c.frame || len(nt.recs) == 0 || c.root == 0 {
		return dst
	}
	win := [4]float32{0, 0, float32(c.w), float32(c.h)}
	m := -1 // the topmost overlay that takes input hides everything beneath it
	for i := 0; i < c.nOvs; i++ {
		if c.ovs[i].kind != OverlayTooltip {
			m = i
		}
	}
	nt.ents, nt.pathBuf = nt.ents[:0], nt.pathBuf[:0]
	for _, t := range nt.recs {
		b := &c.boxes[t.id]
		if b.hid || int(b.ovl)-1 < m {
			continue
		}
		r := intersect(b.rect, win)
		for p := b.parent; p != 0 && !b.ovRoot && !empty(r); p = c.boxes[p].parent {
			if pb := &c.boxes[p]; pb.flags&Clip != 0 {
				r = intersect(r, pb.rect)
			}
			if c.boxes[p].ovRoot {
				break
			}
		}
		if empty(r) {
			continue
		}
		off := len(nt.pathBuf)
		nt.pathBuf = c.appendDrawPath(nt.pathBuf, b)
		nt.ents = append(nt.ents, tipEnt{reg: TooltipRegion{Rect: r, Key: uint64(b.key)}, off: off, n: len(nt.pathBuf) - off})
	}
	// Insertion sort into draw order (stable: equal paths keep declaration
	// order): there are few tooltips, and it allocates nothing.
	e := nt.ents
	for i := 1; i < len(e); i++ {
		for j := i; j > 0 && slices.Compare(nt.pathBuf[e[j].off:e[j].off+e[j].n], nt.pathBuf[e[j-1].off:e[j-1].off+e[j-1].n]) < 0; j-- {
			e[j], e[j-1] = e[j-1], e[j]
		}
	}
	for _, x := range e {
		dst = append(dst, x.reg)
	}
	return dst
}

// appendDrawPath appends to dst the position of box b in the draw order: its
// overlay (0 for the main tree), then, from the root of that tree down to b,
// each box's place among its siblings, with floating boxes after the others.
// Comparing two paths lexicographically gives the draw order (a box before its
// descendants, which are drawn over it).
func (c *Context) appendDrawPath(dst []uint32, b *box) []uint32 {
	dst = append(dst, uint32(b.ovl))
	n := len(dst)
	for x := b; !x.ovRoot && x.parent != 0; x = &c.boxes[x.parent] {
		var pos uint32
		for p := x.prev; p != 0; p = c.boxes[p].prev {
			pos++
		}
		if x.flags&floatingMask != 0 {
			pos |= 1 << 31
		}
		dst = append(dst, pos)
	}
	slices.Reverse(dst[n:]) // collected from b upwards
	return dst
}

// TooltipText appends to dst the text of the tooltip with the given key in
// the last finished frame (or the frame being built, once it declared it)
// and returns it; dst unchanged when there is none. It is what the window's
// tooltip text callback calls when the system shows a tooltip.
func (c *Context) TooltipText(key uint64, dst []byte) []byte {
	nt := &c.nt
	if nt.frame != c.frame {
		return dst
	}
	for _, t := range nt.recs {
		if uint64(c.boxes[t.id].key) == key {
			return append(dst, nt.buf[t.off:t.off+t.n]...)
		}
	}
	return dst
}
