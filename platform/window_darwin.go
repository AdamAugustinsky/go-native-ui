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
	selSetFrameOrigin       = objc.Sel("setFrameOrigin:")
	selStyleMask            = objc.Sel("styleMask")
	selSetTitlebarClear     = objc.Sel("setTitlebarAppearsTransparent:")
	selSetTitleVisible      = objc.Sel("setTitleVisibility:")
	selSetContentMinSize    = objc.Sel("setContentMinSize:")
)

// Open creates and shows a window; on macOS any number can be open
// (MaxWindows), each with its own event queue, display link and frame
// handler, served by one pump (Pump, Wait). Main thread only.
func Open(o Options) (*Window, error) {
	if err := checkPanel(&o); err != nil {
		return nil, err
	}
	if reg.n >= MaxWindows {
		return nil, errTooManyWindows
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
	w.wc = &windowKind // GNUIWindow: NSWindow with the host focus hand-off (host_darwin.go)
	if o.Panel != nil {
		p := *o.Panel
		w.panel, w.wc, style = &p, &panelKind, panelStyle // GNUIPanel (panel_darwin.go)
		o.Titlebar = 0
	}
	w.win = objc.Send(w.wc.cls, selAlloc)
	w.win = objc.ID(F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selInitWindow)).
		ArgRect(0, 0, width, height).Arg(style).Arg(2 /* buffered */).ArgBool(false).Call(objc.MsgSend).R0())
	if w.win == 0 {
		return nil, errors.New("platform: NSWindow init failed")
	}
	objc.Send1(w.win, selSetReleased, 0)
	mark(w.wc.mark, w.win, &live.windows)
	w.publicOnly = o.PublicAPIOnly
	if w.panel != nil {
		w.configurePanel()
	}
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
	mark(live.viewMark, w.view, &live.views)
	mark(live.delegateMark, delegate, &live.delegates)
	w.content = w.view
	w.openShell(&o, delegate, width, height)
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
	if w.panel == nil {
		w.cascade()
	}
	if os.Getenv("GNUI_NOANIM") != "" {
		objc.Send1(w.win, selSetAnimBehavior, 2) // NSWindowAnimationBehaviorNone
	}

	// Pointer enter/exit tracking for the whole view.
	ta := objc.Send(objc.Cls("NSTrackingArea"), selAlloc)
	ta = objc.ID(F.Reset().Arg(uintptr(ta)).Arg(uintptr(selInitTracking)).ArgRect(0, 0, 0, 0).
		Arg(0x01 | 0x20 | 0x200 /* EnteredAndExited | ActiveInKeyWindow | InVisibleRect */).
		Arg(uintptr(w.view)).Arg(0).Call(objc.MsgSend).R0())
	objc.Send1(w.view, selAddTracking, uintptr(ta))
	objc.Send(ta, selReleaseObj) // the view keeps it

	w.wakeEv, w.distantPast, w.distantFuture = appObj.wakeEv, appObj.distantPast, appObj.distantFuture
	if !appObj.tabbing {
		objc.Send1(w.win, selSetTabbingMode, 2) // NSWindowTabbingModeDisallowed (AppOptions.WindowTabbing)
	}

	if err := register(w); err != nil {
		w.closeLights()
		objc.Send(w.win, selClose)
		w.closeShell()
		w.buryObjects()
		return nil, err
	}
	markOpen("view+setup")
	if !o.Hidden {
		w.Show()
		markOpen("orderFront+activate")
	}

	// Display link (macOS 14+), created once the view is on a screen; paused
	// until RequestFrame.
	if w.link = w.makeViewLink(); w.link == 0 {
		unregister(w)
		w.closeLights()
		objc.Send(w.win, selClose)
		w.closeShell()
		w.buryObjects()
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
// Options.Hidden is set. The window becomes key only if the app is active:
// opened in the background (open -g, or a launch that may not activate
// itself) it is not, and an EvFocus says so; it becomes key, with its own
// EvFocus, once the app is activated.
//
// A panel (Options.Panel) shows on the active Space; a NonActivating one is
// ordered front without activating the app or becoming key, so the window
// the user types in keeps the keyboard, and any other panel activates the
// app and becomes key, as a palette does.
func (w *Window) Show() {
	if w.panel != nil {
		w.showPanel()
		return
	}
	objc.Send1(w.win, selMakeKeyFront, 0)
	// A process started from a background shell may not activate itself
	// (macOS 14 cooperative activation); its window would open occluded.
	objc.Send(w.win, selOrderFrontRegardless)
	objc.Send(w.app, selActivate)
	if !objc.SendBool(w.win, selIsKeyWindow) {
		w.focus(false) // windowDidBecomeKey: reported it otherwise
	}
	if w.link != 0 {
		w.setRateRange(w.link) // the screen is known for certain once shown
	}
	w.layoutChrome()
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
	return Native{NSWindow: uintptr(w.win), NSView: uintptr(w.view), Transparent: w.HasMaterials() || w.panel != nil, w: w}
}

// Close closes the window. A second Close does nothing.
func (w *Window) Close() {
	if w.buried {
		return
	}
	pool := objc.F.Call0(objc.PoolPush) // the caller may hold none: what closing autoreleases must not leak
	defer objc.F.Call1(objc.PoolPop, pool)
	forgetAssumedKey(w)
	forgetPaste(w)              // a read of the pasteboard for this window is not delivered (paste_darwin.go)
	w.SetSecureTextInput(false) // its secure field is gone
	w.closeAux()                // sheets first: a window closed under a sheet never calls its completion
	w.closeDrag()
	w.detachPanel()
	objc.Send(w.link, selInvalidate)
	objc.Send(w.link, selReleaseObj) // Open retained the view link; attachMetalLink owns its link
	w.link = 0
	w.cancelTrim() // its request holds the view
	if w.drawable != 0 {
		objc.Send(w.drawable, selReleaseObj)
		w.drawable = 0
	}
	w.closeLights()
	rememberClosed(int(objc.Send(w.win, selWindowNumber))) // events still queued for it reach no other window (eventWindow)
	objc.Send(w.win, selClose)
	w.closeShell() // after close: nothing on screen changes
	unregister(w)
	forgetWindow(w)
	adoptMenuBar() // its bar, if it was the installed one, goes to the next window
	w.buryObjects()
	if secure.want && reg.n == 0 {
		SetSecureInput(false) // the field that wanted it is gone; activation must not bring it back
	}
}

// buryObjects detaches the closed window's delegate and hands Open's
// references to its view, delegate and NSWindow to the next Pump (reap).
func (w *Window) buryObjects() {
	if w.win == 0 || w.buried {
		return
	}
	w.buried = true
	objc.Send1(w.win, selSetDelegate, 0)
	bury(w.view)
	bury(w.delegate)
	bury(w.win)
}

// forgetWindow drops the app-wide references to a closed window: the menu
// bar's owner, the colour panel's target, the event being sent.
func forgetWindow(w *Window) {
	if nativeMenus.owner == w {
		nativeMenus.owner, nativeMenus.state = nil, nil // the closed window's state is not asked any more
	}
	if nativeMenus.popup == w {
		nativeMenus.popup = nil
	}
	if colorPanel.owner == w {
		colorPanel.owner = nil
	}
	if sending == w {
		sending = nil
	}
}

// Focused reports whether the window has the keyboard focus (it is the key
// window); for a window not shown yet, whether it will have it when shown:
// whether the app is active. The app starts its input state from it, before
// the first frame, and EvFocus follows every change.
func (w *Window) Focused() bool {
	if keyAssumed(w) {
		return true
	}
	if objc.SendBool(w.win, selIsVisible) {
		return objc.SendBool(w.win, selIsKeyWindow)
	}
	return objc.SendBool(w.app, selIsActiveApp)
}
