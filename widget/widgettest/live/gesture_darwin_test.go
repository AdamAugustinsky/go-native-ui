//go:build darwin

package live

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"gonativeui/app"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/widget/widgettest"
)

// gestureView is an NSView that records the gesture and scroll events AppKit
// delivers to it, to learn what a synthetic event reaches.
var gestureView struct {
	once sync.Once
	cls  objc.ID
	mu   sync.Mutex
	got  []string
}

func newGestureView(w, h float64) objc.ID {
	gv := &gestureView
	gv.once.Do(func() {
		d := objc.Define("GNUIWidgettestGestureView", objc.Cls("NSView"), nil)
		note := func(name, sel string) {
			d.Method(objc.Sel(sel), "v@:@", func(c *ffi.CB) {
				ev := objc.ID(c.Int(2))
				s := name
				switch name {
				case "magnify":
					s += fmt.Sprintf(" %.2f", objc.SendF64(ev, objc.Sel("magnification")))
				case "rotate":
					rot := objc.Sel("rotation")
					s += fmt.Sprintf(" %.0f", c.Frame().Reset().Arg(uintptr(ev)).Arg(uintptr(rot)).Call(objc.MsgSend).F32(0))
				}
				gv.mu.Lock()
				gv.got = append(gv.got, s)
				gv.mu.Unlock()
			})
		}
		note("magnify", "magnifyWithEvent:")
		note("rotate", "rotateWithEvent:")
		note("smart", "smartMagnifyWithEvent:")
		note("scroll", "scrollWheel:")
		d.Method(objc.Sel("acceptsFirstResponder"), "c@:", func(c *ffi.CB) { c.RetBool(true) })
		gv.cls = d.Register()
	})
	init := objc.Sel("initWithFrame:")
	v := objc.Send(gv.cls, objc.Sel("alloc"))
	return objc.ID(objc.F.Reset().Arg(uintptr(v)).Arg(uintptr(init)).ArgRect(0, 0, w, h).Call(objc.MsgSend).R0())
}

// What AppKit delivers of the synthetic scroll and gesture events to a view
// that covers the window. Scrolls reach it (the app's own scroll tests rely on
// it). Whether magnify and rotate do depends on the window being key, which a
// runner's session may not allow: the test records what it saw, and fails only
// when the window was key and nothing arrived.
func TestSyntheticGesturesReachAView(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(3)
	l.Do(func(a *app.App) {
		w := widgettest.NativeWindow(a)
		cv := objc.Send(w.Window, objc.Sel("contentView"))
		b := objc.SendRect(cv, objc.Sel("bounds"))
		v := newGestureView(b.W, b.H)
		objc.Send1(cv, objc.Sel("addSubview:"), uintptr(v))
		objc.Send1(w.Window, objc.Sel("makeKeyAndOrderFront:"), 0)
		objc.Send1(w.Window, objc.Sel("makeFirstResponder:"), uintptr(v))
	})
	l.Frames(3)
	var key, active bool
	l.Do(func(a *app.App) {
		key = objc.SendBool(objc.ID(a.Win.Native().NSWindow), objc.Sel("isKeyWindow"))
		active = objc.SendBool(widgettest.NSApp(), objc.Sel("isActive"))
	})
	l.Scroll(300, 300, 0, -40)
	l.Magnify(300, 300, 0.5, 3)
	l.Rotate(300, 300, 30, 2)
	l.Native(func(w widgettest.Win) {
		if ev, err := w.SmartMagnifyEvent(300, 300); err == nil {
			widgettest.Dispatch(ev)
		}
	})
	l.Frames(2)
	gestureView.mu.Lock()
	got := slices.Clone(gestureView.got)
	gestureView.mu.Unlock()
	t.Logf("window key %v, app active %v; the view received %v", key, active, got)
	count := func(prefix string) (n int) {
		for _, s := range got {
			if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
				n++
			}
		}
		return
	}
	if count("scroll") == 0 {
		t.Error("a scroll event did not reach the view")
	}
	if key && count("magnify") == 0 {
		t.Error("the window is key and no magnify event reached the view")
	}
	if !key {
		t.Logf("the window is not key here (the session cannot activate the app): gesture delivery is not checked")
	}
}
