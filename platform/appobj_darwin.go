//go:build darwin

package platform

import (
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The application object on macOS (DECISIONS D37): the NSApplication delegate
// (GNUIAppDelegate), the run-loop source that dispatches app events, the
// app-wide pump that runs with no window, and the quit policy's AppKit side.
//
// The delegate is set before finishLaunching. Its methods deliver their
// events synchronously (class 1). The run-loop source is a version-0
// CFRunLoopSource on the main run loop in kCFRunLoopCommonModes, so it fires
// in the default, modal-panel and event-tracking modes: with no window, inside
// menu tracking, under a sheet, and while the answer to a foreign terminate:
// is pending (NSTerminateLater runs the run loop in NSModalPanelRunLoopMode
// until replyToApplicationShouldTerminate:). Producers on any thread signal it
// (CFRunLoopSourceSignal and CFRunLoopWakeUp are thread-safe); its perform
// callback runs DispatchApp on the main thread. Wake goes through it too, so
// a wake reaches the loop with no window, and is not lost when a native loop
// dequeues the NSEvent that unblocks our pump.

var (
	selTerminate          = objc.Sel("terminate:")
	selAppWindows         = objc.Sel("windows")
	selReplyTerminate     = objc.Sel("replyToApplicationShouldTerminate:")
	selActivationPolicy   = objc.Sel("activationPolicy")
	selSharedAEManager    = objc.Sel("sharedAppleEventManager")
	selCurrentAppleEvent  = objc.Sel("currentAppleEvent")
	selEventClass         = objc.Sel("eventClass")
	selEventID            = objc.Sel("eventID")
	selAttrDescriptor     = objc.Sel("attributeDescriptorForKeyword:")
	selParamDescriptor    = objc.Sel("paramDescriptorForKeyword:")
	selEnumCodeValue      = objc.Sel("enumCodeValue")
	selStringValue        = objc.Sel("stringValue")
	selSetEventHandler    = objc.Sel("setEventHandler:andSelector:forEventClass:andEventID:")
	selIsFileURL          = objc.Sel("isFileURL")
	selPath               = objc.Sel("path")
	selAbsoluteString     = objc.Sel("absoluteString")
	selHasSubmenu         = objc.Sel("hasSubmenu")
	selSubmenu            = objc.Sel("submenu")
	selDockSetAction      = objc.Sel("setAction:")
	selDockSetTarget      = objc.Sel("setTarget:")
	selDockTag            = objc.Sel("tag")
	selDockItems          = objc.Sel("numberOfItems")
	selDockItemAt         = objc.Sel("itemAtIndex:")
	selDockAction         = objc.Sel("action")
	selDockSetState       = objc.Sel("setState:")
	selNotificationCenter = objc.Sel("notificationCenter")
	selSharedWorkspace    = objc.Sel("sharedWorkspace")

	selAppWillFinish     = objc.Sel("applicationWillFinishLaunching:")
	selAppShouldTerm     = objc.Sel("applicationShouldTerminate:")
	selAppWillTerm       = objc.Sel("applicationWillTerminate:")
	selAppReopen         = objc.Sel("applicationShouldHandleReopen:hasVisibleWindows:")
	selAppBecameActive   = objc.Sel("applicationDidBecomeActive:")
	selAppResignedActive = objc.Sel("applicationDidResignActive:")
	selAppOpenURLs       = objc.Sel("application:openURLs:")
	selAppDockMenu       = objc.Sel("applicationDockMenu:")
	selAppScreens        = objc.Sel("applicationDidChangeScreenParameters:")
	selAppLastWindow     = objc.Sel("applicationShouldTerminateAfterLastWindowClosed:")
	selAppSecureRestore  = objc.Sel("applicationSupportsSecureRestorableState:")
	selGetURL            = objc.Sel("gnuiGetURL:withReplyEvent:")
	selWillSleep         = objc.Sel("gnuiWillSleep:")
	selDidWake           = objc.Sel("gnuiDidWake:")
	selSessionActive     = objc.Sel("gnuiSessionActive:")
	selSessionInactive   = objc.Sel("gnuiSessionInactive:")
	selDockCommand       = objc.Sel("gnuiDockCommand:")
	selQuitEvent         = objc.Sel("gnuiQuitEvent:withReplyEvent:")

	selSuspendAE    = objc.Sel("suspendCurrentAppleEvent")
	selReplyForSusp = objc.Sel("replyAppleEventForSuspensionID:")
	selResumeSusp   = objc.Sel("resumeWithSuspensionID:")
	selSetParamDesc = objc.Sel("setParamDescriptor:forKeyword:")
	selDescInt32    = objc.Sel("descriptorWithInt32:")
)

// Apple event codes (FourCharCode).
const (
	aeCoreClass     = 0x61657674 // 'aevt'
	aeQuit          = 0x71756974 // 'quit'
	aeQuitReason    = 0x7768793F // 'why?'
	aeInternetClass = 0x4755524C // 'GURL'
	aeGetURL        = 0x4755524C // 'GURL'
	aeDirectObject  = 0x2D2D2D2D // '----'
	aeErrorNumber   = 0x6572726E // 'errn' (keyErrorNumber)
	userCanceledErr = -128       // the reply that refuses a quit (and cancels a logout)
)

// NSApplicationTerminateReply.
const (
	terminateCancel = 0
	terminateNow    = 1
	terminateLater  = 2
)

var appObj struct {
	started   bool
	launched  bool // AppLaunched ran: the late setup is done
	app       objc.ID
	delegate  objc.ID
	dockMenu  objc.ID   // retained, 0 for none
	dockState MenuState // the Dock menu's commands' state, nil for none

	wakeEv, distantPast, distantFuture objc.ID

	source   atomic.Uintptr // the CFRunLoopSource, 0 before StartApp
	mainLoop uintptr
	ctx      *[10]uintptr // the source's context (CF copies it; kept anyway)

	reason      QuitReason // the reason of a terminate: we are sending
	terminating bool       // inside our own terminate: (an allowed quit)

	// quitOutside: the pending quit request was asked without terminate:
	// (the app's own routes, the quit Apple event), so no terminate: waits
	// for its answer. susp are the suspended quit Apple events that wait for
	// the answer of the quit being decided, each resumed once with it.
	quitOutside bool
	susp        [8]uintptr
	nsusp       int
	woke        bool // a wake arrived since WaitApp last reported one

	scratch [4][]byte // the data of synchronous events, by nesting depth
	depth   int
}

var cfFuncs struct {
	create, add, signal, wakeUp, getMain uintptr
	commonModes                          uintptr
}

var appClassOnce sync.Once

// StartApp creates the application object: NSApplication with its activation
// policy and our delegate, the dispatching run-loop source, and
// finishLaunching. Main thread. A second call only updates the options that
// can change (the quit deadline and its action). Open calls it with the
// default options when the app did not.
func StartApp(o AppOptions) error {
	startCommon(o)
	if appObj.started {
		return nil
	}
	if _, err := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit"); err != nil {
		return err
	}
	if _, err := loadFramework("/System/Library/Frameworks/QuartzCore.framework/QuartzCore"); err != nil {
		return err
	}
	foundation, _ := loadFramework("/System/Library/Frameworks/Foundation.framework/Foundation")
	defaultMode = stringConstant(foundation, "NSDefaultRunLoopMode")
	commonModes = stringConstant(foundation, "NSRunLoopCommonModes")
	classesOnce.Do(registerClasses)
	appClassOnce.Do(registerAppDelegate)
	if err := loadCF(); err != nil {
		return err
	}

	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)
	appObj.app = objc.Send(objc.Cls("NSApplication"), selSharedApplication)
	objc.Send1(appObj.app, selSetActivation, uintptr(o.Policy))
	appObj.delegate = objc.Send(objc.Send(appDelegateClass, selAlloc), selInitPlain)
	objc.Send1(appObj.app, selSetDelegate, uintptr(appObj.delegate)) // NSApp keeps a weak reference: ours is never released

	// One reusable application-defined event that unblocks the pump.
	clsNSDate = objc.Cls("NSDate")
	appObj.wakeEv = objc.Retain(objc.ID(F.Reset().Arg(uintptr(objc.Cls("NSEvent"))).Arg(uintptr(selOtherEvent)).
		Arg(15 /* NSEventTypeApplicationDefined */).ArgPoint(0, 0).Arg(0).ArgF64(0).Arg(0).Arg(0).
		Arg(wakeSubtype).Arg(0).Arg(0).Call(objc.MsgSend).R0()))
	appObj.distantPast = objc.Retain(objc.Send(clsNSDate, selDistantPast))
	appObj.distantFuture = objc.Retain(objc.Send(clsNSDate, selDistantFuture))

	// The dispatcher: a version-0 source in the common modes.
	appObj.ctx = new([10]uintptr)
	appObj.ctx[9] = ffi.NewCallback(appPerform) // perform(void *info)
	src := F.Call3(cfFuncs.create, 0, 0, uintptr(unsafe.Pointer(appObj.ctx)))
	appObj.mainLoop = F.Call0(cfFuncs.getMain)
	F.Call3(cfFuncs.add, appObj.mainLoop, src, cfFuncs.commonModes)
	appObj.source.Store(src)

	appObj.started = true
	objc.Send(appObj.app, selFinishLaunching)
	if dispatch.signalled.Load() || wakePending.Load() { // produced before the source existed
		osSignalApp()
	}
	return nil
}

// ensureApp starts the application object with the default options if the
// app did not (a window opened directly with Open).
func ensureApp() error {
	if appObj.started {
		return nil
	}
	return StartApp(AppOptions{})
}

func loadCF() error {
	cf, err := loadFramework("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation")
	if err != nil {
		return err
	}
	for _, s := range []struct {
		p    *uintptr
		name string
	}{{&cfFuncs.create, "CFRunLoopSourceCreate"}, {&cfFuncs.add, "CFRunLoopAddSource"}, {&cfFuncs.signal, "CFRunLoopSourceSignal"},
		{&cfFuncs.wakeUp, "CFRunLoopWakeUp"}, {&cfFuncs.getMain, "CFRunLoopGetMain"}} {
		if *s.p, err = purego.Dlsym(cf, s.name); err != nil {
			return err
		}
	}
	cfFuncs.commonModes = uintptr(stringConstant(cf, "kCFRunLoopCommonModes"))
	return nil
}

// AppLaunched finishes the setup that can wait until the first frame is on
// screen: the system notifications (sleep, wake, the user session). The app
// calls it once its Start returned. Main thread.
func AppLaunched() {
	if appObj.launched || !appObj.started {
		return
	}
	appObj.launched = true
	ws := objc.Send(objc.Cls("NSWorkspace"), selSharedWorkspace)
	nc := objc.Send(ws, selNotificationCenter)
	appKit, _ := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	for _, o := range []struct {
		sel  objc.SEL
		name string
	}{{selWillSleep, "NSWorkspaceWillSleepNotification"}, {selDidWake, "NSWorkspaceDidWakeNotification"},
		{selSessionActive, "NSWorkspaceSessionDidBecomeActiveNotification"}, {selSessionInactive, "NSWorkspaceSessionDidResignActiveNotification"}} {
		if name := stringConstant(appKit, o.name); name != 0 {
			objc.AddObserver(nc, appObj.delegate, o.sel, name, 0)
		}
	}
}

var appDelegateClass objc.ID

// appDelegateDef is GNUIAppDelegate's definition (tests read it).
var appDelegateDef *objc.ClassDef

func registerAppDelegate() {
	d := objc.Define("GNUIAppDelegate", objc.Cls("NSObject"), []string{"NSApplicationDelegate", "NSMenuItemValidation"})
	d.Method(selAppWillFinish, "v@:@", appWillFinishLaunching)
	d.Method(selAppShouldTerm, "Q@:@", appShouldTerminate)
	d.Method(selAppWillTerm, "v@:@", appWillTerminate)
	d.Method(selAppReopen, "c@:@c", appReopen)
	d.Method(selAppBecameActive, "v@:@", func(*ffi.CB) { deliverNow(AppEvActivated, 0, 0, 0, nil) })
	d.Method(selAppResignedActive, "v@:@", func(*ffi.CB) { deliverNow(AppEvDeactivated, 0, 0, 0, nil) })
	d.Method(selAppOpenURLs, "v@:@@", appOpenURLs)
	d.Method(selAppDockMenu, "@@:@", func(c *ffi.CB) { c.Ret(uintptr(appObj.dockMenu)) })
	d.Method(selAppScreens, "v@:@", func(*ffi.CB) { deliverNow(AppEvScreensChanged, 0, 0, 0, nil) })
	d.Method(selAppLastWindow, "c@:@", func(c *ffi.CB) { c.RetBool(false) }) // the app object decides (KeepRunning)
	d.Method(selAppSecureRestore, "c@:@", func(c *ffi.CB) { c.RetBool(true) })
	d.Method(selGetURL, "v@:@@", appGetURL)
	d.Method(selQuitEvent, "v@:@@", appQuitEvent)
	d.Method(selWillSleep, "v@:@", func(*ffi.CB) { deliverNow(AppEvWillSleep, 0, 0, 0, nil) })
	d.Method(selDidWake, "v@:@", func(*ffi.CB) { deliverNow(AppEvDidWake, 0, 0, 0, nil) })
	d.Method(selSessionActive, "v@:@", func(*ffi.CB) { deliverNow(AppEvSessionActive, 0, 1, 0, nil) })
	d.Method(selSessionInactive, "v@:@", func(*ffi.CB) { deliverNow(AppEvSessionActive, 0, 0, 0, nil) })
	d.Method(selDockCommand, "v@:@", func(c *ffi.CB) {
		if item := objc.ID(c.Int(2)); item != 0 {
			if id := uintptr(objc.Send(item, selDockTag)); id != 0 {
				deliverNow(AppEvCommand, uint64(id), 0, 0, nil)
			}
		}
	})
	d.Method(objc.Sel("validateMenuItem:"), "c@:@", dockValidate)
	appDelegateDef = d
	appDelegateClass = d.Register()
}

// appWillFinishLaunching registers our Apple event handlers, before launch
// completes (and after AppKit installed its own, which ours replace): get-URL,
// so that a launch by URL is not lost, and quit (appQuitEvent).
func appWillFinishLaunching(*ffi.CB) {
	m := objc.Send(objc.Cls("NSAppleEventManager"), selSharedAEManager)
	objc.Send4(m, selSetEventHandler, uintptr(appObj.delegate), uintptr(selGetURL), aeInternetClass, aeGetURL)
	objc.Send4(m, selSetEventHandler, uintptr(appObj.delegate), uintptr(selQuitEvent), aeCoreClass, aeQuit)
}

// appQuitEvent is our handler of the quit Apple event (kAEQuitApplication):
// the Dock's Quit, another app's, a logout, restart or shutdown (whose
// kAEQuitReason parameter makes it QuitLogout). AppKit's own handler sends
// terminate:, which ends every open sheet before it asks anything, an alert
// with its first button: a quit would press an alert's default (perhaps
// destructive) action. So a quit event takes ⌘Q's way: the policy is asked
// without terminate:, sheets stay up while the answer is pending, and
// terminate: is sent only once the quit is allowed, after ending the sheets
// as cancelled (sendTermination). The Apple event gets AppKit's answers: an
// allowed quit ends the process; a refused one replies userCanceledErr,
// which cancels a logout; one whose answer is not known yet is suspended
// and resumed with it (resumeQuitEvents), also when it arrived inside a
// frame (asked after it) or while another quit was being decided (answered
// with that one).
func appQuitEvent(c *ffi.CB) {
	reply := objc.ID(c.Int(3))
	reason := quitEventReason(objc.ID(c.Int(2)))
	switch {
	case quitState.phase == quitCommitted:
		// The app is quitting already: the reply says yes (no error), and
		// the termination on its way is not replaced.
	case dispatch.held:
		// A native loop entered from Build: asked after the frame, when the
		// app's loop takes the request (TakeQuitRequest); the event waits
		// for that answer.
		suspendQuitEvent(reply)
		deferQuit(reason)
	case quitState.phase != quitIdle: // a quit is being decided: its answer is this event's too
		suspendQuitEvent(reply)
	case dispatch.sink == nil:
		// No application object (a window opened with Open alone): the
		// window's owner decides, as with its close button.
		if w := theWindow.Load(); w != nil {
			w.q.push(Event{Kind: EvClose, Time: Now()})
			refuseQuitEvent(reply)
			return
		}
		suspendQuitEvent(reply) // answered yes before the process ends (commitQuit)
		commitQuit(reason)
		sendTermination(0)
	default:
		switch beginQuit(reason) {
		case 0:
			appObj.quitOutside = true
			suspendQuitEvent(reply)
		case 1:
			suspendQuitEvent(reply) // answered yes before the process ends (commitQuit)
			commitQuit(reason)
			sendTermination(0) // the process ends here
		default:
			refuseQuit()
			refuseQuitEvent(reply)
		}
	}
}

func aeManager() objc.ID { return objc.Send(objc.Cls("NSAppleEventManager"), selSharedAEManager) }

// The values of kAEQuitReason (AERegistry.h): the parameter's documented
// values (kAEQuitAll, kAEShutDown, kAERestart, kAEReallyLogOut) and the other
// session-ending events of the system process (kAELogOut,
// kAEShowRestartDialog, kAEShowShutdownDialog).
const (
	aeQuitAll           = 0x71756961 // 'quia'
	aeShutDown          = 0x73687574 // 'shut'
	aeRestart           = 0x72657374 // 'rest'
	aeReallyLogOut      = 0x726C676F // 'rlgo'
	aeLogOut            = 0x6C6F676F // 'logo'
	aeShowRestartDialog = 0x72727374 // 'rrst'
	aeShowShutdownDlg   = 0x7273646E // 'rsdn'
)

// quitEventReason is a quit Apple event's reason, from its kAEQuitReason
// parameter: QuitLogout for a logout, restart, shutdown or quit-all; a plain
// quit (QuitDock: the Dock or another app) without the parameter, or with a
// value that is none of those.
func quitEventReason(ev objc.ID) QuitReason {
	if ev == 0 {
		return QuitDock
	}
	why := objc.Send1(ev, selParamDescriptor, aeQuitReason)
	if why == 0 {
		return QuitDock
	}
	switch uint32(objc.Send(why, selEnumCodeValue)) {
	case aeQuitAll, aeShutDown, aeRestart, aeReallyLogOut, aeLogOut, aeShowRestartDialog, aeShowShutdownDlg:
		return QuitLogout
	}
	return QuitDock
}

// QuitEventReason is quitEventReason for an NSAppleEventDescriptor (tests).
func QuitEventReason(ev uintptr) QuitReason { return quitEventReason(objc.ID(ev)) }

// refuseQuitEvent puts userCanceledErr in a quit Apple event's reply: the
// sender (loginwindow for a logout) learns that the app refused.
func refuseQuitEvent(reply objc.ID) {
	if reply != 0 {
		objc.Send2(reply, selSetParamDesc, uintptr(objc.Send1(clsAEDesc(), selDescInt32, 0xFFFFFF80 /* userCanceledErr as an SInt32 */)), aeErrorNumber)
	}
	lastQuitEventReply = userCanceledErr
}

func clsAEDesc() objc.ID { return objc.Cls("NSAppleEventDescriptor") }

// suspendQuitEvent suspends the quit Apple event being handled until the
// answer of the quit being decided (resumeQuitEvents). With too many waiting
// already it is refused at once.
func suspendQuitEvent(reply objc.ID) {
	if appObj.nsusp == len(appObj.susp) {
		refuseQuitEvent(reply)
		return
	}
	appObj.susp[appObj.nsusp] = uintptr(objc.Send(aeManager(), selSuspendAE))
	appObj.nsusp++
}

// resumeQuitEvents answers the suspended quit Apple events, each once:
// refused, the reply carries userCanceledErr.
func resumeQuitEvents(allow bool) {
	if appObj.nsusp == 0 {
		return
	}
	m := aeManager()
	for i := 0; i < appObj.nsusp; i++ {
		id := appObj.susp[i]
		appObj.susp[i] = 0
		if allow {
			lastQuitEventReply = 0
		} else {
			refuseQuitEvent(objc.Send1(m, selReplyForSusp, id))
		}
		objc.Send1(m, selResumeSusp, id)
	}
	appObj.nsusp = 0
}

// SuspendedQuitEvents counts the quit Apple events waiting for an answer
// (tests).
func SuspendedQuitEvents() int { return appObj.nsusp }

// commitQuit: the quit for reason was allowed. The events that waited are
// answered yes, and the quit is committed: until the termination it is not
// asked again, and a later request does not replace it (sendTermination).
func commitQuit(reason QuitReason) {
	resumeQuitEvents(true)
	appObj.quitOutside = false
	quitState.phase, quitState.reason = quitCommitted, reason
}

// refuseQuit: the quit was refused. The events that waited are answered no.
func refuseQuit() {
	resumeQuitEvents(false)
	appObj.quitOutside = false
}

// lastQuitEventReply is the error the last answered quit Apple event got: 0
// none (allowed), userCanceledErr refused; 1 before any.
var lastQuitEventReply = 1

// LastQuitEventReply is the error number put in the reply of the last quit
// Apple event the app answered: 0 when allowed, -128 (userCanceledErr) when
// refused, 1 before any (tests).
func LastQuitEventReply() int { return lastQuitEventReply }

// appShouldTerminate is the quit policy for a terminate: the app did not
// send itself (its own routes and the quit Apple event decide without it and
// send it allowed: answered "now"): the request is delivered at once (class
// 1); an answer given meanwhile is returned directly, and only without one is
// the answer deferred (NSTerminateLater, then ReplyQuit sends it).
func appShouldTerminate(c *ffi.CB) {
	r := shouldTerminate()
	lastShouldTerminate = r
	c.Ret(uintptr(r))
}

// lastShouldTerminate is what applicationShouldTerminate: last returned, -1
// before it was called.
var lastShouldTerminate = -1

// LastShouldTerminate is what the delegate's applicationShouldTerminate: last
// returned: 0 NSTerminateCancel, 1 NSTerminateNow, 2 NSTerminateLater, -1
// not called yet (tests).
func LastShouldTerminate() int { return lastShouldTerminate }

func shouldTerminate() int {
	if appObj.terminating || quitState.phase == quitCommitted {
		// Our own terminate: (an allowed quit), or someone else's while the
		// allowed quit is on its way: it ends as the app allowed.
		appObj.reason = 0
		return terminateNow
	}
	if quitState.phase != quitIdle { // a quit is already being decided
		return terminateCancel
	}
	reason := appObj.reason
	appObj.reason = 0
	if reason == 0 {
		reason = appleEventQuitReason()
	}
	if dispatch.held {
		// A native loop entered from Build: the request is asked after the
		// frame (deferQuit). Not NSTerminateLater: AppKit would wait for the
		// answer inside this terminate:, so inside the frame, which could
		// then never end and deliver the request.
		deferQuit(reason)
		return terminateCancel
	}
	if dispatch.sink == nil {
		// No application object receives the request (a window opened with
		// Open alone): the window's owner decides, as with its close button.
		if w := theWindow.Load(); w != nil {
			w.q.push(Event{Kind: EvClose, Time: Now()})
			return terminateCancel
		}
		return terminateNow
	}
	switch beginQuit(reason) {
	case 1:
		commitQuit(reason)
		return terminateNow
	case 2:
		refuseQuit()
		return terminateCancel
	}
	appObj.quitOutside = false // terminate: waits for the answer (osReplyQuit)
	return terminateLater
}

// appleEventQuitReason tells a quit Apple event's reason: a logout, restart or
// shutdown carries kAEQuitReason; without it, it is the Dock (or another app).
// Without a quit Apple event, terminate: came from some other caller.
func appleEventQuitReason() QuitReason {
	ev := objc.Send(objc.Send(objc.Cls("NSAppleEventManager"), selSharedAEManager), selCurrentAppleEvent)
	if ev == 0 || uint32(objc.Send(ev, selEventClass)) != aeCoreClass || uint32(objc.Send(ev, selEventID)) != aeQuit {
		return QuitOther
	}
	return quitEventReason(ev)
}

// appWillTerminate is the last event. Before it, every sheet is ended (each
// completion runs once: a dialog of ours delivers its cancelled result if the
// user did not answer it) and every result and event already accepted is
// delivered, so EvWillTerminate is the last thing the app sees.
func appWillTerminate(*ffi.CB) {
	pool := objc.F.Call0(objc.PoolPush)
	if w := theWindow.Load(); w != nil {
		w.abortDialogs()
	}
	endSheets()
	objc.F.Call1(objc.PoolPop, pool)
	FlushApp()
	deliverNow(AppEvWillTerminate, 0, 0, 0, nil)
}

// appReopen is applicationShouldHandleReopen:hasVisibleWindows:: EvReopen,
// and NO, so that AppKit does nothing of its own.
func appReopen(c *ffi.CB) {
	var vis int64
	if byte(c.Int(3)) != 0 {
		vis = 1
	}
	deliverNow(AppEvReopen, 0, vis, 0, nil)
	c.RetBool(false)
}

// appOpenURLs is application:openURLs:: file URLs become AppEvOpenFiles (the
// paths), the others AppEvOpenURLs.
func appOpenURLs(c *ffi.CB) {
	urls := objc.ID(c.Int(3))
	n := int(objc.Send(urls, selURLCount))
	for _, files := range [2]bool{true, false} {
		buf := beginScratch()
		count := 0
		for i := 0; i < n; i++ {
			u := objc.Send1(urls, selURLAt, uintptr(i))
			if objc.SendBool(u, selIsFileURL) != files {
				continue
			}
			if count > 0 {
				buf = append(buf, 0)
			}
			if files {
				buf = objc.AppendUTF8(buf, objc.Send(u, selPath))
			} else {
				buf = objc.AppendUTF8(buf, objc.Send(u, selAbsoluteString))
			}
			count++
		}
		if count > 0 {
			kind := AppEvOpenURLs
			if files {
				kind = AppEvOpenFiles
			}
			deliverScratch(kind, int64(count), buf)
		} else {
			endScratch(buf)
		}
	}
}

// appGetURL is the kAEGetURL Apple event handler: AppEvOpenURLs.
func appGetURL(c *ffi.CB) {
	ev := objc.ID(c.Int(2))
	s := objc.Send(objc.Send1(ev, selParamDescriptor, aeDirectObject), selStringValue)
	if s == 0 {
		return
	}
	deliverScratch(AppEvOpenURLs, 1, objc.AppendUTF8(beginScratch(), s))
}

// beginScratch returns the reusable data buffer of the current nesting depth.
func beginScratch() []byte {
	d := min(appObj.depth, len(appObj.scratch)-1)
	appObj.depth++
	return appObj.scratch[d][:0]
}

func endScratch(buf []byte) {
	appObj.depth--
	d := min(appObj.depth, len(appObj.scratch)-1)
	appObj.scratch[d] = buf[:0]
}

func deliverScratch(kind AppEventKind, count int64, buf []byte) {
	deliverNow(kind, 0, count, 0, buf)
	endScratch(buf)
}

// appPerform is the run-loop source's perform callback (main thread): a
// pending Wake is handed to the window and the pump, then the app's events
// are dispatched.
func appPerform(*ffi.CB) {
	if takeWake() {
		pushWake()
	}
	DispatchApp()
}

// pushWake gives a wake to the window (EvWake) and unblocks the pump: the
// application-defined event makes nextEventMatchingMask return. If a native
// loop dequeues that event instead, our pump was not blocked anyway.
func pushWake() {
	appObj.woke = true
	if w := theWindow.Load(); w != nil {
		w.q.push(Event{Kind: EvWake, Time: Now()})
	}
	objc.Send2(appObj.app, selPostEvent, uintptr(appObj.wakeEv), 0)
}

// osSignalApp signals the run-loop source from any thread. The calls need a
// frame that does not move and that no other thread uses meanwhile: one of a
// few, taken with a bit each.
func osSignalApp() {
	src := appObj.source.Load()
	if src == 0 {
		return // StartApp signals once it made the source
	}
	for {
		b := sigFrameBusy.Load()
		free := ^b & (1<<len(sigFrames) - 1)
		if free == 0 {
			runtime.Gosched()
			continue
		}
		i := bits.TrailingZeros32(free)
		if !sigFrameBusy.CompareAndSwap(b, b|1<<i) {
			continue
		}
		f := &sigFrames[i]
		f.Call1(cfFuncs.signal, src)
		f.Call1(cfFuncs.wakeUp, appObj.mainLoop)
		sigFrameBusy.And(^uint32(1 << i))
		return
	}
}

var (
	sigFrames    [4]ffi.Frame
	sigFrameBusy atomic.Uint32
)

// osWake wakes the main loop: through the run-loop source, which hands the
// wake to the window and the pump (appPerform).
func osWake() { osSignalApp() }

// WaitApp is the app-wide pump with no window open: it blocks until an event,
// a wake or the timeout (ns; negative: none), dispatching the events AppKit
// has (menus, key equivalents, Apple events), and reports whether a wake
// arrived. Main thread; not with a window open (its Wait is the pump then).
func WaitApp(timeoutNs int64) bool {
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	until := appObj.distantFuture
	switch {
	case appObj.woke || timeoutNs == 0:
		until = appObj.distantPast
	case timeoutNs > 0:
		until = objc.ID(F.Reset().Arg(uintptr(clsNSDate)).Arg(uintptr(selDateSinceNow)).
			ArgF64(float64(timeoutNs) / 1e9).Call(objc.MsgSend).R0())
	}
	for ev := appNextEvent(until); ev != 0; ev = appNextEvent(appObj.distantPast) {
		if uintptr(objc.Send(ev, selType)) == 15 && uintptr(objc.Send(ev, selSubtype))&0xFFFF == wakeSubtype {
			if takeWake() {
				appObj.woke = true
			}
			continue
		}
		objc.Send1(appObj.app, selSendEvent, uintptr(ev))
	}
	F.Call1(objc.PoolPop, pool)
	woke := appObj.woke
	appObj.woke = false
	return woke
}

func appNextEvent(until objc.ID) objc.ID {
	return objc.Send4(appObj.app, selNextEvent, ^uintptr(0), uintptr(until), uintptr(defaultMode), 1)
}

// Terminate asks the app to quit for reason through the quit policy
// (AppEvQuitRequested), as ⌘Q does; for a quit already allowed (committed)
// it sends the termination. Main thread, from the app's loop: never from
// inside the dispatcher or a Build. Nothing happens while a quit request is
// being decided. With an allowed quit, AppKit ends the process after
// AppEvWillTerminate.
func Terminate(reason QuitReason) {
	if quitState.phase == quitCommitted {
		if dispatch.held {
			deferQuit(reason)
			return
		}
		sendTermination(0)
		return
	}
	sendTerminate(reason, 0)
}

// requestTerminate is the menu's Quit (gnuiQuit:): the quit policy, as for
// AppKit's own Quit item.
func requestTerminate(sender objc.ID) { sendTerminate(QuitMenu, sender) }

// sendTerminate is the app's own quit routes (⌘Q, Terminate): the policy is
// asked here, without terminate:, and terminate: is sent only once the quit
// is allowed, after ending the open sheets as cancelled. AppKit's terminate:
// would end every sheet before it asks the delegate, even when the quit is
// then refused, an alert with its first button (observed on macOS 26; with a
// plain window as the sheet it returns without asking at all); and its
// "later" answer runs a wait of AppKit's own, nested in the app's loop. The
// quit Apple event (the Dock, a logout) takes the same way (appQuitEvent).
// Only a terminate: someone else sends reaches the delegate's policy
// (shouldTerminate). Once a quit is committed the app's loop sends its
// termination (Terminate); another ⌘Q then does nothing.
func sendTerminate(reason QuitReason, sender objc.ID) {
	if !appObj.started || quitState.phase != quitIdle {
		return
	}
	if dispatch.held { // ⌘Q in a native loop entered from Build: asked after the frame
		deferQuit(reason)
		return
	}
	quitWithoutTerminate(reason)
}

// quitWithoutTerminate asks the quit policy what terminate: would ask
// (sendTerminate): allowed, terminate: follows at once (sendTermination);
// deferred, it follows the app's allowing answer (osReplyQuit, then the
// app's loop).
func quitWithoutTerminate(reason QuitReason) {
	if dispatch.sink == nil {
		// No application object (a window opened with Open alone): the
		// window's owner decides, as with its close button.
		if w := theWindow.Load(); w != nil {
			w.q.push(Event{Kind: EvClose, Time: Now()})
			return
		}
		commitQuit(reason)
		sendTermination(0)
		return
	}
	switch beginQuit(reason) {
	case 0:
		appObj.quitOutside = true
	case 1:
		commitQuit(reason)
		sendTermination(0)
	default:
		refuseQuit()
	}
}

// sendTermination sends terminate: for the committed quit, after ending the
// sheets as cancelled: answered "now", it ends the process after
// AppEvWillTerminate. If AppKit returns instead (it terminates nothing while
// a modal session runs), the quit is over and a later request is asked anew.
func sendTermination(sender objc.ID) {
	if quitState.phase != quitCommitted {
		return
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	endSheets()
	appObj.terminating, appObj.reason = true, quitState.reason
	objc.Send1(appObj.app, selTerminate, uintptr(sender))
	appObj.terminating, appObj.reason = false, 0
	if quitState.phase == quitCommitted {
		quitState.phase = quitIdle
	}
}

// endSheets ends the sheets attached to the app's windows, as cancelled
// (NSModalResponseAbort): the quit was allowed.
func endSheets() {
	wins := objc.Send(appObj.app, selAppWindows)
	for i, n := 0, int(objc.Send(wins, selURLCount)); i < n; i++ {
		w := objc.Send1(wins, selURLAt, uintptr(i))
		if sheet := objc.Send(w, selAttachedSheet); sheet != 0 {
			objc.Send2(w, selEndSheet, uintptr(sheet), modalResponseAbort)
		}
	}
}

func osReplyQuit(allow bool) {
	if appObj.quitOutside { // no terminate: is waiting for the answer
		if !allow {
			refuseQuit()
			return
		}
		// terminate: is sent from the app's loop (TakeQuitRequest, then
		// Terminate), not from inside the handler that answered.
		commitQuit(quitState.reason)
		dispatch.quitAsked.Store(int64(quitState.reason))
		Wake()
		return
	}
	// A terminate: someone else sent waits (NSTerminateLater).
	var v uintptr
	if allow {
		commitQuit(quitState.reason)
		v = 1
	} else {
		refuseQuit()
	}
	objc.Send1(appObj.app, selReplyTerminate, v)
}

// osEndApp ends the application object's quit state before Main returns:
// every suspended quit Apple event is answered yes (the app is ending), a
// terminate: someone else sent that still waits is answered no (so that its
// wait returns to the app's loop), and the open dialogs and sheets end as
// cancelled, their results delivered by the drain that follows (EndApp).
func osEndApp() {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	waiting := quitState.phase == quitLater && !appObj.quitOutside
	resumeQuitEvents(true)
	appObj.quitOutside = false
	if waiting {
		objc.Send1(appObj.app, selReplyTerminate, 0)
	}
	if w := theWindow.Load(); w != nil {
		w.abortDialogs()
	}
	if appObj.started {
		endSheets()
	}
}

// AppActivationPolicy is the activation policy NSApp has now (tests).
func AppActivationPolicy() Policy {
	return Policy(objc.Send(appObj.app, selActivationPolicy))
}

// SetDockMenu makes menu idx of spec the Dock menu (applicationDockMenu:);
// nil spec removes it. Its commands arrive as AppEvCommand whether or not a
// window is open. state gives each command's enabled and checked state when
// AppKit shows the menu (nil: enabled, unchecked). Main thread, at a safe
// point; building it allocates.
func SetDockMenu(spec *MenuSpec, idx int, state MenuState) {
	appObj.dockState = state
	if appObj.dockMenu != 0 {
		objc.Release(appObj.dockMenu)
		appObj.dockMenu = 0
	}
	if spec == nil || idx < 0 || idx >= len(spec.Menus) || !appObj.started {
		return
	}
	ensureMenuTarget()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	appObj.dockMenu = buildMenu(spec, idx, 0, nil)
	retargetDock(appObj.dockMenu, 0)
}

// retargetDock points the command items of a Dock menu (and its submenus) at
// the app delegate, so their commands are the app's, not a window's.
func retargetDock(m objc.ID, depth int) {
	if depth > maxMenuDepth {
		return
	}
	n := int(objc.Send(m, selDockItems))
	for i := 0; i < n; i++ {
		it := objc.Send1(m, selDockItemAt, uintptr(i))
		if objc.SendBool(it, selHasSubmenu) {
			retargetDock(objc.Send(it, selSubmenu), depth+1)
			continue
		}
		if objc.SEL(objc.Send(it, selDockAction)) == selGnuiCommand {
			objc.Send1(it, selDockSetTarget, uintptr(appObj.delegate))
			objc.Send1(it, selDockSetAction, uintptr(selDockCommand))
		}
	}
}

// dockValidate is the delegate's validateMenuItem: for the Dock menu's
// commands (the delegate is their target): the state SetDockMenu was given
// enables and checks them.
func dockValidate(c *ffi.CB) {
	item := objc.ID(c.Int(2))
	if appObj.dockState == nil || item == 0 {
		c.RetBool(true)
		return
	}
	en, ck := appObj.dockState(uint16(objc.Send(item, selDockTag)), MenuNone)
	var st uintptr
	if ck {
		st = 1 // NSControlStateValueOn
	}
	objc.Send1(item, selDockSetState, st)
	c.RetBool(en)
}

// DockMenu is the Dock menu as AppKit asks for it (applicationDockMenu:), 0
// for none (tests).
func DockMenu() uintptr {
	return uintptr(objc.Send1(appObj.delegate, selAppDockMenu, uintptr(appObj.app)))
}

// appCommand delivers a menu command that no window can take (none is open)
// to the app (AppEvCommand).
func appCommand(id uintptr) { deliverNow(AppEvCommand, uint64(id), 0, 0, nil) }

// AppDelegate is the GNUIAppDelegate instance (tests, services that hang
// methods on the application's delegate).
func AppDelegate() uintptr { return uintptr(appObj.delegate) }

// BundleID is the main bundle's CFBundleIdentifier, "" when the app is not
// bundled. Main thread.
func BundleID() string {
	b := objc.Send(objc.Cls("NSBundle"), objc.Sel("mainBundle"))
	return string(objc.AppendUTF8(nil, objc.Send(b, objc.Sel("bundleIdentifier"))))
}

// NativeLoop reports whether AppKit runs a loop of its own on the main
// thread now (a live resize, menu tracking, the wait for a quit answer), so
// the app's loop is suspended and the frame handler draws. Main thread.
func NativeLoop() bool {
	w := theWindow.Load()
	return quitWaitNative() || menuTracking() || (w != nil && w.live)
}

// quitWaitNative reports whether AppKit runs its own loop waiting for a quit
// answer (NSTerminateLater): not for a quit decided without terminate:,
// during which the app's loop runs on.
func quitWaitNative() bool { return QuitPending() && !appObj.quitOutside }
