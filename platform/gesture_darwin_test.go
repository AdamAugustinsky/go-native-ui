package platform

import (
	"math"
	"testing"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

// cgGesture builds trackpad gesture NSEvents the way the system's arrive: a
// CGEvent of the gesture type (29) whose HID gesture type says pinch, rotation
// or smart zoom, with the value, phase, window and the point in the window set,
// wrapped by +[NSEvent eventWithCGEvent:]. Nothing is posted to the system.
// The field numbers are CoreGraphics's private gesture fields, found by
// experiment: 110 the gesture type, 113 the amount, 132 the CGGesturePhase, 51
// the window number; CGEventSetWindowLocation sets the point (from the top left).
type cgGesture struct {
	create, setType, setInt, setF64, setWin, release uintptr
}

const (
	cgGestureRotate    = 5
	cgGestureMagnify   = 8
	cgGestureSmartZoom = 22
	cgPhaseBegan       = 1
	cgPhaseChanged     = 2
	cgPhaseEnded       = 4
	cgPhaseCancelled   = 8
	cgPhaseMayBegin    = 128
)

func newCGGesture(t *testing.T) *cgGesture {
	if _, err := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit"); err != nil {
		t.Skip("no AppKit:", err)
	}
	h, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skip("no CoreGraphics:", err)
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	sym := func(lib uintptr, n string) uintptr {
		p, err := purego.Dlsym(lib, n)
		if err != nil {
			t.Skipf("no %s: %v", n, err)
		}
		return p
	}
	return &cgGesture{create: sym(h, "CGEventCreate"), setType: sym(h, "CGEventSetType"), setInt: sym(h, "CGEventSetIntegerValueField"),
		setF64: sym(h, "CGEventSetDoubleValueField"), setWin: sym(h, "CGEventSetWindowLocation"), release: sym(cf, "CFRelease")}
}

// event returns an autoreleased NSEvent for window w, at (x, y) points from its
// top left. kind is cgGestureMagnify, cgGestureRotate or cgGestureSmartZoom.
func (g *cgGesture) event(w *Window, kind, phase int, value, x, y float64) objc.ID {
	f := objc.F
	num := uintptr(objc.Send(w.win, objc.Sel("windowNumber")))
	cg := f.Reset().Arg(0).Call(g.create).R0()
	f.Reset().Arg(cg).Arg32(29).Call(g.setType) // kCGSEventGesture
	f.Reset().Arg(cg).Arg32(110).Arg(uintptr(kind)).Call(g.setInt)
	f.Reset().Arg(cg).Arg32(113).ArgF64(value).Call(g.setF64)
	f.Reset().Arg(cg).Arg32(132).Arg(uintptr(phase)).Call(g.setInt)
	f.Reset().Arg(cg).Arg32(51).Arg(num).Call(g.setInt)
	f.Reset().Arg(cg).ArgPoint(x, y).Call(g.setWin)
	ev := objc.Send1(objc.Cls("NSEvent"), objc.Sel("eventWithCGEvent:"), uintptr(cg))
	f.Reset().Arg(cg).Call(g.release)
	return ev
}

// gestureSequence is a pinch, a rotation and a smart zoom as the trackpad
// sends them, in window points from the top left.
func gestureSequence(g *cgGesture, w *Window) []objc.ID {
	return []objc.ID{
		g.event(w, cgGestureMagnify, cgPhaseBegan, 0.125, 120, 80),
		g.event(w, cgGestureMagnify, cgPhaseChanged, -0.0625, 121, 81),
		g.event(w, cgGestureRotate, cgPhaseBegan, 10, 122, 82),
		g.event(w, cgGestureRotate, cgPhaseChanged, -2.5, 123, 83),
		g.event(w, cgGestureMagnify, cgPhaseEnded, 0, 124, 84),
		g.event(w, cgGestureRotate, cgPhaseCancelled, 0, 125, 85),
		g.event(w, cgGestureSmartZoom, 0, 0, 126, 86),
	}
}

type wantGesture struct {
	kind   EventKind
	phase  ScrollPhase
	amount float32
	x, y   float64 // window points from the top left
}

var gestureWant = []wantGesture{
	{EvMagnify, PhaseBegan, 0.125, 120, 80},
	{EvMagnify, PhaseChanged, -0.0625, 121, 81},
	{EvRotate, PhaseBegan, 10, 122, 82},
	{EvRotate, PhaseChanged, -2.5, 123, 83},
	{EvMagnify, PhaseEnded, 0, 124, 84},
	{EvRotate, PhaseCancelled, 0, 125, 85},
	{EvSmartMagnify, PhaseNone, 0, 126, 86},
}

// checkGestures compares the gestures in evs with gestureWant: kind, phase,
// amount, and the point in pixels from the content's top left (the content
// view is the window's below its titlebar: y counts from there).
func checkGestures(t *testing.T, how string, evs []Event, scale float32, offY float64) {
	t.Helper()
	var got []Event
	for _, e := range evs {
		if e.Kind == EvMagnify || e.Kind == EvRotate || e.Kind == EvSmartMagnify {
			got = append(got, e)
		}
	}
	if len(got) != len(gestureWant) {
		t.Fatalf("%s: %d gesture events %+v, want %d", how, len(got), got, len(gestureWant))
	}
	for i, w := range gestureWant {
		e := got[i]
		x, y := float64(w.x)*float64(scale), (w.y-offY)*float64(scale)
		if e.Kind != w.kind || e.Phase != w.phase || e.Amount != w.amount ||
			math.Abs(float64(e.X)-x) > 0.5 || math.Abs(float64(e.Y)-y) > 0.5 {
			t.Errorf("%s event %d: %+v; want kind %d phase %d amount %v at (%.0f, %.0f)", how, i, e, w.kind, w.phase, w.amount, x, y)
		}
	}
}

// TestGestureEventsThroughTheQueue: synthetic gesture events posted to the
// application's queue (a window's events, never the system's stream) come out
// of Wait in order as EvMagnify, EvRotate and EvSmartMagnify, with their phase,
// amount and point (the platform's pixels), as the loop dequeues them.
func TestGestureEventsThroughTheQueue(t *testing.T) {
	g := newCGGesture(t)
	var evs []Event
	var scale float32
	var offY float64
	var why string
	onMain(func() {
		w, y := openKeyRig()
		if y != "" {
			why = y
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		w.Wait(0)
		scale = w.scale
		offY = objc.SendRect(w.win, objc.Sel("frame")).H - w.hPt // the titlebar above the content, in points
		for _, ev := range gestureSequence(g, w) {
			objc.Send2(w.app, selPostEvent, uintptr(ev), 0)
		}
		for i := 0; i < 20 && len(evs) < len(gestureWant); i++ {
			evs = append(evs, w.Wait(50e6)...)
		}
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	checkGestures(t, "queue", evs, scale, offY)
}

// TestGestureEventsThroughAppKitsRouting: the same events sent through
// -[NSWindow sendEvent:] reach the view's magnifyWithEvent:, rotateWithEvent:
// and smartMagnifyWithEvent: (the route of a gesture a native host handed
// up), and are the same events the loop queues itself.
func TestGestureEventsThroughAppKitsRouting(t *testing.T) {
	g := newCGGesture(t)
	var viaWindow, viaLoop []Event
	var scale float32
	var offY float64
	var why string
	onMain(func() {
		w, y := openKeyRig()
		if y != "" {
			why = y
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		scale = w.scale
		offY = objc.SendRect(w.win, objc.Sel("frame")).H - w.hPt
		w.q.reset()
		for _, ev := range gestureSequence(g, w) {
			objc.Send1(w.win, selSendEvent, uintptr(ev))
		}
		viaWindow = append(viaWindow, w.q.ev...)
		w.q.reset()
		for _, ev := range gestureSequence(g, w) {
			w.handle(ev)
		}
		viaLoop = append(viaLoop, w.q.ev...)
		w.q.reset()
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	checkGestures(t, "window sendEvent:", viaWindow, scale, offY)
	checkGestures(t, "loop", viaLoop, scale, offY)
	for i := range viaLoop {
		a, b := viaLoop[i], viaWindow[i]
		a.Time, b.Time = 0, 0 // (the CGEvents were built apart: their times differ)
		if a != b {
			t.Errorf("event %d: the loop queued %+v, AppKit's routing %+v", i, viaLoop[i], viaWindow[i])
		}
	}
}

// TestGestureEventsThroughNSAppSendEvent: [NSApp sendEvent:] on an active
// application. AppKit drops a synthetic phased gesture there (only a real
// trackpad's stream, which arrives through the event queue as in
// TestGestureEventsThroughTheQueue, starts one), so what is checked is that the
// one-shot smart zoom still gets through with its point, and that nothing wrong
// comes out. Inactive (a runner whose window is not key) it is skipped; the
// phased pinch and rotation on a real trackpad are on the checkpoint-1 hand
// list ("pinch and rotate on the trackpad over the gallery's image scene").
func TestGestureEventsThroughNSAppSendEvent(t *testing.T) {
	g := newCGGesture(t)
	var evs []Event
	var scale float32
	var offY float64
	var active bool
	var why string
	onMain(func() {
		w, y := openKeyRig()
		if y != "" {
			why = y
			return
		}
		defer w.Close()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		objc.Send1(w.app, objc.Sel("activateIgnoringOtherApps:"), 1)
		objc.Send1(w.win, objc.Sel("makeKeyAndOrderFront:"), 0)
		for i := 0; i < 5; i++ {
			w.Wait(20e6)
		}
		active = objc.SendBool(w.app, objc.Sel("isActive")) && objc.SendBool(w.win, objc.Sel("isKeyWindow"))
		scale = w.scale
		offY = objc.SendRect(w.win, objc.Sel("frame")).H - w.hPt
		w.q.reset()
		for _, ev := range gestureSequence(g, w) {
			objc.Send1(w.app, selSendEvent, uintptr(ev))
		}
		evs = append(evs, w.q.ev...)
		w.q.reset()
	})
	if why != "" {
		t.Skipf("cannot open a window here: %s", why)
	}
	if !active {
		t.Skip("the application cannot become active here: [NSApp sendEvent:] drops gestures then; see the checkpoint-1 hand list")
	}
	// Every event that came is one of the sequence's, in order; the smart
	// zoom is among them.
	next, smart := 0, false
	for _, e := range evs {
		if e.Kind != EvMagnify && e.Kind != EvRotate && e.Kind != EvSmartMagnify {
			continue
		}
		found := false
		for ; next < len(gestureWant) && !found; next++ {
			w := gestureWant[next]
			x, y := w.x*float64(scale), (w.y-offY)*float64(scale)
			found = e.Kind == w.kind && e.Phase == w.phase && e.Amount == w.amount &&
				math.Abs(float64(e.X)-x) <= 0.5 && math.Abs(float64(e.Y)-y) <= 0.5
		}
		if !found {
			t.Errorf("NSApp sendEvent: unexpected gesture event %+v", e)
		}
		smart = smart || e.Kind == EvSmartMagnify
	}
	if !smart {
		t.Errorf("NSApp sendEvent: the smart zoom did not arrive: %+v", evs)
	}
}
