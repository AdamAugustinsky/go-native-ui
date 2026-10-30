//go:build darwin

package platform

import (
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Trackpad gestures: GNUIView's magnifyWithEvent:, rotateWithEvent: and
// smartMagnifyWithEvent:. AppKit routes the events to the view under the
// pointer (and, once a gesture began, to the view it began in), so a native
// view that uses a pinch (a hosted NSScrollView with magnification) keeps it,
// and one that hands it up reaches here. Each becomes one event with the
// phase AppKit reports and the point it happened at; ui latches the gesture
// to the box under the first one.

var (
	selMagnification = objc.SelZ("magnification\x00")
	selRotation      = objc.SelZ("rotation\x00")
	selMagnify       = objc.SelZ("magnifyWithEvent:\x00")
	selRotate        = objc.SelZ("rotateWithEvent:\x00")
	selSmartMagnify  = objc.SelZ("smartMagnifyWithEvent:\x00")
)

func registerViewGesture(v *objc.ClassDef) {
	v.Method(selMagnify, "v@:@", viewMagnify)
	v.Method(selRotate, "v@:@", viewRotate)
	v.Method(selSmartMagnify, "v@:@", viewSmartMagnify)
}

func viewMagnify(c *ffi.CB)      { viewGesture(c, EvMagnify) }
func viewRotate(c *ffi.CB)       { viewGesture(c, EvRotate) }
func viewSmartMagnify(c *ffi.CB) { viewGesture(c, EvSmartMagnify) }

// viewGesture is a gesture AppKit handed to our view: one a native host
// declined (the loop queues the others itself, gestureEvent).
func viewGesture(c *ffi.CB, kind EventKind) {
	w := viewWindow(objc.ID(c.Int(0)))
	if w == nil || w.dialogOpen() {
		return // (a sheet owns the window's input)
	}
	ev := objc.ID(c.Int(2))
	w.q.push(w.gestureOf(ev, kind, int64(objc.SendF64(ev, selTimestamp)*1e9)))
}

// gestureEvent queues the gesture NSEvent ev (typ is its NSEventType: 18
// rotate, 30 magnify, 32 smart magnify) and reports that it did. A gesture
// that belongs to a native host is left to AppKit, which gives it to the host's
// view (and to ours if the host hands it up, viewGesture); so is one for
// another window. Who owns a pinch or rotation is decided where it begins
// (pinchToHost), as for a scroll gesture.
func (w *Window) gestureEvent(ev objc.ID, typ uintptr, t int64) bool {
	if win := objc.Send(ev, selWindow); win != 0 && win != w.win {
		return false
	}
	if w.pinchToHost(ev, typ) {
		return false
	}
	kind := EvMagnify
	switch typ {
	case 18:
		kind = EvRotate
	case 32:
		kind = EvSmartMagnify
	}
	w.q.push(w.gestureOf(ev, kind, t))
	return true
}

// gestureOf is the Event of a gesture NSEvent.
func (w *Window) gestureOf(ev objc.ID, kind EventKind, t int64) Event {
	e := Event{Kind: kind, Mods: mods(uintptr(objc.Send(ev, selModifierFlags))), Time: t}
	e.X, e.Y = w.locate(ev)
	switch kind {
	case EvMagnify:
		e.Amount = float32(objc.SendF64(ev, selMagnification))
		e.Phase = phaseOf(uintptr(objc.Send(ev, selPhase)))
	case EvRotate:
		e.Amount = objc.F.Reset().Arg(uintptr(ev)).Arg(uintptr(selRotation)).Call(objc.MsgSend).F32(0)
		e.Phase = phaseOf(uintptr(objc.Send(ev, selPhase)))
	}
	return e
}
