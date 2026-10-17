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
	nOut     int  // how many events that Wait handed out

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

	// Frame-delivery diagnostics (DebugState); plain counters and times.
	ticks, ticksSinceFrame, pauses uint64
	armedT, lastTickT, lastFrameT  int64
	visible                        int8 // last EvVisible: 1, 0, or -1 before any
	visibleT                       int64

	// Live resize (SetFrameHandler). AppKit runs its own loop inside
	// sendEvent while the user drags a window edge; between
	// windowWillStartLiveResize: and windowDidEndLiveResize: the frame
	// handler draws from windowDidResize: and from view-link ticks.
	handler    func()
	inHandler  bool
	live       bool    // live resize in progress and a handler is set
	relink     bool    // the CAMetalDisplayLink was replaced by a view link for the live resize
	frameLayer objc.ID // the CAMetalLayer given to SetFrameSource, to re-attach a metal link to

	curs      cursorMap // cursor regions and override (SetCursorRegions)
	drags     dragMap   // titlebar drag areas (SetDragRegions)
	pointerIn bool      // the pointer is over the view (tracking area enter/exit and mouse events)

	// Input method (NSTextInputClient). marked is the UTF-8 of the marked
	// text the input method is composing, markedSel the caret/selection in it
	// (UTF-8 bytes) and markedSel16 the same in UTF-16 units, which is what
	// AppKit asks for. imeUsed and cmdCalled record, during one key event's
	// sendEvent, whether the input method took the key (setMarkedText,
	// unmarkText, a commit while composing) and whether it handed it back as a
	// command (doCommandBySelector:).
	marked      []byte
	tmp         []byte // scratch for decoding NSStrings
	markedSel   [2]int32
	markedSel16 [2]uintptr
	marked16    uintptr
	imeRect     [4]float32 // caret/composition rectangle, window pixels (SetIMERect)
	textInput   bool
	imeUsed     bool
	cmdCalled   bool
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
	selInputContext         = objc.Sel("inputContext")
	selDiscardMarked        = objc.Sel("discardMarkedText")
	selInvalidateCoords     = objc.Sel("invalidateCharacterCoordinates")

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
	selWillStartLive     = objc.Sel("windowWillStartLiveResize:")
	selDidEndLive        = objc.Sel("windowDidEndLiveResize:")
	selInLiveResize      = objc.Sel("inLiveResize")
	selSetContentSize    = objc.Sel("setContentSize:")

	// Cursors.
	selResetCursorRects   = objc.Sel("resetCursorRects")
	selDiscardCursorRects = objc.Sel("discardCursorRects")
	selAddCursorRect      = objc.Sel("addCursorRect:cursor:")
	selInvalidateCursors  = objc.Sel("invalidateCursorRectsForView:")

	selMouseDownCanMoveWindow = objc.Sel("mouseDownCanMoveWindow")
	selMouseDown              = objc.Sel("mouseDown:")
	selHitTest                = objc.Sel("hitTest:")
	selPerformWindowDrag      = objc.Sel("performWindowDragWithEvent:")
	selClickCount             = objc.Sel("clickCount")
	selZoom                   = objc.Sel("zoom:")
	selMiniaturize            = objc.Sel("miniaturize:")
	selSetContentMinSize      = objc.Sel("setContentMinSize:")

	clsNSArray, clsNSAttributedString objc.ID
	clsNSDate                         objc.ID // cached: objc.Cls allocates, and Wait runs every frame
	clsMetalLink                      objc.ID
	clsRunLoop                        objc.ID
	cursorObjs                        [numCursors]objc.ID // NSCursor singletons, cached at registerClasses
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
		{Sel: selResetCursorRects, Types: "v@:"},
		{Sel: selMouseDownCanMoveWindow, Types: "c@:"},
		{Sel: selMouseDown, Types: "v@:@"},
	})
	delegateClass = objc.NewClass("GNUIWindowDelegate", objc.Cls("NSObject"), []string{"NSWindowDelegate"}, imp, []objc.Method{
		{Sel: selShouldClose, Types: "c@:@"},
		{Sel: selDidResize, Types: "v@:@"},
		{Sel: selDidChangeBacking, Types: "v@:@"},
		{Sel: selDidBecomeKey, Types: "v@:@"},
		{Sel: selDidResignKey, Types: "v@:@"},
		{Sel: selDidChangeOcclus, Types: "v@:@"},
		{Sel: selWillStartLive, Types: "v@:@"},
		{Sel: selDidEndLive, Types: "v@:@"},
	})
	clsNSArray, clsNSAttributedString = objc.Cls("NSArray"), objc.Cls("NSAttributedString")
	clsMetalLink = objc.Cls("CAMetalDisplayLink") // macOS 14+
	clsRunLoop = objc.Cls("NSRunLoop")
}

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

	w := &Window{q: newEventQueue(), scale: 1, linkPaused: true, visible: -1, marked: make([]byte, 0, 256), tmp: make([]byte, 0, 256)}
	w.app = objc.Send(objc.Cls("NSApplication"), selSharedApplication)
	objc.Send1(w.app, selSetActivation, 0) // NSApplicationActivationPolicyRegular
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
	objc.Send1(w.win, selSetContentView, uintptr(w.view))
	objc.Send1(w.win, selMakeFirstResp, uintptr(w.view))
	delegate := objc.Send(objc.Send(delegateClass, selAlloc), objc.Sel("init"))
	objc.Send1(w.win, selSetDelegate, uintptr(delegate))
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
	if w.link = w.makeViewLink(); w.link == 0 {
		return nil, errors.New("platform: displayLinkWithTarget:selector: failed (macOS 14+ required)")
	}
	w.updateSize()
	markOpen("displayLink")
	if wakePending.Load() { // a Wake before Open found no window to post to
		osWake()
	}
	return w, nil
}

// makeViewLink creates the view's CADisplayLink, retained, paused and added
// to the main run loop in the common modes (so it also ticks inside AppKit's
// tracking loops). It returns 0 before macOS 14.
func (w *Window) makeViewLink() objc.ID {
	link := objc.Retain(objc.Send2(w.view, selDisplayLinkWith, uintptr(w.view), uintptr(selDisplayLinkFired)))
	if link == 0 {
		return 0
	}
	objc.Send2(link, selAddToRunLoop, uintptr(objc.Send(clsRunLoop, selMainRunLoop)), uintptr(commonModes))
	objc.Send1(link, selSetPaused, 1)
	w.setRateRange(link)
	return link
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

// Wait blocks until at least one event, a wake, a frame slot or the timeout.
func (w *Window) Wait(timeoutNs int64) []Event {
	if w.returned {
		w.q.dropFirst(w.nOut)
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
	if w.live && !objc.SendBool(w.win, selInLiveResize) {
		w.exitLiveResize() // windowDidEndLiveResize: never arrived (or the resize ended inside a nested loop)
	}
	for ev := w.nextEvent(until); ev != 0; ev = w.nextEvent(w.distantPast) {
		w.handle(ev)
	}
	F.Call1(objc.PoolPop, pool)
	w.returned, w.nOut = true, len(w.q.ev)
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
	keyIdx := -1 // index in the queue of the EvKey of this key-down event
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
		w.pointerIn = true
		if typ == 1 && w.drags.at(e.X, e.Y) && w.hitsView(lx, ly) {
			// A press on the titlebar drag map belongs to the window: the
			// double-click action, or a drag by the window server. The app
			// never sees it and AppKit's titlebar views do not get it.
			if objc.Send(ev, selClickCount) >= 2 {
				w.titlebarDoubleClick()
			} else {
				objc.Send1(w.win, selPerformWindowDrag, uintptr(ev))
			}
			return
		}
		w.q.push(e)
	case 8: // mouse entered (the tracking area)
		w.pointerIn = true
	case 9: // mouse exited
		w.pointerIn = false
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
		if typ == 10 {
			keyIdx = len(w.q.ev) - 1
		}
		if typ == 10 && m == ModSuper && code == 0x0C { // Cmd+Q (there is no menu yet)
			w.q.push(Event{Kind: EvClose, Time: t})
		}
	}
	// The key reaches the view's keyDown: through interpretKeyEvents: the input
	// method sees it first. A key it consumed (composition keys, the key that
	// commits) is not a key press for the app; one it hands back as a command
	// (an arrow or Enter with nothing composing, doCommandBySelector:) is.
	markedBefore := len(w.marked) > 0
	w.imeUsed, w.cmdCalled = false, false
	objc.Send1(w.app, selSendEvent, uintptr(ev))
	w.dropConsumedKey(keyIdx, markedBefore)
}

// dropConsumedKey removes the EvKey at keyIdx (if >= 0) when the input method
// consumed the key: it was composing before the key or took the key during
// it, and did not hand it back as a command.
func (w *Window) dropConsumedKey(keyIdx int, markedBefore bool) {
	if keyIdx >= 0 && keyIdx < len(w.q.ev) && (markedBefore || w.imeUsed) && !w.cmdCalled {
		w.q.ev = append(w.q.ev[:keyIdx], w.q.ev[keyIdx+1:]...)
	}
}

// firstRectIMP implements firstRectForCharacterRange:actualRange: (the IME
// candidate-window anchor): the rectangle the app gave SetIMERect, in screen
// coordinates. The range asked about is ignored: the app reports the caret or
// the composition, which is all an input method asks for.
// amd64: argument 0 is the hidden result pointer, which the trampoline also
// returns in rax. arm64: CGRect is an HFA returned in d0-d3.
func firstRectIMP(c *ffi.CB) {
	var r objc.Rect
	if w := theWindow.Load(); w != nil {
		r = w.imeScreenRect()
	}
	if runtime.GOARCH == "amd64" {
		*(*objc.Rect)(c.IndirectResult()) = r
		return
	}
	c.RetF64(0, r.X)
	c.RetF64(1, r.Y)
	c.RetF64(2, r.W)
	c.RetF64(3, r.H)
}

// imeScreenRect converts the app's IME rectangle (window pixels, y down) to
// screen points (y up): the view is the content view, whose frame origin is
// relative to the window frame's.
func (w *Window) imeScreenRect() objc.Rect {
	s := float64(w.scale)
	wf, vf := objc.SendRect(w.win, selFrame), objc.SendRect(w.view, selFrame)
	x, y := float64(w.imeRect[0])/s, float64(w.imeRect[1])/s
	rw, rh := float64(w.imeRect[2])/s, float64(w.imeRect[3])/s
	return objc.Rect{X: wf.X + vf.X + x, Y: wf.Y + vf.Y + w.hPt - y - rh, W: rw, H: rh}
}

// SetIMERect tells the input method where the text caret (or the composition,
// while one is shown) is, in window pixels with the origin at the top left;
// candidate windows are placed next to it. Main thread; does nothing when the
// rectangle is unchanged, so it can be called every frame.
func (w *Window) SetIMERect(x, y, wd, h float32) {
	r := [4]float32{x, y, wd, h}
	if r == w.imeRect {
		return
	}
	w.imeRect = r
	if ctx := objc.Send(w.view, selInputContext); ctx != 0 {
		objc.Send(ctx, selInvalidateCoords)
	}
}

// SetTextInput says whether a text field has keyboard focus. When it stops
// having it while an input method is composing, the composition is discarded
// (EvPreedit with no text follows). Main thread.
func (w *Window) SetTextInput(active bool) {
	if w.textInput == active {
		return
	}
	w.textInput = active
	if !active && len(w.marked) > 0 {
		if ctx := objc.Send(w.view, selInputContext); ctx != 0 {
			objc.Send(ctx, selDiscardMarked)
		}
		w.setMarkedFrom(nil, 0, 0, 0)
	}
}

// setMarkedFrom records the marked text and queues an EvPreedit for it (an empty
// text clears it). sel16 is the selection in UTF-16 units of the marked
// string; the same range in bytes goes into the event.
func (w *Window) setMarkedFrom(b []byte, loc16, len16, total16 uintptr) {
	w.marked = append(w.marked[:0], b...)
	w.marked16 = total16
	w.markedSel16 = [2]uintptr{loc16, len16}
	start := u16ToByte(b, loc16)
	end := u16ToByte(b, loc16+len16)
	w.markedSel = [2]int32{int32(start), int32(end - start)}
	w.q.pushPreedit(w.marked, w.markedSel[0], w.markedSel[1], Now())
}

// commitMarked turns the marked text into typed text: the composition ends
// and the same text arrives as EvText.
func (w *Window) commitMarked() {
	off := len(w.q.text)
	w.q.text = append(w.q.text, w.marked...)
	n := len(w.q.text) - off
	w.setMarkedFrom(nil, 0, 0, 0)
	if n > 0 {
		w.q.push(Event{Kind: EvText, Text: TextRef{uint32(off), uint32(n)}, Time: Now()})
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
	case selKeyUp, selFlagsChanged:
		// Swallowed: no beep.
	case selDoCommand:
		// Swallowed (the key is the app's: it already has its EvKey), but
		// remember that the input method handed this key back.
		if w != nil {
			w.cmdCalled = true
		}
	case selSetMarkedText:
		// setMarkedText:selectedRange:replacementRange: — the composition
		// (an NSString or NSAttributedString) and the caret or selection inside
		// it, in UTF-16 units. The replacement range is not used: the app owns
		// the text and shows the composition at its caret.
		if w == nil {
			return
		}
		str := objc.ID(c.Int(2))
		if str != 0 && objc.SendBool1(str, selIsKindOfClass, uintptr(clsNSAttributedString)) {
			str = objc.Send(str, selString)
		}
		w.tmp = objc.AppendUTF8(w.tmp[:0], str)
		b := w.tmp
		total := utf16Len(b)
		loc, n := c.Int(3), c.Int(4)
		if loc > total { // NSNotFound or past the end
			loc = total
		}
		n = min(n, total-loc)
		w.imeUsed = true
		w.setMarkedFrom(b, loc, n, total)
	case selUnmarkText:
		// The input method gives up the marking and leaves the text: it
		// becomes ordinary typed text.
		if w != nil && len(w.marked) > 0 {
			w.imeUsed = true
			w.commitMarked()
		}
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
		n := len(w.q.text) - off
		if len(w.marked) > 0 { // a commit: the composition ends where the text lands
			w.imeUsed = true
			w.setMarkedFrom(nil, 0, 0, 0)
		}
		if n > 0 {
			w.q.push(Event{Kind: EvText, Text: TextRef{uint32(off), uint32(n)}, Time: Now()})
		}
	case selHasMarkedText:
		c.RetBool(w != nil && len(w.marked) > 0)
	case selMarkedRange:
		// The app owns the text and has no document positions to report: the
		// marked text is described as starting at 0.
		if w != nil && len(w.marked) > 0 {
			c.Ret2(0, w.marked16)
		} else {
			c.Ret2(uintptr(math.MaxInt64), 0) // {NSNotFound, 0}
		}
	case selSelectedRange:
		if w != nil && len(w.marked) > 0 {
			c.Ret2(w.markedSel16[0], w.markedSel16[1])
		} else {
			c.Ret2(0, 0)
		}
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
				if !w.live && w.handler != nil && objc.SendBool(w.win, selInLiveResize) {
					w.enterLiveResize() // the will-start notification was missed
				}
			}
			if w.pushResize() && w.live {
				w.runHandler() // inside AppKit's resize loop: draw in step with the window
			}
		}
	case selWillStartLive:
		if w != nil {
			w.enterLiveResize()
		}
	case selDidEndLive:
		if w != nil {
			w.exitLiveResize()
		}
	case selResetCursorRects:
		if w != nil {
			w.addCursorRects()
		}
	case selMouseDown:
		// Swallowed: a press the app did not take must not fall through to the
		// window's titlebar handling (which would start a window drag).
	case selMouseDownCanMoveWindow:
		c.RetBool(false) // a press in the view is the app's; window drags are explicit (SetDragRegions)
	case selDisplayLinkFired, selMetalLinkUpdate:
		if w == nil {
			return
		}
		if sel == selDisplayLinkFired && w.metalLayer != 0 && !w.live {
			layer := w.metalLayer
			w.metalLayer = 0
			w.attachMetalLink(layer) // its first tick delivers this frame, with a drawable
			break
		}
		now := Now()
		w.ticks, w.ticksSinceFrame, w.lastTickT = w.ticks+1, w.ticksSinceFrame+1, now
		// A drawable is held for one tick at most: one kept through an idle
		// period would leave the link without a free drawable (2 in the
		// pool), and it stops ticking.
		if w.drawable != 0 {
			objc.Send(w.drawable, selReleaseObj)
			w.drawable = 0
		}
		if w.frameArmed {
			w.frameArmed, w.idleTicks = false, 0
			if sel == selMetalLinkUpdate { // metalDisplayLink:needsUpdate: (link, update)
				w.drawable = objc.Retain(objc.Send(objc.ID(c.Int(3)), selUpdateDrawable))
			}
			w.ticksSinceFrame, w.lastFrameT = 0, now
			w.q.push(Event{Kind: EvFrame, Time: now})
			objc.Send2(w.app, selPostEvent, uintptr(w.wakeEv), 0) // unblock nextEventMatchingMask
			if w.live {
				w.runHandler() // a requested frame (animation) while AppKit's resize loop runs
			}
			break
		}
		// Keep ticking through short gaps: pausing after every tick made the
		// link re-sync on each RequestFrame (uneven intervals on ProMotion).
		if w.idleTicks++; w.idleTicks >= linkIdleTicks && !w.linkPaused {
			objc.Send1(w.link, selSetPaused, 1)
			w.linkPaused = true
			w.pauses++
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
			w.visible, w.visibleT = 0, e.Time
			if uintptr(objc.Send(w.win, selOcclusionState))&2 != 0 { // NSWindowOcclusionStateVisible
				e.Flags = FlagDown
				w.visible = 1
			}
			w.q.push(e)
		}
	}
}

// Text returns the UTF-8 bytes of an EvText event.
func (w *Window) Text(r TextRef) []byte { return w.q.textOf(r) }

// RequestFrame arms one EvFrame at the next display refresh.
func (w *Window) RequestFrame() {
	if !w.frameArmed {
		w.armedT = Now()
	}
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
		w.metalLayer, w.frameLayer = objc.ID(layer), objc.ID(layer)
	}
}

// attachMetalLink replaces the view's CADisplayLink with a CAMetalDisplayLink
// on layer. Called from the view link's own tick (invalidating it there is
// allowed; it is autoreleased, not released, while on the stack) and when a
// live resize ends.
func (w *Window) attachMetalLink(layer objc.ID) {
	link := objc.Send(clsMetalLink, selAlloc)
	link = objc.Send1(link, selInitWithLayer, uintptr(layer))
	if link == 0 {
		return
	}
	objc.Send1(link, selSetDelegate, uintptr(w.view))
	objc.F.Reset().Arg(uintptr(link)).Arg(uintptr(selSetFrameLatency)).ArgF32(metalLinkLatency).Call(objc.MsgSend)
	w.setRateRange(link)
	objc.Send2(link, selAddToRunLoop, uintptr(objc.Send(clsRunLoop, selMainRunLoop)), uintptr(commonModes))
	objc.Send1(link, selSetPaused, 1)
	objc.Send(w.link, selInvalidate)
	objc.Send(w.link, selAutorelease)
	w.link, w.linkPaused, w.metalLink = link, true, true
	if w.frameArmed {
		objc.Send1(w.link, selSetPaused, 0)
		w.linkPaused = false
	}
}

// SetFrameHandler registers fn to be called, on the main thread, from inside
// AppKit's live-resize loop. While the user drags a window edge, sendEvent:
// does not return and Wait does not either, so the app's loop cannot draw;
// the handler is the way to keep the content in step with the window.
//
// It runs from windowDidResize: (size changed) and, while a frame is
// requested with RequestFrame, from display-link ticks. Take Pending, resize
// the surface on EvResize, build, draw, present. gpu.Surface presents those
// frames with presentsWithTransaction so each commits in the same Core
// Animation transaction as the window's new size. See enterLiveResize.
//
// Without a handler a live resize keeps the old behaviour: the app is not
// redrawn until the drag ends.
func (w *Window) SetFrameHandler(fn func()) { w.handler = fn }

// Pending returns and clears the events queued so far without pumping the
// OS, so it is safe to call from inside the frame handler. The slice is valid
// until the next Pending or Wait call.
func (w *Window) Pending() []Event {
	if w.returned {
		w.q.dropFirst(w.nOut)
		w.returned = false
	}
	return w.q.takePending()
}

// InLiveResize reports whether a live resize with a frame handler is in
// progress: the frame source is then not a CAMetalDisplayLink (which makes
// nextDrawable throw), so gpu.Surface acquires drawables with nextDrawable
// and presents with presentsWithTransaction. Main thread only.
func InLiveResize() bool {
	w := theWindow.Load()
	return w != nil && w.live
}

func (w *Window) runHandler() {
	if w.handler == nil || w.inHandler {
		return
	}
	w.inHandler = true
	w.handler()
	w.inHandler = false
}

// enterLiveResize starts the live-resize mode. A layer driven by a
// CAMetalDisplayLink refuses nextDrawable, so the metal link is invalidated
// and released and replaced by a plain view CADisplayLink (the one the first
// frames used); exitLiveResize re-attaches a new metal link.
func (w *Window) enterLiveResize() {
	if w.live || w.handler == nil {
		return
	}
	if w.drawable != 0 { // a tick's drawable must not outlive its link
		objc.Send(w.drawable, selReleaseObj)
		w.drawable = 0
	}
	if w.metalLink {
		link := w.makeViewLink()
		if link == 0 {
			return
		}
		objc.Send(w.link, selInvalidate)
		objc.Send(w.link, selReleaseObj)
		w.link, w.linkPaused, w.metalLink, w.relink = link, true, false, true
		if w.frameArmed {
			objc.Send1(w.link, selSetPaused, 0)
			w.linkPaused = false
		}
	}
	w.live = true
	if debugIMP {
		println("live resize begins; metal link detached:", w.relink)
	}
}

// exitLiveResize leaves the live-resize mode and restores the
// CAMetalDisplayLink if the live resize had replaced it.
func (w *Window) exitLiveResize() {
	if !w.live {
		return
	}
	w.live = false
	if w.relink {
		w.relink = false
		w.attachMetalLink(w.frameLayer)
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
	frame := objc.Send(w.view, selSuperview)
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

// metalLinkLatency is CAMetalDisplayLink.preferredFrameLatency: how many
// frames ahead of display the drawable is handed over.
const metalLinkLatency = 1

// TakeMetalDrawable returns the drawable delivered with the current frame
// tick, retained (the caller releases it after presenting), or 0. A tick's
// drawable is valid until the next tick, so draw on EvFrame. linked reports
// that a CAMetalDisplayLink drives the layer: then a 0 drawable means no
// frame is ready (one is requested), and the caller must not call
// nextDrawable (the layer throws). Main thread only.
func TakeMetalDrawable() (drawable uintptr, linked bool) {
	w := theWindow.Load()
	if w == nil {
		return 0, false
	}
	d := w.drawable
	w.drawable = 0
	if d == 0 && w.metalLink {
		w.RequestFrame() // no frame is ready: the caller draws on the EvFrame that follows
	}
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
	objc.Send(w.link, selReleaseObj) // Open retained the view link; attachMetalLink owns its link
	w.link = 0
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
