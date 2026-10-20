package ui

import "testing"

// The colour panel has one holder: a box that is activated takes it, and the
// box that had it learns at its next call.
func TestColorPanelHasOneHolder(t *testing.T) {
	h := newHarness(t, 100, 100)
	run := func(calls func(c *Context)) (owner Key, rgba uint32, ok bool) {
		h.frame(calls)
		return h.c.ColorPanelRequest()
	}
	const a, b = Key(1), Key(2)
	if o, v, ok := run(func(c *Context) {
		if !c.ColorPanel(a, 0x11, true) {
			t.Error("a claim was refused")
		}
	}); !ok || o != a || v != 0x11 {
		t.Fatalf("A claims: %v %x %v", o, v, ok)
	}
	// B claims while A keeps asking: B holds it from now on; A is told next frame.
	run(func(c *Context) {
		c.ColorPanel(a, 0x11, false)
		if !c.ColorPanel(b, 0x22, true) {
			t.Error("B's claim was refused")
		}
	})
	if o, v, ok := h.c.ColorPanelRequest(); !ok || o != b || v != 0x22 {
		t.Fatalf("B claims: %v %x %v", o, v, ok)
	}
	run(func(c *Context) {
		if c.ColorPanel(a, 0x11, false) {
			t.Error("A still holds the panel after B took it")
		}
		if !c.ColorPanel(b, 0x22, false) {
			t.Error("B lost the panel")
		}
	})
	// nobody asks: the request goes (the app closes the panel)
	if _, _, ok := run(nil); ok {
		t.Error("a frame without a call still requests the panel")
	}
	// the owner's picks and closing are for the owner only
	run(func(c *Context) { c.ColorPanel(a, 0, true) })
	h.in.SetColorPick(0xAABBCCDD)
	h.frame(func(c *Context) {
		if v, ok := c.TakeColorPick(a); !ok || v != 0xAABBCCDD {
			t.Errorf("the owner's pick: %x %v", v, ok)
		}
		if _, ok := c.TakeColorPick(b); ok {
			t.Error("another box got the pick")
		}
		c.ColorPanel(a, 0, false)
	})
}
