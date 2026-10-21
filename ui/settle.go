package ui

// Settle frames. A build sees the previous frame's layout: Rect, RectOf,
// ScrollExtent, ShownRect and ListRange report what the last End laid out.
// A builder that sizes or places a box from such a value (a
// band as high as its content was, a button at the bottom of a field, a popup
// against its anchor) is one frame late whenever the content changes size in
// the frame itself; if nothing else asks for a frame then, the window would
// rest on that stale layout, drawn wrong and hit-tested wrong until some
// input comes.
//
// So the Context notes every such read made during a build (between Begin
// and End), and End, once it has laid the frame out, compares each value read
// with what the same call reports now. When one differs and nothing else asks
// for a frame, End asks for one more (Animating, Settling): its build reads the
// new layout. A layout that keeps changing what it reads (each frame's reads
// change the next layout) gets at most maxSettleFrames such frames in a row,
// so an idle window still draws nothing once they are spent. A value an app
// keeps from a layout by other means (copied after End, derived from a
// Signal's pointer position) is not seen: it asks for its own frame
// (RequestFrame).

// maxSettleFrames is how many frames in a row End asks for only because the
// build read layout values that the frame's layout changed.
const maxSettleFrames = 2

// layoutRead is a value a build read from the previous layout: of the box
// with key, kind says which.
type layoutRead struct {
	key  Key
	kind readKind
	v    [4]float32
}

type readKind uint8

const (
	readRect   readKind = iota // the rectangle (Rect, RectOf, ListRange)
	readExtent                 // content and view (ScrollExtent)
	readShown                  // the shown part (ShownRect)
)

// settleState is what the Context keeps for settle frames.
type settleState struct {
	building bool         // between Begin and End: reads are noted
	reads    []layoutRead // this build's
	run      int          // frames in a row End asked for to settle
	asked    bool         // the last End asked for a frame to settle (Settling)
	frames   uint64       // ... in all (SettleFrames)
}

// noteRead records that the build read v, of kind, from box id's last layout.
func (c *Context) noteRead(id ID, kind readKind, v [4]float32) {
	if !c.st.building || id == 0 {
		return
	}
	k := c.boxes[id].key
	if n := len(c.st.reads); n > 0 {
		if l := &c.st.reads[n-1]; l.key == k && l.kind == kind {
			return // the same read again (Rect(id)[2] - Rect(id)[0])
		}
	}
	c.st.reads = append(c.st.reads, layoutRead{key: k, kind: kind, v: v})
}

// readNow is what a read of kind of the box with key reports after this
// frame's layout: what the next build would read.
func (c *Context) readNow(k Key, kind readKind) [4]float32 {
	v, ok := c.table.Get(uint64(k))
	if !ok {
		return [4]float32{}
	}
	b := &c.boxes[v]
	switch kind {
	case readExtent:
		return [4]float32{float32(b.content[0]), float32(b.content[1]), b.size[0], b.size[1]}
	case readShown:
		if b.hid {
			return [4]float32{}
		}
		if r := c.clippedRect(ID(v)); !empty(r) {
			return r
		}
		return [4]float32{}
	}
	return b.rect
}

// readsStale reports whether a value this build read differs from what the
// frame's layout gives, and forgets the reads.
func (c *Context) readsStale() bool {
	stale := false
	for i := range c.st.reads {
		r := &c.st.reads[i]
		now := c.readNow(r.key, r.kind)
		for j := range now {
			if d := now[j] - r.v[j]; d > settleEps || d < -settleEps {
				stale = true
				break
			}
		}
		if stale {
			break
		}
	}
	c.st.reads = c.st.reads[:0]
	return stale
}

// settleEps is the change of a read value, in pixels, that a settle frame is
// asked for.
const settleEps = 0.01

// settle is End's step after layout and animation: one more frame when the
// build read stale layout values and nothing else asks for a frame.
func (c *Context) settle() {
	stale := c.readsStale()
	if !c.st.asked { // this frame was not one End asked for to settle
		c.st.run = 0
	}
	c.st.asked = false
	if !stale || c.animating {
		return // (a frame comes anyway, and reads this layout)
	}
	if c.st.run >= maxSettleFrames {
		return
	}
	c.st.run++
	c.st.frames++
	c.st.asked = true
	c.animating = true
}

// Settling reports whether the last End asked for another frame because the
// build read values of the previous layout (Rect, RectOf, ScrollExtent,
// ShownRect, ListRange) that the frame's own layout
// changed: the next build reads the new ones. Animating is true then too.
func (c *Context) Settling() bool { return c.st.asked }

// SettleFrames counts the frames End asked for to settle (Settling).
func (c *Context) SettleFrames() uint64 { return c.st.frames }
