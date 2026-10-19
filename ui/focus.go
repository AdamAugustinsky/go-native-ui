package ui

// Focus traversal (phase 5, D28). Tab and Shift+Tab move the keyboard focus
// through the focusable boxes of the frame in build order, wrapping around;
// disabled boxes are skipped, and with a popup or modal overlay open the
// traversal stays inside the topmost one. Tab is taken at End, so any handler
// that claimed it during the build (a field in a code editor) keeps it.

// FocusVisible reports whether the focus came from the keyboard (Tab, or
// SetFocusVisible) and not from a pointer press: widgets draw their focus ring
// only then. It is false after a press of any mouse button.
func (c *Context) FocusVisible() bool { return c.focusVisible }

// SetFocusVisible sets what FocusVisible reports. A widget that moves the
// focus with the keyboard itself (arrow keys in a radio group or a list) calls
// SetFocus and then SetFocusVisible(true).
func (c *Context) SetFocusVisible(v bool) { c.focusVisible = v }

// traverseFocus handles Tab and Shift+Tab after the frame was built, and drops
// the focus from a box that became disabled.
func (c *Context) traverseFocus() {
	c.overlayFocus()
	c.cancelBeneathModal()
	if c.focusKey != 0 {
		if v, ok := c.table.Get(uint64(c.focusKey)); ok {
			if b := &c.boxes[v]; b.lastFrame == c.frame && (b.flags&Disabled != 0 || b.hid) {
				c.focusKey = 0
				c.animating = true
			}
		}
	}
	fwd := c.TakeKey(KeyTab, 0)
	back := !fwd && c.TakeKey(KeyTab, ModShift)
	if !fwd && !back {
		return
	}
	scope := c.focusScope()
	var first, last, prev, next ID
	seenCur := false
	for _, id := range c.order {
		b := &c.boxes[id]
		if b.flags&Focusable == 0 || b.flags&Disabled != 0 || b.hid || b.ovl != scope || b.ovRoot {
			continue
		}
		if first == 0 {
			first = id
		}
		if b.key == c.focusKey {
			seenCur = true
			prev = last
		} else if seenCur && next == 0 {
			next = id
		}
		last = id
	}
	var to ID
	switch {
	case fwd && next != 0:
		to = next
	case fwd:
		to = first
	case seenCur && prev != 0:
		to = prev
	default:
		to = last
	}
	if to == 0 {
		return
	}
	c.focusKey = c.boxes[to].key
	c.focusVisible = true
	c.animating = true // the focus ring animates in the frame that follows
}
