//go:build darwin

package platform

import "gonativeui/internal/objc"

// Selectors are resolved up front: objc.Sel uses the main thread's call frame,
// so it must not be called in the middle of building an argument list.
var (
	selClassName = objc.Sel("className")
)

// SimulateLiveResize is a test hook (macOS only): it runs the same code
// path as a user dragging a window edge, without a mouse. It enters live
// resize mode, sets each content size in turn (in logical units; AppKit
// delivers windowDidResize: synchronously, which calls the frame handler)
// and leaves live resize mode. Call it from the main thread between Wait
// calls, with a frame handler registered.
//
// After each size it also does what a display-link tick does when the app
// has asked for a frame (the frame after a resize, an animation), in the same
// run-loop turn, as a real drag does: two draws then land in one Core
// Animation transaction, which once starved the drawable pool (nextDrawable
// blocked for its 1 s timeout on every step; gpu.Surface.EndFrame).
func (w *Window) SimulateLiveResize(sizes [][2]float64) {
	w.enterLiveResize()
	for _, s := range sizes {
		objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetContentSize)).ArgPoint(s[0], s[1]).Call(objc.MsgSend)
		if w.frameArmed {
			now := Now()
			w.frameArmed, w.idleTicks, w.ticksSinceFrame, w.lastFrameT = false, 0, 0, now
			w.q.push(Event{Kind: EvFrame, Time: now})
			w.runHandlerFrom(true)
		}
	}
	w.exitLiveResize()
}

// DebugHitTest is a probe hook (macOS only): it asks the window's theme frame
// (the content view's superview, which holds the content view and the
// titlebar container) which view AppKit's hit test finds at a point of the
// content given in physical pixels, origin top left, and returns its class
// name ("" if none). It says who a mouse press at the point would be routed
// to. Main thread.
func (w *Window) DebugHitTest(xPx, yPx float32) string {
	frame := objc.Send(w.content, selSuperview)
	if frame == 0 {
		return ""
	}
	s := float64(w.scale)
	x, y := float64(xPx)/s, w.hPt-float64(yPx)/s
	// hitTest: takes a point in the superview's coordinates; the theme frame has none, so window coordinates.
	hit := objc.ID(objc.F.Reset().Arg(uintptr(frame)).Arg(uintptr(selHitTest)).ArgPoint(x, y).Call(objc.MsgSend).R0())
	if hit == 0 {
		return ""
	}
	return string(objc.AppendUTF8(nil, objc.Send(hit, selClassName)))
}

// DebugIsContentView reports whether the view hit at the point is the
// window's own content view (see DebugHitTest).
func (w *Window) DebugIsContentView(xPx, yPx float32) bool {
	s := float64(w.scale)
	return w.hitsView(float64(xPx)/s, w.hPt-float64(yPx)/s)
}

// DebugDragAt reports whether a left press at a point of the content
// (physical pixels, origin top left) would move the window: the drag map set
// with SetDragRegions says so and AppKit's hit test finds our view there.
func (w *Window) DebugDragAt(xPx, yPx float32) bool {
	s := float64(w.scale)
	return w.drags.at(xPx, yPx) && w.hitsView(float64(xPx)/s, w.hPt-float64(yPx)/s)
}

// DebugDoubleClickSetting is the system setting a double click on a drag area
// follows (AppleActionOnDoubleClick: "Maximize", "Fill", "Minimize", "None";
// "" when unset, which zooms).
func (w *Window) DebugDoubleClickSetting() string { return defaultsString("AppleActionOnDoubleClick") }

var (
	selIsPausedLink = objc.Sel("isPaused")
	selIsKeyWindow  = objc.Sel("isKeyWindow")
	selIsActiveApp  = objc.Sel("isActive")
	selIsMini       = objc.Sel("isMiniaturized")
)

// DebugState returns a snapshot of the frame-delivery state (see DebugState).
// Main thread; it sends four Objective-C messages and allocates nothing.
func (w *Window) DebugState() DebugState {
	occ := uint32(uintptr(objc.Send(w.win, selOcclusionState)))
	s := DebugState{
		Valid: true, Now: Now(),
		Occlusion: occ, Visible: occ&2 != 0, LastVisible: w.visible, LastVisibleT: w.visibleT,
		Key:       objc.SendBool(w.win, selIsKeyWindow),
		AppActive: objc.SendBool(w.app, selIsActiveApp),
		Mini:      objc.SendBool(w.win, selIsMini),
		MetalLink: w.metalLink, LinkPaused: w.linkPaused,
		FrameArmed: w.frameArmed, ArmedT: w.armedT,
		Drawable: w.drawable != 0, Live: w.live, Relink: w.relink,
		Ticks: w.ticks, TicksSinceFrame: w.ticksSinceFrame, LastTickT: w.lastTickT, LastFrameT: w.lastFrameT,
		Pauses: w.pauses, IdleTicks: w.idleTicks, Restarts: w.restarts,
	}
	if w.link != 0 {
		s.LinkPausedOS = objc.SendBool(w.link, selIsPausedLink)
	}
	return s
}

// DebugPostMouse is a test hook (macOS only): it posts a mouse event of the
// given NSEventType (5 moved, 1 left down, 2 left up) at a point of the
// content in logical units from its top left to the window's own event
// queue, so it takes the path a real one takes (the pump, sendEvent,
// AppKit's hit test) without moving the system's pointer. Main thread.
func (w *Window) DebugPostMouse(typ uintptr, xPt, yPt float64) {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	uptime := objc.SendF64(objc.Send(objc.Cls("NSProcessInfo"), objc.Sel("processInfo")), objc.Sel("systemUptime"))
	num := objc.Send(w.win, objc.Sel("windowNumber"))
	sel := objc.Sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:")
	cls := objc.Cls("NSEvent")
	var clicks uintptr
	if typ == 1 || typ == 2 {
		clicks = 1
	}
	ev := objc.ID(objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).Arg(typ).ArgPoint(xPt, w.hPt-yPt).Arg(0).ArgF64(uptime).
		Arg(uintptr(num)).Arg(0).Arg(0).Arg(clicks).ArgF32(1).Call(objc.MsgSend).R0())
	if ev != 0 {
		objc.Send2(w.app, selPostEvent, uintptr(ev), 0)
	}
}

// DebugStopLink is a test hook (macOS only): the window's CAMetalDisplayLink
// stops calling back while the window believes it runs, as one did on CI's
// virtual display (linkStopped): the link is paused behind the window's
// back. It reports whether there was a metal link to stop. Main thread.
func (w *Window) DebugStopLink() bool {
	if !w.metalLink || w.link == 0 {
		return false
	}
	objc.Send1(w.link, selSetPaused, 1)
	w.linkPaused = false
	return true
}

// DebugToggleFullScreen is a test hook (macOS only): toggleFullScreen:, as
// the green button or ⌃⌘F does, from the run loop's next turn (as a click
// would arrive) rather than inside the caller's frame. Main thread.
func (w *Window) DebugToggleFullScreen() {
	sel, toggle := objc.Sel("performSelector:withObject:afterDelay:"), objc.Sel("toggleFullScreen:")
	objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(sel)).Arg(uintptr(toggle)).Arg(0).ArgF64(0).Call(objc.MsgSend)
}

// DebugGlassRadius is a test hook (macOS only): the backdrop's corner radius
// as last set, and whether AppKit considers the window full screen.
func (w *Window) DebugGlassRadius() (radius float64, fullScreen bool) {
	return w.glassRadius, objc.Send(w.win, selStyleMask)&(1<<14) != 0
}
