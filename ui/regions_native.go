package ui

import "gonativeui/internal/uitypes"

// Native tooltips (phase 6, N2, D41). Where the OS draws tooltips (macOS), a
// widget does not draw one: it reports the text of a box for the frame
// (NativeTooltip), the app hands the finished frame's regions to the window
// (TooltipRegions, like CursorRegions), and the window registers them with
// the system, which owns the delay, the look and the placement. The text is
// not copied to the OS: the system asks for it when it shows a tooltip, and
// the app answers from the last finished frame (TooltipText).

// TooltipRegion is a rectangle of the window (x0, y0, x1, y1 in pixels,
// origin top left) with the key of the box whose tooltip it shows.
type TooltipRegion = uitypes.TooltipRegion

// tipRec is one tooltip declared in a frame: the box and its text, which
// lives in nativeTips.buf at [off, off+n).
type tipRec struct {
	id     ID
	off, n int
	next   int32 // 1 + the next declaration for the same box
}

// nativeTips is the context's native-tooltip state. The records and the text
// belong to the frame that declared them (frame); the first declaration of a
// newer frame starts them over, and the storage is reused, so declaring
// allocates nothing once it has grown to the frame's size.
type nativeTips struct {
	enabled    bool
	frame      uint32
	recs       []tipRec
	buf        []byte
	heads      []int32         // 1 + the first declaration for each box
	regions    []TooltipRegion // emit order, clipped by the draw traversal
	collecting bool
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

func (c *Context) beginTooltips() {
	nt := &c.nt
	nt.regions = nt.regions[:0]
	nt.collecting = nt.frame == c.frame && len(nt.recs) > 0
	if !nt.collecting {
		return
	}
	if cap(nt.heads) < len(c.boxes) {
		nt.heads = make([]int32, len(c.boxes), cap(c.boxes))
	}
	nt.heads = nt.heads[:len(c.boxes)]
	clear(nt.heads)
	// Prepending in reverse preserves declaration order for repeated boxes.
	for i := len(nt.recs) - 1; i >= 0; i-- {
		r := &nt.recs[i]
		r.next = nt.heads[r.id]
		nt.heads[r.id] = int32(i + 1)
	}
}

func (c *Context) collectTooltips(id ID, r [4]float32) {
	nt := &c.nt
	if !nt.collecting {
		return
	}
	for i := nt.heads[id]; i > 0; i = nt.recs[i-1].next {
		nt.regions = append(nt.regions, TooltipRegion{Rect: r, Key: uint64(c.boxes[id].key)})
	}
}

// FrameTooltipRegions borrows the last finished frame's tooltip map. The slice
// is read-only and valid until the next End.
func (c *Context) FrameTooltipRegions() []TooltipRegion { return c.nt.regions }

// TooltipRegions copies into dst's storage the tooltips of the last finished
// frame (after End), as pixel rectangles clipped by their ancestors' clip
// rectangles and the window, in draw order, as CursorRegions does: where
// regions overlap, the last one is the topmost, whatever order they were
// declared in (a floating box is drawn after its siblings, an overlay after
// the tree). A box hidden by a flow, and its descendants, is left out; so are
// the boxes under an overlay that takes input (a popup or a modal: the
// pointer is theirs, as the drawn tooltip was not shown while one was open),
// and empty rectangles. It allocates nothing when dst has room.
func (c *Context) TooltipRegions(dst []TooltipRegion) []TooltipRegion {
	return append(dst[:0], c.nt.regions...)
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
