//go:build darwin

package platform

import (
	"errors"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Native holds the AppKit handles a GPU surface attaches to.
type Native struct{ NSWindow, NSView uintptr }

// Window is an AppKit window driven by our own event loop
// (nextEventMatchingMask + sendEvent; [NSApp run] is never called).
type Window struct {
	q        eventQueue
	returned bool // q was handed out by the previous Wait

	app, win, view, link, wakeEv objc.ID
	distantPast, distantFuture   objc.ID

	wPt, hPt   float64
	titlebar   float64    // custom titlebar height in points (Options.Titlebar); 0 = standard
	lights     [2]float32 // close button centre (Options.TrafficLights)
	wPx, hPx   int32
	scale      float32
	frameArmed bool
	linkPaused bool
	metalLink  bool    // w.link is a CAMetalDisplayLink (SetFrameSource)
	metalLayer objc.ID // layer to attach a CAMetalDisplayLink to on the next view-link tick
	drawable   objc.ID // retained drawable from the current CAMetalDisplayLink tick
	idleTicks  int     // display-link ticks since the last frame request
}

const wakeSubtype = 0x4E55 // our NSEventTypeApplicationDefined subtype

var (
	theWindow atomic.Pointer[Window]
	imp       uintptr // the one IMP shared by every method of our classes

	defaultMode, commonModes objc.ID

	selSharedApplication    = objc.Sel("sharedApplication")
	selSetActivation        = objc.Sel("setActivationPolicy:")
	selFinishLaunching      = objc.Sel("finishLaunching")
	selActivate             = objc.Sel("activate")
	selAlloc                = objc.Sel("alloc")
	selInitWindow           = objc.Sel("initWithContentRect:styleMask:backing:defer:")
	selInitWithFrame        = objc.Sel("initWithFrame:")
	selSetContentView       = objc.Sel("setContentView:")
	selMakeFirstResp        = objc.Sel("makeFirstResponder:")
	selSetDelegate          = objc.Sel("setDelegate:")
	selSetMouseMoved        = objc.Sel("setAcceptsMouseMovedEvents:")
	selSetReleased          = objc.Sel("setReleasedWhenClosed:")
	selSetTitle             = objc.Sel("setTitle:")
	selCenter               = objc.Sel("center")
	selSetAnimBehavior      = objc.Sel("setAnimationBehavior:")
	selMakeKeyFront         = objc.Sel("makeKeyAndOrderFront:")
	selOrderFrontRegardless = objc.Sel("orderFrontRegardless")
	selClose                = objc.Sel("close")
	selBounds               = objc.Sel("bounds")
	selBackingScale         = objc.Sel("backingScaleFactor")
	selOcclusionState       = objc.Sel("occlusionState")
	selNextEvent            = objc.Sel("nextEventMatchingMask:untilDate:inMode:dequeue:")
	selSendEvent            = objc.Sel("sendEvent:")
	selPostEvent            = objc.Sel("postEvent:atStart:")
	selDistantPast          = objc.Sel("distantPast")
	selDistantFuture        = objc.Sel("distantFuture")
	selDateSinceNow         = objc.Sel("dateWithTimeIntervalSinceNow:")
	selOtherEvent           = objc.Sel("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:")
	selType                 = objc.Sel("type")
	selSubtype              = objc.Sel("subtype")
	selTimestamp            = objc.Sel("timestamp")
	selWindow               = objc.Sel("window")
	selLocation             = objc.Sel("locationInWindow")
	selButtonNumber         = objc.Sel("buttonNumber")
	selModifierFlags        = objc.Sel("modifierFlags")
	selKeyCode              = objc.Sel("keyCode")
	selIsARepeat            = objc.Sel("isARepeat")
	selScrollDX             = objc.Sel("scrollingDeltaX")
	selScrollDY             = objc.Sel("scrollingDeltaY")
	selPrecise              = objc.Sel("hasPreciseScrollingDeltas")
	selDisplayLinkWith      = objc.Sel("displayLinkWithTarget:selector:")
	selScreen               = objc.Sel("screen")
	selMaxFPS               = objc.Sel("maximumFramesPerSecond")
	selSetRateRange         = objc.Sel("setPreferredFrameRateRange:")
	selAddToRunLoop         = objc.Sel("addToRunLoop:forMode:")
	selMainRunLoop          = objc.Sel("mainRunLoop")
	selSetPaused            = objc.Sel("setPaused:")
	selInvalidate           = objc.Sel("invalidate")
	selInitTracking         = objc.Sel("initWithRect:options:owner:userInfo:")
	selAddTracking          = objc.Sel("addTrackingArea:")
	selArrayWithObject      = objc.Sel("arrayWithObject:")
	selArray                = objc.Sel("array")
	selInterpretKeys        = objc.Sel("interpretKeyEvents:")
	selIsKindOfClass        = objc.Sel("isKindOfClass:")
	selString               = objc.Sel("string")
	selRespondsTo           = objc.Sel("respondsToSelector:")

	// Methods implemented by our classes (dispatched in impHandler).
	selAcceptsFirstResp  = objc.Sel("acceptsFirstResponder")
	selKeyDown           = objc.Sel("keyDown:")
	selKeyUp             = objc.Sel("keyUp:")
	selFlagsChanged      = objc.Sel("flagsChanged:")
	selInsertText        = objc.Sel("insertText:replacementRange:")
	selDoCommand         = objc.Sel("doCommandBySelector:")
	selHasMarkedText     = objc.Sel("hasMarkedText")
	selMarkedRange       = objc.Sel("markedRange")
	selSelectedRange     = objc.Sel("selectedRange")
	selSetMarkedText     = objc.Sel("setMarkedText:selectedRange:replacementRange:")
	selUnmarkText        = objc.Sel("unmarkText")
	selValidAttrs        = objc.Sel("validAttributesForMarkedText")
	selAttrSubstring     = objc.Sel("attributedSubstringForProposedRange:actualRange:")
	selCharIndexForPoint = objc.Sel("characterIndexForPoint:")
	selFirstRect         = objc.Sel("firstRectForCharacterRange:actualRange:")
	selViewBacking       = objc.Sel("viewDidChangeBackingProperties")
	selDisplayLinkFired  = objc.Sel("gnuiDisplayLink:")
	selMetalLinkUpdate   = objc.Sel("metalDisplayLink:needsUpdate:")
	selInitWithLayer     = objc.Sel("initWithMetalLayer:")
	selSetFrameLatency   = objc.Sel("setPreferredFrameLatency:")
	selUpdateDrawable    = objc.Sel("drawable")
	selReleaseObj        = objc.Sel("release")
	selAutorelease       = objc.Sel("autorelease")
	selStdButton         = objc.Sel("standardWindowButton:")
	selSuperview         = objc.Sel("superview")
	selFrame             = objc.Sel("frame")
	selSetFrame          = objc.Sel("setFrame:")
	selSetFrameOrigin    = objc.Sel("setFrameOrigin:")
	selSetTitlebarClear  = objc.Sel("setTitlebarAppearsTransparent:")
	selSetTitleVisible   = objc.Sel("setTitleVisibility:")
	selShouldClose       = objc.Sel("windowShouldClose:")
	selDidResize         = objc.Sel("windowDidResize:")
	selDidChangeBacking  = objc.Sel("windowDidChangeBackingProperties:")
	selDidBecomeKey      = objc.Sel("windowDidBecomeKey:")
	selDidResignKey      = objc.Sel("windowDidResignKey:")
	selDidChangeOcclus   = objc.Sel("windowDidChangeOcclusionState:")

	clsNSArray, clsNSAttributedString objc.ID
	clsNSDate                         objc.ID // cached: objc.Cls allocates, and Wait runs every frame
	clsMetalLink                      objc.ID
)

// GNUI_DEBUG=1 traces every Objective-C method our classes receive (allocates).
var debugIMP = os.Getenv("GNUI_DEBUG") != ""

var classesOnce sync.Once
var viewClass, delegateClass objc.ID

func registerClasses() {
	imp = ffi.NewCallback(impHandler)
	viewClass = objc.NewClass("GNUIView", objc.Cls("NSView"), []string{"NSTextInputClient"}, imp, []objc.Method{
		{Sel: selAcceptsFirstResp, Types: "c@:"},
		{Sel: selKeyDown, Types: "v@:@"},
		{Sel: selKeyUp, Types: "v@:@"},
		{Sel: selFlagsChanged, Types: "v@:@"},
		{Sel: selInsertText, Types: "v@:@{_NSRange=QQ}"},
		{Sel: selDoCommand, Types: "v@::"},
		{Sel: selHasMarkedText, Types: "c@:"},
		{Sel: selMarkedRange, Types: "{_NSRange=QQ}@:"},
		{Sel: selSelectedRange, Types: "{_NSRange=QQ}@:"},
		{Sel: selSetMarkedText, Types: "v@:@{_NSRange=QQ}{_NSRange=QQ}"},
		{Sel: selUnmarkText, Types: "v@:"},
		{Sel: selValidAttrs, Types: "@@:"},
		{Sel: selAttrSubstring, Types: "@@:{_NSRange=QQ}^{_NSRange=QQ}"},
		{Sel: selCharIndexForPoint, Types: "Q@:{CGPoint=dd}"},
		// Returns a CGRect: through a hidden pointer on amd64, which shifts
		// self/_cmd by one argument, so it gets its own IMP.
		{Sel: selFirstRect, Types: "{CGRect={CGPoint=dd}{CGSize=dd}}@:{_NSRange=QQ}^{_NSRange=QQ}", IMP: ffi.NewCallback(firstRectIMP)},
		{Sel: selViewBacking, Types: "v@:"},
		{Sel: selDisplayLinkFired, Types: "v@:@"},
		{Sel: selMetalLinkUpdate, Types: "v@:@@"},
	})
	delegateClass = objc.NewClass("GNUIWindowDelegate", objc.Cls("NSObject"), []string{"NSWindowDelegate"}, imp, []objc.Method{
		{Sel: selShouldClose, Types: "c@:@"},
		{Sel: selDidResize, Types: "v@:@"},
		{Sel: selDidChangeBacking, Types: "v@:@"},
		{Sel: selDidBecomeKey, Types: "v@:@"},
		{Sel: selDidResignKey, Types: "v@:@"},
		{Sel: selDidChangeOcclus, Types: "v@:@"},
	})
	clsNSArray, clsNSAttributedString = objc.Cls("NSArray"), objc.Cls("NSAttributedString")
	clsMetalLink = objc.Cls("CAMetalDisplayLink") // macOS 14+
}

func loadFramework(path string) (uintptr, error) {
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_GLOBAL)
}

func stringConstant(lib uintptr, name string) objc.ID {
	p, err := purego.Dlsym(lib, name)
	if err != nil || p == 0 {
		return 0
	}
	return *(*objc.ID)(ffi.PtrFromC(p))
}

// Open creates and shows the window. Main thread only.
func Open(o Options) (*Window, error) {
	if theWindow.Load() != nil {
		return nil, errors.New("platform: only one window is supported in phase 1")
	}
	nOpenStages = 0
	markOpen("start")
	appKit, err := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	if err != nil {
		return nil, err
	}
	if _, err := loadFramework("/System/Library/Frameworks/QuartzCore.framework/QuartzCore"); err != nil {
		return nil, err
	}
	foundation, _ := loadFramework("/System/Library/Frameworks/Foundation.framework/Foundation")
	defaultMode = stringConstant(foundation, "NSDefaultRunLoopMode")
	commonModes = stringConstant(foundation, "NSRunLoopCommonModes")
	_ = appKit
	markOpen("frameworks")
	classesOnce.Do(registerClasses)
	markOpen("classes")

	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)

	w := &Window{q: newEventQueue(), scale: 1, linkPaused: true}
	w.app = objc.Send(objc.Cls("NSApplication"), selSharedApplication)
	objc.Send1(w.app, selSetActivation, 0) // NSApplicationActivationPolicyRegular
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
	objc.Send1(w.win, selSetContentView, uintptr(w.view))
	objc.Send1(w.win, selMakeFirstResp, uintptr(w.view))
	delegate := objc.Send(objc.Send(delegateClass, selAlloc), objc.Sel("init"))
	objc.Send1(w.win, selSetDelegate, uintptr(delegate))
	objc.Send1(w.win, selSetMouseMoved, 1)
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

	// One reusable application-defined event for wakeups.
	w.wakeEv = objc.Retain(objc.ID(F.Reset().Arg(uintptr(objc.Cls("NSEvent"))).Arg(uintptr(selOtherEvent)).
		Arg(15 /* NSEventTypeApplicationDefined */).ArgPoint(0, 0).Arg(0).ArgF64(0).Arg(0).Arg(0).
		Arg(wakeSubtype).Arg(0).Arg(0).Call(objc.MsgSend).R0()))
	clsNSDate = objc.Cls("NSDate")
	w.distantPast = objc.Retain(objc.Send(clsNSDate, selDistantPast))
	w.distantFuture = objc.Retain(objc.Send(clsNSDate, selDistantFuture))

	theWindow.Store(w)
	markOpen("view+setup")
	objc.Send(w.app, selFinishLaunching)
	markOpen("finishLaunching")
	if !o.Hidden {
		w.Show()
		markOpen("orderFront+activate")
	}

	// Display link (macOS 14+), created once the view is on a screen; paused
	// until RequestFrame.
	w.link = objc.Retain(objc.Send2(w.view, selDisplayLinkWith, uintptr(w.view), uintptr(selDisplayLinkFired)))
	if w.link == 0 {
		return nil, errors.New("platform: displayLinkWithTarget:selector: failed (macOS 14+ required)")
	}
	objc.Send2(w.link, selAddToRunLoop, uintptr(objc.Send(objc.Cls("NSRunLoop"), selMainRunLoop)), uintptr(commonModes))
	objc.Send1(w.link, selSetPaused, 1)
	w.setRateRange(w.link)
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

// layoutChrome sizes the titlebar container to the custom titlebar height
// and moves the window buttons (the approach Electron's trafficLightPosition
// uses). The container's superview, the theme frame, is not flipped.
func (w *Window) layoutChrome() {
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

func (w *Window) pushResize() {
	if !w.updateSize() {
		return
	}
	if n := len(w.q.ev); n > 0 && w.q.ev[n-1].Kind == EvResize {
		w.q.ev = w.q.ev[:n-1] // coalesce
	}
	w.q.push(Event{Kind: EvResize, W: w.wPx, H: w.hPx, Scale: w.scale, Time: Now()})
}

// Wait blocks until at least one event, a wake, a frame slot or the timeout.
func (w *Window) Wait(timeoutNs int64) []Event {
	if w.returned {
		w.q.reset()
		w.returned = false
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	until := w.distantFuture
	switch {
	case len(w.q.ev) > 0 || timeoutNs == 0:
		until = w.distantPast
	case timeoutNs > 0:
		until = objc.ID(F.Reset().Arg(uintptr(clsNSDate)).Arg(uintptr(selDateSinceNow)).
			ArgF64(float64(timeoutNs) / 1e9).Call(objc.MsgSend).R0())
	}
	for ev := w.nextEvent(until); ev != 0; ev = w.nextEvent(w.distantPast) {
		w.handle(ev)
	}
	F.Call1(objc.PoolPop, pool)
	w.returned = true
	return w.q.ev
}

func (w *Window) nextEvent(until objc.ID) objc.ID {
	return objc.Send4(w.app, selNextEvent, ^uintptr(0), uintptr(until), uintptr(defaultMode), 1)
}

func mods(flags uintptr) Mods {
	var m Mods
	if flags&(1<<17) != 0 {
		m |= ModShift
	}
	if flags&(1<<18) != 0 {
		m |= ModCtrl
	}
	if flags&(1<<19) != 0 {
		m |= ModAlt
	}
	if flags&(1<<20) != 0 {
		m |= ModSuper
	}
	return m
}

func (w *Window) handle(ev objc.ID) {
	typ := uintptr(objc.Send(ev, selType))
	if typ == 15 && uintptr(objc.Send(ev, selSubtype))&0xFFFF == wakeSubtype {
		if takeWake() {
			w.q.push(Event{Kind: EvWake, Time: Now()})
		}
		return // also used to unblock the loop after a display-link tick
	}
	t := int64(objc.SendF64(ev, selTimestamp) * 1e9)
	switch typ {
	case 1, 2, 3, 4, 5, 6, 7, 25, 26, 27: // mouse down/up/moved/dragged
		if objc.Send(ev, selWindow) != w.win {
			break
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
		w.q.push(e)
	case 9: // mouse exited
		w.q.push(Event{Kind: EvPointerLeave, Time: t})
	case 22: // scroll wheel
		dx, dy := objc.SendF64(ev, selScrollDX), objc.SendF64(ev, selScrollDY)
		e := Event{Kind: EvScroll, Mods: mods(uintptr(objc.Send(ev, selModifierFlags))), Time: t}
		unit := float64(w.scale)
		if objc.SendBool(ev, selPrecise) {
			e.Flags = FlagPrecise
		} else {
			unit *= 16 // lines → logical pixels
		}
		e.DX, e.DY = float32(dx*unit), float32(dy*unit)
		w.q.push(e)
	case 10, 11: // key down / up
		code := uint32(objc.Send(ev, selKeyCode)) & 0xFFFF
		m := mods(uintptr(objc.Send(ev, selModifierFlags)))
		e := Event{Kind: EvKey, Key: macKey(code), Code: code, Mods: m, Time: t}
		if typ == 10 {
			e.Flags = FlagDown
			if objc.SendBool(ev, selIsARepeat) {
				e.Flags |= FlagRepeat
			}
		}
		w.q.push(e)
		if typ == 10 && m == ModSuper && code == 0x0C { // Cmd+Q (there is no menu yet)
			w.q.push(Event{Kind: EvClose, Time: t})
		}
	}
	objc.Send1(w.app, selSendEvent, uintptr(ev))
}

// firstRectIMP implements firstRectForCharacterRange:actualRange: (the IME
// candidate-window anchor). Phase 1 returns an empty rect; IME is phase 3.
// amd64: argument 0 is the hidden result pointer, which the trampoline also
// returns in rax. arm64: CGRect is an HFA returned in d0-d3.
func firstRectIMP(c *ffi.CB) {
	if runtime.GOARCH == "amd64" {
		*(*objc.Rect)(c.IndirectResult()) = objc.Rect{}
		return
	}
	for i := range 4 {
		c.RetF64(i, 0)
	}
}

// impHandler is the IMP of every method on GNUIView and GNUIWindowDelegate,
// except firstRectIMP.
// Arguments: 0 = self, 1 = _cmd, 2.. = method arguments.
func impHandler(c *ffi.CB) {
	w := theWindow.Load()
	sel := objc.SEL(c.Int(1))
	if debugIMP {
		println("imp", objc.SelName(sel))
	}
	switch sel {
	case selAcceptsFirstResp:
		c.RetBool(true)
	case selKeyDown:
		arr := objc.Send1(clsNSArray, selArrayWithObject, c.Int(2))
		objc.Send1(objc.ID(c.Int(0)), selInterpretKeys, uintptr(arr))
	case selKeyUp, selFlagsChanged, selDoCommand, selSetMarkedText, selUnmarkText:
		// Swallowed: no beep. IME preedit comes in phase 3.
	case selInsertText:
		if w == nil {
			return
		}
		str := objc.ID(c.Int(2))
		if objc.SendBool1(str, selIsKindOfClass, uintptr(clsNSAttributedString)) {
			str = objc.Send(str, selString)
		}
		off := len(w.q.text)
		w.q.text = objc.AppendUTF8(w.q.text, str)
		if n := len(w.q.text) - off; n > 0 {
			w.q.push(Event{Kind: EvText, Text: TextRef{uint32(off), uint32(n)}, Time: Now()})
		}
	case selHasMarkedText:
		c.RetBool(false)
	case selMarkedRange:
		c.Ret2(uintptr(math.MaxInt64), 0) // {NSNotFound, 0}
	case selSelectedRange:
		c.Ret2(0, 0)
	case selValidAttrs:
		c.Ret(uintptr(objc.Send(clsNSArray, selArray)))
	case selAttrSubstring:
		c.Ret(0)
	case selCharIndexForPoint:
		c.Ret(uintptr(math.MaxInt64))
	case selViewBacking, selDidChangeBacking, selDidResize:
		if w != nil {
			if sel == selDidResize {
				w.layoutChrome() // AppKit re-lays out the titlebar on resize
			}
			w.pushResize()
		}
	case selDisplayLinkFired, selMetalLinkUpdate:
		if w == nil {
			return
		}
		if sel == selDisplayLinkFired && w.metalLayer != 0 {
			w.attachMetalLink() // its first tick delivers this frame, with a drawable
			break
		}
		if w.frameArmed {
			w.frameArmed, w.idleTicks = false, 0
			if sel == selMetalLinkUpdate { // metalDisplayLink:needsUpdate: (link, update)
				if w.drawable != 0 {
					objc.Send(w.drawable, selReleaseObj) // an earlier tick's drawable nobody presented
				}
				w.drawable = objc.Retain(objc.Send(objc.ID(c.Int(3)), selUpdateDrawable))
			}
			w.q.push(Event{Kind: EvFrame, Time: Now()})
			objc.Send2(w.app, selPostEvent, uintptr(w.wakeEv), 0) // unblock nextEventMatchingMask
			break
		}
		// Keep ticking through short gaps: pausing after every tick made the
		// link re-sync on each RequestFrame (uneven intervals on ProMotion).
		if w.idleTicks++; w.idleTicks >= linkIdleTicks && !w.linkPaused {
			objc.Send1(w.link, selSetPaused, 1)
			w.linkPaused = true
		}
	case selShouldClose:
		if w != nil {
			w.q.push(Event{Kind: EvClose, Time: Now()})
		}
		c.RetBool(false)
	case selDidBecomeKey, selDidResignKey:
		if w != nil {
			e := Event{Kind: EvFocus, Time: Now()}
			if sel == selDidBecomeKey {
				e.Flags = FlagDown
			}
			w.q.push(e)
		}
	case selDidChangeOcclus:
		if w != nil {
			e := Event{Kind: EvVisible, Time: Now()}
			if uintptr(objc.Send(w.win, selOcclusionState))&2 != 0 { // NSWindowOcclusionStateVisible
				e.Flags = FlagDown
			}
			w.q.push(e)
		}
	}
}

// Text returns the UTF-8 bytes of an EvText event.
func (w *Window) Text(r TextRef) []byte { return w.q.textOf(r) }

// RequestFrame arms one EvFrame at the next display refresh.
func (w *Window) RequestFrame() {
	w.frameArmed = true
	if w.linkPaused {
		objc.Send1(w.link, selSetPaused, 0)
		w.linkPaused = false
	}
}

// Size returns the content size in physical pixels and the backing scale.
func (w *Window) Size() (int32, int32, float32) { return w.wPx, w.hPx, w.scale }

// Native returns the AppKit handles.
func (w *Window) Native() Native { return Native{NSWindow: uintptr(w.win), NSView: uintptr(w.view)} }

// SetFrameSource switches frame pacing to a CAMetalDisplayLink on the given
// CAMetalLayer (the GPU surface's FrameSource). Each tick then hands over a
// drawable (TakeMetalDrawable), so presenting never blocks in nextDrawable;
// the view's CADisplayLink blocked the main thread for most of each frame.
//
// A layer driven by a CAMetalDisplayLink refuses nextDrawable, but the first
// frame is drawn before the run loop first spins (draw-first startup), so
// the switch happens on the view link's first tick, which always follows the
// first present.
func (w *Window) SetFrameSource(layer uintptr) {
	if clsMetalLink != 0 {
		w.metalLayer = objc.ID(layer)
	}
}

// attachMetalLink replaces the view's CADisplayLink with a CAMetalDisplayLink
// on w.metalLayer. Called from the view link's own tick (invalidating it
// there is allowed; it is autoreleased, not released, while on the stack).
func (w *Window) attachMetalLink() {
	layer := w.metalLayer
	w.metalLayer = 0
	link := objc.Send(clsMetalLink, selAlloc)
	link = objc.Send1(link, selInitWithLayer, uintptr(layer))
	if link == 0 {
		return
	}
	objc.Send1(link, selSetDelegate, uintptr(w.view))
	objc.F.Reset().Arg(uintptr(link)).Arg(uintptr(selSetFrameLatency)).ArgF32(metalLinkLatency).Call(objc.MsgSend)
	w.setRateRange(link)
	objc.Send2(link, selAddToRunLoop, uintptr(objc.Send(objc.Cls("NSRunLoop"), selMainRunLoop)), uintptr(commonModes))
	objc.Send1(link, selSetPaused, 1)
	objc.Send(w.link, selInvalidate)
	objc.Send(w.link, selAutorelease)
	w.link, w.linkPaused, w.metalLink = link, true, true
	if w.frameArmed {
		objc.Send1(w.link, selSetPaused, 0)
		w.linkPaused = false
	}
}

// metalLinkLatency is CAMetalDisplayLink.preferredFrameLatency: how many
// frames ahead of display the drawable is handed over.
const metalLinkLatency = 1

// TakeMetalDrawable returns the drawable delivered with the current frame
// tick, retained (the caller releases it after presenting), or 0. linked
// reports that a CAMetalDisplayLink drives the layer: then a 0 drawable
// means no frame is ready, and the caller must not call nextDrawable (the
// layer throws). Main thread only.
func TakeMetalDrawable() (drawable uintptr, linked bool) {
	w := theWindow.Load()
	if w == nil {
		return 0, false
	}
	d := w.drawable
	w.drawable = 0
	return uintptr(d), w.metalLink
}

func (w *Window) setRateRange(link objc.ID) {
	// Ask for the display's full rate while animating (ProMotion would
	// otherwise pick a lower adaptive rate).
	if fps := float32(objc.Send(objc.Send(w.win, selScreen), selMaxFPS)); fps > 0 {
		objc.F.Reset().Arg(uintptr(link)).Arg(uintptr(selSetRateRange)).ArgFloat3(fps/2, fps, fps).Call(objc.MsgSend)
	}
}

// Close closes the window.
func (w *Window) Close() {
	objc.Send(w.link, selInvalidate)
	if w.drawable != 0 {
		objc.Send(w.drawable, selReleaseObj)
		w.drawable = 0
	}
	objc.Send(w.win, selClose)
	theWindow.Store(nil)
}

// linkIdleTicks is how many display-link ticks without a frame request pass
// before the link is paused (idle must stay at zero wakeups).
const linkIdleTicks = 4

var (
	wakeMu    sync.Mutex
	wakeFrame = new(ffi.Frame)
)

// osWake posts the reusable application-defined event; postEvent:atStart:
// may be called from any thread.
func osWake() {
	w := theWindow.Load()
	if w == nil {
		return
	}
	// Autorelease pools are per thread: stay on one OS thread from push to pop.
	runtime.LockOSThread()
	wakeMu.Lock()
	pool := wakeFrame.Call0(objc.PoolPush)
	wakeFrame.Call4(objc.MsgSend, uintptr(w.app), uintptr(selPostEvent), uintptr(w.wakeEv), 0)
	wakeFrame.Call1(objc.PoolPop, pool)
	wakeMu.Unlock()
	runtime.UnlockOSThread()
}

// macKey maps a kVK_* virtual key code to Key.
func macKey(code uint32) Key {
	switch code {
	case 0x35:
		return KeyEscape
	case 0x24, 0x4C:
		return KeyEnter
	case 0x30:
		return KeyTab
	case 0x33:
		return KeyBackspace
	case 0x75:
		return KeyDelete
	case 0x31:
		return KeySpace
	case 0x7B:
		return KeyLeft
	case 0x7C:
		return KeyRight
	case 0x7E:
		return KeyUp
	case 0x7D:
		return KeyDown
	case 0x73:
		return KeyHome
	case 0x77:
		return KeyEnd
	case 0x74:
		return KeyPageUp
	case 0x79:
		return KeyPageDown
	}
	if code < uint32(len(macLetters)) && macLetters[code] != 0 {
		return macLetters[code]
	}
	for i, c := range macFKeys {
		if c == code {
			return KeyF1 + Key(i)
		}
	}
	return KeyUnknown
}

var macFKeys = [12]uint32{0x7A, 0x78, 0x63, 0x76, 0x60, 0x61, 0x62, 0x64, 0x65, 0x6D, 0x67, 0x6F}

var macLetters = func() (t [0x30]Key) {
	letters := map[uint32]byte{0x00: 'A', 0x0B: 'B', 0x08: 'C', 0x02: 'D', 0x0E: 'E', 0x03: 'F', 0x05: 'G', 0x04: 'H',
		0x22: 'I', 0x26: 'J', 0x28: 'K', 0x25: 'L', 0x2E: 'M', 0x2D: 'N', 0x1F: 'O', 0x23: 'P', 0x0C: 'Q', 0x0F: 'R',
		0x01: 'S', 0x11: 'T', 0x20: 'U', 0x09: 'V', 0x0D: 'W', 0x07: 'X', 0x10: 'Y', 0x06: 'Z'}
	for c, l := range letters {
		t[c] = KeyA + Key(l-'A')
	}
	digits := [10]uint32{0x1D, 0x12, 0x13, 0x14, 0x15, 0x17, 0x16, 0x1A, 0x1C, 0x19}
	for i, c := range digits {
		t[c] = Key0 + Key(i)
	}
	return t
}()
