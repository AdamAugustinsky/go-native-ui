package ui

import "testing"

// optRig: an anchor button at (20, 20) 80×24, another button at (200, 200)
// 80×24 and a text field at (20, 80); a popup with the options in opts,
// anchored to the button and 100×40, when open.
type optRig struct {
	*harness
	opts              OverlayOpts
	open              bool
	dismissed         bool
	anchorSig, underS Signal
	field             TextBuffer
	fieldKey          Key
}

func newOptRig(t *testing.T) *optRig {
	r := &optRig{harness: newHarness(t, 400, 300)}
	for i := 0; i < 3; i++ {
		r.frame(r.build)
	}
	return r
}

func (r *optRig) build(c *Context) {
	c.NextSize(Px(80), Px(24))
	c.NextPos(20, 20)
	a := c.Box(Clickable|Focusable|FloatingX|FloatingY, "anchor")
	r.anchorSig = c.Signal(a)
	c.NextSize(Px(80), Px(24))
	c.NextPos(200, 200)
	u := c.Box(Clickable|Focusable|FloatingX|FloatingY, "under")
	r.underS = c.Signal(u)
	c.NextSize(Px(200), Px(24))
	c.NextPos(20, 80)
	fb := c.Box(FloatingX|FloatingY, "fieldbox")
	c.Push(fb)
	fr := c.TextField("field", &r.field)
	r.fieldKey = c.KeyOf(fr.ID)
	c.Pop()
	c.NextSize(Px(100), Px(60))
	c.NextPos(200, 100)
	c.Host(7, HashString(0, "native"), HostFocusable)
	if r.open {
		c.NextOverlay(r.opts)
		s := c.BeginOverlay(1, OverlayPopup, c.Rect(a), PlaceBelow)
		r.dismissed = r.dismissed || s.Dismissed
		c.NextSize(Px(100), Px(40))
		c.NextBG(0x00AA00FF)
		c.Box(DrawBackground, "panel")
		c.EndOverlay()
	}
}

func (r *optRig) run(n int) {
	for i := 0; i < n; i++ {
		r.frame(r.build)
	}
}

// click presses and releases at (x, y) and returns whether the popup was
// dismissed by it.
func (r *optRig) click(x, y float32) {
	r.move(x, y)
	r.run(1)
	r.in.SetButton(ButtonLeft, true, 1)
	r.run(1)
	r.in.SetButton(ButtonLeft, false, 0)
	r.run(1)
}

func (r *optRig) reopen(o OverlayOpts) {
	r.opts, r.open, r.dismissed = o, true, false
	r.run(3)
}

// A popup that takes no focus leaves the focus where it was when it opens
// and when it closes, and Tab goes on through the window beneath it.
func TestNoFocusPopupKeepsTheFocus(t *testing.T) {
	r := newOptRig(t)
	r.c.SetFocus(r.fieldKey)
	r.run(2)
	r.reopen(OverlayOpts{NoFocus: true, Dismiss: DismissApplication})
	if r.c.FocusKey() != r.fieldKey {
		t.Fatalf("opening a no-focus popup moved the focus")
	}
	r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
	r.run(1)
	if r.c.FocusKey() == r.fieldKey {
		t.Errorf("Tab stayed in the field: the popup trapped nothing, the traversal goes on beneath it")
	}
	r.c.SetFocus(r.fieldKey)
	r.open = false
	r.run(3)
	if r.c.FocusKey() != r.fieldKey {
		t.Errorf("closing it moved the focus")
	}
	// the default popup does take it (the old behaviour)
	r.reopen(OverlayOpts{})
	if r.c.FocusKey() == r.fieldKey {
		t.Errorf("an ordinary popup did not take the focus")
	}
}

// What a press outside does, by policy, as measured on NSPopover: a menu's is
// consumed; a transient popover's reaches what it hit, except the anchor's;
// a semitransient one's reaches it and leaves the popover open on the
// anchor; an application-defined one does not close.
func TestPopupDismissalPolicies(t *testing.T) {
	cases := []struct {
		name                       string
		opts                       OverlayOpts
		x, y                       float32 // where the press lands
		dismissed, anchor, reached bool
	}{
		{"menu beneath", OverlayOpts{}, 240, 210, true, false, false},
		{"transient beneath", OverlayOpts{Dismiss: DismissTransient}, 240, 210, true, false, true},
		{"transient on the anchor", OverlayOpts{Dismiss: DismissTransient}, 60, 32, true, false, false},
		{"semitransient beneath", OverlayOpts{Dismiss: DismissSemitransient}, 240, 210, true, false, true},
		{"semitransient on the anchor", OverlayOpts{Dismiss: DismissSemitransient}, 60, 32, false, true, false},
		{"application beneath", OverlayOpts{Dismiss: DismissApplication}, 240, 210, false, false, true},
		{"application on the anchor", OverlayOpts{Dismiss: DismissApplication}, 60, 32, false, true, false},
	}
	for _, tc := range cases {
		r := newOptRig(t)
		r.reopen(tc.opts)
		var clickedAnchor, clickedUnder bool
		r.move(tc.x, tc.y)
		r.run(1)
		r.in.SetButton(ButtonLeft, true, 1)
		r.run(1)
		clickedAnchor, clickedUnder = r.anchorSig.Pressed, r.underS.Pressed
		r.in.SetButton(ButtonLeft, false, 0)
		r.run(1)
		if r.dismissed != tc.dismissed {
			t.Errorf("%s: dismissed %v, want %v", tc.name, r.dismissed, tc.dismissed)
		}
		if clickedAnchor != tc.anchor {
			t.Errorf("%s: the anchor got the press: %v, want %v", tc.name, clickedAnchor, tc.anchor)
		}
		if clickedUnder != tc.reached {
			t.Errorf("%s: what was beneath got the press: %v, want %v", tc.name, clickedUnder, tc.reached)
		}
	}
}

// Escape closes a transient and a semitransient popup and a menu, not an
// application-defined one.
func TestPopupEscapeByPolicy(t *testing.T) {
	for _, tc := range []struct {
		opts OverlayOpts
		want bool
	}{
		{OverlayOpts{}, true},
		{OverlayOpts{Dismiss: DismissTransient}, true},
		{OverlayOpts{Dismiss: DismissSemitransient}, true},
		{OverlayOpts{Dismiss: DismissApplication}, false},
	} {
		r := newOptRig(t)
		r.reopen(tc.opts)
		r.in.PushKey(KeyEvent{Code: KeyEscape, Down: true})
		r.run(1)
		if r.dismissed != tc.want {
			t.Errorf("policy %d: Escape dismissed %v, want %v", tc.opts.Dismiss, r.dismissed, tc.want)
		}
	}
}

// A hosted native view that has the keyboard keeps it while a popup that takes
// no focus opens and closes: ui never asks the platform to take the focus
// from the view (FocusedHost stays the host's), as an ordinary popup does.
func TestNoFocusPopupLeavesAHostedViewItsFocus(t *testing.T) {
	r := newOptRig(t)
	r.c.FocusHostBox(7)
	r.run(2)
	if r.c.FocusedHost() != 7 {
		t.Fatalf("the host does not have the focus: %d", r.c.FocusedHost())
	}
	r.reopen(OverlayOpts{NoFocus: true, Dismiss: DismissTransient})
	if r.c.FocusedHost() != 7 {
		t.Fatalf("opening the popup took the focus from the host: %d", r.c.FocusedHost())
	}
	r.open = false
	r.run(3)
	if r.c.FocusedHost() != 7 {
		t.Fatalf("closing it did: %d", r.c.FocusedHost())
	}
	r.reopen(OverlayOpts{})
	if r.c.FocusedHost() == 7 {
		t.Fatal("an ordinary popup did not take the focus from the host (the test sees nothing)")
	}
}
