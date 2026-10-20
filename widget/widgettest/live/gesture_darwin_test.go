//go:build darwin

package live

import (
	"math"
	"sync"
	"testing"

	"gonativeui/app"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/widget/widgettest"
)

// gotEvent is one message a gestureView received.
type gotEvent struct {
	kind   string  // magnify, rotate, smart, scroll
	amount float64 // the magnification, the rotation in degrees, the vertical scrolling delta
	x, y   float64 // the view-local point of the event, in points from the view's bottom left
	phase  uintptr // NSEventPhase
}

// gestureView is an NSView that records the gesture and scroll events AppKit
// delivers to it.
var gestureView gestureRecorder

type gestureRecorder struct {
	once sync.Once
	cls  objc.ID
	mu   sync.Mutex
	got  []gotEvent
}

func newGestureView(w, h float64) objc.ID {
	gv := &gestureView
	gv.once.Do(func() {
		d := objc.Define("GNUIWidgettestGestureView", objc.Cls("NSView"), nil)
		note := func(kind, sel string) {
			d.Method(objc.Sel(sel), "v@:@", func(c *ffi.CB) {
				self, ev := objc.ID(c.Int(0)), objc.ID(c.Int(2))
				g := gotEvent{kind: kind}
				switch kind {
				case "magnify":
					g.amount = objc.SendF64(ev, objc.Sel("magnification"))
				case "rotate":
					rot := objc.Sel("rotation")
					g.amount = float64(c.Frame().Reset().Arg(uintptr(ev)).Arg(uintptr(rot)).Call(objc.MsgSend).F32(0))
				case "scroll":
					g.amount = objc.SendF64(ev, objc.Sel("scrollingDeltaY"))
					g.phase = uintptr(objc.Send(ev, objc.Sel("phase")))
				}
				wx, wy := objc.SendPoint(ev, objc.Sel("locationInWindow"))
				convert := objc.Sel("convertPoint:fromView:")
				g.x, g.y = func() (float64, float64) {
					f := c.Frame().Reset().Arg(uintptr(self)).Arg(uintptr(convert)).ArgPoint(wx, wy).Arg(0).Call(objc.MsgSend)
					return f.F64(0), f.F64(1)
				}()
				gv.mu.Lock()
				gv.got = append(gv.got, g)
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

// take returns what the view received since the last call.
func (gv *gestureRecorder) take() []gotEvent {
	gv.mu.Lock()
	defer gv.mu.Unlock()
	g := gv.got
	gv.got = nil
	return g
}

// Scrolls, pinches, rotations and smart magnifies built for a window reach the
// view under them whether or not the application is active and the window
// key, with the amounts and the point they were made with: the view sees what
// a trackpad would have sent. A view that covers the window is the target.
func TestSyntheticGesturesReachAView(t *testing.T) {
	l, _ := startProbe(t)
	l.Frames(3)
	var view objc.ID
	l.Do(func(a *app.App) {
		w := widgettest.NativeWindow(a)
		cv := objc.Send(w.Window, objc.Sel("contentView"))
		b := objc.SendRect(cv, objc.Sel("bounds"))
		view = newGestureView(b.W, b.H)
		objc.Send1(cv, objc.Sel("addSubview:"), uintptr(view))
		objc.Send1(w.Window, objc.Sel("makeFirstResponder:"), uintptr(view))
	})
	l.Frames(3)
	var wantX, wantY float64 // the view-local point of (300, 300) px
	l.Do(func(a *app.App) {
		w := widgettest.NativeWindow(a)
		wx, wy := w.WindowPoint(300, 300)
		convert := objc.Sel("convertPoint:fromView:")
		objc.F.Reset().Arg(uintptr(view)).Arg(uintptr(convert)).ArgPoint(wx, wy).Arg(0).Call(objc.MsgSend)
		wantX, wantY = objc.F.F64(0), objc.F.F64(1)
	})
	gestureView.take()

	l.Scroll(300, 300, 0, -40)
	scrolls := gestureView.take()
	l.Magnify(300, 300, 0.5, 3)
	pinches := gestureView.take()
	l.Rotate(300, 300, 30, 2)
	turns := gestureView.take()
	l.Native(func(w widgettest.Win) {
		ev, err := w.SmartMagnifyEvent(300, 300)
		if err != nil {
			t.Errorf("%v", err)
			return
		}
		widgettest.Dispatch(ev)
	})
	l.Frames(1)
	smarts := gestureView.take()

	sum := func(got []gotEvent, kind string) (n int, total float64) {
		for _, g := range got {
			if g.kind == kind {
				n++
				total += g.amount
				if math.Abs(g.x-wantX) > 0.01 || math.Abs(g.y-wantY) > 0.01 {
					t.Errorf("%s event arrived at view point %.2f,%.2f, want %.2f,%.2f", kind, g.x, g.y, wantX, wantY)
				}
			}
		}
		return
	}
	if n, total := sum(scrolls, "scroll"); n != 3 || total != -120 {
		t.Errorf("scroll: %d events adding up to %v, want 3 adding up to -120: %+v", n, total, scrolls)
	}
	if n, total := sum(pinches, "magnify"); n != 5 || math.Abs(total-0.5) > 1e-6 {
		t.Errorf("magnify: %d events adding up to %v, want 5 (began, 3 changes, ended) adding up to 0.5: %+v", n, total, pinches)
	}
	if n, total := sum(turns, "rotate"); n != 4 || math.Abs(total-30) > 1e-3 {
		t.Errorf("rotate: %d events adding up to %v, want 4 (began, 2 changes, ended) adding up to 30 degrees: %+v", n, total, turns)
	}
	if n, _ := sum(smarts, "smart"); n != 1 {
		t.Errorf("smart magnify: %d events, want 1: %+v", n, smarts)
	}
}
