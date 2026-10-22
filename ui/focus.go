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

// SetTabReachesControls sets whether Tab and Shift+Tab reach every focusable
// box (the default, and the convention of Windows and Linux) or only the
// text-entry controls and the lists (FocusText, FocusList: macOS's convention
// with System Settings' Keyboard navigation off). A control that has the
// focus keeps it, and Tab leaves it for the next text-entry control or list.
// Clicking a control, an assistive technology and SetFocus still focus it.
// The app sets it before each frame from platform.TabReachesControls.
func (c *Context) SetTabReachesControls(all bool) { c.textOnlyTab = !all }

// TabReachesControls reports what SetTabReachesControls set.
func (c *Context) TabReachesControls() bool { return !c.textOnlyTab }

// richFocus is a focus target inside a layout box, not another layout/hit box.
// It uses the same traversal, overlay scope and key claiming as box controls.
type richFocus struct {
	key   Key
	id    ID
	span  int
	shown bool
}

func (c *Context) richLinkShown(id ID) bool {
	clip := c.clippedRect(id)
	for _, r := range c.rects {
		if !empty(intersect(r, clip)) {
			return true
		}
	}
	return false
}

// focusBox resolves either a box key or a visible span key to its layout owner.
// Before the build, richFocus is still the previous frame's registry (AX).
func (c *Context) focusBox(k Key) ID {
	if v, ok := c.table.Get(uint64(k)); ok {
		return ID(v)
	}
	for _, f := range c.richFocus {
		if f.key == k && f.shown {
			return f.id
		}
	}
	return 0
}

func (c *Context) prepareRichFocus() {
	for i := range c.richFocus {
		f := &c.richFocus[i]
		b := &c.boxes[f.id]
		r := c.recOf(b)
		pv := c.richView(b, r)
		sp := &pv.Spans[f.span]
		w := max(b.size[0]-b.padSum(0), 0)
		c.rects = pv.RangeRects(int(sp.Off), int(sp.Off+sp.Len), w, c.rects[:0])
		for j := range c.rects {
			rc := &c.rects[j]
			dx := b.rect[0] + b.pad[0] + alignDX(&pv, pv.LineAt(rc[1]+0.5), w, b.align)
			rc[0], rc[2] = rc[0]+dx, rc[2]+dx
			rc[1], rc[3] = rc[1]+b.rect[1]+b.pad[1], rc[3]+b.rect[1]+b.pad[1]
		}
		f.shown = !b.hid && b.flags&Disabled == 0 && c.richLinkShown(f.id)
	}
}

// traverseFocus handles Tab and Shift+Tab after the frame was built, and drops
// the focus from a box that became disabled.
func (c *Context) traverseFocus() {
	c.prepareRichFocus()
	c.overlayFocus()
	c.cancelBeneathModal()
	if c.focusKey != 0 {
		if v := c.focusBox(c.focusKey); v != 0 {
			if b := &c.boxes[v]; b.lastFrame == c.frame && (b.flags&Disabled != 0 || b.hid) {
				c.focusKey = 0
				c.animating = true
				c.dropFieldOutputs()
			}
		} else {
			c.focusKey = 0
			c.animating = true
			c.dropFieldOutputs()
		}
	}
	fwd := c.TakeKey(KeyTab, 0)
	back := !fwd && c.TakeKey(KeyTab, ModShift)
	if !fwd && !back {
		return
	}
	scope := c.focusScope()
	var first, last, prev, next Key
	seenCur := false
	consider := func(k Key, control bool) {
		cur := k == c.focusKey
		if cur {
			seenCur = true
			prev = last
		}
		if c.textOnlyTab && control {
			return // controls still mark their place when skipped
		}
		if first == 0 {
			first = k
		}
		if !cur && seenCur && next == 0 {
			next = k
		}
		last = k
	}
	for _, id := range c.order {
		b := &c.boxes[id]
		if b.flags&Disabled != 0 || b.hid || b.ovl != scope || b.ovRoot {
			continue
		}
		if b.flags&Focusable != 0 {
			consider(b.key, b.flags&(FocusText|FocusList) == 0)
		}
		if r := c.recOf(b); r != nil {
			for _, f := range c.richFocus[r.focusOff : r.focusOff+r.focusN] {
				if f.shown {
					consider(f.key, true)
				}
			}
		}
	}
	var to Key
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
	c.focusKey = to
	c.focusVisible = true
	c.animating = true // the focus ring animates in the frame that follows
	c.secureOn = c.boxes[c.focusBox(to)].secure && c.in.Focused
}

// dropFieldOutputs withdraws what the focused text field gave the app this
// frame, when layout takes the focus from it (it became disabled or hidden
// after it was built): the input method's candidate rectangle, so that the
// platform stops text input (IMERect), and the caret blink's wake-up. A
// composition that is on screen is left for the next frame, whose field finds
// itself orphaned and commits the composition as AppKit does when the first
// responder changes, and asks the app to discard the platform's (IMECancel);
// asking now would discard the text before it was committed.
func (c *Context) dropFieldOutputs() {
	c.secureOn = false
	if c.imeOK {
		c.imeOK, c.imeRect, c.wakeNs = false, [4]float32{}, 0
	}
}
