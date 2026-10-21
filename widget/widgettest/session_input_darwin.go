//go:build darwin

package widgettest

import (
	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/ui"
)

// What a test does to a LiveApp: look into the semantic tree of the last
// frame, and send input to the window through AppKit. Each call is a Do (or
// several) and ends with frames run, so the app has seen the input when it
// returns.

// Tree is a copy of the semantic tree of the app's last frame, valid after
// the call (the context's own snapshot is not).
func (l *LiveApp) Tree() *ui.SemTree {
	l.t.Helper()
	t := &ui.SemTree{}
	l.Do(func(a *app.App) {
		var s ui.SemTree
		a.UI.Semantics(&s)
		t.Nodes = append(t.Nodes, s.Nodes...)
		t.Bytes = append(t.Bytes, s.Bytes...)
		t.Dropped = s.Dropped
	})
	return t
}

// Find returns the first node of the last frame's tree with the role and name.
func (l *LiveApp) Find(role ui.Role, name string) (ui.SemNode, bool) {
	l.t.Helper()
	t := l.Tree()
	for _, n := range t.Nodes {
		if n.Role == role && string(t.Text(n.Name)) == name {
			return n, true
		}
	}
	return ui.SemNode{}, false
}

// Must is Find that fails the test when there is no such node.
func (l *LiveApp) Must(role ui.Role, name string) ui.SemNode {
	l.t.Helper()
	n, ok := l.Find(role, name)
	if !ok {
		l.t.Fatalf("widgettest: no node with role %s named %q in the semantic tree", RoleName(role), name)
	}
	return n
}

// Dump is the semantic tree of the last frame as text (package function Dump).
func (l *LiveApp) Dump() string { l.t.Helper(); return Dump(l.Tree()) }

// Native runs f with the window events are aimed at.
func (l *LiveApp) Native(f func(w Win)) {
	l.t.Helper()
	l.Do(func(a *app.App) { f(NativeWindow(a)) })
}

// MoveTo moves the pointer to (x, y) pixels with a real mouse-moved event.
func (l *LiveApp) MoveTo(x, y float32) {
	l.t.Helper()
	l.Native(func(w Win) { w.Move(x, y) })
	l.Frames(1)
}

// ClickAt presses and releases the left button at (x, y) pixels, the press
// in a frame of its own: the pointer moves there, a frame runs, the button
// goes down, a frame runs, it goes up, a frame runs.
func (l *LiveApp) ClickAt(x, y float32, mods ui.Mods) {
	l.t.Helper()
	l.Native(func(w Win) { Post(w.MouseEvent(MouseMove, x, y, mods, 0)) })
	l.Frames(2)
	l.Native(func(w Win) { Post(w.MouseEvent(MouseDown, x, y, mods, 1)) })
	l.Frames(2)
	l.Native(func(w Win) { Post(w.MouseEvent(MouseUp, x, y, mods, 1)) })
	l.Frames(2)
}

// Click clicks the centre of the node with the role and name.
func (l *LiveApp) Click(role ui.Role, name string) {
	l.t.Helper()
	x, y := Centre(l.Must(role, name))
	l.ClickAt(x, y, 0)
}

// Press presses and releases a key with the modifiers through the window.
func (l *LiveApp) Press(code ui.KeyCode, mods ui.Mods) {
	l.t.Helper()
	l.Native(func(w Win) { w.Press(code, mods) })
	l.Frames(2)
}

// Type types s with key events, as a keyboard does.
func (l *LiveApp) Type(s string) {
	l.t.Helper()
	l.Native(func(w Win) { w.TypeText(s) })
	l.Frames(2)
}

// Scroll sends a trackpad scroll of (dx, dy) points over (x, y) pixels as a
// begin, a change and an end, a frame between.
func (l *LiveApp) Scroll(x, y, dx, dy float32) { l.t.Helper(); l.ScrollWith(x, y, dx, dy, 0) }

// ScrollWith is Scroll with the modifiers held: a Shift-wheel or ⌘-scroll.
func (l *LiveApp) ScrollWith(x, y, dx, dy float32, mods ui.Mods) {
	l.t.Helper()
	for _, p := range [3]ScrollPhase{ScrollBegan, ScrollChanged, ScrollEnded} {
		var err error
		l.Native(func(w Win) {
			var ev objc.ID
			if ev, err = w.ScrollEvent(x, y, dx, dy, p, ScrollNone, mods); err == nil {
				Dispatch(ev)
			}
		})
		if err != nil {
			l.t.Fatalf("widgettest: %v", err)
		}
		l.Frames(1)
	}
	l.Frames(1)
}

// Magnify sends a pinch over (x, y) pixels: steps events that add up to the
// fraction total (0.5 grows by half), begun, changed and ended as a real
// gesture is, a frame between.
func (l *LiveApp) Magnify(x, y float32, total float64, steps int) {
	l.t.Helper()
	l.gesture(x, y, steps, func(w Win, p GesturePhase, per float64) (objc.ID, error) { return w.MagnifyEvent(x, y, per, p) }, total)
}

// SmartMagnify sends the two-finger double tap over (x, y) pixels.
func (l *LiveApp) SmartMagnify(x, y float32) {
	l.t.Helper()
	var err error
	l.Native(func(w Win) {
		var ev objc.ID
		if ev, err = w.SmartMagnifyEvent(x, y); err == nil {
			Dispatch(ev)
		}
	})
	if err != nil {
		l.t.Fatalf("widgettest: %v", err)
	}
	l.Frames(2)
}

// Rotate sends a rotation over (x, y) pixels: steps events that add up to
// degrees, phased like a real gesture.
func (l *LiveApp) Rotate(x, y float32, degrees float64, steps int) {
	l.t.Helper()
	l.gesture(x, y, steps, func(w Win, p GesturePhase, per float64) (objc.ID, error) { return w.RotateEvent(x, y, per, p) }, degrees)
}

func (l *LiveApp) gesture(x, y float32, steps int, build func(w Win, p GesturePhase, per float64) (objc.ID, error), total float64) {
	l.t.Helper()
	if steps < 1 {
		steps = 1
	}
	send := func(mk func(w Win) (objc.ID, error)) {
		var err error
		l.Native(func(w Win) {
			var ev objc.ID
			if ev, err = mk(w); err == nil {
				Dispatch(ev)
			}
		})
		if err != nil {
			l.t.Fatalf("widgettest: %v", err)
		}
		l.Frames(1)
	}
	phase := func(p GesturePhase, per float64) {
		send(func(w Win) (objc.ID, error) { return build(w, p, per) })
	}
	send(func(w Win) (objc.ID, error) { return w.BeginGestureEvent(x, y) })
	phase(GestureBegan, 0)
	for i := 0; i < steps; i++ {
		phase(GestureChanged, total/float64(steps))
	}
	phase(GestureEnded, 0)
	send(func(w Win) (objc.ID, error) { return w.EndGestureEvent(x, y) })
}
