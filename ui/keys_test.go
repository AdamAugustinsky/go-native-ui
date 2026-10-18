package ui

import (
	"fmt"
	"testing"
)

// keyRig builds a text field, a command matcher and a button in every order,
// to show that a chord has exactly one handler whatever the build order.
type keyRig struct {
	*fieldRig
	order    [3]int // 0: field, 1: matcher, 2: button
	matchKey KeyCode
	matchMod Mods
	matched  int // how often the matcher took its chord
	pressed  int // how often the button was activated
	copies   int // how often the field copied
	res      TextFieldSignal
	btn      ID
	submits  int
}

func (k *keyRig) build(c *Context) {
	for _, what := range k.order {
		switch what {
		case 0:
			c.NextSize(Px(200), Px(24))
			c.NextPad(rigPad)
			k.res = c.TextField("f", &k.buf)
			if k.res.Submitted {
				k.submits++
			}
		case 1:
			if c.TakeKey(k.matchKey, k.matchMod) {
				k.matched++
			}
		case 2:
			c.NextSize(Px(60), Px(24))
			k.btn = c.Box(Clickable|Focusable, "button")
			if c.Signal(k.btn).Activated {
				k.pressed++
			}
		}
	}
}

func (k *keyRig) frame() {
	k.now += 16e6
	k.in.Time = k.now
	k.harness.frame(k.build)
}

var orders = [][3]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}

func newKeyRig(t *testing.T, order [3]int) *keyRig {
	r := newRig(t, true)
	r.c.ClipboardSet = nil
	k := &keyRig{fieldRig: r, order: order}
	k.c.ClipboardSet = func(b []byte) { k.copies++; k.clip = string(b) }
	k.frame()
	k.frame()
	k.c.SetFocus(k.c.KeyOf(k.res.ID))
	k.frame()
	return k
}

func (k *keyRig) press(code KeyCode, mods Mods) {
	k.in.PushKey(KeyEvent{Code: code, Mods: mods, Down: true})
	k.frame()
}

func TestOneChordOneHandler(t *testing.T) {
	for _, order := range orders {
		// The field's chord: the matcher, asking for the same chord, never gets it.
		k := newKeyRig(t, order)
		k.matchKey, k.matchMod = 'C', ModSuper
		k.buf.SetString("hello")
		k.buf.SelectAll()
		k.press('C', ModSuper)
		if k.copies != 1 || k.matched != 0 || k.clip != "hello" {
			t.Errorf("order %v: ⌘C copied %d times, matcher took %d", order, k.copies, k.matched)
		}

		// A chord the field does not use goes to the matcher, once.
		k = newKeyRig(t, order)
		k.matchKey, k.matchMod = 'N', ModSuper
		k.press('N', ModSuper)
		if k.matched != 1 {
			t.Errorf("order %v: ⌘N reached the matcher %d times", order, k.matched)
		}
		k.frame()
		if k.matched != 1 {
			t.Errorf("order %v: ⌘N handled again in the next frame", order)
		}

		// Enter belongs to the focused field, not to a button built before or after.
		k = newKeyRig(t, order)
		k.matchKey, k.matchMod = KeyEnter, 0
		k.press(KeyEnter, 0)
		if k.submits != 1 || k.matched != 0 || k.pressed != 0 {
			t.Errorf("order %v: Enter submitted %d, matcher %d, button %d", order, k.submits, k.matched, k.pressed)
		}

		// With the button focused, Enter goes to the button or to the matcher,
		// whichever is built first, and to no one else.
		k.c.SetFocus(k.c.KeyOf(k.btn))
		k.frame()
		k.press(KeyEnter, 0)
		if k.pressed+k.matched != 1 || k.submits != 1 {
			t.Errorf("order %v: Enter on the button went to the button %d and the matcher %d times; the field submitted %d", order, k.pressed, k.matched, k.submits)
		}
	}
}

func TestTakeKey(t *testing.T) {
	h := newHarness(t, 100, 100)
	h.in.PushKey(KeyEvent{Code: 'S', Mods: ModSuper | ModShift, Down: true})
	h.in.PushKey(KeyEvent{Code: 'S', Mods: ModSuper, Down: true})
	h.in.PushKey(KeyEvent{Code: 'S', Mods: ModSuper})             // a release
	h.in.PushKey(KeyEvent{Code: 'S', Mods: ModSuper, Down: true}) // a second press
	h.frame(func(c *Context) {
		if c.KeyCount() != 4 {
			t.Fatal("KeyCount")
		}
		if c.TakeKey('S', ModShift) || c.TakeKey('S', ModSuper|ModAlt) {
			t.Error("modifiers must match exactly")
		}
		if !c.TakeKey('S', ModSuper) {
			t.Fatal("⌘S not found")
		}
		if _, claimed := c.KeyAt(1); !claimed {
			t.Error("the first match was not claimed")
		}
		if _, claimed := c.KeyAt(0); claimed {
			t.Error("⇧⌘S claimed by ⌘S")
		}
		if !c.TakeKey('S', ModSuper) { // the second press: unclaimed ones are taken in order
			t.Error("second ⌘S press not found")
		}
		if c.TakeKey('S', ModSuper) {
			t.Error("a key was taken twice (or a release was taken)")
		}
		c.ClaimKey(0)
		if c.TakeKey('S', ModSuper|ModShift) {
			t.Error("a claimed key was taken")
		}
		if e, _ := c.KeyAt(9); e != (KeyEvent{}) {
			t.Error("KeyAt out of range")
		}
	})
}

func TestPrimaryMod(t *testing.T) {
	old := macKeys
	defer func() { macKeys = old }()
	macKeys = true
	if PrimaryMod() != ModSuper {
		t.Error("macOS primary is ⌘")
	}
	macKeys = false
	if PrimaryMod() != ModCtrl {
		t.Error("elsewhere the primary is Ctrl")
	}
}

// Tab is not a key of the text field; the other keys without the primary
// modifier are, also the ones a command could ask for.
func TestFieldClaimsEditingKeys(t *testing.T) {
	r := newRig(t, true)
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
	r.in.PushKey(KeyEvent{Code: KeyEscape, Down: true})
	r.in.PushKey(KeyEvent{Code: 'Q', Mods: ModSuper, Down: true})
	r.in.PushKey(KeyEvent{Code: 'Q', Mods: ModCtrl, Down: true}) // not the primary modifier on macOS
	r.in.PushKey(KeyEvent{Code: KeyLeft, Mods: ModSuper, Down: true})
	r.in.PushKey(KeyEvent{Code: 'Z', Mods: ModSuper | ModShift, Down: true})
	r.in.PushKey(KeyEvent{Code: 'Y', Mods: ModSuper, Down: true}) // macOS has no ⌘Y chord
	var got [8]bool
	r.harness.frame(func(c *Context) {
		for i := 0; i < c.KeyCount(); i++ {
			_, got[i] = c.KeyAt(i)
		}
		c.NextSize(Px(200), Px(24))
		c.TextField("f", &r.buf)
	})
	want := [7]bool{false, true, false, true, true, true, false}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("key %d claimed=%v, want %v", i, got[i], w)
		}
	}
}

func TestFieldKeysNotClaimedWithoutFocus(t *testing.T) {
	r := newRig(t, true)
	r.c.SetFocus(0)
	r.frame()
	r.in.PushKey(KeyEvent{Code: 'A', Down: true})
	var claimed bool
	r.harness.frame(func(c *Context) {
		_, claimed = c.KeyAt(0)
		c.NextSize(Px(200), Px(24))
		c.TextField("f", &r.buf)
	})
	if claimed {
		t.Error("an unfocused field claimed a key")
	}
	if r.text() != "" {
		t.Errorf("an unfocused field took the key: %q", r.text())
	}
}
func TestUndoChords(t *testing.T) {
	for _, mac := range []bool{true, false} {
		r := newRig(t, mac)
		pm := ModCtrl
		if mac {
			pm = ModSuper
		}
		r.typ("hello")
		r.step(1500)
		r.typ(" world")
		r.key('Z', pm)
		if r.text() != "hello" {
			t.Fatalf("mac=%v undo: %q", mac, r.text())
		}
		r.key('Z', pm|ModShift)
		if r.text() != "hello world" {
			t.Fatalf("mac=%v redo: %q", mac, r.text())
		}
		r.key('Z', pm)
		r.key('Y', pm) // redo on Windows and Linux
		want := "hello world"
		if mac {
			want = "hello" // ⌘Y is not a chord of the text field on macOS
		}
		if r.text() != want {
			t.Fatalf("mac=%v ctrl+y: %q, want %q", mac, r.text(), want)
		}
		// Edit actions do the same as the chords.
		undone, redone := "hello", "hello world" // where the history stands
		if mac {
			undone, redone = "", "hello"
		}
		r.in.PushEdit(EditUndo)
		r.frame()
		if r.text() != undone {
			t.Fatalf("mac=%v EditUndo: %q, want %q", mac, r.text(), undone)
		}
		r.in.PushEdit(EditRedo)
		r.frame()
		if r.text() != redone {
			t.Fatalf("mac=%v EditRedo: %q, want %q", mac, r.text(), redone)
		}
	}
}

// A frame's keys, edit actions and typed text act on the field that had the
// focus at Begin, once, even when a handler moves the focus to a field built
// later in the same frame.
func TestFocusTransferDuringBuildAppliesInputOnce(t *testing.T) {
	cases := []struct {
		name  string
		push  func(in *Input)
		a, b  string // the texts after the frame
		setup func(a, b *TextBuffer)
	}{
		{"edit delete", func(in *Input) { in.PushEdit(EditDelete) }, "", "hello",
			func(a, b *TextBuffer) { a.SetString("hello"); a.SelectAll(); b.SetString("hello"); b.SelectAll() }},
		{"backspace", func(in *Input) { in.PushKey(KeyEvent{Code: KeyBackspace, Down: true}) }, "hell", "hello",
			func(a, b *TextBuffer) { a.SetString("hello"); b.SetString("hello") }},
		{"typed text", func(in *Input) { in.AppendText("x") }, "hellox", "hello",
			func(a, b *TextBuffer) { a.SetString("hello"); b.SetString("hello") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, false)
			var a, b TextBuffer
			var ka, kb Key
			build := func(c *Context) {
				c.NextSize(Px(200), Px(24))
				ra := c.TextField("a", &a)
				if ra.Changed {
					c.SetFocus(kb) // (kb: the key from the previous frame)
				}
				c.NextSize(Px(200), Px(24))
				rb := c.TextField("b", &b)
				ka, kb = c.KeyOf(ra.ID), c.KeyOf(rb.ID)
			}
			r.harness.frame(build)
			r.c.SetFocus(ka)
			r.harness.frame(build)
			tc.setup(&a, &b)
			r.harness.frame(build)
			tc.push(&r.in)
			r.harness.frame(build)
			if a.String() != tc.a || b.String() != tc.b {
				t.Fatalf("a=%q b=%q, want a=%q b=%q", a.String(), b.String(), tc.a, tc.b)
			}
			if r.c.FocusKey() != kb {
				t.Fatal("the focus did not move")
			}
		})
	}
}

// Escape in a text field: the field reports Cancelled. In the main tree the
// field claims the key; inside an overlay it leaves it for the overlay, so a
// dialog's field does not swallow the dialog's Escape.
func TestEscapePropagationFromTextField(t *testing.T) {
	for _, inOverlay := range []bool{false, true} {
		r := newRig(t, false)
		var cancelled, taken int
		build := func(c *Context) {
			if inOverlay {
				c.BeginOverlay(1, OverlayModal, [4]float32{0, 0, 400, 100}, PlaceCenter)
			}
			c.NextSize(Px(200), Px(24))
			res := c.TextField("g", &r.buf)
			if res.Cancelled {
				cancelled++
			}
			if inOverlay {
				if c.TakeKey(KeyEscape, 0) { // what the owning modal does
					taken++
				}
				c.EndOverlay()
			}
			r.res = res
		}
		for i := 0; i < 3; i++ {
			r.harness.frame(build)
		}
		var key Key
		build2 := func(c *Context) { build(c); key = c.KeyOf(r.res.ID) }
		r.harness.frame(build2)
		r.c.SetFocus(key)
		r.harness.frame(build2)
		r.harness.frame(build2)
		cancelled, taken = 0, 0
		r.in.PushKey(KeyEvent{Code: KeyEscape, Down: true})
		r.harness.frame(build2)
		if cancelled != 1 {
			t.Fatalf("overlay=%v: field reported Cancelled %d times", inOverlay, cancelled)
		}
		if want := map[bool]int{false: 0, true: 1}[inOverlay]; taken != want {
			t.Fatalf("overlay=%v: the owner took Escape %d times, want %d", inOverlay, taken, want)
		}
	}
}

// The frame's input belongs to the field that had the focus at Begin, however
// the focus moves during the build: when the build moves it to B before A is
// built, A still gets all of it and B none, in either build order.
func TestInputOwnershipIsFixedAtBegin(t *testing.T) {
	cases := []struct {
		name  string
		setup func(a, b *TextBuffer)
		push  func(in *Input)
		a, b  string
	}{
		{"typed text", func(a, b *TextBuffer) {}, func(in *Input) { in.AppendText("x") }, "hellox", "world"},
		{"backspace", func(a, b *TextBuffer) {}, func(in *Input) { in.PushKey(KeyEvent{Code: KeyBackspace, Down: true}) }, "hell", "world"},
		{"edit action", func(a, b *TextBuffer) { a.SelectAll(); b.SelectAll() }, func(in *Input) { in.PushEdit(EditDelete) }, "", "world"},
		{"replacement", func(a, b *TextBuffer) {}, func(in *Input) { in.ReplaceText(-1, 1, []byte("X")) }, "hellX", "world"},
		{"typed text and a key", func(a, b *TextBuffer) {}, func(in *Input) {
			in.AppendText("x")
			in.PushKey(KeyEvent{Code: KeyLeft, Down: true})
		}, "hellox", "world"},
	}
	for _, order := range [][2]int{{0, 1}, {1, 0}} {
		for _, tc := range cases {
			t.Run(fmt.Sprint(tc.name, order), func(t *testing.T) {
				r := newRig(t, false)
				var bufs [2]TextBuffer
				bufs[0].SetString("hello")
				bufs[1].SetString("world")
				var keys [2]Key
				move := false
				build := func(c *Context) {
					if move {
						c.SetFocus(keys[1]) // before A is built, whatever the order
					}
					for _, i := range order {
						c.NextSize(Px(200), Px(24))
						keys[i] = c.KeyOf(c.TextField([...]string{"a", "b"}[i], &bufs[i]).ID)
					}
				}
				r.harness.frame(build)
				r.c.SetFocus(keys[0])
				r.harness.frame(build)
				r.harness.frame(build)
				bufs[0].SetCaret(5)
				bufs[1].SetCaret(5)
				tc.setup(&bufs[0], &bufs[1])
				move = true
				tc.push(&r.in)
				r.harness.frame(build)
				if bufs[0].String() != tc.a || bufs[1].String() != tc.b {
					t.Fatalf("order %v: a=%q b=%q, want a=%q b=%q", order, bufs[0].String(), bufs[1].String(), tc.a, tc.b)
				}
				if r.c.FocusKey() != keys[1] {
					t.Fatal("the focus did not move to b")
				}
			})
		}
	}
}

// A paste answers the field that asked while that field owns the frame's
// input; a focus move during the build does not redirect it.
func TestPasteFollowsInputOwnership(t *testing.T) {
	r := newRig(t, false)
	var a, b TextBuffer
	var ka, kb Key
	move := false
	build := func(c *Context) {
		if move {
			c.SetFocus(kb)
		}
		c.NextSize(Px(200), Px(24))
		ka = c.KeyOf(c.TextField("a", &a).ID)
		c.NextSize(Px(200), Px(24))
		kb = c.KeyOf(c.TextField("b", &b).ID)
	}
	r.harness.frame(build)
	r.c.SetFocus(ka)
	r.harness.frame(build)
	r.harness.frame(build)
	move = true
	r.in.SetPaste(ka, []byte("pasted"), true)
	r.harness.frame(build)
	if a.String() != "pasted" || b.String() != "" {
		t.Fatalf("a=%q b=%q", a.String(), b.String())
	}
}

// A popup above a modal, holding a text field. Escape in the field is the
// field's when it is composing or clears itself; otherwise it closes the popup.
type escRig struct {
	*fieldRig
	popup     bool
	clears    bool // the field is marked NextEscapeClears (and clears itself on Cancelled, like a search field)
	dismissed int  // frames in which the popup was dismissed
	modalEsc  int  // times the modal below took Escape
	cancelled int
	key       Key
}

func (e *escRig) build(c *Context) {
	c.BeginOverlay(1, OverlayModal, [4]float32{0, 0, 400, 100}, PlaceCenter)
	c.NextSize(Px(300), Px(60))
	c.Box(0, "dialog")
	if e.popup {
		os := c.BeginOverlay(2, OverlayPopup, [4]float32{0, 0, 100, 10}, PlaceBelow)
		if os.Dismissed {
			e.dismissed++
			e.popup = false
		}
		c.NextSize(Px(200), Px(24))
		if e.clears {
			c.NextEscapeClears()
		}
		res := c.TextField("f", &e.buf)
		e.key = c.KeyOf(res.ID)
		if res.Cancelled {
			e.cancelled++
			if e.clears && e.buf.Len() > 0 {
				e.buf.Reset()
				c.FieldEmptied(res.ID)
			}
		}
		c.EndOverlay()
	}
	if c.TakeKey(KeyEscape, 0) { // the modal's owner
		e.modalEsc++
	}
	c.EndOverlay()
}

func newEscRig(t *testing.T) *escRig {
	e := &escRig{fieldRig: newRig(t, false)}
	for i := 0; i < 3; i++ {
		e.harness.frame(e.build)
	}
	e.popup = true
	for i := 0; i < 4; i++ {
		e.harness.frame(e.build)
	}
	return e
}

func (e *escRig) esc() {
	e.in.PushKey(KeyEvent{Code: KeyEscape, Down: true})
	e.harness.frame(e.build)
}

func TestEscapeInPopupFieldCancelsOnlyTheComposition(t *testing.T) {
	e := newEscRig(t)
	if e.c.FocusKey() != e.key || !e.c.TextFieldFocused() {
		t.Fatal("the focus did not move into the popup's field")
	}
	e.in.SetPreedit([]byte("か"), 3, 0)
	e.harness.frame(e.build)
	e.harness.frame(e.build)
	e.esc()
	if !e.c.IMECancel() {
		t.Fatal("Escape did not cancel the composition")
	}
	e.harness.frame(e.build)
	if e.dismissed != 0 || !e.popup || e.modalEsc != 0 {
		t.Fatalf("Escape during a composition: popup dismissed %d times, open %v, modal took %d", e.dismissed, e.popup, e.modalEsc)
	}
	// with the composition gone Escape reaches the popup
	e.in.SetPreedit(nil, 0, 0)
	e.harness.frame(e.build)
	e.esc()
	e.harness.frame(e.build)
	if e.dismissed != 1 || e.popup {
		t.Fatalf("the next Escape: popup dismissed %d times, open %v", e.dismissed, e.popup)
	}
	if e.modalEsc != 0 {
		t.Fatalf("the modal below took Escape %d times", e.modalEsc)
	}
}

func TestEscapeClearsPopupSearchFieldBeforeClosingPopup(t *testing.T) {
	e := newEscRig(t)
	e.clears = true
	e.in.AppendText("abc")
	e.harness.frame(e.build)
	e.harness.frame(e.build)
	// two Escapes in consecutive frames, with no rebuild between them
	e.esc()
	if e.buf.Len() != 0 || e.dismissed != 0 || !e.popup || e.modalEsc != 0 {
		t.Fatalf("first Escape: text %q, popup dismissed %d, open %v, modal took %d", e.buf.String(), e.dismissed, e.popup, e.modalEsc)
	}
	e.esc()
	e.harness.frame(e.build)
	if e.dismissed != 1 || e.popup {
		t.Fatalf("second Escape: popup dismissed %d times, open %v", e.dismissed, e.popup)
	}
	if e.modalEsc != 0 {
		t.Fatalf("the modal below took Escape %d times", e.modalEsc)
	}
}

// Text typed in the frame of a Tab key is text: ui does not take tabs out of
// it (a commit "a\tb" batched with a traversal Tab). A single-line field turns
// the tab into a space.
func TestTextWithATabIsKeptInTheFrameOfATabKey(t *testing.T) {
	r := newRig(t, false)
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
	r.in.AppendText("a\tb")
	r.frame()
	if r.text() != "a b" {
		t.Fatalf("the field holds %q, want %q", r.text(), "a b")
	}
}
