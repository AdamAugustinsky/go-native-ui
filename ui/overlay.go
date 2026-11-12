package ui

// Overlays, focus and modality (phase 5, D28).
//
// An overlay is a subtree that is laid out and drawn above the main tree and
// hit-tested before it, wherever in the build it is made: BeginOverlay …
// EndOverlay can be called inside any box (a menu inside its button), and the
// overlay's boxes belong to the overlay, not to that box. Each overlay is
// placed against an anchor (a rectangle, or a box laid out in the same frame:
// BeginOverlayOn) and kept inside the window's usable area (D106). They form a
// stack in build order (a submenu is built inside its menu and lies above it).
//
// Placement. A popup or tooltip goes on its Placement's side of the anchor,
// OverlayOpts.Gap away, aligned with the anchor by OverlayOpts.Align. It keeps
// 8 pt from the window's edges and out of the insets (SetOverlayInsets, and the
// system's window buttons, SystemButtons, below which nothing is placed). On
// the side's axis it flips to the other side when its own has no room and the
// other has; when neither has, it takes the larger, and with OverlayOpts.Fit
// its length is bounded to that room (a scroll box in it that can give, sized
// Pct, Grow or Children(0), then scrolls); without Fit it shifts into the
// area. Along the anchor, an alignment that runs out flips to the other end
// when that fits, then shifts, and the overlay keeps touching its anchor while
// the window allows. A dialog (OverlayModal) and PlaceCenter centre on the
// anchor and are kept inside the window only. A nested popup and a tooltip
// follow the same rules.
//
// A popup (menu, dropdown) is closed by a press outside it and by Escape; ui
// reports that to the popup's owner (OverlaySignal.Dismissed) and the owner
// stops building it. NextOverlay changes that (Dismissal: what closes it and
// what becomes of the press) and can make a popup that takes no focus. A modal (dialog) blocks hit testing, scrolling, drags,
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

// Dismissal says what closes a popup overlay and what becomes of the press that
// closes it. The three NSPopover behaviors are measured on macOS 27.2
// (tools/appkitref/popover.sh behavior, popover_behavior.m): a click outside
// closes a transient one and reaches the control under it, except on the
// anchor, which the click closes it on and does not reach; Escape closes
// transient and semitransient ones, not an application-defined one; a
// semitransient one closes on a click in the anchor's window outside it (the
// anchor then gets the click and the popover stays), not on one in another
// window of the app; an application-defined one is closed by nobody but its
// owner and every click passes through.
type Dismissal uint8

const (
	// DismissMenu is a menu's: Escape and a press outside close it, and a
	// press outside on the main tree is consumed (it does not also act on
	// what it hit). The zero value, and what OverlayPopup always was.
	DismissMenu Dismissal = iota
	// DismissTransient closes on Escape and on a press outside, which is not
	// consumed, except on the anchor: that press closes the popup and is
	// consumed, so a button that opens it does not reopen it.
	DismissTransient
	// DismissSemitransient closes on Escape and on a press outside it in this
	// window, which is not consumed; a press on the anchor neither closes it
	// nor is consumed. A context sees only its own window, so presses in
	// another window of the app, which a transient popover closes on and a
	// semitransient one ignores, are the app's to report (Context.Dismiss
	// does not exist: it stops building the popup).
	DismissSemitransient
	// DismissApplication is closed by its owner only: Escape and presses
	// outside are not taken and pass through.
	DismissApplication
)

// OverlayOpts are the options of the next popup overlay, set with
// NextOverlay.
type OverlayOpts struct {
	// Dismiss says what closes the popup (the zero value is a menu's).
	Dismiss Dismissal
	// NoFocus makes a popup that never takes the keyboard focus: the focus
	// stays where it is, a hosted native view's first responder included, when
	// the popup opens and closes, and Tab does not stay inside it. A
	// completion list is one.
	NoFocus bool
	// AvoidHosts makes a popup that never lies over a hosted native view
	// (Context.Host): a native view is above everything ui draws, so a popup
	// over one would hide the view (HostRegions). The popup's owner places it
	// beside the view from the last frame's layout; if the layout of this
	// frame, finished, puts it over a host after all (a parent moved the host
	// since), the popup is not drawn, takes no input and is not reported to
	// hide the view in this frame, and another frame is asked for, in which
	// the owner places it against the new layout. Only hosts below the popup
	// count.
	AvoidHosts bool

	// Align, Gap and Fit place any overlay but a dialog (a tooltip too; the
	// options above are a popup's only).

	// Align is where the overlay sits along its anchor's edge: OverlayStart
	// (the left edges of a PlaceBelow or PlaceAbove overlay and its anchor
	// aligned, the tops of a PlaceRight or PlaceLeft one; the zero value),
	// OverlayCenter or OverlayEnd.
	Align OverlayAlign
	// Gap is the room between the anchor and the overlay, in pixels.
	Gap float32
	// Fit bounds the overlay's length on its side's axis to the room there
	// when it fits on neither side (see Placement), and its other length to
	// the usable area.
	Fit bool
}

// OverlayAlign is where an overlay sits along its anchor's edge
// (OverlayOpts.Align).
type OverlayAlign uint8

const (
	// OverlayStart aligns the overlay's start with the anchor's: left edges
	// below or above it, tops beside it.
	OverlayStart OverlayAlign = iota
	// OverlayCenter centres the overlay on the anchor's edge.
	OverlayCenter
	// OverlayEnd aligns the ends: right edges below or above, bottoms beside.
	OverlayEnd
)

// NextOverlay sets the options of the next BeginOverlay (a popup or modal).
func (c *Context) NextOverlay(o OverlayOpts) { c.nextOvl = o }

// OverlaySignal is what BeginOverlay reports about the overlay.
type OverlaySignal struct {
	// Dismissed: the user asked to close it this frame (a press outside it, or
	// Escape); the owner stops building it.
	Dismissed bool
	// Rect is the overlay's rectangle (x0, y0, x1, y1) in the previous frame,
	// zero for one that has just opened.
	Rect [4]float32
	// Flipped: the previous frame placed it on the other side of its anchor
	// than its Placement asks, for want of room (an arrow points the other
	// way).
	Flipped bool
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
	on     ID   // the anchor box, laid out this frame (BeginOverlayOn); 0: anchor
	flip   bool // placed on the other side than asked (OverlaySignal.Flipped)
	opts   OverlayOpts
	// avoided: AvoidHosts left it out of this frame, because it lay over the
	// host whose clipped rectangle is avoidedHost.
	avoided     bool
	avoidedHost [4]float32
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
	return c.beginOverlay(key, kind, anchor, 0, place)
}

// BeginOverlayOn is BeginOverlay against box anchor of this frame (the button
// that opens a menu, the item of a submenu): the overlay is placed against the
// rectangle layout gives the anchor in this frame, not the last one, so it
// never lands a frame late and then moves. The anchor must be built before
// BeginOverlayOn (its ID says it is); the bound of OverlayOpts.Fit is
// reckoned from the anchor's last rectangle, as sizes are known before
// positions, and a frame is asked for when the anchor moved.
func (c *Context) BeginOverlayOn(key Key, kind OverlayKind, anchor ID, place Placement) OverlaySignal {
	if anchor == 0 || int(anchor) >= len(c.boxes) || c.boxes[anchor].lastFrame != c.frame {
		panic("ui: BeginOverlayOn with a box not built this frame")
	}
	return c.beginOverlay(key, kind, c.boxes[anchor].rect, anchor, place)
}

func (c *Context) beginOverlay(key Key, kind OverlayKind, anchor [4]float32, on ID, place Placement) OverlaySignal {
	if c.nOvs >= maxOverlays || c.nOvOpen >= len(c.ovOpen) {
		panic("ui: too many overlays")
	}
	okey := KeyN(overlayBase, uint64(key))
	_, existed := c.table.Get(uint64(okey))
	opts := c.nextOvl
	c.nextOvl = OverlayOpts{}
	if kind != OverlayPopup { // (those are a popup's; the placement is anyone's)
		opts = OverlayOpts{Align: opts.Align, Gap: opts.Gap, Fit: opts.Fit}
	}
	var flags Flags
	if kind != OverlayTooltip {
		flags = Hoverable | Focusable
		if opts.NoFocus {
			flags = Hoverable
		}
	}
	c.ovOpen[c.nOvOpen] = c.nparents
	c.nOvOpen++
	c.parentless = true
	id := c.BoxKeyed(flags, okey, nil)
	c.parentless = false
	b := &c.boxes[id]
	b.ovRoot, b.ovl = true, uint8(c.nOvs+1)
	if !existed && !opts.NoFocus {
		b.savedFocus = c.focusKey
	}
	c.ovs[c.nOvs] = overlay{id: id, key: b.key, kind: kind, place: place, opened: !existed, anchor: anchor, on: on, opts: opts}
	c.nOvs++
	c.Push(id)
	var s OverlaySignal
	s.Rect = b.rect
	for i := 0; i < c.nOvPrev; i++ {
		if c.ovPrev[i].key == b.key {
			s.Flipped = c.ovPrev[i].flip
		}
	}
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
		if c.ovPrev[i].kind != OverlayTooltip && !c.ovPrev[i].opts.NoFocus {
			return c.ovPrev[i].key == okey
		}
	}
	// None before: the first overlay built now is it.
	for i := 0; i < c.nOvs; i++ {
		if c.ovs[i].kind != OverlayTooltip && !c.ovs[i].opts.NoFocus {
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
	if in.FocusLost || !in.Focused {
		for i := 0; i < c.nOvPrev; i++ {
			p := &c.ovPrev[i]
			if p.kind == OverlayPopup && (p.opts.Dismiss == DismissMenu || p.opts.Dismiss == DismissTransient) {
				c.dismiss(p.key)
			}
		}
	}
	pressed := false
	for b := range in.Pressed {
		pressed = pressed || in.Pressed[b]
	}
	if pressed {
		level := -1 // the overlay the pointer is in, -1 for the main tree
		if c.hotID != 0 {
			level = int(c.boxes[c.hotID].ovl) - 1
		}
		mx, my := in.MouseX, in.MouseY
		menu := false // a menu-style popup closed: the press is consumed on the main tree
		for i := max(level, c.modalIdx) + 1; i < c.nOvPrev; i++ {
			p := &c.ovPrev[i]
			if p.kind != OverlayPopup {
				continue
			}
			onAnchor := mx >= p.anchor[0] && mx < p.anchor[2] && my >= p.anchor[1] && my < p.anchor[3]
			switch p.opts.Dismiss {
			case DismissMenu:
				c.dismiss(p.key)
				menu = true
			case DismissTransient:
				c.dismiss(p.key)
				if onAnchor { // the press that closes it on its anchor is the anchor's no more
					menu = true
				}
			case DismissSemitransient:
				if !onAnchor {
					c.dismiss(p.key)
				}
			}
		}
		// A press on the main tree that closed popups does not also act there.
		c.pressConsumed = menu && c.nDismiss > 0 && level < 0
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
			if c.ovPrev[i].opts.Dismiss == DismissApplication {
				continue // (its owner's: Escape is not taken)
			}
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

// overlayEdgePt is the room a popup or tooltip keeps from the window's edges
// and its insets, in points.
const overlayEdgePt = 8

// SetOverlayInsets keeps popups and tooltips out of the window's edges by
// these many pixels (left, top, right, bottom), on top of their 8 pt margin:
// a titlebar band the app draws under which nothing should open. The
// rectangle of the system's window buttons (SetSystemButtons) is a top inset
// already. It holds until it is set again.
func (c *Context) SetOverlayInsets(l, t, r, b float32) { c.ovInsets = [4]float32{l, t, r, b} }

// OverlayInsets are the insets popups and tooltips keep out of (left, top,
// right, bottom, pixels): SetOverlayInsets', with the top at least the bottom
// of the system's window buttons when there are any.
func (c *Context) OverlayInsets() [4]float32 {
	in := c.ovInsets
	if sb := c.win.buttons; sb[2] > sb[0] && sb[3] > sb[1] {
		in[1] = max(in[1], sb[3])
	}
	return in
}

// overlayArea is where overlay ov may lie: lo and hi per axis, the window less
// the insets and the margin, and insLo and insHi, the window less the insets
// only (a dialog and a centred overlay: the window).
func (c *Context) overlayArea(ov *overlay) (lo, hi, insLo, insHi [2]float32) {
	W, H := float32(c.w), float32(c.h)
	if ov.kind == OverlayModal || ov.place == PlaceCenter {
		return [2]float32{}, [2]float32{W, H}, [2]float32{}, [2]float32{W, H}
	}
	in := c.OverlayInsets()
	m := c.Pt(overlayEdgePt)
	insLo, insHi = [2]float32{in[0], in[1]}, [2]float32{W - in[2], H - in[3]}
	return [2]float32{insLo[0] + m, insLo[1] + m}, [2]float32{insHi[0] - m, insHi[1] - m}, insLo, insHi
}

// sideAxis is the axis along which an overlay placed so leaves its anchor: Y
// below and above, X beside.
func sideAxis(p Placement) int {
	if p == PlaceRight || p == PlaceLeft {
		return 0
	}
	return 1
}

// overlaySide is on which side of anchor a an overlay ov of length size on
// its side's axis goes: after (below, right) or before, and the room there.
// The room is reckoned within lo..hi (the margin kept); when the overlay fits
// on neither side so, but does against the insets (insLo..insHi), the margin
// yields: an owner that sized it to the room up to the window's edge (a
// completion list above its field) keeps it against its anchor. It flips to
// the other side when its own has too little room and the other has enough,
// or more.
func overlaySide(ov *overlay, a [4]float32, size float32, lo, hi, insLo, insHi [2]float32) (after bool, room float32) {
	ax, g := sideAxis(ov.place), ov.opts.Gap
	pref := ov.place == PlaceBelow || ov.place == PlaceRight
	rooms := func(lo, hi [2]float32) (own, other float32) {
		before, aft := a[ax]-g-lo[ax], hi[ax]-(a[ax+2]+g)
		if pref {
			return aft, before
		}
		return before, aft
	}
	own, other := rooms(lo, hi)
	if size > own && size > other {
		if io, ix := rooms(insLo, insHi); size <= io {
			return pref, io
		} else if size <= ix {
			return !pref, ix
		}
	}
	if size > own && (size <= other || other > own) {
		return !pref, max(other, 0)
	}
	return pref, max(own, 0)
}

// fitOverlay bounds overlay root b's size on axis a for OverlayOpts.Fit (the
// violations stage of sizeAxis, before its children are fitted to it).
func (c *Context) fitOverlay(b *box, a int) {
	ov := &c.ovs[b.ovl-1]
	if !ov.opts.Fit || ov.kind == OverlayModal || ov.place == PlaceCenter {
		return
	}
	lo, hi, insLo, insHi := c.overlayArea(ov)
	if a == sideAxis(ov.place) {
		if _, room := overlaySide(ov, ov.anchor, b.size[a], lo, hi, insLo, insHi); b.size[a] > room && room > 0 {
			b.size[a] = room
		}
		return
	}
	if r := hi[a] - lo[a]; b.size[a] > r && r > 0 {
		b.size[a] = r
	}
}

// placeOverlay positions an overlay root against its anchor once its size is
// known (see Placement in the package's overlay notes): side, alignment, shift
// into the usable area, attachment to the anchor, and the window last.
func (c *Context) placeOverlay(b *box) {
	ov := &c.ovs[b.ovl-1]
	if ov.on != 0 { // the anchor's rectangle of this frame: built before, so placed already
		if r := c.boxes[ov.on].rect; r != ov.anchor {
			if ov.opts.Fit {
				c.animating = true // the bound was reckoned from where it was
			}
			ov.anchor = r
		}
	}
	a := ov.anchor
	win := [2]float32{float32(c.w), float32(c.h)}
	size := b.size
	var pos [2]float32
	ov.flip = false
	if ov.kind == OverlayModal || ov.place == PlaceCenter {
		pos = legacySide(ov.place, a, size)
	} else {
		lo, hi, insLo, insHi := c.overlayArea(ov)
		ax := sideAxis(ov.place)
		cx := 1 - ax
		after, room := overlaySide(ov, a, size[ax], lo, hi, insLo, insHi)
		ov.flip = after != (ov.place == PlaceBelow || ov.place == PlaceRight)
		if after {
			pos[ax] = a[ax+2] + ov.opts.Gap
		} else {
			pos[ax] = a[ax] - ov.opts.Gap - size[ax]
		}
		start, end := a[cx], a[cx+2]-size[cx]
		switch ov.opts.Align {
		case OverlayCenter:
			pos[cx] = (a[cx] + a[cx+2] - size[cx]) / 2
		case OverlayEnd:
			pos[cx] = end
			if end < lo[cx] && start+size[cx] <= hi[cx] { // no room before it: the starts aligned
				pos[cx] = start
			}
		default:
			pos[cx] = start
			if start+size[cx] > hi[cx] && end >= lo[cx] { // no room after it: the ends aligned, as a menu from a button near the window's end
				pos[cx] = end
			}
		}
		for i := 0; i < 2; i++ { // shift into the area; one too large for it keeps the insets, then the window
			l, h := lo[i], hi[i]
			if i == ax && size[i] <= room { // it fits there: against its anchor, the margin is for its far edge
				l, h = insLo[i], insHi[i]
			}
			if size[i] > h-l {
				l, h = insLo[i], insHi[i]
			}
			if size[i] > h-l {
				l, h = 0, win[i]
			}
			pos[i] = max(min(pos[i], h-size[i]), l)
		}
		// still touching its anchor along the anchor's edge (into the margin,
		// not the insets)
		pos[cx] = min(max(pos[cx], a[cx]-size[cx]), a[cx+2])
		if size[cx] <= insHi[cx]-insLo[cx] {
			pos[cx] = max(min(pos[cx], insHi[cx]-size[cx]), insLo[cx])
		}
	}
	for i := 0; i < 2; i++ {
		pos[i] = max(min(pos[i], win[i]-size[i]), 0)
	}
	b.origin = pos
	setRect(b)
}

// legacySide is where a dialog or a centred overlay goes, before it is kept
// in the window: centred on the anchor (PlaceCenter), or on a side of it with
// the starts aligned (a dialog takes none of a popup's rules).
func legacySide(p Placement, a [4]float32, size [2]float32) [2]float32 {
	switch p {
	case PlaceBelow:
		return [2]float32{a[0], a[3]}
	case PlaceAbove:
		return [2]float32{a[0], a[1] - size[1]}
	case PlaceRight:
		return [2]float32{a[2], a[1]}
	case PlaceLeft:
		return [2]float32{a[0] - size[0], a[1]}
	}
	return [2]float32{(a[0] + a[2] - size[0]) / 2, (a[1] + a[3] - size[1]) / 2}
}

// avoidHosts hides, in the finished layout, the AvoidHosts overlays that lie
// over a host (End, after layout, before the hidden marks are spread to the
// descendants). Another frame is asked for only when the overlap is new: an
// overlay already left out for the same host rectangle in the previous frame
// is one whose owner cannot place it clear, and asking again would draw
// forever.
func (c *Context) avoidHosts() {
	if c.shell.frame != c.frame || c.shell.nHosts == 0 {
		return
	}
	for i := 0; i < c.nOvs; i++ {
		ov := &c.ovs[i]
		b := &c.boxes[ov.id]
		if !ov.opts.AvoidHosts || b.hid {
			continue
		}
		for j := 0; j < c.shell.nHosts; j++ {
			h := c.shell.hosts[j].id
			hb := &c.boxes[h]
			if hb.lastFrame != c.frame || hb.hid || int(hb.ovl) >= i+1 {
				continue
			}
			if !empty(intersect(b.rect, c.clippedRect(h))) {
				c.hide(b, b.origin[0], b.origin[1])
				ov.avoided, ov.avoidedHost = true, c.clippedRect(h)
				if !c.avoidedBefore(ov) {
					c.animating = true
				}
				break
			}
		}
	}
}

// avoidedBefore reports whether the overlay ov, with the same key, was left
// out over the same host rectangle in the previous frame.
func (c *Context) avoidedBefore(ov *overlay) bool {
	for i := 0; i < c.nOvPrev; i++ {
		if p := &c.ovPrev[i]; p.key == ov.key {
			return p.avoided && p.avoidedHost == ov.avoidedHost
		}
	}
	return false
}

// overlayFocus moves the focus for the overlays that closed and opened in this
// frame (End, after layout).
func (c *Context) overlayFocus() {
	// Closed overlays, the topmost first: the focus goes back to where it was
	// when they opened, if it is lost with them.
	for i := c.nOvPrev - 1; i >= 0; i-- {
		p := &c.ovPrev[i]
		b := &c.boxes[p.id]
		if b.lastFrame == c.frame || p.kind == OverlayTooltip {
			continue
		}
		if b.savedFocus == 0 {
			// It opened with nothing focused: the focus it took (its root or
			// a box in it, all built last frame only) goes with it, rather
			// than naming a box that no longer exists (D90). A
			// pending focus elsewhere stays.
			if v, ok := c.table.Get(uint64(c.focusKey)); ok && c.focusKey != 0 && c.boxes[v].lastFrame != c.frame && int(c.boxes[v].ovl) == i+1 {
				c.focusKey = 0
				c.animating = true
			}
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
		if !ov.opened || ov.kind == OverlayTooltip || ov.opts.NoFocus {
			continue
		}
		if v := c.focusBox(c.focusKey); v != 0 && c.boxes[v].lastFrame == c.frame && int(c.boxes[v].ovl) == i+1 {
			continue // the owner already put the focus inside
		}
		to := c.boxes[ov.id].key
		for _, id := range c.order {
			b := &c.boxes[id]
			if int(b.ovl) != i+1 || b.ovRoot || b.hid || b.flags&Disabled != 0 {
				continue
			}
			if b.flags&Focusable != 0 {
				to = b.key
				break
			}
			if r := c.recOf(b); r != nil {
				for _, f := range c.richFocus[r.focusOff : r.focusOff+r.focusN] {
					if f.shown {
						to = f.key
						break
					}
				}
				if to != c.boxes[ov.id].key {
					break
				}
			}
		}
		c.focusKey = to
		c.animating = true
	}
}

// builtThisFrame reports whether the box with key k exists in this frame's tree.
func (c *Context) builtThisFrame(k Key) bool {
	if k == 0 {
		return false
	}
	v := c.focusBox(k)
	return v != 0 && c.boxes[v].lastFrame == c.frame
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
		if c.ovs[i].kind != OverlayTooltip && !c.ovs[i].opts.NoFocus {
			return uint8(i + 1)
		}
	}
	return 0
}
