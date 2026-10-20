//go:build darwin

package platform

import (
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Mouse and cursor: pointer events, cursor regions, and the titlebar drag
// map. registerViewMouse adds the view's mouse methods to GNUIView.

var (
	selWindow                 = objc.Sel("window")
	selLocation               = objc.Sel("locationInWindow")
	selButtonNumber           = objc.Sel("buttonNumber")
	selScrollDX               = objc.Sel("scrollingDeltaX")
	selScrollDY               = objc.Sel("scrollingDeltaY")
	selPrecise                = objc.Sel("hasPreciseScrollingDeltas")
	selPhase                  = objc.Sel("phase")
	selMomentumPhase          = objc.Sel("momentumPhase")
	selInverted               = objc.Sel("isDirectionInvertedFromDevice")
	selResetCursorRects       = objc.Sel("resetCursorRects")
	selDiscardCursorRects     = objc.Sel("discardCursorRects")
	selAddCursorRect          = objc.Sel("addCursorRect:cursor:")
	selInvalidateCursors      = objc.Sel("invalidateCursorRectsForView:")
	selMouseDownCanMoveWindow = objc.Sel("mouseDownCanMoveWindow")
	selMouseDown              = objc.Sel("mouseDown:")
	selPerformWindowDrag      = objc.Sel("performWindowDragWithEvent:")
	selClickCount             = objc.Sel("clickCount")
	selZoom                   = objc.Sel("zoom:")
	selMiniaturize            = objc.Sel("miniaturize:")
)

func registerViewMouse(v *objc.ClassDef) {
	v.Method(selResetCursorRects, "v@:", viewResetCursorRects)
	v.Method(selMouseDownCanMoveWindow, "c@:", viewMouseDownCanMoveWindow)
	v.Method(selMouseDown, "v@:@", viewMouseDown)
	v.Method(selScrollWheel, "v@:@", viewScrollWheel) // host_darwin.go
}

func viewResetCursorRects(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
		w.addCursorRects()
	}
}

// viewMouseDown swallows the press: one the app did not take must not fall
// through to the window's titlebar handling (which would start a window drag).
func viewMouseDown(*ffi.CB) {}

// viewMouseDownCanMoveWindow: a press in the view is the app's; window drags
// are explicit (SetDragRegions).
func viewMouseDownCanMoveWindow(c *ffi.CB) { c.RetBool(false) }

// pointerEvent queues the pointer move or button of a mouse event of this
// window. It reports that the event was a press on the titlebar drag map,
// which belongs to the window: the caller must not pass it on to AppKit.
func (w *Window) pointerEvent(ev objc.ID, typ uintptr, t int64) (consumed bool) {
	if objc.Send(ev, selWindow) != w.win {
		return false
	}
	lx, ly := objc.SendPoint(ev, selLocation)
	s := float64(w.scale)
	e := Event{X: float32(lx * s), Y: float32((w.hPt - ly) * s), Mods: mods(uintptr(objc.Send(ev, selModifierFlags))), Time: t}
	switch typ {
	case 5, 6, 7, 27:
		e.Kind = EvPointerMove
	default:
		e.Kind = EvPointerButton
		switch typ {
		case 1, 2:
			e.Button = ButtonLeft
		case 3, 4:
			e.Button = ButtonRight
		default:
			switch objc.Send(ev, selButtonNumber) {
			case 2:
				e.Button = ButtonMiddle
			case 3:
				e.Button = ButtonBack
			case 4:
				e.Button = ButtonForward
			}
		}
		if typ == 1 || typ == 3 || typ == 25 {
			e.Flags = FlagDown
		}
	}
	w.pointerIn = true
	if typ == 5 && w.hostMove(e.X, e.Y, t) {
		return false // over a host (host_darwin.go)
	}
	if w.content != w.view && (typ == 1 || typ == 3 || typ == 25) && !w.hitsView(lx, ly) {
		// A press on a native view (a button of Options.Buttons, a window
		// button) is AppKit's: its controls track the release themselves, so
		// ui would see a press that never ends.
		return false
	}
	if typ == 1 && w.drags.at(e.X, e.Y) && w.hitsView(lx, ly) {
		// A press on the titlebar drag map belongs to the window: the
		// double-click action, or a drag by the window server. The app
		// never sees it and AppKit's titlebar views do not get it.
		if objc.Send(ev, selClickCount) >= 2 {
			w.titlebarDoubleClick()
		} else {
			objc.Send1(w.win, selPerformWindowDrag, uintptr(ev))
		}
		return true
	}
	if typ == 1 {
		w.notePress(ev) // a drag out starts from this press (drag_darwin.go)
	}
	w.q.push(e)
	return false
}

// scrollEvent queues a scroll-wheel event.
// The event carries where it happened (FlagPos), also when it bubbled up
// from a native host the pointer is over.
func (w *Window) scrollEvent(ev objc.ID, t int64) {
	if w.scrollToHost(ev) {
		w.hosts.queuedScroll = 0
		return // the host gets it; one it does not use comes back (host_darwin.go)
	}
	e := scrollEventOf(ev, w.scale, t)
	e.X, e.Y = w.locate(ev)
	e.Flags |= FlagPos
	w.q.push(e)
	if !w.hosts.bubbling {
		w.hosts.queuedScroll = ev // for this delivery (host_darwin.go)
	}
}

// locate is where mouse event ev happened in the window's content, in
// pixels from its top-left.
func (w *Window) locate(ev objc.ID) (x, y float32) {
	lx, ly := objc.SendPoint(ev, selLocation)
	s := float64(w.scale)
	return float32(lx * s), float32((w.hPt - ly) * s)
}

// lineScroll is how many points a wheel line scrolls: NSScrollView's
// verticalLineScroll and horizontalLineScroll (measured with
// tools/appkitref/scroll.m: a 3-line event scrolls 30 points).
const lineScroll = 10

// scrollEventOf is the Event of an NSEvent of type scrollWheel in a window
// of the given scale: precise deltas in pixels or lines converted to pixels,
// the gesture and momentum phases, and whether the device is inverted.
func scrollEventOf(ev objc.ID, scale float32, t int64) Event {
	dx, dy := objc.SendF64(ev, selScrollDX), objc.SendF64(ev, selScrollDY)
	e := Event{Kind: EvScroll, Mods: mods(uintptr(objc.Send(ev, selModifierFlags))), Time: t}
	unit := float64(scale)
	if objc.SendBool(ev, selPrecise) {
		e.Flags = FlagPrecise
	} else {
		unit *= lineScroll
	}
	if objc.SendBool(ev, selInverted) {
		e.Flags |= FlagInverted
	}
	e.Phase = phaseOf(uintptr(objc.Send(ev, selPhase)))
	e.Momentum = phaseOf(uintptr(objc.Send(ev, selMomentumPhase)))
	e.DX, e.DY = float32(dx*unit), float32(dy*unit)
	return e
}

// phaseOf maps an NSEventPhase (a bit set, one bit at a time in practice) to
// a ScrollPhase.
func phaseOf(p uintptr) ScrollPhase {
	switch {
	case p&32 != 0: // NSEventPhaseMayBegin
		return PhaseMayBegin
	case p&1 != 0: // Began
		return PhaseBegan
	case p&8 != 0: // Ended
		return PhaseEnded
	case p&16 != 0: // Cancelled
		return PhaseCancelled
	case p&4 != 0: // Changed
		return PhaseChanged
	case p&2 != 0: // Stationary
		return PhaseStationary
	}
	return PhaseNone
}

// cursorObjs caches the NSCursor singletons, loaded by loadCursors.
var cursorObjs [numCursors]objc.ID

// loadCursors caches the NSCursor singletons. It runs once NSApplication
// exists: before that +arrowCursor and +IBeamCursor return nil.
func loadCursors() {
	clsCursor := objc.Cls("NSCursor")
	for c, name := range [numCursors]string{
		CursorArrow:    "arrowCursor",
		CursorPointer:  "pointingHandCursor",
		CursorText:     "IBeamCursor",
		CursorResizeEW: "resizeLeftRightCursor",
		CursorResizeNS: "resizeUpDownCursor",
	} {
		cursorObjs[c] = objc.Retain(objc.Send(clsCursor, objc.Sel(name)))
	}
}

// SetCursorRegions sets the cursor regions: where in the content the pointer
// shows which shape (later entries are on top). Everything outside them shows
// the arrow. Main thread. It copies into storage the window owns and does
// nothing when the list equals the previous one, so it can be called every
// frame without allocating.
//
// AppKit's cursor rects are the only mechanism, one rect per region, as
// NSSplitView does for its dividers: AppKit applies them as the pointer moves
// (so a divider's cursor needs no mouse event from us), restores the arrow
// outside them, and cooperates with the titlebar and other views. A change
// rebuilds the rects at once (discard + reset + invalidate), not lazily: a
// lazily rebuilt stale rect reset the cursor on the next mouse move. [NSCursor
// set] alone is not used: AppKit ignores a repeated set of the instance it
// believes is current, which left the arrow unrestorable. See addCursorRects
// for how overlapping regions are resolved.
func (w *Window) SetCursorRegions(r []CursorRegion) {
	if w.curs.setRegions(r) && w.curs.override == CursorArrow {
		w.rebuildCursorRects()
	}
}

// SetCursorOverride shows c over the whole content while it is not
// CursorArrow, whatever the regions say (a drag keeps its cursor wherever the
// pointer goes). CursorArrow removes the override. Main thread. A change
// rebuilds the cursor rects; while an override is set, changes of the regions
// do not.
func (w *Window) SetCursorOverride(c Cursor) {
	if w.curs.setOverride(c) {
		w.rebuildCursorRects()
	}
}

func (w *Window) rebuildCursorRects() {
	objc.Send(w.view, selDiscardCursorRects)
	objc.Send(w.view, selResetCursorRects)
	objc.Send1(w.win, selInvalidateCursors, uintptr(w.view)) // AppKit re-evaluates the rects at the pointer now
}

// addCursorRects is the view's resetCursorRects: the override as one rect over
// the bounds, else the regions.
func (w *Window) addCursorRects() {
	add := func(x, y, wd, h float64, c Cursor) {
		objc.F.Reset().Arg(uintptr(w.view)).Arg(uintptr(selAddCursorRect)).ArgRect(x, y, wd, h).
			Arg(uintptr(cursorObjs[c])).Call(objc.MsgSend)
	}
	b := objc.SendRect(w.view, selBounds)
	if o := w.curs.override; o != CursorArrow {
		add(b.X, b.Y, b.W, b.H, o)
		return
	}
	s := float64(w.scale)
	// AppKit does not specify which of two overlapping cursor rects of a view
	// wins, so it gets none: the regions are cut down so that each point
	// belongs to the topmost one only.
	for _, r := range w.curs.flatten() {
		// The view is not flipped: y runs up from the bottom edge.
		add(b.X+float64(r.Rect[0])/s, b.Y+b.H-float64(r.Rect[3])/s,
			float64(r.Rect[2]-r.Rect[0])/s, float64(r.Rect[3]-r.Rect[1])/s, r.Cursor)
	}
}

// SetDragRegions sets the window drag map (see DragRegion): a left press in a
// drag area moves the window (and a double click does what the system's "double-click a window's title bar"
// setting says) instead of reaching the app. Main thread; copies, and does
// nothing when unchanged.
func (w *Window) SetDragRegions(r []DragRegion) { w.drags.set(r) }

// hitsView reports whether AppKit's hit test, asked of the theme frame (the
// content view's superview, which also holds the titlebar container and the
// window buttons) at a point in window coordinates, finds our view. A press on
// a window button or the resize border is somebody else's; only a press that
// would reach our view is a window drag.
func (w *Window) hitsView(x, y float64) bool {
	content := w.content
	if content == 0 {
		content = w.view
	}
	frame := objc.Send(content, selSuperview)
	if frame == 0 {
		return true
	}
	hit := objc.ID(objc.F.Reset().Arg(uintptr(frame)).Arg(uintptr(selHitTest)).ArgPoint(x, y).Call(objc.MsgSend).R0())
	return hit == w.view
}

// titlebarDoubleClick is what a double click on a drag area does: the system
// setting AppleActionOnDoubleClick ("Maximize" and "Fill" zoom, "Minimize"
// miniaturizes, "None" nothing); zoom when it is not set.
func (w *Window) titlebarDoubleClick() {
	switch defaultsString("AppleActionOnDoubleClick") {
	case "Minimize":
		objc.Send1(w.win, selMiniaturize, 0)
	case "None":
	default:
		objc.Send1(w.win, selZoom, 0)
	}
}

// defaultsString reads a string from the global defaults domain ("" if unset).
func defaultsString(key string) string {
	d := objc.Send(objc.Cls("NSUserDefaults"), objc.Sel("standardUserDefaults"))
	str := objc.Send1(d, objc.Sel("stringForKey:"), uintptr(objc.Str(key)))
	if str == 0 {
		return ""
	}
	return string(objc.AppendUTF8(nil, str))
}
