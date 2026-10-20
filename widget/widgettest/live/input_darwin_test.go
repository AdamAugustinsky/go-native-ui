//go:build darwin

package live

import (
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

// The events the builders make are what AppKit calls them.
func TestEventsAreWhatAppKitCallsThem(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(3)
	l.Do(func(a *app.App) {
		w := widgettest.NativeWindow(a)
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
		}
		ev, _ := w.MagnifyEvent(100, 100, 0.25, widgettest.GestureChanged)
		if m := objc.SendF64(ev, objc.Sel("magnification")); m != 0.25 {
			t.Errorf("magnification %v, want 0.25", m)
		}
		// aimed at the point asked, whatever the real pointer is doing
		x, y := objc.SendPoint(ev, objc.Sel("locationInWindow"))
		if x != 50 || y < 100 {
			t.Errorf("locationInWindow %v,%v, want x 50 (100 px at scale 2)", x, y)
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
