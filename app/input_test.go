package app

import (
	"runtime"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

func ev(k platform.EventKind, f func(*platform.Event)) *platform.Event {
	e := &platform.Event{Kind: k}
	if f != nil {
		f(e)
	}
	return e
}

func press(x, y float32, t int64, b platform.Button) *platform.Event {
	return ev(platform.EvPointerButton, func(e *platform.Event) {
		e.Button, e.Flags, e.X, e.Y, e.Time = b, platform.FlagDown, x, y, t
	})
}

func release(x, y float32, t int64, b platform.Button) *platform.Event {
	return ev(platform.EvPointerButton, func(e *platform.Event) { e.Button, e.X, e.Y, e.Time = b, x, y, t })
}

func TestPointerMoveAndLeave(t *testing.T) {
	tr := newTranslator()
	if tr.in.Inside {
		t.Fatal("a new translator has the pointer inside")
	}
	if !tr.event(ev(platform.EvPointerMove, func(e *platform.Event) { e.X, e.Y = 10, 20 }), nil) {
		t.Fatal("a move should repaint")
	}
	if !tr.in.Inside || tr.in.MouseX != 10 || tr.in.MouseY != 20 {
		t.Fatalf("pointer %+v", tr.in)
	}
	if tr.event(ev(platform.EvPointerMove, func(e *platform.Event) { e.X, e.Y = 10, 20 }), nil) {
		t.Fatal("a move to the same place repaints")
	}
	if !tr.event(ev(platform.EvPointerLeave, nil), nil) || tr.in.Inside {
		t.Fatal("leave not applied")
	}
	if tr.event(ev(platform.EvPointerLeave, nil), nil) {
		t.Fatal("a second leave repaints")
	}
}

func TestButtonEdges(t *testing.T) {
	tr := newTranslator()
	tr.event(press(5, 6, 100, platform.ButtonLeft), nil)
	in := &tr.in
	if !in.Down[ui.ButtonLeft] || !in.Pressed[ui.ButtonLeft] || in.Released[ui.ButtonLeft] || in.Clicks[ui.ButtonLeft] != 1 {
		t.Fatalf("after press: down %v pressed %v released %v clicks %v", in.Down, in.Pressed, in.Released, in.Clicks)
	}
	if !in.Inside || in.MouseX != 5 || in.MouseY != 6 {
		t.Fatalf("a button event carries the position: %+v", in)
	}
	in.ClearFrame()
	if !in.Down[ui.ButtonLeft] || in.Pressed[ui.ButtonLeft] {
		t.Fatal("ClearFrame must keep the held button and drop the edge")
	}
	tr.event(release(5, 6, 200, platform.ButtonLeft), nil)
	if in.Down[ui.ButtonLeft] || !in.Released[ui.ButtonLeft] {
		t.Fatalf("after release: down %v released %v", in.Down, in.Released)
	}

	// A click faster than a frame takes two frames: the release waits for the
	// next one, so no frame holds both transitions of a button.
	tr.endFrame()
	tr.event(press(5, 6, 10_000_000_000, platform.ButtonRight), nil)
	tr.event(release(5, 6, 10_000_000_001, platform.ButtonRight), nil)
	if !in.Down[ui.ButtonRight] || !in.Pressed[ui.ButtonRight] || in.Released[ui.ButtonRight] || len(tr.carry) == 0 {
		t.Fatalf("fast right click, first frame: down %v pressed %v released %v deferred %d", in.Down, in.Pressed, in.Released, len(tr.carry))
	}
	tr.endFrame()
	if in.Down[ui.ButtonRight] || in.Pressed[ui.ButtonRight] || !in.Released[ui.ButtonRight] || len(tr.carry) != 0 || !tr.pending() {
		t.Fatalf("fast right click, second frame: down %v pressed %v released %v deferred %d", in.Down, in.Pressed, in.Released, len(tr.carry))
	}

	// Buttons ui does not have are ignored and do not repaint.
	if tr.event(press(1, 1, 1, platform.ButtonBack), nil) {
		t.Fatal("the back button repaints")
	}
}

func TestDoubleClickCount(t *testing.T) {
	const ms = int64(1e6)
	tr := newTranslator()
	click := func(x, y float32, at int64) uint8 {
		tr.endFrame()
		tr.event(press(x, y, at, platform.ButtonLeft), nil)
		c := tr.in.Clicks[ui.ButtonLeft]
		tr.event(release(x, y, at+10*ms, platform.ButtonLeft), nil) // waits for the next frame
		tr.endFrame()
		tr.endFrame()
		return c
	}
	base := int64(10_000) * ms
	if c := click(100, 100, base); c != 1 {
		t.Fatalf("first click counted %d", c)
	}
	if c := click(101, 99, base+200*ms); c != 2 {
		t.Fatalf("a second click 200 ms later counted %d, want 2", c)
	}
	if c := click(100, 100, base+400*ms); c != 3 {
		t.Fatalf("a third quick click counted %d, want 3", c)
	}
	if c := click(100, 100, base+1500*ms); c != 1 {
		t.Fatalf("a click after a pause counted %d, want 1", c)
	}
	if c := click(160, 100, base+1600*ms); c != 1 {
		t.Fatalf("a quick click 60 px away counted %d, want 1", c)
	}
	// The distance limit follows the scale: 4 pt is 8 px at 2×.
	tr.scale = 2
	click(100, 100, base+5000*ms)
	if c := click(107, 100, base+5100*ms); c != 2 {
		t.Fatalf("7 px away at 2× counted %d, want 2", c)
	}
}

func TestPreciseAndWheelScroll(t *testing.T) {
	scroll := func(dx, dy float32, flags platform.Flags) *platform.Event {
		return ev(platform.EvScroll, func(e *platform.Event) { e.DX, e.DY, e.Flags = dx, dy, flags })
	}

	tr := newTranslator()
	tr.event(scroll(0, 30, platform.FlagPrecise), nil)
	tr.event(scroll(2, 12, platform.FlagPrecise), nil)
	in := &tr.in
	if !in.Precise || in.ScrollX != 2 || in.ScrollY != 42 {
		t.Fatalf("trackpad scroll accumulates pixels as is: %+v", []any{in.Precise, in.ScrollX, in.ScrollY})
	}

	// A wheel's pixels go to ui as notches of the wheel step.
	tr = newTranslator()
	tr.wheelStep = 48
	tr.event(scroll(0, 96, 0), nil)
	if tr.in.Precise || tr.in.ScrollY != 2 {
		t.Fatalf("wheel: precise %v, scroll %v, want 2 notches", tr.in.Precise, tr.in.ScrollY)
	}

	// Mixed kinds in one frame become pixels, in either order.
	tr = newTranslator()
	tr.wheelStep = 48
	tr.event(scroll(0, 96, 0), nil)
	tr.event(scroll(0, 10, platform.FlagPrecise), nil)
	if !tr.in.Precise || tr.in.ScrollY != 2*48+10 {
		t.Fatalf("wheel then trackpad: precise %v scroll %v", tr.in.Precise, tr.in.ScrollY)
	}
	tr = newTranslator()
	tr.wheelStep = 48
	tr.event(scroll(0, 10, platform.FlagPrecise), nil)
	tr.event(scroll(0, 96, 0), nil)
	if !tr.in.Precise || tr.in.ScrollY != 106 {
		t.Fatalf("trackpad then wheel: precise %v scroll %v", tr.in.Precise, tr.in.ScrollY)
	}

	if tr.event(scroll(0, 0, platform.FlagPrecise), nil) {
		t.Fatal("an empty scroll repaints")
	}
}

// TestScrollPhasesReachUI: every scroll event goes to ui.Input.Scrolls with
// its phases, those without a delta (fingers resting, lifting) too, and a
// wheel's as notches.
func TestScrollPhasesReachUI(t *testing.T) {
	for p := platform.PhaseNone; p <= platform.PhaseStationary; p++ {
		if want := [...]ui.ScrollPhase{ui.PhaseNone, ui.PhaseMayBegin, ui.PhaseBegan, ui.PhaseChanged, ui.PhaseEnded,
			ui.PhaseCancelled, ui.PhaseStationary}[p]; ui.ScrollPhase(p) != want {
			t.Fatalf("platform phase %d is ui phase %d, want %d", p, ui.ScrollPhase(p), want)
		}
	}
	tr := newTranslator()
	tr.wheelStep = 48
	add := func(e platform.Event) bool { e.Kind = platform.EvScroll; return tr.event(&e, nil) }
	if !add(platform.Event{Flags: platform.FlagPrecise, Phase: platform.PhaseMayBegin, Time: 1}) {
		t.Fatal("fingers resting on the trackpad are not input")
	}
	add(platform.Event{Flags: platform.FlagPrecise | platform.FlagInverted, Phase: platform.PhaseBegan, DY: 4, Time: 2})
	add(platform.Event{Flags: platform.FlagPrecise, Momentum: platform.PhaseChanged, DY: 6, Time: 3})
	add(platform.Event{DY: 96, Time: 4})
	in := &tr.in
	want := []ui.ScrollEvent{
		{Phase: ui.PhaseMayBegin, Precise: true, Time: 1},
		{Phase: ui.PhaseBegan, DY: 4, Precise: true, Inverted: true, Time: 2},
		{Momentum: ui.PhaseChanged, DY: 6, Precise: true, Time: 3},
		{DY: 2, Time: 4},
	}
	if in.NScrolls != len(want) {
		t.Fatalf("%d records, want %d: %+v", in.NScrolls, len(want), in.Scrolls[:in.NScrolls])
	}
	for i, w := range want {
		if in.Scrolls[i] != w {
			t.Errorf("record %d: %+v, want %+v", i, in.Scrolls[i], w)
		}
	}
}

func TestKeysAndText(t *testing.T) {
	tr := newTranslator()
	key := func(k platform.Key, flags platform.Flags, m platform.Mods) *platform.Event {
		return ev(platform.EvKey, func(e *platform.Event) { e.Key, e.Flags, e.Mods = k, flags, m })
	}
	tr.event(key(platform.KeyDown, platform.FlagDown, platform.ModShift|platform.ModSuper), nil)
	tr.event(key(platform.KeyDown, platform.FlagDown|platform.FlagRepeat, 0), nil)
	tr.event(key(platform.KeyDown, 0, 0), nil)
	tr.event(key(platform.KeyA+2, platform.FlagDown, platform.ModCtrl), nil)
	tr.event(key(platform.Key0+7, platform.FlagDown, 0), nil)
	if tr.event(key(platform.KeyF5, platform.FlagDown, 0), nil) {
		t.Fatal("a function key repaints although ui has no code for it")
	}
	in := &tr.in
	want := []ui.KeyEvent{
		{Code: ui.KeyDown, Mods: ui.ModShift | ui.ModSuper, Down: true},
		{Code: ui.KeyDown, Down: true, Repeat: true},
		{Code: ui.KeyDown},
		{Code: 'C', Mods: ui.ModCtrl, Down: true},
		{Code: '7', Down: true},
	}
	if in.NKeys != len(want) {
		t.Fatalf("%d key events, want %d", in.NKeys, len(want))
	}
	for i, w := range want {
		if in.Keys[i] != w {
			t.Errorf("key %d = %+v, want %+v", i, in.Keys[i], w)
		}
	}

	tr.event(ev(platform.EvText, nil), []byte("héllo"))
	tr.event(ev(platform.EvText, nil), []byte(" wörld"))
	if got := string(in.Text[:in.NText]); got != "héllo wörld" {
		t.Fatalf("text %q", got)
	}
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'x'
	}
	tr.event(ev(platform.EvText, nil), long) // what does not fit waits for the next frame (see TestTextOverflowCarries)
	if in.NText != ui.MaxText {
		t.Fatalf("NText %d after overflow, want %d", in.NText, ui.MaxText)
	}
}

func TestFocusCancelsButtons(t *testing.T) {
	tr := newTranslator()
	if !tr.in.Focused {
		t.Fatal("windows start focused")
	}
	tr.event(press(1, 1, 1, platform.ButtonLeft), nil)
	tr.endFrame()
	if !tr.event(ev(platform.EvFocus, nil), nil) || tr.in.Focused {
		t.Fatal("focus loss not applied")
	}
	if tr.in.Down[ui.ButtonLeft] || tr.in.Released[ui.ButtonLeft] || !tr.in.Cancelled[ui.ButtonLeft] {
		t.Fatal("losing focus must cancel a held button (its release goes to another window), not release it")
	}
	if tr.event(ev(platform.EvFocus, nil), nil) {
		t.Fatal("unchanged focus repaints")
	}
	if !tr.event(ev(platform.EvFocus, func(e *platform.Event) { e.Flags = platform.FlagDown }), nil) || !tr.in.Focused {
		t.Fatal("focus gain not applied")
	}
}

// The oldest timestamped event behind a frame starts its input-to-photon
// interval; untimed events repaint but do not start it.
func TestInputTimestamp(t *testing.T) {
	tr := newTranslator()
	move := func(x float32, at int64) {
		tr.event(ev(platform.EvPointerMove, func(e *platform.Event) { e.X, e.Time = x, at }), nil)
	}
	move(1, 0)
	move(2, 500)
	move(3, 900)
	first, dirty := tr.take()
	if first != 500 || !dirty {
		t.Fatalf("take = %d, %v; want 500, true", first, dirty)
	}
	if first, dirty := tr.take(); first != 0 || dirty {
		t.Fatalf("a second take = %d, %v; want 0, false", first, dirty)
	}
}

func TestWindowEventsAreNotInput(t *testing.T) {
	tr := newTranslator()
	for _, k := range []platform.EventKind{platform.EvClose, platform.EvResize, platform.EvVisible, platform.EvFrame, platform.EvWake, platform.EvNone} {
		if tr.event(ev(k, nil), nil) {
			t.Errorf("event kind %d is input", k)
		}
	}
	if tr.dirty {
		t.Fatal("window events dirtied the input")
	}
}

// FocusGained is the edge of the window becoming active: set for the frame
// that takes the gain, not for the focus event of a window that already has
// it (the one every window gets as it is shown), cleared by the frame's end.
func TestFocusGainedIsAnEdge(t *testing.T) {
	down := func(e *platform.Event) { e.Flags = platform.FlagDown }
	tr := newTranslator()
	tr.event(ev(platform.EvFocus, down), nil) // shown: already focused
	if tr.in.FocusGained {
		t.Fatal("the focus a new window starts with is not a gain")
	}
	tr.event(ev(platform.EvFocus, nil), nil) // lost
	tr.endFrame()
	tr.event(ev(platform.EvFocus, nil), nil) // (the loss is applied by the frame after it was deferred)
	tr.endFrame()
	if tr.in.Focused {
		t.Fatal("setup: focus should be lost")
	}
	if !tr.event(ev(platform.EvFocus, down), nil) || !tr.in.Focused || !tr.in.FocusGained {
		t.Fatalf("a regained focus is a gain: focused %v gained %v", tr.in.Focused, tr.in.FocusGained)
	}
	tr.endFrame()
	if tr.in.FocusGained {
		t.Error("FocusGained lasts one frame")
	}
}

// The character some platforms send along with a Tab key is not text: a lone
// "\t" that directly follows the key press is dropped. Text that holds a tab
// (an input-method commit) or a "\t" that follows anything else stays.
func TestTabCharacterIsNotText(t *testing.T) {
	tab := func(down platform.Flags) *platform.Event {
		return ev(platform.EvKey, func(e *platform.Event) { e.Key, e.Flags = platform.KeyTab, down })
	}
	text := func(tr *translator, s string) { tr.event(ev(platform.EvText, nil), []byte(s)) }
	got := func(tr *translator) string { return string(tr.in.Text[:tr.in.NText]) }

	tr := newTranslator()
	tr.event(tab(platform.FlagDown), nil)
	text(&tr, "\t")
	if got(&tr) != "" || tr.in.NKeys != 1 || tr.in.Keys[0].Code != ui.KeyTab {
		t.Fatalf("Tab's own character: text %q, %d keys", got(&tr), tr.in.NKeys)
	}
	text(&tr, "\t") // a second one does not follow the key
	if got(&tr) != "\t" {
		t.Fatalf("a tab that does not follow a Tab key: %q", got(&tr))
	}

	// a commit that holds a tab, batched with a Tab key
	tr = newTranslator()
	tr.event(tab(platform.FlagDown), nil)
	tr.event(ev(platform.EvPreedit, nil), nil) // the composition ends ...
	text(&tr, "a\tb")                          // ... and commits
	if got(&tr) != "a\tb" || tr.in.CommitLen != 3 {
		t.Fatalf("a commit with a tab: text %q, commit %d bytes", got(&tr), tr.in.CommitLen)
	}
	// even straight after the key
	tr = newTranslator()
	tr.event(tab(platform.FlagDown), nil)
	text(&tr, "a\tb")
	if got(&tr) != "a\tb" {
		t.Fatalf("text with a tab after a Tab key: %q", got(&tr))
	}
	// a key release, or another key, does not arm it
	tr = newTranslator()
	tr.event(tab(0), nil)
	text(&tr, "\t")
	tr.event(tab(platform.FlagDown), nil)
	tr.event(ev(platform.EvKey, func(e *platform.Event) { e.Key, e.Flags = platform.KeyDown, platform.FlagDown }), nil)
	text(&tr, "\t")
	if got(&tr) != "\t\t" {
		t.Fatalf("tabs after a release and after another key: %q", got(&tr))
	}
}

// TestScrollKeepsItsPointerPosition: a scroll event records where the pointer
// was when it came, so a pointer move later in the same frame does not choose
// the box it scrolls.
func TestScrollKeepsItsPointerPosition(t *testing.T) {
	tr := newTranslator()
	tr.event(ev(platform.EvPointerMove, func(e *platform.Event) { e.X, e.Y = 10, 20 }), nil)
	tr.event(ev(platform.EvScroll, func(e *platform.Event) { e.DY, e.Flags, e.Phase = 3, platform.FlagPrecise, platform.PhaseBegan }), nil)
	tr.event(ev(platform.EvPointerMove, func(e *platform.Event) { e.X, e.Y = 300, 40 }), nil)
	in := &tr.in
	if r := in.Scrolls[0]; !r.Pos || r.X != 10 || r.Y != 20 || in.MouseX != 300 {
		t.Fatalf("record at %v,%v (pos %v), pointer at %v", r.X, r.Y, r.Pos, in.MouseX)
	}
}

// TestWheelJumpsOnMacOS: a wheel jumps on macOS, as AppKit's does, and
// glides elsewhere.
func TestWheelJumpsOnMacOS(t *testing.T) {
	c := ui.New(nil)
	uiDefaults(c)
	if c.WheelJumps != (runtime.GOOS == "darwin") {
		t.Fatalf("WheelJumps %v on %s", c.WheelJumps, runtime.GOOS)
	}
}
