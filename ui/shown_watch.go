package ui

// A box that does something only while it shows (an animated image) reads
// whether it showed in the last layout (ShownRect) while it is built, so it
// is one layout late: a layout that reveals it leaves it idle until something
// else asks for a frame. WatchShown closes that gap.

type shownWatch struct {
	key   Key
	shown bool
}

// WatchShown tells the context that the box keyed key was built as shown
// (shown true: it had some of itself in view in the last layout) or as hidden.
// After this frame's layout, a box whose visibility is not what it was built
// with makes the context ask for another frame (Animating), so that it is
// built again with the new answer; a box that stays as it was costs nothing.
// Call it every frame the box is built; every box is watched, however many
// (the list keeps its capacity from frame to frame, so a steady state does not
// allocate).
func (c *Context) WatchShown(key Key, shown bool) {
	c.shownWatch = append(c.shownWatch, shownWatch{key, shown})
}

// checkShownWatch compares the watched boxes with the layout just done.
func (c *Context) checkShownWatch() {
	for _, w := range c.shownWatch {
		v, ok := c.table.Get(uint64(w.key))
		if !ok {
			continue
		}
		b := &c.boxes[v]
		if b.lastFrame != c.frame {
			continue // not built this frame: nothing was laid out
		}
		if now := !b.hid && !empty(c.clippedRect(ID(v))); now != w.shown {
			c.animating = true
		}
	}
	c.shownWatch = c.shownWatch[:0]
}
