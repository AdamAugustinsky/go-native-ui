package ui

import (
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
