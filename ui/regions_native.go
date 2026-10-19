package ui

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

// TooltipRegions appends to dst the tooltips declared in the last finished
// frame (after End), as pixel rectangles clipped by their ancestors' clip
// rectangles and the window, in declaration order. A box under a modal
// overlay is left out, as are empty rectangles. It allocates nothing when dst
// has room.
func (c *Context) TooltipRegions(dst []TooltipRegion) []TooltipRegion {
	dst = dst[:0]
	nt := &c.nt
	if nt.frame != c.frame || len(nt.recs) == 0 || c.root == 0 {
		return dst
	}
	win := [4]float32{0, 0, float32(c.w), float32(c.h)}
	m := -1 // the topmost modal hides everything beneath it
	for i := 0; i < c.nOvs; i++ {
		if c.ovs[i].kind == OverlayModal {
			m = i
		}
	}
	for _, t := range nt.recs {
		b := &c.boxes[t.id]
		if int(b.ovl)-1 < m {
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
		if !empty(r) {
			dst = append(dst, TooltipRegion{Rect: r, Key: uint64(b.key)})
		}
	}
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
