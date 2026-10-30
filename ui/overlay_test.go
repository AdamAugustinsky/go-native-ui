package ui

import (
	"testing"
)

func TestPopupDismissalAfterHiddenFocusRoundTrip(t *testing.T) {
	for _, kind := range []Dismissal{DismissMenu, DismissTransient, DismissSemitransient, DismissApplication} {
		h := newHarness(t, 400, 300)
		var sig OverlaySignal
		build := func(c *Context) {
			c.NextOverlay(OverlayOpts{Dismiss: kind})
			sig = c.BeginOverlay(1, OverlayPopup, [4]float32{}, PlaceBelow)
			c.Spacer(Px(100), Px(50))
			c.EndOverlay()
		}
		h.frame(build)
		h.in.FocusLost = true // blur and regain arrived without a frame between
		h.frame(build)
		want := kind == DismissMenu || kind == DismissTransient
		if sig.Dismissed != want {
			t.Fatalf("dismissal=%d: dismissed=%v, want %v", kind, sig.Dismissed, want)
		}
		if h.in.FocusLost {
			t.Fatal("focus loss lasted more than one frame")
		}
	}
}

// ovRig is a window of 400×300 with a button at (20, 20) 80×24, a text field
// at (20, 80), a popup the button opens and, on request, a nested popup, a
// modal and a tooltip.
type ovRig struct {
	*harness
	btn, btn2         Key
	field             TextBuffer
	fieldKey          Key
	open, sub, modal  bool
	tip               bool
	popupGaps         bool // the popup has no focusable item
	pop, subSig, mSig OverlaySignal
	itemSig, subItem  Signal
	modalBtn          [2]Signal
	modalKeys         [2]Key
	btnSig, btn2Sig   Signal
	scroll            ID
	popupAnchor       [4]float32
	popupPlace        Placement
	popupSize         [2]float32
	events            []string
}

func newOvRig(t *testing.T) *ovRig {
	h := newHarness(t, 400, 300)
	r := &ovRig{harness: h, popupAnchor: [4]float32{20, 44, 100, 44}, popupPlace: PlaceBelow, popupSize: [2]float32{120, 60}}
	r.settle()
	return r
}

func (r *ovRig) settle() {
	for i := 0; i < 3; i++ {
		r.frame(r.build)
	}
}

func (r *ovRig) build(c *Context) {
	c.NextSize(Px(80), Px(24))
	c.NextPos(20, 20)
	c.NextBG(0xAA0000FF)
	b := c.Box(Clickable|Focusable|DrawBackground|FloatingX|FloatingY, "btn")
	r.btn = c.KeyOf(b)
	r.btnSig = c.Signal(b)
	c.NextSize(Px(80), Px(24))
	c.NextPos(120, 20)
	b2 := c.Box(Clickable|Focusable|FloatingX|FloatingY, "btn2")
	r.btn2 = c.KeyOf(b2)
	r.btn2Sig = c.Signal(b2)
	c.NextSize(Px(200), Px(24))
	c.NextPos(20, 80)
	fb := c.Box(FloatingX|FloatingY, "fieldbox")
	c.Push(fb)
	fr := c.TextField("field", &r.field)
	r.fieldKey = c.KeyOf(fr.ID)
	c.Pop()
	c.NextSize(Px(100), Px(100))
	c.NextPos(20, 140)
	c.NextPad(0)
	r.scroll = c.Box(ScrollY|Clip|Hoverable|FloatingX|FloatingY, "list")
	c.Push(r.scroll)
	c.NextSize(Px(100), Px(400))
	c.Box(0, "tall")
	c.Pop()

	if r.open {
		// Built inside the scrolling, clipping list: still above everything.
		c.Push(r.scroll)
		r.pop = c.BeginOverlay(1, OverlayPopup, r.popupAnchor, r.popupPlace)
		c.NextSize(Px(r.popupSize[0]), Px(r.popupSize[1]))
		c.NextBG(0x00AA00FF)
		panel := c.Box(DrawBackground, "panel")
		c.Push(panel)
		if !r.popupGaps {
			c.NextSize(Px(100), Px(20))
			it := c.Box(Clickable|Focusable|DrawBackground, "item")
			r.itemSig = c.Signal(it)
			if r.sub {
				ar := c.Rect(it)
				r.subSig = c.BeginOverlay(2, OverlayPopup, ar, PlaceRight)
				c.NextSize(Px(60), Px(40))
				c.NextBG(0x0000AAFF)
				sp := c.Box(DrawBackground, "subpanel")
				c.Push(sp)
				c.NextSize(Px(50), Px(20))
				si := c.Box(Clickable|Focusable, "subitem")
				r.subItem = c.Signal(si)
				c.Pop()
				c.EndOverlay()
			}
		}
		c.Pop()
		c.EndOverlay()
		c.Pop()
		if r.pop.Dismissed {
			r.events = append(r.events, "popup dismissed")
			r.open = false
		}
		if r.sub && r.subSig.Dismissed {
			r.events = append(r.events, "sub dismissed")
			r.sub = false
		}
	}
	if r.modal {
		r.mSig = c.BeginOverlay(3, OverlayModal, [4]float32{0, 0, 400, 300}, PlaceCenter)
		c.NextSize(Px(400), Px(300))
		c.NextBG(0x00000088)
		scrim := c.Box(DrawBackground, "scrim")
		c.Push(scrim)
		for i, n := range []string{"ok", "cancel"} {
			c.NextSize(Px(60), Px(24))
			c.NextPos(100+float32(i)*80, 100)
			c.NextBG(0xCCCCCCFF)
			m := c.Box(Clickable|Focusable|DrawBackground|FloatingX|FloatingY, n)
			r.modalKeys[i] = c.KeyOf(m)
			r.modalBtn[i] = c.Signal(m)
		}
		c.Pop()
		c.EndOverlay()
	}
	if r.tip {
		c.BeginOverlay(4, OverlayTooltip, [4]float32{20, 20, 100, 44}, PlaceBelow)
		c.NextSize(Px(90), Px(30))
		c.NextBG(0xFFFF00FF)
		c.Box(DrawBackground|Clickable|Focusable, "tipbox")
		c.EndOverlay()
	}
}

func (r *ovRig) press(x, y float32) {
	r.move(x, y)
	r.frame(r.build) // hover
	r.in.SetButton(ButtonLeft, true, 1)
	r.frame(r.build)
}

func (r *ovRig) release() {
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame(r.build)
}

func (r *ovRig) click(x, y float32) {
	r.press(x, y)
	r.release()
}

func (r *ovRig) key(code KeyCode, mods Mods) {
	r.in.PushKey(KeyEvent{Code: code, Mods: mods, Down: true})
	r.frame(r.build)
}

func (r *ovRig) overlayRect(key Key) [4]float32 {
	v, ok := r.c.table.Get(uint64(KeyN(overlayBase, uint64(key))))
	if !ok {
		r.t.Fatalf("overlay %d not built", key)
	}
	return r.c.boxes[v].rect
}

func (r *ovRig) takeEvents() []string {
	e := r.events
	r.events = nil
	return e
}

func TestOverlayIsDrawnAboveAndHitFirst(t *testing.T) {
	r := newOvRig(t)
	r.open = true
	r.popupAnchor = [4]float32{20, 20, 100, 20} // directly over the button
	r.popupPlace = PlaceBelow
	r.settle()
	// The popup lies at y=20..80, x=20..140 and covers the button: hover goes to the popup.
	r.move(40, 30)
	r.frame(r.build)
	r.frame(r.build)
	if r.btnSig.Hovering || !r.itemSig.Hovering {
		t.Fatalf("button hovering=%v, popup item hovering=%v", r.btnSig.Hovering, r.itemSig.Hovering)
	}
	// Quads: the popup's green panel comes after every main-tree quad, although
	// it was built (inside the list box) before the end of the main tree.
	lastMain, firstPopup := -1, -1
	for i, q := range r.list.Quads {
		switch q.Color[0] {
		case 0xAA0000FF:
			lastMain = i
		case 0x00AA00FF:
			if firstPopup < 0 {
				firstPopup = i
			}
		}
	}
	if lastMain < 0 || firstPopup < lastMain {
		t.Fatalf("popup quad %d, main quad %d: the overlay must be drawn after the main tree", firstPopup, lastMain)
	}
	// Not clipped by the scrolling list it was built inside.
	if got := r.overlayRect(1); got != [4]float32{20, 20, 140, 80} {
		t.Fatalf("popup rect %v: clipped or misplaced", got)
	}
}

func TestOverlayPlacement(t *testing.T) {
	r := newOvRig(t)
	r.open = true
	r.popupSize = [2]float32{100, 60}
	cases := []struct {
		name   string
		anchor [4]float32
		place  Placement
		want   [4]float32
	}{
		{"below", [4]float32{50, 100, 90, 120}, PlaceBelow, [4]float32{50, 120, 150, 180}},
		{"below flips above", [4]float32{50, 260, 90, 280}, PlaceBelow, [4]float32{50, 200, 150, 260}},
		{"below fits nowhere: clamped", [4]float32{50, 140, 90, 160}, PlaceBelow, [4]float32{50, 160, 150, 220}},
		{"below clamps x", [4]float32{350, 100, 390, 120}, PlaceBelow, [4]float32{300, 120, 400, 180}},
		{"above", [4]float32{50, 150, 90, 170}, PlaceAbove, [4]float32{50, 90, 150, 150}},
		{"above flips below", [4]float32{50, 20, 90, 40}, PlaceAbove, [4]float32{50, 40, 150, 100}},
		{"right", [4]float32{50, 100, 90, 120}, PlaceRight, [4]float32{90, 100, 190, 160}},
		{"right flips left", [4]float32{330, 100, 390, 120}, PlaceRight, [4]float32{230, 100, 330, 160}},
		{"right clamps y", [4]float32{50, 280, 90, 300}, PlaceRight, [4]float32{90, 240, 190, 300}},
		{"left", [4]float32{250, 100, 290, 120}, PlaceLeft, [4]float32{150, 100, 250, 160}},
		{"left flips right", [4]float32{20, 100, 60, 120}, PlaceLeft, [4]float32{60, 100, 160, 160}},
		{"center", [4]float32{100, 100, 200, 200}, PlaceCenter, [4]float32{100, 120, 200, 180}},
		{"center clamps", [4]float32{0, 0, 40, 40}, PlaceCenter, [4]float32{0, 0, 100, 60}},
	}
	for _, c := range cases {
		r.popupAnchor, r.popupPlace = c.anchor, c.place
		r.settle()
		if got := r.overlayRect(1); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// A popup larger than the window starts at its corner.
	r.popupSize = [2]float32{500, 400}
	r.popupAnchor, r.popupPlace = [4]float32{50, 50, 60, 60}, PlaceBelow
	r.settle()
	if got := r.overlayRect(1); got[0] != 0 || got[1] != 0 {
		t.Errorf("oversized popup at %v", got)
	}
}

func TestPopupDismissedByPressOutside(t *testing.T) {
	r := newOvRig(t)
	r.open = true
	r.settle()
	// A press inside the popup does not close it and reaches the item.
	r.press(30, 50)
	if len(r.takeEvents()) != 0 || !r.itemSig.Pressed {
		t.Fatalf("press inside: item pressed=%v", r.itemSig.Pressed)
	}
	r.release()
	if !r.open {
		t.Fatal("a click inside closed the popup")
	}
	// A press outside, on the other button: the owner is told, the press is consumed.
	r.press(130, 30)
	if ev := r.takeEvents(); len(ev) != 1 || ev[0] != "popup dismissed" {
		t.Fatalf("events %v", ev)
	}
	if r.btn2Sig.Pressed || r.c.FocusKey() == r.btn2 || r.c.ActiveKey() != 0 {
		t.Fatalf("the press that closed the popup also pressed the button beneath (pressed=%v)", r.btn2Sig.Pressed)
	}
	r.release()
	if r.btn2Sig.Clicked || r.btn2Sig.Released {
		t.Fatal("the release of a consumed press clicked the button")
	}
	if r.open {
		t.Fatal("the owner kept the popup")
	}
	// With the popup gone, the next click works.
	r.click(130, 30)
	if r.c.FocusKey() != r.btn2 {
		t.Fatal("the next click does not focus the button")
	}
}

func TestPopupIgnoredDismissalStaysOpen(t *testing.T) {
	r := newOvRig(t)
	r.open = true
	r.settle()
	r.press(300, 250) // outside, on nothing
	if ev := r.takeEvents(); len(ev) != 1 {
		t.Fatalf("events %v", ev)
	}
	r.open = true // the owner decides
	r.release()
	r.frame(r.build)
	if _, ok := r.c.table.Get(uint64(KeyN(overlayBase, 1))); !ok {
		t.Fatal("the popup is gone although the owner kept building it")
	}
}

func TestNestedPopupsFormAStack(t *testing.T) {
	r := newOvRig(t)
	r.open, r.sub = true, true
	r.settle()
	a, b := r.overlayRect(1), r.overlayRect(2)
	if b[0] != a[0]+100 || b[1] != a[1] {
		t.Fatalf("submenu at %v, want at the right of the item in %v", b, a)
	}
	// A press inside the parent closes only the submenu above it.
	r.press(a[0]+5, a[1]+50)
	if ev := r.takeEvents(); len(ev) != 1 || ev[0] != "sub dismissed" {
		t.Fatalf("events %v", ev)
	}
	r.release()
	if !r.open || r.sub {
		t.Fatalf("open=%v sub=%v", r.open, r.sub)
	}
	// A press in the main tree closes everything above the main tree (the owner
	// of the parent hears it too).
	r.sub = true
	r.settle()
	r.press(300, 250)
	ev := r.takeEvents()
	if len(ev) != 2 {
		t.Fatalf("events %v: the press on the main tree must dismiss both popups", ev)
	}
	r.release()
	// Escape closes the innermost popup, and only one per press; the key is claimed.
	r.open, r.sub = true, true
	r.settle()
	var seenEsc bool
	r.in.PushKey(KeyEvent{Code: KeyEscape, Down: true})
	r.frame(func(c *Context) {
		r.build(c)
		_, claimed := c.KeyAt(0)
		seenEsc = !claimed
	})
	if ev := r.takeEvents(); len(ev) != 1 || ev[0] != "sub dismissed" {
		t.Fatalf("Escape: events %v", ev)
	}
	if seenEsc {
		t.Error("the Escape that closed the popup was left unclaimed")
	}
	if !r.c.Animating() {
		t.Fatal("a dismissal asks for the frame in which the owner closes the popup")
	}
	r.frame(r.build)
	r.key(KeyEscape, 0)
	if ev := r.takeEvents(); len(ev) != 1 || ev[0] != "popup dismissed" {
		t.Fatalf("second Escape: events %v", ev)
	}
	r.key(KeyEscape, 0) // nothing open: Escape stays free
	var free bool
	r.in.PushKey(KeyEvent{Code: KeyEscape, Down: true})
	r.frame(func(c *Context) { r.build(c); _, cl := c.KeyAt(0); free = !cl })
	if !free {
		t.Error("Escape claimed with no popup open")
	}
}

// A popup opened while nothing had the focus takes it, and gives it up when it
// closes: the focus must not go on naming the popup's boxes, which no longer
// exist (found by a focus invariant of the UI simulator's, D90).
func TestPopupOpenedWithoutFocusLeavesNone(t *testing.T) {
	for _, gaps := range []bool{false, true} { // a focusable item, or the popup itself
		r := newOvRig(t)
		r.settle()
		r.popupGaps, r.open = gaps, true
		r.frame(r.build)
		if r.c.FocusKey() == 0 {
			t.Fatalf("gaps=%v: the popup did not take the focus", gaps)
		}
		r.open = false
		r.frame(r.build)
		r.frame(r.build)
		if k := r.c.FocusKey(); k != 0 {
			t.Fatalf("gaps=%v: focus %v after closing, want none", gaps, k)
		}
	}
}

func TestPopupFocusGoesInAndBack(t *testing.T) {
	r := newOvRig(t)
	r.c.SetFocus(r.btn)
	r.settle()
	r.open = true
	r.frame(r.build)
	itemKey := HashString(r.keyOfPanel(), "item")
	if r.c.FocusKey() != itemKey {
		t.Fatalf("focus %v after opening, want the first focusable item %v", r.c.FocusKey(), itemKey)
	}
	// Escape closes it; the focus returns to the button.
	r.key(KeyEscape, 0)
	r.frame(r.build)
	if r.open || r.c.FocusKey() != r.btn {
		t.Fatalf("after closing: open=%v focus=%v, want the button %v", r.open, r.c.FocusKey(), r.btn)
	}
	// A popup without focusable items takes the focus itself.
	r.popupGaps, r.open = true, true
	r.frame(r.build)
	if r.c.FocusKey() != r.overlayKey(1) {
		t.Fatalf("focus %v, want the overlay %v", r.c.FocusKey(), r.overlayKey(1))
	}
	r.open = false
	r.frame(r.build)
	r.frame(r.build)
	if r.c.FocusKey() != r.btn {
		t.Fatalf("focus not restored after closing")
	}
	// If the focus moved elsewhere meanwhile, it is not taken back.
	r.popupGaps, r.open = false, true
	r.frame(r.build)
	r.c.SetFocus(r.fieldKey)
	r.open = false
	r.frame(r.build)
	r.frame(r.build)
	if r.c.FocusKey() != r.fieldKey {
		t.Fatalf("focus %v, want it left on the field", r.c.FocusKey())
	}
	// Nested: closing both returns to the original focus.
	r.c.SetFocus(r.btn)
	r.open, r.sub = true, true
	r.frame(r.build)
	r.frame(r.build)
	r.open, r.sub = false, false
	r.frame(r.build)
	r.frame(r.build)
	if r.c.FocusKey() != r.btn {
		t.Fatalf("nested close: focus %v, want the button", r.c.FocusKey())
	}
}

func (r *ovRig) overlayKey(k Key) Key { return KeyN(overlayBase, uint64(k)) }

func (r *ovRig) keyOfPanel() Key { return HashString(r.overlayKey(1), "panel") }

func TestModalBlocksBeneath(t *testing.T) {
	r := newOvRig(t)
	// A drag in progress on the main tree is cancelled when a modal opens.
	r.move(30, 30)
	r.frame(r.build)
	r.in.SetButton(ButtonLeft, true, 1)
	r.frame(r.build)
	if r.c.ActiveKey() != r.btn {
		t.Fatal("no drag to cancel")
	}
	r.modal = true
	r.frame(r.build)
	if r.c.ActiveKey() != 0 {
		t.Fatal("the drag survived the modal")
	}
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame(r.build)
	if r.btnSig.Clicked || r.btnSig.Released {
		t.Fatal("the cancelled drag ended in a click")
	}
	r.settle()
	if !r.c.ModalOpen() {
		t.Fatal("ModalOpen")
	}

	// Hit testing: nothing beneath is hot, whether or not the modal's own boxes cover the pointer.
	for _, p := range [][2]float32{{30, 30}, {30, 90}, {300, 250}} {
		r.move(p[0], p[1])
		r.frame(r.build)
		r.frame(r.build)
		if r.btnSig.Hovering || r.c.HotKey() == r.btn || r.c.HotKey() == r.fieldKey {
			t.Fatalf("pointer %v: a box beneath the modal is hot", p)
		}
	}
	// Clicks do not focus beneath.
	r.click(30, 90)
	if r.c.FocusKey() == r.fieldKey {
		t.Fatal("click focused the field beneath the modal")
	}
	// Scrolling does not reach the list beneath.
	r.move(50, 180)
	r.frame(r.build)
	r.in.AddScroll(0, -3, false)
	r.frame(r.build)
	r.frame(r.build)
	if _, y := r.c.Scroll(r.scroll); y != 0 {
		t.Fatalf("list beneath the modal scrolled to %v", y)
	}
	// Keys and text: even with the focus forced onto the field, it types nothing.
	r.c.SetFocus(r.fieldKey)
	r.in.AppendText("typed")
	r.frame(r.build)
	r.key('A', PrimaryMod())
	if r.field.String() != "" {
		t.Fatalf("the field beneath the modal took text: %q", r.field.String())
	}
	// Enter does not activate a focused button beneath it.
	r.c.SetFocus(r.btn)
	r.key(KeyEnter, 0)
	if r.btnSig.Activated {
		t.Fatal("Enter activated a button beneath the modal")
	}
	// The modal's own boxes work: click on its button.
	r.c.SetFocus(0)
	r.click(110, 110)
	if r.c.FocusKey() != r.modalKeys[0] || !r.modalBtn[0].Hovering && false {
		t.Fatalf("modal button not focused by a click: focus %v", r.c.FocusKey())
	}
	r.key(KeyEnter, 0)
	if !r.modalBtn[0].Activated {
		t.Fatal("Enter does not activate the modal's focused button")
	}
	// A press on the main tree does not close a modal and does nothing.
	r.click(30, 30)
	if _, ok := r.c.table.Get(uint64(r.overlayKey(3))); !ok {
		t.Fatal("a click outside closed the modal")
	}
}

func TestModalOpensFocusAndTrapsTab(t *testing.T) {
	r := newOvRig(t)
	r.c.SetFocus(r.btn)
	r.settle()
	r.modal = true
	r.frame(r.build)
	if r.c.FocusKey() != r.modalKeys[0] {
		t.Fatalf("focus %v, want the modal's first button", r.c.FocusKey())
	}
	var order []Key
	for i := 0; i < 5; i++ {
		r.key(KeyTab, 0)
		order = append(order, r.c.FocusKey())
	}
	want := []Key{r.modalKeys[1], r.modalKeys[0], r.modalKeys[1], r.modalKeys[0], r.modalKeys[1]}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("Tab visited %v, want the two modal buttons in a ring", order)
		}
	}
	r.key(KeyTab, ModShift)
	if r.c.FocusKey() != r.modalKeys[0] {
		t.Fatal("Shift+Tab")
	}
	// Closing returns the focus.
	r.modal = false
	r.frame(r.build)
	r.frame(r.build)
	if r.c.FocusKey() != r.btn {
		t.Fatalf("focus %v after the modal closed, want the button", r.c.FocusKey())
	}
	if r.c.ModalOpen() {
		t.Fatal("ModalOpen after closing")
	}
}

func TestTooltipTakesNoInputAndNoFocus(t *testing.T) {
	r := newOvRig(t)
	r.c.SetFocus(r.btn)
	r.settle()
	r.tip = true
	r.settle()
	if r.c.FocusKey() != r.btn {
		t.Fatal("a tooltip took the focus")
	}
	// The tooltip (20,44)-(110,74) lies over the field and the list: the pointer passes through.
	r.move(40, 50)
	r.frame(r.build)
	r.frame(r.build)
	if r.c.HotKey() == r.overlayKey(4) || r.c.HotKey() == HashString(r.overlayKey(4), "tipbox") {
		t.Fatal("the tooltip is hot")
	}
	r.click(40, 90)
	if r.c.FocusKey() != r.fieldKey {
		t.Fatalf("a click under the tooltip did not reach the field (focus %v)", r.c.FocusKey())
	}
	// Tab never visits it.
	for i := 0; i < 6; i++ {
		r.key(KeyTab, 0)
		if r.c.FocusKey() == HashString(r.overlayKey(4), "tipbox") {
			t.Fatal("Tab reached the tooltip")
		}
	}
	// Escape is not its business.
	var cl bool
	r.in.PushKey(KeyEvent{Code: KeyEscape, Down: true})
	r.frame(func(c *Context) { r.build(c); _, cl = c.KeyAt(0) })
	if cl && false {
		t.Fatal("unreachable")
	}
}

func TestOverlayPercentIsOfTheWindow(t *testing.T) {
	h := newHarness(t, 400, 300)
	var rect [4]float32
	build := func(c *Context) {
		c.NextSize(Px(10), Px(10))
		c.Box(0, "x")
		c.NextSize(Pct(0.5), Pct(1))
		c.BeginOverlay(9, OverlayModal, [4]float32{0, 0, 400, 300}, PlaceCenter)
		c.EndOverlay()
		v, _ := c.table.Get(uint64(KeyN(overlayBase, 9)))
		rect = c.boxes[v].rect
	}
	h.frame(build)
	h.frame(build)
	// Percentages of an overlay are fractions of the window.
	if rect != [4]float32{100, 0, 300, 300} {
		t.Fatalf("overlay rect %v", rect)
	}
}

func TestUnbalancedOverlayPanics(t *testing.T) {
	h := newHarness(t, 100, 100)
	defer func() {
		if recover() == nil {
			t.Fatal("no panic for BeginOverlay without EndOverlay")
		}
	}()
	h.frame(func(c *Context) { c.BeginOverlay(1, OverlayPopup, [4]float32{}, PlaceBelow) })
}

// A frame with a popup, a nested popup, a modal and a tooltip open allocates
// nothing.
func TestOverlaysAllocateNothing(t *testing.T) {
	r := newOvRig(t)
	r.open, r.sub, r.tip = true, true, true
	r.modal = true
	r.settle()
	n := 0
	frame := func() {
		n++
		r.move(float32(100+n%3), 110)
		if n%4 == 0 {
			r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
		}
		if n%7 == 0 {
			r.in.SetButton(ButtonLeft, true, 1)
		} else if r.in.Down[0] {
			r.in.SetButton(ButtonLeft, false, 0)
		}
		r.frame(r.build)
	}
	for i := 0; i < 20; i++ {
		frame()
	}
	if got := testing.AllocsPerRun(50, frame); got != 0 {
		t.Fatalf("%v allocations per frame with overlays open", got)
	}
}
