//go:build darwin

package platform

import (
	"errors"
	"math"
	"os"

	"gonativeui/internal/objc"
)

// Creating the window and its chrome: Open, Show, the custom titlebar and the
// window buttons.

var (
	selSetActivation        = objc.Sel("setActivationPolicy:")
	selFinishLaunching      = objc.Sel("finishLaunching")
	selActivate             = objc.Sel("activate")
	selInitWindow           = objc.Sel("initWithContentRect:styleMask:backing:defer:")
	selInitWithFrame        = objc.Sel("initWithFrame:")
	selSetContentView       = objc.Sel("setContentView:")
	selMakeFirstResp        = objc.Sel("makeFirstResponder:")
	selSetMouseMoved        = objc.Sel("setAcceptsMouseMovedEvents:")
	selSetReleased          = objc.Sel("setReleasedWhenClosed:")
	selSetTitle             = objc.Sel("setTitle:")
	selCenter               = objc.Sel("center")
	selSetAnimBehavior      = objc.Sel("setAnimationBehavior:")
	selMakeKeyFront         = objc.Sel("makeKeyAndOrderFront:")
	selOrderFrontRegardless = objc.Sel("orderFrontRegardless")
	selClose                = objc.Sel("close")
	selBackingScale         = objc.Sel("backingScaleFactor")
	selDistantPast          = objc.Sel("distantPast")
	selDistantFuture        = objc.Sel("distantFuture")
	selOtherEvent           = objc.Sel("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:")
	selInitTracking         = objc.Sel("initWithRect:options:owner:userInfo:")
	selAddTracking          = objc.Sel("addTrackingArea:")
	selStdButton            = objc.Sel("standardWindowButton:")
	selFrame                = objc.Sel("frame")
	selSetFrame             = objc.Sel("setFrame:")
	selSetFrameOrigin       = objc.Sel("setFrameOrigin:")
	selStyleMask            = objc.Sel("styleMask")
	selSetTitlebarClear     = objc.Sel("setTitlebarAppearsTransparent:")
	selSetTitleVisible      = objc.Sel("setTitleVisibility:")
	selSetContentMinSize    = objc.Sel("setContentMinSize:")
)

// Open creates and shows the window. Main thread only.
func Open(o Options) (*Window, error) {
	if theWindow.Load() != nil {
		return nil, errors.New("platform: only one window is supported in phase 1")
	}
	nOpenStages = 0
	markOpen("start")
	if err := ensureApp(); err != nil { // frameworks, classes, NSApp and its delegate (appobj_darwin.go)
		return nil, err
	}
	markOpen("app")

	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)

	w := &Window{q: newEventQueue(), scale: 1, linkPaused: true, visible: -1, marked: make([]byte, 0, 256), tmp: make([]byte, 0, 256)}
	w.app = appObj.app
	loadCursors()
	markOpen("NSApplication")

	width, height := float64(o.Width), float64(o.Height)
	if width <= 0 || height <= 0 {
		width, height = 960, 600
	}
	style := uintptr(1 | 2 | 4 | 8) // titled | closable | miniaturizable | resizable
	if o.Titlebar > 0 {
		style |= 1 << 15 // NSWindowStyleMaskFullSizeContentView
	}
	w.win = objc.Send(objc.Cls("NSWindow"), selAlloc)
	w.win = objc.ID(F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selInitWindow)).
		ArgRect(0, 0, width, height).Arg(style).Arg(2 /* buffered */).ArgBool(false).Call(objc.MsgSend).R0())
	if w.win == 0 {
		return nil, errors.New("platform: NSWindow init failed")
	}
	objc.Send1(w.win, selSetReleased, 0)
	if o.Titlebar > 0 {
		w.titlebar, w.lights = float64(o.Titlebar), o.TrafficLights
		objc.Send1(w.win, selSetTitlebarClear, 1)
		objc.Send1(w.win, selSetTitleVisible, 1) // NSWindowTitleHidden
		w.layoutChrome()
	}
	markOpen("NSWindow")
	w.view = objc.Send(viewClass, selAlloc)
	w.view = objc.ID(F.Reset().Arg(uintptr(w.view)).Arg(uintptr(selInitWithFrame)).
		ArgRect(0, 0, width, height).Call(objc.MsgSend).R0())
	delegate := objc.Send(objc.Send(delegateClass, selAlloc), objc.Sel("init"))
	w.content = w.view
	if o.Backdrop != BackdropNone {
		w.makeBackdrop(o.Backdrop, width, height)
		markOpen("backdrop")
	}
	if len(o.Buttons) > 0 {
		w.makeButtons(o.Buttons, delegate, width, height)
		markOpen("buttons")
	}
	objc.Send1(w.win, selSetContentView, uintptr(w.content))
	objc.Send1(w.win, selMakeFirstResp, uintptr(w.view))
	objc.Send1(w.win, selSetDelegate, uintptr(delegate))
	w.fitBackdrop()
	objc.Send1(w.win, selSetMouseMoved, 1)
	if o.MinWidth > 0 || o.MinHeight > 0 {
		F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetContentMinSize)).ArgPoint(float64(o.MinWidth), float64(o.MinHeight)).Call(objc.MsgSend)
	}
	objc.Send1(w.win, selSetTitle, uintptr(objc.Str(o.Title)))
	objc.Send(w.win, selCenter)
	if os.Getenv("GNUI_NOANIM") != "" {
		objc.Send1(w.win, selSetAnimBehavior, 2) // NSWindowAnimationBehaviorNone
	}

	// Pointer enter/exit tracking for the whole view.
	ta := objc.Send(objc.Cls("NSTrackingArea"), selAlloc)
	ta = objc.ID(F.Reset().Arg(uintptr(ta)).Arg(uintptr(selInitTracking)).ArgRect(0, 0, 0, 0).
		Arg(0x01 | 0x20 | 0x200 /* EnteredAndExited | ActiveInKeyWindow | InVisibleRect */).
		Arg(uintptr(w.view)).Arg(0).Call(objc.MsgSend).R0())
	objc.Send1(w.view, selAddTracking, uintptr(ta))

	w.wakeEv, w.distantPast, w.distantFuture = appObj.wakeEv, appObj.distantPast, appObj.distantFuture

	theWindow.Store(w)
	markOpen("view+setup")
	if !o.Hidden {
		w.Show()
		markOpen("orderFront+activate")
	}

	// Display link (macOS 14+), created once the view is on a screen; paused
	// until RequestFrame.
	if w.link = w.makeViewLink(); w.link == 0 {
		objc.Send(w.win, selClose)
		w.closeShell()
		theWindow.Store(nil)
		return nil, errors.New("platform: displayLinkWithTarget:selector: failed (macOS 14+ required)")
	}
	w.updateSize()
	markOpen("displayLink")
	if wakePending.Load() { // a Wake before Open found no window to post to
		osWake()
	}
	return w, nil
}

// Show orders the window front and activates the app. Open calls it unless
// Options.Hidden is set.
func (w *Window) Show() {
	objc.Send1(w.win, selMakeKeyFront, 0)
	// A process started from a background shell may not activate itself
	// (macOS 14 cooperative activation); its window would open occluded.
	objc.Send(w.win, selOrderFrontRegardless)
	objc.Send(w.app, selActivate)
	if w.link != 0 {
		w.setRateRange(w.link) // the screen is known for certain once shown
	}
	w.layoutChrome()
}

// SystemButtons is where the OS draws the window buttons (close, minimise,
// zoom) over the content: the union of the three buttons' frames in the
// content's coordinates, in logical units from its top left. It is the
// rectangle the app keeps its own content out of (ui.Context.SetSystemButtons).
// It is empty when the window has no custom titlebar (Options.Titlebar 0: the
// system's title bar sits above the content and holds the buttons) and in full
// screen, where the buttons are not over the content. It is updated whenever
// the window lays out its chrome: when it opens and is shown, and on resize,
// full screen and appearance changes. Main thread only.
func (w *Window) SystemButtons() [4]float32 { return w.sysButtons }

// layoutChrome sizes the titlebar container to the custom titlebar height
// and moves the window buttons (the approach Electron's trafficLightPosition
// uses). The container's superview, the theme frame, is not flipped. It then
// records where the buttons are (SystemButtons).
func (w *Window) layoutChrome() {
	w.sysButtons = [4]float32{}
	if w.titlebar == 0 {
		return
	}
	var btn [3]objc.ID
	for i := range btn {
		if btn[i] = objc.Send1(w.win, selStdButton, uintptr(i)); btn[i] == 0 {
			return
		}
	}
	container := objc.Send(objc.Send(btn[0], selSuperview), selSuperview)
	wf, cf := objc.SendRect(w.win, selFrame), objc.SendRect(container, selFrame)
	objc.F.Reset().Arg(uintptr(container)).Arg(uintptr(selSetFrame)).ArgRect(cf.X, wf.H-w.titlebar, cf.W, w.titlebar).Call(objc.MsgSend)
	f0, f1 := objc.SendRect(btn[0], selFrame), objc.SendRect(btn[1], selFrame)
	space := f1.X - f0.X
	x0 := float64(w.lights[0]) - f0.W/2
	y := w.titlebar - float64(w.lights[1]) - f0.H/2
	for i, b := range btn {
		objc.F.Reset().Arg(uintptr(b)).Arg(uintptr(selSetFrameOrigin)).ArgPoint(x0+float64(i)*space, y).Call(objc.MsgSend)
	}
	if objc.Send(w.win, selStyleMask)&(1<<14) != 0 { // NSWindowStyleMaskFullScreen: the buttons are not over the content
		return
	}
	// The union of the buttons, in window coordinates (origin bottom left), then
	// turned to the content's (origin top left; the content fills the window).
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
	wf = objc.SendRect(w.win, selFrame)
	w.sysButtons = [4]float32{float32(u[0]), float32(wf.H - u[3]), float32(u[2]), float32(wf.H - u[1])}
}

func (w *Window) updateSize() bool {
	b := objc.SendRect(w.view, selBounds)
	s := float32(objc.SendF64(w.win, selBackingScale))
	if s <= 0 {
		s = 1
	}
	wPx, hPx := int32(math.Round(b.W*float64(s))), int32(math.Round(b.H*float64(s)))
	changed := wPx != w.wPx || hPx != w.hPx || s != w.scale
	w.wPt, w.hPt, w.wPx, w.hPx, w.scale = b.W, b.H, wPx, hPx, s
	return changed
}

// pushResize queues an EvResize if the size or scale changed and reports
// whether it did.
func (w *Window) pushResize() bool {
	if !w.updateSize() {
		return false
	}
	if n := len(w.q.ev); n > 0 && w.q.ev[n-1].Kind == EvResize {
		w.q.ev = w.q.ev[:n-1] // coalesce
	}
	w.q.push(Event{Kind: EvResize, W: w.wPx, H: w.hPx, Scale: w.scale, Time: Now()})
	return true
}

// Size returns the content size in physical pixels and the backing scale.
func (w *Window) Size() (int32, int32, float32) { return w.wPx, w.hPx, w.scale }

// Native returns the AppKit handles.
func (w *Window) Native() Native {
	return Native{NSWindow: uintptr(w.win), NSView: uintptr(w.view), Transparent: w.backdrop != BackdropNone}
}

// Close closes the window.
func (w *Window) Close() {
	objc.Send(w.link, selInvalidate)
	objc.Send(w.link, selReleaseObj) // Open retained the view link; attachMetalLink owns its link
	w.link = 0
	if w.drawable != 0 {
		objc.Send(w.drawable, selReleaseObj)
		w.drawable = 0
	}
	objc.Send(w.win, selClose)
	w.closeShell() // after close: nothing on screen changes
	theWindow.Store(nil)
}
