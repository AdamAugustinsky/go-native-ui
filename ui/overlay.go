package ui

// Overlays, focus and modality (phase 5, D28).
//
// An overlay is a subtree that is laid out and drawn above the main tree and
// hit-tested before it, wherever in the build it is made: BeginOverlay …
// EndOverlay can be called inside any box (a menu inside its button), and the
// overlay's boxes belong to the overlay, not to that box. Each overlay is
// placed against an anchor rectangle and kept inside the window. They form a
// stack in build order (a submenu is built inside its menu and lies above it).
//
// A popup (menu, dropdown) is closed by a press outside it and by Escape; ui
// reports that to the popup's owner (OverlaySignal.Dismissed) and the owner
// stops building it. A modal (dialog) blocks hit testing, scrolling, drags,
// text input, keys and shortcuts beneath it, and Tab stays inside. A tooltip
// takes no input and never takes focus.
//
// Focus moves into an overlay when it opens (to its first focusable box, else
// to the overlay itself) and goes back to where it was when it closes, unless
// the focus has moved elsewhere by then.

// OverlayKind selects how an overlay behaves.
type OverlayKind uint8

const (
	// OverlayPopup is a menu or dropdown: closed by a press outside it and by
	// Escape.
	OverlayPopup OverlayKind = iota
	// OverlayModal is a dialog: everything beneath it is inert.
	OverlayModal
	// OverlayTooltip takes no input and no focus.
	OverlayTooltip
)

// Placement says where an overlay goes relative to its anchor rectangle.
type Placement uint8

const (
	// PlaceBelow puts the overlay under the anchor with their left edges
	// aligned; if it does not fit there and fits above, it goes above.
	PlaceBelow Placement = iota
	// PlaceAbove is PlaceBelow the other way round.
	PlaceAbove
	// PlaceRight puts the overlay beside the anchor's right edge with their
	// tops aligned (a submenu); it flips to the left side if it does not fit.
	PlaceRight
	// PlaceLeft is PlaceRight the other way round.
	PlaceLeft
	// PlaceCenter centres the overlay on the anchor (a dialog on the window).
	PlaceCenter
)

// OverlaySignal is what BeginOverlay reports about the overlay.
type OverlaySignal struct {
	// Dismissed: the user asked to close it this frame (a press outside it, or
	// Escape); the owner stops building it.
	Dismissed bool
	// Rect is the overlay's rectangle (x0, y0, x1, y1) in the previous frame,
	// zero for one that has just opened.
	Rect [4]float32
}

// maxOverlays is the number of overlays one frame can hold at once.
const maxOverlays = 16

// overlay is one overlay of a frame, in build order.
type overlay struct {
	id     ID
	key    Key
	kind   OverlayKind
	place  Placement
	opened bool // it was not built in the previous frame
	anchor [4]float32
}

var overlayBase = HashString(0, "ui:overlay")

// BeginOverlay starts an overlay and makes it the parent of the boxes built
// next, until EndOverlay. key identifies it among the overlays (the owner's
// state, as for any box); anchor is the rectangle (x0, y0, x1, y1 in window
// pixels, such as Rect of the button that opened it) it is placed against.
//
// The overlay box takes the styles set with the Next… setters (NextBG,
// NextPad, NextSize, ...); a size left on auto is the size of its children, and
// a percentage is a fraction of the window. A popup or modal takes input (it is
// Hoverable, so what it covers is not hit-tested) and can have the focus;
// a tooltip is inert. At most 16 overlays can be open; one more panics.
func (c *Context) BeginOverlay(key Key, kind OverlayKind, anchor [4]float32, place Placement) OverlaySignal {
	if c.nOvs >= maxOverlays || c.nOvOpen >= len(c.ovOpen) {
		panic("ui: too many overlays")
	}
	okey := KeyN(overlayBase, uint64(key))
	_, existed := c.table.Get(uint64(okey))
	var flags Flags
	if kind != OverlayTooltip {
		flags = Hoverable | Focusable
	}
	c.ovOpen[c.nOvOpen] = c.nparents
	c.nOvOpen++
	c.parentless = true
	id := c.BoxKeyed(flags, okey, nil)
	c.parentless = false
	b := &c.boxes[id]
	b.ovRoot, b.ovl = true, uint8(c.nOvs+1)
	if !existed {
		b.savedFocus = c.focusKey
	}
	c.ovs[c.nOvs] = overlay{id: id, key: b.key, kind: kind, place: place, opened: !existed, anchor: anchor}
	c.nOvs++
	c.Push(id)
	var s OverlaySignal
	s.Rect = b.rect
	for i := 0; i < c.nDismiss; i++ {
		if c.dismissed[i] == b.key {
			s.Dismissed = true
		}
	}
	return s
}

// EndOverlay ends the overlay BeginOverlay started; the parent is what it was
// before.
func (c *Context) EndOverlay() {
	if c.nOvOpen == 0 {
		panic("ui: EndOverlay without BeginOverlay")
	}
	c.nOvOpen--
	c.nparents = c.ovOpen[c.nOvOpen]
}

// ModalOpen reports whether a modal overlay is open: one that was built in the
// previous frame or that has been built so far in this one. Commands and
// shortcuts of the app (package menu) do not act while it is.
func (c *Context) ModalOpen() bool {
	if c.modalIdx >= 0 {
		return true
	}
	for i := 0; i < c.nOvs; i++ {
		if c.ovs[i].kind == OverlayModal {
			return true
		}
	}
	return false
}

// OverlayOpen reports whether the overlay with the given key was built in the
// previous frame (so it is open now, until its owner stops building it).
func (c *Context) OverlayOpen(key Key) bool {
	v, ok := c.table.Get(uint64(KeyN(overlayBase, uint64(key))))
	return ok && c.boxes[v].lastFrame == c.frame-1 && c.boxes[v].ovRoot
}

// OverlayTopmost reports whether the overlay with the given key (as passed to
// BeginOverlay) is the one that takes the keys of this frame: the topmost
// popup or modal of the previous frame's stack (tooltips do not count), or,
// when that frame had none, an overlay that opens in this one and is the first
// built. Only that overlay may act on Enter, Escape and the like, whatever
// order the overlays are built in: a dialog that does not check this lets the
// dialog beneath it take the key first. An overlay that opens above others
// takes keys from the next frame on.
func (c *Context) OverlayTopmost(key Key) bool {
	okey := KeyN(overlayBase, uint64(key))
	for i := c.nOvPrev - 1; i >= 0; i-- {
		if c.ovPrev[i].kind != OverlayTooltip {
			return c.ovPrev[i].key == okey
		}
	}
	// None before: the first overlay built now is it.
	for i := 0; i < c.nOvs; i++ {
		if c.ovs[i].kind != OverlayTooltip {
			return c.ovs[i].key == okey
		}
	}
	return false
}

// topModal is the index of the topmost modal overlay of the previous frame, -1
// if there is none.
func (c *Context) topModal() int {
	for i := c.nOvPrev - 1; i >= 0; i-- {
		if c.ovPrev[i].kind == OverlayModal {
			return i
		}
	}
	return -1
}

// blocked reports whether box b lies beneath the modal overlay: it takes no
// input then.
func (c *Context) blocked(b *box) bool { return c.modalIdx >= 0 && int(b.ovl)-1 < c.modalIdx }

// overlayInput is the first step of input distribution (Begin, after hit
// testing): a press outside the popups closes them, and the press is consumed.
func (c *Context) overlayInput() {
	c.nDismiss, c.pressConsumed = 0, false
	if c.nOvPrev == 0 {
		return
	}
	in := &c.in
	pressed := false
	for b := range in.Pressed {
		pressed = pressed || in.Pressed[b]
	}
	if pressed {
		level := -1 // the overlay the pointer is in, -1 for the main tree
		if c.hotID != 0 {
			level = int(c.boxes[c.hotID].ovl) - 1
		}
		for i := max(level, c.modalIdx) + 1; i < c.nOvPrev; i++ {
			if c.ovPrev[i].kind == OverlayPopup {
				c.dismiss(c.ovPrev[i].key)
			}
		}
		// A press on the main tree that closed popups does not also act there.
		c.pressConsumed = c.nDismiss > 0 && level < 0
	}
}

// overlayEscape closes the innermost popup on Escape (after the focused text
// field has claimed what it handles).
func (c *Context) overlayEscape() {
	for i := c.nOvPrev - 1; i >= 0; i-- {
		switch c.ovPrev[i].kind {
		case OverlayTooltip:
			continue
		case OverlayPopup:
			if c.TakeKey(KeyEscape, 0) {
				c.dismiss(c.ovPrev[i].key)
			}
		}
		return
	}
}

func (c *Context) dismiss(k Key) {
	c.animating = true // the owner stops building it in the frame that follows
	if c.nDismiss < len(c.dismissed) {
		c.dismissed[c.nDismiss] = k
		c.nDismiss++
	}
}

// placeOverlay positions an overlay root against its anchor once its size is
// known, keeping it inside the window.
func (c *Context) placeOverlay(b *box) {
	ov := &c.ovs[b.ovl-1]
	W, H := float32(c.w), float32(c.h)
	w, h := b.size[0], b.size[1]
	a := ov.anchor
	var x, y float32
	switch ov.place {
	case PlaceBelow:
		x, y = a[0], a[3]
		if y+h > H && a[1]-h >= 0 {
			y = a[1] - h
		}
	case PlaceAbove:
		x, y = a[0], a[1]-h
		if y < 0 && a[3]+h <= H {
			y = a[3]
		}
	case PlaceRight:
		x, y = a[2], a[1]
		if x+w > W && a[0]-w >= 0 {
			x = a[0] - w
		}
	case PlaceLeft:
		x, y = a[0]-w, a[1]
		if x < 0 && a[2]+w <= W {
			x = a[2]
		}
	case PlaceCenter:
		x, y = (a[0]+a[2]-w)/2, (a[1]+a[3]-h)/2
	}
	x = max(min(x, W-w), 0)
	y = max(min(y, H-h), 0)
	b.origin = [2]float32{x, y}
	setRect(b)
}

// overlayFocus moves the focus for the overlays that closed and opened in this
// frame (End, after layout).
func (c *Context) overlayFocus() {
	// Closed overlays, the topmost first: the focus goes back to where it was
	// when they opened, if it is lost with them.
	for i := c.nOvPrev - 1; i >= 0; i-- {
		p := &c.ovPrev[i]
		b := &c.boxes[p.id]
		if b.lastFrame == c.frame || p.kind == OverlayTooltip || b.savedFocus == 0 {
			continue
		}
		if !c.builtThisFrame(c.focusKey) {
			if c.builtThisFrame(b.savedFocus) {
				c.focusKey = b.savedFocus
				c.animating = true
			} else {
				c.focusKey = 0
			}
		}
	}
	// Opened overlays: the focus goes into them.
	for i := 0; i < c.nOvs; i++ {
		ov := &c.ovs[i]
		if !ov.opened || ov.kind == OverlayTooltip {
			continue
		}
		if v, ok := c.table.Get(uint64(c.focusKey)); ok && c.boxes[v].lastFrame == c.frame && int(c.boxes[v].ovl) == i+1 {
			continue // the owner already put the focus inside
		}
		to := ov.id
		for _, id := range c.order {
			if b := &c.boxes[id]; int(b.ovl) == i+1 && !b.ovRoot && b.flags&(Focusable|Disabled) == Focusable {
				to = id
				break
			}
		}
		c.focusKey = c.boxes[to].key
		c.animating = true
	}
}

// builtThisFrame reports whether the box with key k exists in this frame's tree.
func (c *Context) builtThisFrame(k Key) bool {
	if k == 0 {
		return false
	}
	v, ok := c.table.Get(uint64(k))
	return ok && c.boxes[v].lastFrame == c.frame
}

// cancelBeneathModal drops the mouse captures (drags) of boxes beneath a modal
// that opened in this frame.
func (c *Context) cancelBeneathModal() {
	m := -1
	for i := 0; i < c.nOvs; i++ {
		if c.ovs[i].kind == OverlayModal {
			m = i
		}
	}
	if m < 0 {
		return
	}
	for btn := range c.active {
		if k := c.active[btn]; k != 0 {
			if v, ok := c.table.Get(uint64(k)); ok && c.boxes[v].lastFrame == c.frame && int(c.boxes[v].ovl)-1 < m {
				c.active[btn] = 0
			}
		}
	}
}

// focusScope is the overlay that Tab stays in: the topmost popup or modal of
// this frame's build (its index plus one), 0 for the main tree.
func (c *Context) focusScope() uint8 {
	for i := c.nOvs - 1; i >= 0; i-- {
		if c.ovs[i].kind != OverlayTooltip {
			return uint8(i + 1)
		}
	}
	return 0
}
