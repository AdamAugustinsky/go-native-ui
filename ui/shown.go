package ui

// ShownRect is the part of box id's rectangle that showed in the last layout
// (during a build, the previous frame's): clipped by the clip rectangles of
// the boxes around it (a scroll area, a Clip box) and by the window, and
// empty (all zeros) when a flow hid the box or one around it. A box that
// should not animate, decode or load while it cannot be seen asks it: the
// rectangle is empty for a row scrolled out of its scroll area although it is
// inside the window.
func (c *Context) ShownRect(id ID) [4]float32 {
	var r [4]float32
	if !c.wasHidden(&c.boxes[id]) {
		if r = c.clippedRect(id); empty(r) {
			r = [4]float32{}
		}
	}
	c.noteRead(id, readShown, r)
	return r
}
