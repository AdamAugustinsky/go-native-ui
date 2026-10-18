package ui

import (
	"fmt"
	"testing"
)

// focusRig builds a column of focusable boxes a, b, c, d and e, 20 px high
// each at y = 0, 20, ..., with the ones named in off disabled.
type focusRig struct {
	*harness
	off  map[string]bool
	keys map[string]Key
	sig  map[string]Signal
}

func newFocusRig(t *testing.T) *focusRig {
	r := &focusRig{harness: newHarness(t, 200, 200), off: map[string]bool{}, keys: map[string]Key{}, sig: map[string]Signal{}}
	r.frame(r.build)
	r.frame(r.build)
	return r
}

func (r *focusRig) build(c *Context) {
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		fl := Clickable | Focusable
		if r.off[n] {
			fl |= Disabled
		}
		c.NextSize(Px(100), Px(20))
		id := c.Box(fl|DrawBackground, n)
		c.NextBG(0xFF0000FF)
		r.keys[n] = c.KeyOf(id)
		r.sig[n] = c.Signal(id)
	}
}

func (r *focusRig) tab(mods Mods) {
	r.in.PushKey(KeyEvent{Code: KeyTab, Mods: mods, Down: true})
	r.frame(r.build)
}

func (r *focusRig) focused() string {
	for n, k := range r.keys {
		if r.c.FocusKey() == k {
			return n
		}
	}
	return ""
}

func TestTabTraversal(t *testing.T) {
	r := newFocusRig(t)
	if r.focused() != "" || r.c.FocusVisible() {
		t.Fatal("starts without focus")
	}
	var got []string
	r.off["c"] = true
	for i := 0; i < 6; i++ {
		r.tab(0)
		got = append(got, r.focused())
	}
	want := []string{"a", "b", "d", "e", "a", "b"} // c is skipped, and it wraps around
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Tab order %v, want %v", got, want)
		}
	}
	if !r.c.FocusVisible() {
		t.Error("focus moved by Tab is not visible")
	}
	if !r.c.Animating() {
		t.Error("a focus move asks for the frame that draws the ring")
	}
	got = nil
	for i := 0; i < 5; i++ {
		r.tab(ModShift)
		got = append(got, r.focused())
	}
	want = []string{"a", "e", "d", "b", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Shift+Tab order %v, want %v", got, want)
		}
	}
	// Shift+Tab with no focus starts at the end.
	r.c.SetFocus(0)
	r.frame(r.build)
	r.tab(ModShift)
	if r.focused() != "e" {
		t.Fatalf("Shift+Tab from nothing: %q", r.focused())
	}
}

func TestFocusVisibleFollowsThePointer(t *testing.T) {
	r := newFocusRig(t)
	r.tab(0)
	if !r.c.FocusVisible() || r.focused() != "a" {
		t.Fatal("Tab")
	}
	r.move(50, 30) // over b
	r.frame(r.build)
	r.in.SetButton(ButtonLeft, true, 1)
	r.frame(r.build)
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame(r.build)
	if r.focused() != "b" || r.c.FocusVisible() {
		t.Fatalf("after a click on b: focus %q visible=%v", r.focused(), r.c.FocusVisible())
	}
	r.tab(0)
	if r.focused() != "c" || !r.c.FocusVisible() {
		t.Fatalf("Tab after the click: %q visible=%v", r.focused(), r.c.FocusVisible())
	}
	r.c.SetFocusVisible(false)
	if r.c.FocusVisible() {
		t.Error("SetFocusVisible(false)")
	}
}

func TestTabLeavesTheTextField(t *testing.T) {
	r := newRig(t, false)
	var other Key
	build := func(c *Context) {
		c.NextSize(Px(200), Px(24))
		r.res = c.TextField("f", &r.buf)
		c.NextSize(Px(50), Px(20))
		other = c.KeyOf(c.Box(Focusable|Clickable, "next"))
	}
	r.harness.frame(build)
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
	r.harness.frame(build)
	if r.c.FocusKey() != other {
		t.Fatalf("Tab from the field did not reach the next box")
	}
	if r.text() != "" {
		t.Errorf("Tab typed into the field: %q", r.text())
	}
}

func TestClaimedTabDoesNotMoveFocus(t *testing.T) {
	r := newFocusRig(t)
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
	r.frame(func(c *Context) {
		c.TakeKey(KeyTab, 0) // a handler that wants Tab for itself
		r.build(c)
	})
	if r.focused() != "" {
		t.Fatalf("Tab moved the focus although it was claimed: %q", r.focused())
	}
}

func TestDisabledBoxesAreInert(t *testing.T) {
	r := newFocusRig(t)
	r.off["b"] = true
	r.frame(r.build)
	// Not hit-tested: the pointer over b hovers nothing, and the press does not
	// focus it or press it.
	r.move(50, 30)
	r.frame(r.build)
	if r.sig["b"].Hovering || r.c.HotKey() != 0 {
		t.Fatal("a disabled box is hovered")
	}
	r.in.SetButton(ButtonLeft, true, 1)
	r.frame(r.build)
	if r.sig["b"].Pressed || r.c.FocusKey() == r.keys["b"] || r.c.ActiveKey() != 0 {
		t.Fatal("a disabled box was pressed or focused")
	}
	r.in.SetButton(ButtonLeft, false, 0)
	r.frame(r.build)
	if r.sig["b"] != (Signal{}) {
		t.Fatalf("disabled signal %+v", r.sig["b"])
	}
	// It does not block what lies beneath it.
	var hovered bool
	build := func(c *Context) {
		c.NextSize(Px(100), Px(20))
		under := c.Box(Clickable, "under")
		c.NextSize(Px(100), Px(20))
		c.NextPos(0, 0)
		c.Box(Clickable|Disabled|FloatingX|FloatingY, "over")
		hovered = c.Signal(under).Hovering
	}
	r.frame(build)
	r.move(10, 10)
	r.frame(build)
	if !hovered {
		t.Error("a disabled box on top blocks the box below it")
	}
}

func TestDisabledRejectsQueuedActivation(t *testing.T) {
	r := newFocusRig(t)
	r.c.SetFocus(r.keys["a"])
	r.frame(r.build)
	// Enter arrives in the frame in which the box is disabled.
	r.off["a"] = true
	r.in.PushKey(KeyEvent{Code: KeyEnter, Down: true})
	var kclaimed bool
	r.frame(func(c *Context) {
		r.build(c)
		_, kclaimed = c.KeyAt(0)
	})
	if r.sig["a"].Activated {
		t.Fatal("a disabled box was activated")
	}
	if kclaimed {
		t.Error("a disabled box claimed the key")
	}
	if r.c.FocusKey() != 0 {
		t.Error("the focus stays on a box that became disabled")
	}
	// Enabled again, the same box activates (on an Enter after the focus returns).
	r.off["a"] = false
	r.c.SetFocus(r.keys["a"])
	r.in.PushKey(KeyEvent{Code: KeyEnter, Down: true})
	r.frame(r.build)
	if !r.sig["a"].Activated {
		t.Fatal("an enabled box does not activate")
	}
}

func TestDisabledDrawsLikeEnabled(t *testing.T) {
	quads := func(disabled bool) []string {
		h := newHarness(t, 100, 60)
		for i := 0; i < 2; i++ {
			h.frame(func(c *Context) {
				c.NextSize(Px(60), Px(20))
				c.NextBG(0x3366CCFF)
				c.NextBorder(0xFFFFFFFF, 1)
				fl := Clickable | Focusable | DrawBackground | DrawBorder | DrawText | HotAnimation
				if disabled {
					fl |= Disabled
				}
				c.Box(fl, "Button")
			})
		}
		var out []string
		for _, q := range h.list.Quads {
			out = append(out, fmtQuad(q.Rect, q.Color[0], q.Border))
		}
		return out
	}
	a, b := quads(false), quads(true)
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("%d and %d quads", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("quad %d differs: %s / %s", i, a[i], b[i])
		}
	}
}

func fmtQuad(r [4]float32, col uint32, border float32) string {
	return fmt.Sprint(r, col, border)
}
