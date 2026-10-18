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
	frame := objc.Send(w.view, selSuperview)
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
		Pauses: w.pauses, IdleTicks: w.idleTicks,
	}
	if w.link != 0 {
		s.LinkPausedOS = objc.SendBool(w.link, selIsPausedLink)
	}
	return s
}
