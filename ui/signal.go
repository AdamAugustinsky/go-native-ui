package ui

import "math"

// ---- hit testing (against the previous frame's layout) ----

// hitTest finds the topmost hit-testable box and the topmost scrollable box
// under the pointer, walking the previous frame's tree in draw order (a
// box's children after it, floating children after their siblings) so that
// the last box drawn wins. Clip boxes hide their subtree outside their
// rectangle.
//
// Overlays are hit-tested after the main tree, in order, so the topmost wins;
// everything beneath a modal is skipped, and tooltips are never hit.
//
// Scroll events that carry where they happened (ScrollEvent.Pos) find their
// box there instead (scrollAt).
func (c *Context) hitTest() {
	c.hotID, c.hotKey, c.scrollKey, c.sp.hit = 0, 0, 0, 0
	c.tooltipID, c.tooltipKey = 0, 0
	if c.root == 0 || !c.in.Inside {
		return
	}
	c.hitAll(c.in.MouseX, c.in.MouseY)
	if c.tooltipID != 0 {
		c.tooltipKey = c.boxes[c.tooltipID].key
	}
	if c.hotID != 0 {
		c.hotKey = c.boxes[c.hotID].key
	}
}

// hitAll walks the main tree (unless a modal covers it) and the overlays at
// (x, y).
func (c *Context) hitAll(x, y float32) {
	if c.modalIdx < 0 {
		c.hitWalk(c.root, x, y)
	}
	for i := max(c.modalIdx, 0); i < c.nOvPrev; i++ {
		if c.ovPrev[i].kind != OverlayTooltip {
			c.hitWalk(c.ovPrev[i].id, x, y)
		}
	}
}

func (c *Context) hitWalk(id ID, x, y float32) {
	b := &c.boxes[id]
	if b.hid {
		return
	}
	inside := x >= b.rect[0] && x < b.rect[2] && y >= b.rect[1] && y < b.rect[3]
	if inside {
		if b.flags&(hitMask|Disabled) != 0 {
			c.tooltipID = id
		}
		if b.flags&hitMask != 0 && b.flags&Disabled == 0 {
			c.hotID = id
		}
		if b.flags&scrollMask != 0 {
			c.scrollKey, c.sp.hit = b.key, id
		} else if b.stopWheel {
			c.scrollKey, c.sp.hit = 0, 0
		}
	} else if b.flags&Clip != 0 {
		return
	}
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if c.boxes[ch].flags&floatingMask == 0 {
			c.hitWalk(ch, x, y)
		}
	}
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if c.boxes[ch].flags&floatingMask != 0 {
			c.hitWalk(ch, x, y)
		}
	}
}

// mouse applies this frame's button edges to the active/focus state and
// scrolls the box under the pointer.
func (c *Context) mouse() {
	in := &c.in
	var hot *box
	if c.hotID != 0 {
		hot = &c.boxes[c.hotID]
	}
	for btn := 0; btn < 2; btn++ {
		c.pressedKey[btn], c.releasedKey[btn] = 0, 0
		// A release followed by a new press in one frame (Down is still true):
		// the old press ends first, or the new capture would be cleared at once.
		releaseFirst := in.Released[btn] && in.Pressed[btn] && in.Down[btn]
		if releaseFirst {
			c.releasedKey[btn] = c.active[btn]
			c.relPos[btn], c.relClicks[btn] = c.pressPos[btn], c.pressClicks[btn] // before the new press replaces them
			c.active[btn] = 0
		}
		if in.Pressed[btn] && !c.pressConsumed {
			c.focusVisible = false // the pointer, not the keyboard, moves the focus now
			if hot != nil && hot.flags&Clickable != 0 {
				c.active[btn] = hot.key
				c.pressedKey[btn] = hot.key
				c.pressPos[btn] = [2]float32{in.MouseX, in.MouseY}
				c.pressClicks[btn] = max(in.Clicks[btn], 1)
			}
			if btn == ButtonLeft {
				c.focusOnPress()
			}
		}
		if in.Cancelled[btn] {
			c.active[btn] = 0 // dropped without a release: nobody is clicked
		} else if !releaseFirst && (in.Released[btn] || (!in.Down[btn] && c.active[btn] != 0)) {
			c.releasedKey[btn] = c.active[btn]
			c.relPos[btn], c.relClicks[btn] = c.pressPos[btn], c.pressClicks[btn]
			c.active[btn] = 0
		}
	}

	c.scrollInput()
	c.gestureInput()
}

// focusOnPress moves focus to the nearest focusable box at or above the box
// under the pointer (unless it has KeepFocus); a press on nothing clears focus.
func (c *Context) focusOnPress() {
	if c.hotID == 0 {
		c.focusKey = 0
		return
	}
	for id := c.hotID; id != 0; id = c.boxes[id].parent {
		if c.boxes[id].flags&(Focusable|Disabled) == Focusable {
			if c.boxes[id].flags&KeepFocus == 0 {
				c.focusKey = c.boxes[id].key
			}
			return
		}
	}
}

// stepScroll moves every scroll offset toward its target, and the springs of
// the rubber bands.
func (c *Context) stepScroll() {
	c.stepRubber()
	for _, id := range c.prevOrder {
		b := &c.boxes[id]
		if b.flags&scrollMask == 0 {
			continue
		}
		for a := 0; a < 2; a++ {
			d := b.scrollTgt[a] - b.scroll[a]
			if d == 0 {
				continue
			}
			if math.Abs(d) < scrollEps {
				b.scroll[a] = b.scrollTgt[a]
				continue
			}
			b.scroll[a] += d * float64(c.rScroll)
			if math.Abs(b.scrollTgt[a]-b.scroll[a]) < scrollEps {
				b.scroll[a] = b.scrollTgt[a]
			} else {
				c.animating = true
			}
		}
	}
}

const scrollEps = 0.05 // pixels

// ---- signals ----

// Signal reports what the pointer and keyboard did to box id this frame. It
// can be called any time after the box is built (and as often as wanted).
// Boxes need Clickable, Hoverable or Focusable to take part in hit testing.
func (c *Context) Signal(id ID) Signal {
	var s Signal
	if id == 0 {
		return s
	}
	b := &c.boxes[id]
	k := b.key
	if b.flags&Disabled != 0 || c.blocked(b) || c.wasHidden(b) {
		return s // inert: also an activation or press that was queued before
	}
	for i := 0; i < c.in.NActions; i++ {
		if r := &c.in.Actions[i]; r.Key == k {
			s.Action |= r.Action
			s.ActionValue = r.Value
			if r.Action&ActPress != 0 {
				s.Activated = true
			}
		}
	}
	if c.gs.n > 0 {
		c.gestureOf(b, &s)
	}
	if b.flags&(hitMask|scrollMask) == 0 {
		return s
	}
	hot := c.hotKey == k
	if b.flags&hitMask != 0 {
		s.Hovering = hot && (c.active[0] == 0 || c.active[0] == k) && (c.active[1] == 0 || c.active[1] == k)
		s.Mouse = [2]float32{c.in.MouseX - b.rect[0], c.in.MouseY - b.rect[1]}
	}
	if b.flags&Clickable != 0 {
		s.Pressed = c.pressedKey[0] == k
		s.Dragging = c.active[0] == k && c.in.Down[0]
		s.Released = c.releasedKey[0] == k
		s.Clicked = s.Released && hot
		s.DoubleClicked = s.Clicked && c.relClicks[0] >= 2
		s.RightClicked = c.releasedKey[1] == k && hot
		if s.Released { // the press that ended, even if a new one began in the same frame
			s.DragDelta = [2]float32{c.in.MouseX - c.relPos[0][0], c.in.MouseY - c.relPos[0][1]}
		} else if c.active[0] == k {
			s.DragDelta = [2]float32{c.in.MouseX - c.pressPos[0][0], c.in.MouseY - c.pressPos[0][1]}
		}
	}
	if b.flags&scrollMask != 0 {
		s.Scroll = c.appliedTo(k)
	}
	if c.focusKey == k && b.flags&Focusable != 0 {
		if c.activated == k {
			s.Activated = true // taken by an earlier call this frame
		}
		for i := 0; i < c.in.NKeys && !s.Activated; i++ {
			if e := &c.in.Keys[i]; e.Down && !e.Repeat && (e.Code == KeyEnter || e.Code == KeySpace) && c.claimed&(1<<uint(i)) == 0 {
				c.claimed |= 1 << uint(i)
				c.activated = k
				s.Activated = true
			}
		}
	}
	return s
}

// PeekSignal is Signal for the box with key k as the previous frame left it,
// usable before the box is built this frame (a widget that wants to change
// the state its own build reads, such as a splitter that moves the layout).
// It reports the zero Signal for a key no box had last frame.
func (c *Context) PeekSignal(k Key) Signal {
	if v, ok := c.table.Get(uint64(k)); ok {
		return c.Signal(ID(v))
	}
	return Signal{}
}

// HotTOf, ActiveTOf and FocusTOf are HotT, ActiveT and FocusT of the box
// with key k as of the previous End, 0 for a key no box had; styling a box
// from its own animation value needs them, because the box cannot be asked
// before it is built.
func (c *Context) HotTOf(k Key) float32 { return c.tOf(k, 0) }

// ActiveTOf is ActiveT of the box with key k as of the previous End; see HotTOf.
func (c *Context) ActiveTOf(k Key) float32 { return c.tOf(k, 1) }

// FocusTOf is FocusT of the box with key k as of the previous End; see HotTOf.
func (c *Context) FocusTOf(k Key) float32 { return c.tOf(k, 2) }

func (c *Context) tOf(k Key, which int) float32 {
	v, ok := c.table.Get(uint64(k))
	if !ok {
		return 0
	}
	b := &c.boxes[v]
	switch which {
	case 0:
		return b.hotT
	case 1:
		return b.activeT
	}
	return b.focusT
}

// Hot reports whether id is the box under the pointer (ignoring captures).
func (c *Context) Hot(id ID) bool { return id != 0 && c.hotKey == c.boxes[id].key }

// TooltipHovered reports whether id is under the pointer for a tooltip,
// including disabled boxes, with the same clipping and draw order as Hot.
// It does not make the box hot, pressable or focusable.
func (c *Context) TooltipHovered(id ID) bool {
	return id != 0 && c.tooltipKey == c.boxes[id].key
}

// HotKey is the key of the hit-testable box under the pointer, 0 if none.
func (c *Context) HotKey() Key { return c.hotKey }

// ActiveKey is the key of the box that holds the left mouse button, 0 if none.
func (c *Context) ActiveKey() Key { return c.active[0] }

// FocusKey is the key of the focused box, 0 if none.
func (c *Context) FocusKey() Key { return c.focusKey }

// SetFocus moves focus to the box with key k (0 clears it).
func (c *Context) SetFocus(k Key) { c.focusKey = k }

// Focused reports whether id has focus.
func (c *Context) Focused(id ID) bool { return id != 0 && c.focusKey == c.boxes[id].key }

// HotT, ActiveT and FocusT are the animated 0..1 values of a box (they move
// only when the matching animation flag is set).
func (c *Context) HotT(id ID) float32 { return c.boxes[id].hotT }

// ActiveT is the animated 0..1 press value of box id; see HotT.
func (c *Context) ActiveT(id ID) float32 { return c.boxes[id].activeT }

// FocusT is the animated 0..1 focus value of box id; see HotT.
func (c *Context) FocusT(id ID) float32 { return c.boxes[id].focusT }

// ---- scrolling and lists ----

// Scroll returns the current scroll offset of id in pixels.
func (c *Context) Scroll(id ID) (x, y float64) { return c.boxes[id].scroll[0], c.boxes[id].scroll[1] }

// SetScroll jumps the scroll offset of id (and its target); the layout clamps
// it to the content. A trackpad's stretch of the box is let go (the jump
// shows exactly the offset); see AnchorScroll.
func (c *Context) SetScroll(id ID, x, y float64) {
	b := &c.boxes[id]
	b.scroll, b.scrollTgt = [2]float64{x, y}, [2]float64{x, y}
	c.unstretch(b.key, 3)
}

// AnchorScroll is SetScroll for a view that states its position again every
// frame, such as a list kept on an anchor item (base.VList): the offset is
// the same place in other terms, so a trackpad's stretch of the box stays.
func (c *Context) AnchorScroll(id ID, x, y float64) {
	b := &c.boxes[id]
	b.scroll, b.scrollTgt = [2]float64{x, y}, [2]float64{x, y}
}

// ScrollTo sets the scroll target of id; the offset approaches it. The target
// is clamped to the content when the frame is laid out (End), and End marks the
// frame animating if the offset is not there yet, so a frame loop that draws
// only while Animating carries on to the target, also when this frame's own
// content is what makes the target valid.
//
// A trackpad's stretch of the box is let go, so that the glide ends at the
// target.
func (c *Context) ScrollTo(id ID, x, y float64) {
	c.boxes[id].scrollTgt = [2]float64{x, y}
	c.scrollSet = true
	c.unstretch(c.boxes[id].key, 3)
}

// scrollPending reports whether a scroll offset of a box built this frame is
// still short of its (laid out, so clamped) target. End calls it after layout
// when ScrollTo was used this frame.
func (c *Context) scrollPending() bool {
	for _, id := range c.order {
		b := &c.boxes[id]
		for a := 0; a < 2; a++ {
			if b.scrollOn(a) && math.Abs(b.scrollTgt[a]-b.scroll[a]) >= scrollEps {
				return true
			}
		}
	}
	return false
}

// ScrollExtent returns the size of the content of the scrolling box id and of
// its view (the box itself), in pixels, from the previous layout: the content
// is what the box scrolls over, padding included, and scrolls by at most
// content − view on each axis. Both are zero for a box that was not laid out.
func (c *Context) ScrollExtent(id ID) (content, view [2]float32) {
	b := &c.boxes[id]
	content = [2]float32{float32(b.content[0]), float32(b.content[1])}
	c.noteRead(id, readExtent, [4]float32{content[0], content[1], b.size[0], b.size[1]})
	return content, b.size
}

// ScrollIntoView scrolls box id by the smallest amount that shows the
// rectangle r (x0, y0, x1, y1) of its content, in content coordinates: from the
// top left corner of the content, padding included (ScrollExtent's space). A
// rectangle already in view moves nothing, one that is larger than the view is
// aligned at its start. The offset approaches the target like ScrollTo, and
// the target is clamped to the content when the frame is laid out. It
// compares with the target of an earlier ScrollTo or ScrollIntoView of the
// frame, so a series of calls (a selection that grows) accumulates. Axes the
// box does not scroll on are left alone. A trackpad's stretch that would
// clip the rectangle is let go.
//
// The view is the box less what covers it (SetObscured): a rectangle under
// a toolbar's band is out of view, and is revealed below it.
func (c *Context) ScrollIntoView(id ID, r [4]float32) {
	b := &c.boxes[id]
	var clipped uint8
	for a := 0; a < 2; a++ {
		if !b.scrollOn(a) {
			continue
		}
		top, bottom := 0.0, 0.0
		if a == 1 {
			top, bottom = float64(b.obscured[0]), float64(b.obscured[1])
		}
		view := math.Max(float64(b.size[a])-top-bottom, 0)
		lo, hi := float64(r[a])-top, float64(r[a+2])-top
		t := b.scrollTgt[a]
		switch {
		case hi-lo >= view || lo < t:
			t = lo
		case hi > t+view:
			t = hi - view
		}
		b.scrollTgt[a] = math.Max(t, 0)
		if s := c.stretchOf(b, a); s != 0 && (lo < b.scrollTgt[a]+s || hi > b.scrollTgt[a]+s+view) {
			clipped |= 1 << a
		}
	}
	c.unstretch(b.key, clipped)
	c.scrollSet = true
}

// SetObscured says that this frame the view of scroll box id is covered by
// top pixels at its top and bottom at its bottom (a toolbar's band over a
// view that runs under it): its content scrolls there but is not in view, so
// every reveal (ScrollIntoView, ActScrollIntoView, the keyboard's) brings a
// rectangle between them. Call it after the box is built, each frame.
func (c *Context) SetObscured(id ID, top, bottom float32) {
	c.boxes[id].obscured = [2]float32{max(top, 0), max(bottom, 0)}
}

// StopWheel makes box id, this frame, keep the wheel and the trackpad's
// scrolling from the scroll boxes beneath it (drawn before it): over it they
// scroll only a scroll box inside it. A toolbar's band over content that runs
// under it is one: AppKit's titlebar views do not pass the wheel on to the
// content (measured on 27.2 through NSWindow's routing).
func (c *Context) StopWheel(id ID) { c.boxes[id].stopWheel = true }

// ListRange declares box id a virtual list of rows fixed-height rows and
// returns the half-open row range [first, last) to build: the rows that
// intersect the box (its previous-frame height, or the window height the
// first time) at the current scroll offset, widened by overscan rows on each
// side. It is O(1) whatever rows is.
//
// The box must scroll vertically (ScrollY) and lay out its children along Y
// with gap 0. The app builds only the rows first..last-1, as direct
// children with height rowHeightPx (rows should be keyed by row number, see
// KeyN, so their state is stable while scrolling). The layout places the
// first built row at first×rowHeightPx and sets the scroll content height
// to rows×rowHeightPx plus padding: no spacer boxes are needed.
//
// Offsets are kept in float64, so 1M rows of 24 px (24M px, beyond float32's
// exact integers) still position rows to the pixel.
func (c *Context) ListRange(id ID, rows int, rowHeightPx float32, overscan int) (first, last int) {
	b := &c.boxes[id]
	if rows <= 0 || rowHeightPx <= 0 {
		b.listRows, b.listFirst = 0, 0
		return 0, 0
	}
	c.noteRead(id, readRect, b.rect)
	viewH := float64(b.rect[3] - b.rect[1] - b.padSum(1))
	if viewH <= 0 {
		viewH = float64(c.h)
	}
	rh := float64(rowHeightPx)
	scroll := b.scroll[1]
	if hi := float64(rows)*rh - viewH; scroll > hi {
		scroll = hi
	}
	scroll = math.Max(scroll, 0)
	// the rows start below the top padding (a scroll inset under a
	// toolbar): the view's top shows them from the offset less that
	vf := int(math.Floor(math.Max(scroll-float64(b.pad[1]), 0) / rh))
	vl := int(math.Ceil((scroll + viewH) / rh))
	first = min(max(vf-overscan, 0), rows)
	last = min(max(vl+overscan, first), rows)
	b.listRows, b.listFirst, b.listRowH = rows, first, rowHeightPx
	return first, last
}
