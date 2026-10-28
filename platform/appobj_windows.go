package platform

import (
	"errors"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// The application object on Windows (D37, built for Windows by the phase 8
// plan's D68). The delivery classes and the quit policy are appobj.go's; the
// dispatcher runs from the app's loop after every pump, and from the wake
// message while a native modal loop suspends that loop. The hidden app window
// (registry_windows.go) keeps the app running with no window open, so
// WaitApp waits there, and answers the session's end:
//
//   - WM_QUERYENDSESSION is a quit request (QuitLogout). An answer given
//     during it is returned at once (TRUE lets the session end, FALSE
//     refuses). Without one the session is told to wait: FALSE, with
//     ShutdownBlockReasonCreate giving AppOptions.QuitBlockReason, which
//     Windows shows beside the app while the user decides; the app's answer
//     then removes the reason, and an allowed quit ends the app's loop.
//     Windows gives an app about 5 s before it offers to end it; that time is
//     the system's.
//   - WM_ENDSESSION with the session ending is the last moment: what was
//     accepted is delivered and AppEvWillTerminate follows before the window
//     procedure returns, after which Windows may end the process.

var (
	ole32          uintptr
	pOleInitialize uintptr
)

// StartApp applies the options, makes the hidden app window and makes the
// calling thread (the main thread) an OLE single-threaded apartment, which
// RichEdit's callbacks, file dialogs, drag and drop and WebView2 need
// (phase 8's standing rules; P1 measured about 2 ms cold).
func StartApp(o AppOptions) error {
	startCommon(o)
	setDPIAwareness()
	if !ensureAppWindow() {
		return errors.New("platform: cannot create the app window")
	}
	if tid := uint32(win32.Call(pGetCurrentThreadID)); reg.app.oleThread != tid {
		if ole32 == 0 {
			ole32 = win32.LoadSystem("ole32.dll")
			pOleInitialize = win32.Proc(ole32, "OleInitialize")
		}
		if pOleInitialize != 0 && win32.HR(win32.Call(pOleInitialize, 0)) >= 0 { // S_OK, or S_FALSE when already one
			reg.app.oleThread = tid
		}
	}
	return nil
}

// AppLaunched does nothing here: the session's messages reach the app window
// from its start.
func AppLaunched() {}

// osSignalApp gives the dispatcher a turn: the loop's, or, inside a native
// modal loop, the wake message's. No window is told (WakeLoop).
func osSignalApp() { WakeLoop() }

// WaitApp is the pump with no window open (D37, D68): it blocks until the app
// window handles something (a wake, an app event's signal, a service's
// message, the session's end) or the timeout passes (ns; 0 polls, negative
// waits without one), and reports whether a wake came. Main thread.
func WaitApp(timeoutNs int64) bool {
	if reg.app.hwnd == 0 && !ensureAppWindow() {
		return false
	}
	return pump(timeoutNs)
}

// Terminate runs the quit policy for reason: AppEvQuitRequested, and an
// allowed quit ends the app's loop (QuitApproved).
func Terminate(reason QuitReason) {
	if quitState.phase != quitIdle {
		return
	}
	if beginQuit(reason) == 1 {
		quitState.approved.Store(true)
		Wake()
	}
}

// osReplyQuit sends a deferred answer: a logout that waits for it has its
// block reason removed (the user's choice in Windows' screen goes on), and an
// allowed quit ends the app's loop.
func osReplyQuit(allow bool) {
	unblockShutdown()
	if allow {
		quitState.approved.Store(true)
		Wake()
	}
}

// osEndApp removes a logout's block reason that is still up: the app ends.
func osEndApp() { unblockShutdown() }

// queryEndSession answers WM_QUERYENDSESSION (lParam: ENDSESSION_LOGOFF and
// the others): a quit request, QuitLogout, answered at once when the app
// answers during it, else blocked with the app's reason until it does.
func queryEndSession(lp uintptr) uintptr {
	reg.app.got = true
	if quitState.approved.Load() || reg.app.ending {
		return 1
	}
	if quitState.phase != quitIdle { // a request waits for its answer already, or is being asked
		blockShutdown()
		return 0
	}
	switch beginQuit(QuitLogout) {
	case 1:
		quitState.approved.Store(true)
		reg.app.ending = true
		Wake()
		return 1
	case 2:
		return 0
	}
	blockShutdown()
	return 0
}

// endSession handles WM_ENDSESSION: the session ends (ending) or the logout
// was cancelled. A request still waiting for the app's answer keeps waiting
// after a cancel; an allowed answer then quits the app.
func endSession(ending bool) {
	reg.app.got = true
	unblockShutdown()
	if !ending {
		reg.app.ending = false
		return
	}
	quitState.approved.Store(true)
	Wake()
	FlushApp()
	deliverNow(AppEvWillTerminate, 0, 0, 0, nil)
}

// defaultBlockReason is shown while a logout waits for the app's answer and
// AppOptions.QuitBlockReason is empty.
const defaultBlockReason = "Saving changes"

// blockShutdown tells Windows why the app keeps the session from ending
// (ShutdownBlockReasonCreate), once.
func blockShutdown() {
	if reg.app.blockReason || reg.app.hwnd == 0 || pShutdownBlockCreate == 0 {
		return
	}
	reason := quitState.opts.QuitBlockReason
	if reason == "" {
		reason = defaultBlockReason
	}
	u, err := syscall.UTF16FromString(reason)
	if err != nil {
		u, _ = syscall.UTF16FromString(defaultBlockReason)
	}
	reg.app.reasonText = u
	if win32.Bool(win32.Call(pShutdownBlockCreate, reg.app.hwnd, uintptr(unsafe.Pointer(&reg.app.reasonText[0])))) {
		reg.app.blockReason = true
	}
}

// unblockShutdown removes the reason blockShutdown gave.
func unblockShutdown() {
	if !reg.app.blockReason {
		return
	}
	reg.app.blockReason = false
	if reg.app.hwnd != 0 && pShutdownBlockDestroy != 0 {
		win32.Call(pShutdownBlockDestroy, reg.app.hwnd)
	}
}

// SetDockMenu does nothing here (no Dock; the taskbar's jump list is not
// built yet).
func SetDockMenu(spec *MenuSpec, idx int, state MenuState) {}

// SetAppMenuBar does nothing here (no native menu bar).
func SetAppMenuBar(spec *MenuSpec, state MenuState) {}

// AppActivationPolicy is PolicyRegular here.
func AppActivationPolicy() Policy { return PolicyRegular }

// BundleID is the app bundle's identifier; "" here.
func BundleID() string { return "" }

// NativeLoop reports whether a native modal loop runs on the main thread now
// (a window's size/move loop, menu tracking), so the app's loop is suspended
// and the windows draw through their frame handlers. Main thread.
func NativeLoop() bool { return reg.app.modal > 0 }

// ArmOnWake reports whether what reaches the app outside its loop asks for
// the frame that draws it: inside a native modal loop, whose tick draws only
// the windows that asked for a frame.
func ArmOnWake() bool { return NativeLoop() }
