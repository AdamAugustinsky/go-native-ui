//go:build darwin

package live

import (
	"math"
	"testing"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// A click, keys and a scroll sent as AppKit events reach the app through the
// pump and the view, and come out as its state.
func TestRealInputReachesTheApp(t *testing.T) {
	l, p := startProbe(t)
	l.Frames(5)

	l.Click(ui.RoleButton, "Press")
	if p.press != 1 {
		t.Errorf("a real click on Press: %d presses", p.press)
	}

	l.Click(ui.RoleTextField, "Name")
	l.Type("Hello x")
	l.Press(ui.KeyBackspace, 0)
	if got := p.buf.String(); got != "Hello " {
		t.Errorf("typed %q, then Backspace: the field holds %q", "Hello x", got)
	}
	l.Press(ui.KeyCode('A'), ui.ModSuper)
	l.Type("z")
	if got := p.buf.String(); got != "z" {
		t.Errorf("⌘A then z: the field holds %q, want z", got)
	}

	if r := firstRow(l); r != "row 0" {
		t.Fatalf("first row %q before scrolling", r)
	}
	x, y := widgettest.Centre(l.Must(ui.RoleListItem, "row 3"))
	l.Scroll(x, y, 0, -125)
	l.Scroll(x, y, 0, -125)
	if r := firstRow(l); r == "row 0" {
		t.Errorf("a trackpad scroll of 250 pt moved nothing: first row %q", r)
	}
}

// The events the builders make are what AppKit calls them, and AppKit itself
// says which window and which point they are aimed at: no stand-in answers
// for it.
func TestEventsAreWhatAppKitCallsThem(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(3)
	l.Do(func(a *app.App) {
		w := widgettest.NativeWindow(a)
		wantX, wantY := w.WindowPoint(100, 100)
		for _, c := range []struct {
			name string
			ev   func() (objc.ID, error)
			typ  int
		}{
			{"magnify", func() (objc.ID, error) { return w.MagnifyEvent(100, 100, 0.25, widgettest.GestureChanged) }, 30},
			{"rotate", func() (objc.ID, error) { return w.RotateEvent(100, 100, 15, widgettest.GestureChanged) }, 18},
			{"smart magnify", func() (objc.ID, error) { return w.SmartMagnifyEvent(100, 100) }, 32},
			{"begin gesture", func() (objc.ID, error) { return w.BeginGestureEvent(100, 100) }, 19},
			{"end gesture", func() (objc.ID, error) { return w.EndGestureEvent(100, 100) }, 20},
			{"scroll", func() (objc.ID, error) {
				return w.ScrollEvent(100, 100, 0, -50, widgettest.ScrollChanged, widgettest.ScrollNone, 0)
			}, 22},
			{"wheel", func() (objc.ID, error) { return w.ScrollLinesEvent(100, 100, 0, -1.5, 0) }, 22},
		} {
			ev, err := c.ev()
			if err != nil || ev == 0 {
				t.Errorf("%s: %v", c.name, err)
				continue
			}
			if got := widgettest.EventType(ev); got != c.typ {
				t.Errorf("%s event has NSEventType %d, want %d", c.name, got, c.typ)
			}
			if objc.Send(ev, objc.Sel("window")) != w.Window {
				t.Errorf("%s event is not aimed at the window", c.name)
			}
			if int(objc.Send(ev, objc.Sel("windowNumber"))) != w.Number() {
				t.Errorf("%s event names window number %d, want %d", c.name, objc.Send(ev, objc.Sel("windowNumber")), w.Number())
			}
			// aimed at the point asked, whatever the real pointer is doing
			if x, y := objc.SendPoint(ev, objc.Sel("locationInWindow")); x != wantX || y != wantY {
				t.Errorf("%s: locationInWindow %v,%v, want %v,%v (100,100 px at scale %v)", c.name, x, y, wantX, wantY, w.Scale)
			}
		}
		ev, _ := w.MagnifyEvent(100, 100, 0.25, widgettest.GestureChanged)
		if m := objc.SendF64(ev, objc.Sel("magnification")); m != 0.25 {
			t.Errorf("magnification %v, want 0.25", m)
		}
		ev, _ = w.ScrollEvent(100, 100, 3, -50, widgettest.ScrollChanged, widgettest.ScrollNone, 0)
		if dx, dy := objc.SendF64(ev, objc.Sel("scrollingDeltaX")), objc.SendF64(ev, objc.Sel("scrollingDeltaY")); dx != 3 || dy != -50 {
			t.Errorf("scroll deltas %v,%v, want 3,-50", dx, dy)
		}
		if !objc.SendBool(ev, objc.Sel("hasPreciseScrollingDeltas")) {
			t.Error("a pixel scroll is not precise")
		}
	})
}

// An event keeps its aim for as long as it lives, however many others are
// made: the aim is in the event, not in a table beside it.
func TestEventsKeepTheirAim(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(2)
	l.Do(func(a *app.App) {
		w := widgettest.NativeWindow(a)
		wantX, wantY := w.WindowPoint(40, 60)
		first, err := w.ScrollEvent(40, 60, 0, -5, widgettest.ScrollChanged, widgettest.ScrollNone, 0)
		if err != nil {
			t.Errorf("%v", err) // not Fatal: this runs on the main thread
			return
		}
		objc.Retain(first)
		for i := 0; i < 5000; i++ {
			if _, err := w.ScrollEvent(float32(i%300), 10, 0, 1, widgettest.ScrollChanged, widgettest.ScrollNone, 0); err != nil {
				t.Errorf("event %d: %v", i, err)
				break
			}
		}
		x, y := objc.SendPoint(first, objc.Sel("locationInWindow"))
		if x != wantX || y != wantY || objc.Send(first, objc.Sel("window")) != w.Window {
			t.Errorf("a retained event lost its aim after 5000 others: %v,%v window %v, want %v,%v window %v",
				x, y, objc.Send(first, objc.Sel("window")), wantX, wantY, w.Window)
		}
		objc.Release(first)
	})
}

// Precise scroll deltas are whole points: a fraction is refused, never
// truncated to something else; the wheel's line deltas carry fractions.
func TestScrollDeltas(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(2)
	l.Do(func(a *app.App) {
		w := widgettest.NativeWindow(a)
		if ev, err := w.ScrollEvent(10, 10, 0.5, -0.25, widgettest.ScrollChanged, widgettest.ScrollNone, 0); err == nil {
			t.Errorf("a precise scroll of (0.5, -0.25) was built (deltas %v, %v), want an error",
				objc.SendF64(ev, objc.Sel("scrollingDeltaX")), objc.SendF64(ev, objc.Sel("scrollingDeltaY")))
		}
		for _, d := range [][2]float32{{0.5, -0.25}, {3, -50}, {-1.5, 0}, {100.75, 0.1}, {0, 7.6}} {
			ev, err := w.ScrollLinesEvent(10, 10, d[0], d[1], 0)
			if err != nil {
				t.Errorf("%v: %v", d, err)
				continue
			}
			dx, dy := objc.SendF64(ev, objc.Sel("scrollingDeltaX")), objc.SendF64(ev, objc.Sel("scrollingDeltaY"))
			if math.Abs(dx-float64(d[0])) > 1.0/32768 || math.Abs(dy-float64(d[1])) > 1.0/32768 { // the deltas are 16.16 fixed point
				t.Errorf("wheel (%v, %v) has scrolling deltas (%v, %v)", d[0], d[1], dx, dy)
			}
			if objc.SendBool(ev, objc.Sel("hasPreciseScrollingDeltas")) {
				t.Errorf("a wheel in lines reports precise deltas")
			}
		}
	})
}

// The characters of a key follow the keyboard layout and the modifiers.
func TestKeyCharactersFollowTheLayout(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(2)
	str := func(ev objc.ID, sel string) string { return string(objc.AppendUTF8(nil, objc.Send(ev, objc.Sel(sel)))) }
	l.Do(func(a *app.App) {
		w := widgettest.NativeWindow(a)
		for _, c := range []struct {
			code         ui.KeyCode
			mods         ui.Mods
			chars, plain string
		}{
			{ui.KeyCode('1'), 0, "1", "1"},
			{ui.KeyCode('1'), ui.ModShift, "!", "!"},
			{ui.KeyCode('A'), 0, "a", "a"},
			{ui.KeyCode('A'), ui.ModShift, "A", "A"},
			{ui.KeyCode('A'), ui.ModSuper, "a", "a"},
			{ui.KeyLeftBracket, ui.ModShift, "{", "{"},
			{ui.KeyRightBracket, ui.ModShift, "}", "}"},
			{ui.KeyPeriod, ui.ModShift, ">", ">"},
			// With ⌘ the characters lose Shift, the ones ignoring modifiers keep
			// it: what AppKit itself makes of a keyboard CGEvent with these
			// flags (+[NSEvent eventWithCGEvent:], 2026-10-20), which NSMenu
			// matches key equivalents against.
			{ui.KeyCode('4'), ui.ModShift | ui.ModSuper, "4", "$"},
			{ui.KeyCode('Z'), ui.ModShift | ui.ModSuper, "z", "Z"},
			{ui.KeyCode('S'), ui.ModShift | ui.ModSuper, "s", "S"},
			{ui.KeyLeftBracket, ui.ModShift | ui.ModSuper, "[", "{"},
			{ui.KeyEnter, 0, "\r", "\r"},
			{ui.KeyLeft, ui.ModShift, "\uf702", "\uf702"},
		} {
			ev := w.Key(true, c.code, c.mods)
			got, plain := str(ev, "characters"), str(ev, "charactersIgnoringModifiers")
			if got != c.chars || plain != c.plain {
				t.Errorf("key %q with mods %b: characters %q / ignoring modifiers %q, want %q / %q", rune(c.code), c.mods, got, plain, c.chars, c.plain)
			}
		}
	})
}

// The same physical key types other characters on another layout, chosen
// in the process: the user's settings are not touched.
func TestKeyCharactersOnAnotherLayout(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(2)
	str := func(ev objc.ID, sel string) string { return string(objc.AppendUTF8(nil, objc.Send(ev, objc.Sel(sel)))) }
	l.Do(func(a *app.App) {
		w := widgettest.NativeWindow(a)
		fr, err := widgettest.NamedKeyLayout("com.apple.keylayout.French")
		if err != nil {
			t.Errorf("%v", err)
			return
		}
		// the key that is "1" on the US layout is "&" on the French one, and "1" with Shift
		for _, c := range []struct {
			mods  ui.Mods
			chars string
		}{{0, "&"}, {ui.ModShift, "1"}} {
			ev := w.KeyIn(fr, true, ui.KeyCode('1'), c.mods)
			if got := str(ev, "characters"); got != c.chars {
				t.Errorf("French layout, key 1, mods %b: characters %q, want %q", c.mods, got, c.chars)
			}
			if vk := objc.Send(ev, objc.Sel("keyCode")); vk != 0x12 {
				t.Errorf("French layout: key code %#x, want the physical key 0x12", vk)
			}
		}
		if got := str(w.Key(true, ui.KeyCode('1'), ui.ModShift), "characters"); got != "!" {
			t.Errorf("Key is not the US layout after a French one was read: %q", got)
		}
	})
}
