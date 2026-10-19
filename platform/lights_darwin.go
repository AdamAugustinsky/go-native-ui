//go:build darwin

package platform

import (
	"math"
	"sync"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The window buttons (close, minimise, zoom: the "traffic lights") of a window
// with a custom titlebar (Options.Titlebar, Options.TrafficLights).
//
// AppKit lays the titlebar out again whenever it likes: on a title change, key
// and main changes, appearance changes, full screen, hover tracking and more.
// Moving the buttons from outside, as Electron's trafficLightPosition does
// (setting the titlebar container's frame and the buttons' origins after open
// and on resize), lasts only until that next layout. AppKit puts the container
// back to its own height (32 pt on macOS 27) and the buttons either back to
// their default place or, keeping their offset from the container's bottom, to
// a centre about 8 pt from the window's top, cut off by the corner. The hover
// area of the button group (a tracking area of the theme frame) is computed
// from AppKit's own model of where the buttons are, so the glyphs (×, −, +)
// appeared over the default place and not over the moved buttons. Re-applying
// the frames after every layout (NSViewFrameDidChangeNotification) was tried:
// the positions can be held, but the hover area still follows AppKit's model,
// and fixing that takes overriding a private method (_mouseInGroup:, as
// Electron, Godot and iTerm2 do).
//
// So AppKit places the buttons itself. NSWindow keeps a titlebar height and a
// button-group offset of its own (setTitlebarHeight: and
// setStandardWindowButtonGroupOffset:, private), which every layout pass of
// the titlebar honours. The buttons, the group's hover area, the zoom button's
// tiling menu, key and appearance changes all follow from AppKit's own
// layout, so nothing undoes them. The offset is calibrated against where
// AppKit actually puts the close button, so its centre lands on
// Options.TrafficLights whatever AppKit's default inset is.
//
// Two things AppKit does not restore: full screen drops the custom height when
// it starts (customTitlebarHeightPriorToFSMode keeps it), and a failed entry
// does not put it back. So a GNUITitlebarObserver watches the container, the
// titlebar view and the three buttons (NSViewFrameDidChangeNotification,
// public) and runs layoutChrome after each change AppKit makes: it sets the
// height and offset again if AppKit dropped them, and keeps SystemButtons
// equal to where the buttons really are. In full screen the buttons are
// AppKit's, in its own titlebar: layoutChrome leaves them alone from
// windowWillEnterFullScreen: until the exit or the failed entry
// (shell_darwin.go), and runs then and on the next run-loop turn.
//
// The selectors are checked (respondsToSelector:) once per window. Where any
// is missing, or AppKit does not put the close button where asked, the
// buttons stay where AppKit puts them by default; SystemButtons still says
// where that is, so the app keeps its content clear of them.
//
// Measured and rejected: the NSToolbar styles give the buttons fixed centres,
// (26, 26) unified and (19, 20) unifiedCompact on macOS 27, not a configurable
// one, and a toolbar's title area takes the presses meant for tabs drawn in
// the band (docs/design/native-shell-spike.md); a titlebar accessory leaves
// the buttons at (16, 16).

// titlebarChrome is the state of the window buttons' placement.
type titlebarChrome struct {
	setUp      bool
	closed     bool       // closeLights ran: the window is going
	native     bool       // AppKit places the buttons at lights (else they stay at its default)
	busy       bool       // layoutChrome is running: the frame changes it causes come back as notifications
	inNote     bool       // layoutChrome runs from a frame-change notification, inside AppKit's layout
	calibrated bool       // offset is known
	offset     [2]float64 // the button-group offset that puts the close button's centre on lights
	watched    [5]objc.ID // container, titlebar view and the three buttons, observed for frame changes
	observer   objc.ID
}

var (
	selSetTitlebarHeight    = objc.Sel("setTitlebarHeight:")                  // NSWindow, private
	selTitlebarHeight       = objc.Sel("titlebarHeight")                      // NSWindow, private
	selSetButtonGroupOffset = objc.Sel("setStandardWindowButtonGroupOffset:") // NSWindow, private; an NSSize whose height points up
	selButtonGroupOffset    = objc.Sel("standardWindowButtonGroupOffset")     // NSWindow, private
	selLayoutSubtree        = objc.Sel("layoutSubtreeIfNeeded")
	selUpdateTrackingAreas  = objc.Sel("updateTrackingAreas")
	selTitlebarFrameChanged = objc.Sel("gnuiTitlebarFrameChanged:")
	selLayoutChromeLater    = objc.Sel("gnuiLayoutChrome:")
	selPerformAfterDelay    = objc.Sel("performSelector:withObject:afterDelay:")
	selCancelPerform        = objc.Sel("cancelPreviousPerformRequestsWithTarget:")
	selPostsFrameChanged    = objc.Sel("postsFrameChangedNotifications")
	selSetPostsFrameChanged = objc.Sel("setPostsFrameChangedNotifications:")
	selInit                 = objc.Sel("init")

	lightsOnce                           sync.Once
	lightsObserverClass, lightsNSObject  objc.ID
	lightsCenter, lightsFrameChangedName objc.ID
	lightsForceDefault                   bool // tests: new windows behave as where the selectors are missing
)

func lightsSetup() {
	appKit, _ := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	lightsFrameChangedName = objc.Retain(stringConstant(appKit, "NSViewFrameDidChangeNotification"))
	lightsCenter = objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
	lightsNSObject = objc.Cls("NSObject")
	def := objc.Define("GNUITitlebarObserver", lightsNSObject, nil)
	def.Method(selTitlebarFrameChanged, "v@:@", func(*ffi.CB) {
		if w := theWindow.Load(); w != nil && !w.tb.busy {
			w.tb.inNote = true
			w.layoutChrome()
			w.tb.inNote = false
		}
	})
	def.Method(selLayoutChromeLater, "v@:@", func(*ffi.CB) {
		if w := theWindow.Load(); w != nil {
			w.layoutChrome()
		}
	})
	lightsObserverClass = def.Register()
}

// SystemButtons is where the OS draws the window buttons (close, minimise,
// zoom) over the content: the union of the three buttons' frames in the
// content's coordinates, in logical units from its top left. It is the
// rectangle the app keeps its own content out of (ui.Context.SetSystemButtons).
// It is empty when the window has no custom titlebar (Options.Titlebar 0: the
// system's title bar sits above the content and holds the buttons) and in full
// screen, where the buttons are not over the content. It follows every change
// AppKit makes to the buttons (lights_darwin.go). Main thread only.
func (w *Window) SystemButtons() [4]float32 { return w.sysButtons }

// layoutChrome has AppKit put the window buttons' centres where
// Options.TrafficLights says (see the top of this file) and records where
// they are (SystemButtons). It runs at open and show, on resize, after every
// frame change AppKit makes to the titlebar, and after full screen. Main
// thread; it allocates nothing once set up.
func (w *Window) layoutChrome() {
	if w.titlebar == 0 {
		w.sysButtons = [4]float32{}
		return
	}
	if w.tb.busy || w.tb.closed {
		return
	}
	w.tb.busy = true
	w.placeButtons()
	w.tb.busy = false
}

func (w *Window) placeButtons() {
	if !w.tb.setUp {
		w.setUpLights()
	}
	var btn [3]objc.ID
	for i := range btn {
		if btn[i] = objc.Send1(w.win, selStdButton, uintptr(i)); btn[i] == 0 {
			w.sysButtons = [4]float32{}
			return
		}
	}
	w.watchTitlebar(btn)
	if w.fullScreen || objc.Send(w.win, selStyleMask)&(1<<14) != 0 { // NSWindowStyleMaskFullScreen: AppKit's buttons, in its own titlebar
		w.sysButtons = [4]float32{}
		return
	}
	if w.tb.native {
		w.placeNative(btn)
	}
	w.measureButtons(btn)
}

// setUpLights checks the selectors and creates the observer (once per
// window).
func (w *Window) setUpLights() {
	lightsOnce.Do(lightsSetup)
	w.tb.setUp = true
	w.tb.native = !lightsForceDefault &&
		objc.SendBool1(w.win, selRespondsTo, uintptr(selSetTitlebarHeight)) &&
		objc.SendBool1(w.win, selRespondsTo, uintptr(selTitlebarHeight)) &&
		objc.SendBool1(w.win, selRespondsTo, uintptr(selSetButtonGroupOffset)) &&
		objc.SendBool1(w.win, selRespondsTo, uintptr(selButtonGroupOffset))
	w.tb.observer = objc.Send(objc.Send(lightsObserverClass, selAlloc), selInit)
}

// placeNative sets AppKit's titlebar height and button-group offset where
// they differ from ours. From a frame-change notification (inside AppKit's
// layout) that is all: AppKit's layout places the buttons and posts their
// frame changes, which measure them. Otherwise, when it changed them or has
// not calibrated yet, it lays the titlebar out and checks the close button:
// if its centre is off it corrects the offset by the difference (AppKit's
// default inset is not ours to know), and if that does not land either it
// gives up and leaves AppKit's default.
func (w *Window) placeNative(btn [3]objc.ID) {
	changed := false
	if h := objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selTitlebarHeight)).Call(objc.MsgSend).F64(0); math.Abs(h-w.titlebar) > 0.01 {
		objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetTitlebarHeight)).ArgF64(w.titlebar).Call(objc.MsgSend)
		changed = true
	}
	if ox, oy := objc.SendPoint(w.win, selButtonGroupOffset); ox != w.tb.offset[0] || oy != w.tb.offset[1] {
		w.setGroupOffset()
		changed = true
	}
	if w.tb.inNote || (!changed && w.tb.calibrated) {
		return
	}
	frame := objc.Send(objc.Send(objc.Send(btn[0], selSuperview), selSuperview), selSuperview) // the theme frame
	for try := 0; ; try++ {
		objc.Send(frame, selLayoutSubtree)
		cx, cy := w.buttonCentre(btn[0])
		dx, dy := float64(w.lights[0])-cx, float64(w.lights[1])-cy
		if math.Abs(dx) <= 0.25 && math.Abs(dy) <= 0.25 {
			w.tb.calibrated = true
			break
		}
		if try == 2 { // AppKit does not put them where asked: leave its default
			w.tb.native = false
			w.tb.offset = [2]float64{}
			w.setGroupOffset()
			objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetTitlebarHeight)).ArgF64(-1).Call(objc.MsgSend)
			objc.Send(frame, selLayoutSubtree)
			break
		}
		w.tb.offset[0] += dx
		w.tb.offset[1] -= dy
		w.setGroupOffset()
	}
	// The group's hover area follows a new offset only at the theme frame's
	// next tracking update.
	objc.Send(frame, selUpdateTrackingAreas)
}

func (w *Window) setGroupOffset() {
	objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetButtonGroupOffset)).ArgPoint(w.tb.offset[0], w.tb.offset[1]).Call(objc.MsgSend)
}

// watchTitlebar observes frame changes of the container, the titlebar view
// and the buttons, again whenever AppKit has replaced one of them.
func (w *Window) watchTitlebar(btn [3]objc.ID) {
	tv := objc.Send(btn[0], selSuperview)
	now := [5]objc.ID{objc.Send(tv, selSuperview), tv, btn[0], btn[1], btn[2]}
	if now == w.tb.watched {
		return
	}
	w.unwatchTitlebar()
	for _, v := range now {
		if v == 0 {
			continue
		}
		if !objc.SendBool(v, selPostsFrameChanged) {
			objc.Send1(v, selSetPostsFrameChanged, 1)
		}
		objc.AddObserver(lightsCenter, w.tb.observer, selTitlebarFrameChanged, lightsFrameChangedName, v)
	}
	w.tb.watched = now
}

func (w *Window) unwatchTitlebar() {
	for i, v := range w.tb.watched {
		if v != 0 {
			objc.RemoveObserver(lightsCenter, w.tb.observer, lightsFrameChangedName, v)
		}
		w.tb.watched[i] = 0
	}
}

// layoutChromeSoon runs layoutChrome now and again on the run loop's next
// turn, after AppKit has finished what it was doing (the end of full screen).
func (w *Window) layoutChromeSoon() {
	w.layoutChrome()
	if w.tb.observer != 0 {
		objc.F.Reset().Arg(uintptr(w.tb.observer)).Arg(uintptr(selPerformAfterDelay)).Arg(uintptr(selLayoutChromeLater)).Arg(0).ArgF64(0).Call(objc.MsgSend)
	}
}

// closeLights stops observing and frees the observer (Close).
func (w *Window) closeLights() {
	w.tb.closed = true
	if w.tb.observer == 0 {
		return
	}
	w.unwatchTitlebar()
	objc.Send1(lightsNSObject, selCancelPerform, uintptr(w.tb.observer))
	objc.Release(w.tb.observer)
	w.tb.observer = 0
}

// buttonCentre is a button's centre in window coordinates from the top left.
func (w *Window) buttonCentre(b objc.ID) (x, y float64) {
	bounds := objc.SendRect(b, selBounds)
	objc.F.Reset().Arg(uintptr(b)).Arg(uintptr(selConvertPointToView)).ArgPoint(bounds.W/2, bounds.H/2).Arg(0).Call(objc.MsgSend)
	x, y = objc.F.F64(0), objc.F.F64(1)
	return x, objc.SendRect(w.win, selFrame).H - y
}

// measureButtons records the union of the buttons' frames (SystemButtons).
func (w *Window) measureButtons(btn [3]objc.ID) {
	// In window coordinates (origin bottom left), then turned to the
	// content's (origin top left; the content fills the window).
	u := [4]float64{math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64, -math.MaxFloat64}
	for _, b := range btn {
		bounds := objc.SendRect(b, selBounds)
		// both corners: a flipped view's origin is its top left
		for _, p := range [2][2]float64{{0, 0}, {bounds.W, bounds.H}} {
			objc.F.Reset().Arg(uintptr(b)).Arg(uintptr(selConvertPointToView)).ArgPoint(p[0], p[1]).Arg(0).Call(objc.MsgSend)
			x, y := objc.F.F64(0), objc.F.F64(1)
			u[0], u[1] = math.Min(u[0], x), math.Min(u[1], y)
			u[2], u[3] = math.Max(u[2], x), math.Max(u[3], y)
		}
	}
	wf := objc.SendRect(w.win, selFrame)
	w.sysButtons = [4]float32{float32(u[0]), float32(wf.H - u[3]), float32(u[2]), float32(wf.H - u[1])}
}
