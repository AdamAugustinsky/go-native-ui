package platform

import (
	"errors"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// The window registry on Windows (phase 8 W1, D68; D30 ported): every open
// window, in the order they opened, the app-wide pump that serves them, and
// the hidden app window that owns what is the app's rather than a window's.
//
// One app-wide pump waits on every window's frame-latency waitable plus the
// message queue in one MsgWaitForMultipleObjectsEx. That call takes at most
// MAXIMUM_WAIT_OBJECTS-1 = 63 handles besides the queue (winbase.h), which
// is the budget: MaxWindows windows, the wake event, the appearance watcher
// and one spare. Messages are routed by HWND (wndProc looks the window up),
// so each window's events go to its own queue; a window's frame is asked for
// and delivered on its own, so an idle window neither draws nor holds up
// another.
//
// Wakes are messages posted to the app window, not an event in the wait set:
// a native modal loop (the size/move loop, menu tracking, a file dialog)
// dispatches them, so a wake or an app event reaches the app while its own
// loop is suspended (D30's rule, on Windows). The appearance watcher keeps
// its goroutine and wakes the loop. The waitWake and waitAppearance slots
// therefore stay empty (W1 note in docs/PHASE8-PLAN.md).

// MaxWindows is how many windows can be open at once on Windows (the wait
// budget above; macOS allows 64).
const MaxWindows = 60

// Wait handles besides the windows' waitables (indices into waitSet.h after
// the windows).
const (
	waitWake       = iota // the wake event (osWake from any thread)
	waitAppearance        // the registry watcher of the system appearance
	waitSpare             // reserved (capture's or a service's event)
	numAppHandles
)

// maxWaitHandles is the whole budget given to MsgWaitForMultipleObjectsEx.
const maxWaitHandles = MaxWindows + numAppHandles

// winRegistry is every open window, in the order they opened, and the
// hidden app window (D68).
type winRegistry struct {
	slots [MaxWindows]*Window // by slot; nil: free
	open  [MaxWindows]*Window // the open windows, in the order they opened
	n     int
	gen   uint32 // the last generation given
	app   appWindow

	creating *Window // the window inside its CreateWindowExW (no HWND yet)
	focus    *Window // the window that had the keyboard focus last
	set      waitSet // the pump's handles, refilled every wait
}

// reg is the process's registry. Main thread.
var reg winRegistry

// appWindow is the window that is never shown (D68): a top-level WS_POPUP
// with WS_EX_TOOLWINDOW, so it is in neither the taskbar nor Alt+Tab, and,
// unlike a message-only window, receives broadcasts. It owns the wake
// message, the tray's and hotkeys' messages, WM_QUERYENDSESSION and
// WM_ENDSESSION, TaskbarCreated and the second instance's hand-off, so the
// app runs with zero windows.
type appWindow struct {
	hwnd           uintptr
	thread         uint32 // the thread that made it (its messages go to that thread's queue)
	taskbarCreated uint32 // RegisterWindowMessageW("TaskbarCreated")
	ending         bool   // between WM_QUERYENDSESSION allowed and WM_ENDSESSION
	blockReason    bool   // ShutdownBlockReasonCreate is in effect
	oleThread      uint32 // the thread OleInitialize succeeded on (0: none)
	got            bool   // it handled something the app's loop must see: the pump returns
	modal          int    // native modal loops running now (enterModal)
	pumps, wakes   uint64 // PumpStats
	wc             wndClassEx
	reasonText     []uint16 // the shutdown block reason, NUL-terminated (kept for the call)
}

// waitSet is the handle array of one wait and which window each handle is
// for, rebuilt for every wait from the windows that asked for a frame.
type waitSet struct {
	h   [maxWaitHandles]uintptr
	win [maxWaitHandles]*Window // nil for the app handles
	n   int
}

// errTooManyWindows is Open's error past MaxWindows.
var errTooManyWindows = errors.New("platform: too many windows open")

// add puts w in the first free slot and at the end of the open order. It
// returns false when MaxWindows are open.
func (r *winRegistry) add(w *Window) (slot int, ok bool) {
	if r.n == MaxWindows {
		return -1, false
	}
	for i := range r.slots {
		if r.slots[i] == nil {
			r.slots[i] = w
			r.open[r.n] = w
			r.n++
			r.gen++
			return i, true
		}
	}
	return -1, false
}

// remove takes w out of the registry, keeping the others' order.
func (r *winRegistry) remove(w *Window) {
	for i := range r.slots {
		if r.slots[i] == w {
			r.slots[i] = nil
		}
	}
	if r.focus == w {
		r.focus = nil
	}
	for i := 0; i < r.n; i++ {
		if r.open[i] == w {
			copy(r.open[i:r.n], r.open[i+1:r.n])
			r.n--
			r.open[r.n] = nil
			return
		}
	}
}

// byHWND finds the window of a top-level HWND, nil for none (the app
// window, a dialog, another process's).
func (r *winRegistry) byHWND(hwnd uintptr) *Window {
	for i := 0; i < r.n; i++ {
		if r.open[i].hwnd == hwnd {
			return r.open[i]
		}
	}
	return nil
}

// fill rebuilds s from the open windows that wait for a frame on their
// waitable, then the app's handles (0 entries are skipped). It allocates
// nothing.
func (r *winRegistry) fill(s *waitSet, app [numAppHandles]uintptr) {
	s.n = 0
	for i := 0; i < r.n; i++ {
		if w := r.open[i]; w.waitable != 0 && w.frameArmed {
			s.h[s.n], s.win[s.n] = w.waitable, w
			s.n++
		}
	}
	for _, h := range app {
		if h != 0 {
			s.h[s.n], s.win[s.n] = h, nil
			s.n++
		}
	}
}

// openWindows is the open windows, in the order they opened. The slice is
// the registry's: it changes when a window opens or closes. Main thread.
func openWindows() []*Window { return reg.open[:reg.n] }

// OpenCount is how many windows are open. Main thread.
func OpenCount() int { return reg.n }

// firstWindow is the window that had the focus last, else the first one
// opened; nil with none open (the clipboard's owner).
func firstWindow() *Window {
	if reg.focus != nil {
		return reg.focus
	}
	if reg.n > 0 {
		return reg.open[0]
	}
	return nil
}

// ---- The app window -----------------------------------------------------------

const (
	wsPopup          = 0x80000000
	wsExToolWindow   = 0x00000080
	wmQueryEndSess   = 0x0011
	wmEndSession     = 0x0016
	wmShowWindow     = 0x0018
	wmEnterMenuLoop  = 0x0211
	wmExitMenuLoop   = 0x0212
	wmAppGone        = 0x8000 + 3 // WM_APP+3: an app window a thread before this one made, to destroy on its own thread
	modalTimerID     = 2
	modalTimerMs     = 16 // the frame handler's tick inside a native modal loop
	endSessionLogoff = 0x80000000
)

var (
	appClassUTF16      = mustUTF16("GoNativeUIApp")
	appWndProcCallback = uintptr(0) // made with the first app window
	taskbarCreatedName = mustUTF16("TaskbarCreated")

	pGetCurrentThreadID   = win32.Proc(kernel32, "GetCurrentThreadId")
	pRegisterWindowMsg    = win32.Proc(user32, "RegisterWindowMessageW")
	pSendMessageW         = win32.Proc(user32, "SendMessageW")
	pShutdownBlockCreate  = win32.Proc(user32, "ShutdownBlockReasonCreate")
	pShutdownBlockDestroy = win32.Proc(user32, "ShutdownBlockReasonDestroy")
)

// ensureAppWindow makes the app window on the calling thread if it has none
// there: the first Open or StartApp makes it. A test that opens windows on
// another thread with none open gets a new one on its thread (Win32 sends a
// window's messages to the thread that made it). It reports false when the
// window cannot be made: the app then runs without it (no windowless
// running, no session-end answers), and wakes go nowhere until a later try.
func ensureAppWindow() bool {
	tid := uint32(win32.Call(pGetCurrentThreadID))
	if reg.app.hwnd != 0 {
		if reg.app.thread == tid || reg.n > 0 {
			return true
		}
		destroyAppWindow() // a thread before this one made it, and no window of it is open
	}
	if pCreateWindowExW == 0 {
		return false
	}
	hinst := win32.Call(pGetModuleHandleW, 0)
	if appWndProcCallback == 0 {
		appWndProcCallback = syscall.NewCallback(appWndProc)
		reg.app.wc = wndClassEx{
			wndProc:   appWndProcCallback,
			instance:  hinst,
			className: appClassUTF16,
		}
		reg.app.wc.size = uint32(unsafe.Sizeof(reg.app.wc))
		if win32.Call(pRegisterClassExW, uintptr(unsafe.Pointer(&reg.app.wc)))&0xFFFF == 0 {
			return false
		}
		if pRegisterWindowMsg != 0 {
			reg.app.taskbarCreated = uint32(win32.Call(pRegisterWindowMsg, uintptr(unsafe.Pointer(taskbarCreatedName))))
		}
	}
	// Never shown: WS_POPUP with no WS_VISIBLE, WS_EX_TOOLWINDOW. A top-level
	// window (no HWND_MESSAGE parent) receives the broadcasts.
	h := win32.Call(pCreateWindowExW, wsExToolWindow, uintptr(unsafe.Pointer(appClassUTF16)), 0, wsPopup,
		0, 0, 0, 0, 0, 0, hinst, 0)
	if h == 0 {
		return false
	}
	reg.app.hwnd, reg.app.thread = h, tid
	wakeTarget.Store(h)
	if wakePending.Load() || loopWake.Load() || anyWindowWake() {
		postWake() // a wake asked for before there was a window to take it
	}
	return true
}

// destroyAppWindow destroys the app window (a thread change in tests; the
// process's end otherwise takes it). Only the thread that made a window may
// destroy it: from another thread it is posted wmAppGone, which destroys it
// if that thread pumps again, and it is forgotten here either way.
func destroyAppWindow() {
	if reg.app.hwnd == 0 {
		return
	}
	if reg.app.modal > 0 {
		win32.Call(pKillTimer, reg.app.hwnd, modalTimerID)
	}
	if reg.app.blockReason && pShutdownBlockDestroy != 0 {
		win32.Call(pShutdownBlockDestroy, reg.app.hwnd)
	}
	h, owner := reg.app.hwnd, reg.app.thread
	wakeTarget.Store(0)
	reg.app.hwnd, reg.app.thread, reg.app.modal, reg.app.blockReason, reg.app.ending = 0, 0, 0, false, false
	if owner == uint32(win32.Call(pGetCurrentThreadID)) {
		win32.Call(pDestroyWindow, h)
	} else {
		win32.Call(pPostMessageW, h, wmAppGone, 0, 0)
	}
}

// AppWindow is the HWND of the hidden app window (D68), 0 before the first
// window or StartApp made it: the owner of the tray icon's and the global
// hotkeys' messages (HandleAppMessage). Main thread.
func AppWindow() uintptr { return reg.app.hwnd }

// AppMessageFunc handles one message sent or posted to the app window and
// returns its result. It runs on the main thread, from the pump or from a
// native modal loop.
type AppMessageFunc func(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr

// appHandlers are the services' message handlers (HandleAppMessage).
var appHandlers struct {
	msg [16]uint32
	fn  [16]AppMessageFunc
	n   int
}

// HandleAppMessage makes fn the handler of message msg sent to the app window
// (a tray icon's callback message, WM_HOTKEY), or removes it with fn nil.
// TaskbarCreatedMessage is the message the shell broadcasts once Explorer
// has restarted, for a tray icon to be added again. At most 16 handlers. Main
// thread.
func HandleAppMessage(msg uint32, fn AppMessageFunc) error {
	for i := 0; i < appHandlers.n; i++ {
		if appHandlers.msg[i] == msg {
			if fn == nil {
				appHandlers.n--
				appHandlers.msg[i], appHandlers.fn[i] = appHandlers.msg[appHandlers.n], appHandlers.fn[appHandlers.n]
				appHandlers.fn[appHandlers.n] = nil
				return nil
			}
			appHandlers.fn[i] = fn
			return nil
		}
	}
	if fn == nil {
		return nil
	}
	if appHandlers.n == len(appHandlers.msg) {
		return errors.New("platform: too many app window message handlers")
	}
	appHandlers.msg[appHandlers.n], appHandlers.fn[appHandlers.n] = msg, fn
	appHandlers.n++
	return nil
}

// TaskbarCreatedMessage is the message the shell broadcasts when the taskbar
// is created again (Explorer restarted); 0 before the app window exists.
func TaskbarCreatedMessage() uint32 { return reg.app.taskbarCreated }

// appWndProc is the app window's procedure.
func appWndProc(hwnd, m, wp, lp uintptr) uintptr {
	switch m {
	case wmWake:
		reg.app.wakes++
		if giveWakes() {
			reg.app.got = true
		}
		if reg.app.modal > 0 {
			// The app's loop is suspended in a native loop: the dispatcher runs
			// here, and what it delivers asks for its windows' frames (ArmOnWake).
			DispatchApp()
		}
		return 0
	case wmTimer:
		if wp == modalTimerID {
			modalTick()
			return 0
		}
	case wmClose:
		// Another program asks the app to close (taskkill without /F, an
		// installer): a quit request, decided by the app's loop. An app
		// window a thread before made is not the app's any more.
		if hwnd == reg.app.hwnd {
			RequestQuit(QuitDock)
			return 0
		}
	case wmAppGone:
		if hwnd != reg.app.hwnd {
			win32.Call(pDestroyWindow, hwnd)
		}
		return 0
	case wmQueryEndSess:
		return queryEndSession(lp)
	case wmEndSession:
		endSession(wp != 0)
		return 0
	}
	for i := 0; i < appHandlers.n; i++ {
		if uintptr(appHandlers.msg[i]) == m {
			reg.app.got = true
			return appHandlers.fn[i](hwnd, uint32(m), wp, lp)
		}
	}
	return defProc(hwnd, m, wp, lp)
}

// ---- Wakes ------------------------------------------------------------------------

// loopWake is a pending WakeLoop: the loop turns once, and no window is told.
var loopWake atomic.Bool

// wakeTarget is the app window's HWND for the wake message, read from any
// thread (0 before it exists).
var wakeTarget atomic.Uintptr

// osWake posts the wake message to the app window; safe from any thread.
func osWake() { postWake() }

func postWake() {
	h := wakeTarget.Load()
	if h == 0 {
		return // ensureAppWindow posts the pending wakes
	}
	if !win32.Bool(win32.Call(pPostMessageW, h, wmWake, 0, 0)) {
		// The queue is full (or the window is gone): don't leave the
		// coalescing flags stuck, or no later wake would be posted.
		takeWake()
		loopWake.Store(false)
	}
}

// WakeLoop makes the loop turn once, from any goroutine, without telling any
// window that something changed (Wake does): for work that waits for the
// loop's safe point (a window to close) and draws nothing by itself.
func WakeLoop() {
	if loopWake.CompareAndSwap(false, true) {
		osWake()
	}
}

// Wake marks this window as having something new from another goroutine (a
// worker's result) and wakes the loop: the window gets an EvWake, the others
// do not. Any goroutine; cheap when one is pending. A closed window's mark is
// dropped.
func (w *Window) Wake() {
	if w.wakeMark.CompareAndSwap(false, true) {
		osWake()
	}
}

// anyWindowWake reports whether a window's own wake is pending.
func anyWindowWake() bool {
	for _, w := range openWindows() {
		if w.wakeMark.Load() {
			return true
		}
	}
	return false
}

// giveWakes turns the pending wakes into EvWake events: the app-wide Wake for
// every window, a window's own Wake for it. Inside a native modal loop the
// woken windows ask for a frame, which the modal tick draws through their
// frame handlers. It reports whether any wake was pending.
func giveWakes() bool {
	all := takeWake()
	any := loopWake.Swap(false) || all
	modal := reg.app.modal > 0
	for _, w := range openWindows() {
		if w.wakeMark.Swap(false) || all {
			any = true
			w.q.push(Event{Kind: EvWake, Time: Now()})
			if modal {
				w.RequestFrame()
			}
		}
	}
	return any
}

// ---- The pump ---------------------------------------------------------------------

// Pump is the app-wide event pump (D5, D30, D68): it blocks until an event of
// any window, a wake, a frame slot of any window that asked for one, a
// message for the app window or the timeout (ns; 0 polls, negative waits
// without one), hands every message to the window it is for, and returns.
// Each window's events are then taken with Events. The events Events or Wait
// handed out before are released first, so a window with events still
// queued (pushed between two pumps) does not block it. Main thread.
func Pump(timeoutNs int64) {
	pending := false
	for _, w := range openWindows() {
		w.release()
		if len(w.q.ev) > 0 {
			pending = true
		}
	}
	if pending {
		timeoutNs = 0
	}
	pump(timeoutNs)
}

// pump waits and dispatches until something arrived or the timeout passed.
// It reports whether the app window handled something (a wake, a service's
// message, the session's end).
func pump(timeoutNs int64) bool {
	reg.app.pumps++
	reg.app.got = false
	var deadline int64
	if timeoutNs > 0 {
		deadline = nanotime() + timeoutNs
	}
	for {
		now := nanotime()
		var due int64 // the soonest clipboard time limit of any window
		queued := false
		for _, w := range openWindows() {
			w.reapDrags()
			if w.clipDue != 0 && now >= w.clipDue {
				w.clipStep()
			}
			if w.clipDue != 0 && (due == 0 || w.clipDue < due) {
				due = w.clipDue
			}
			if w.frameArmed && w.waitable == 0 {
				w.frameArmed = false
				w.q.push(Event{Kind: EvFrame, Time: Now()})
			}
			queued = queued || len(w.q.ev) > 0
		}

		ms := uintptr(infinite)
		switch {
		case timeoutNs == 0 || queued || reg.app.got:
			ms = 0
		case timeoutNs > 0:
			ms = 0
			if rem := deadline - now; rem > 0 {
				ms = uintptr(min((rem+999_999)/1_000_000, infinite-1))
			}
		}
		if due != 0 { // do not sleep past the next try at the clipboard
			d := uintptr(0)
			if rem := due - now; rem > 0 {
				d = uintptr((rem + 999_999) / 1_000_000)
			}
			ms = min(ms, d)
		}
		s := &reg.set
		reg.fill(s, [numAppHandles]uintptr{})
		r := uint32(win32.Call(pMsgWait, uintptr(s.n), uintptr(unsafe.Pointer(&s.h[0])), ms, qsAllInput, mwmoInputAvail))
		switch {
		case r < uint32(s.n):
			// One waitable is signalled (the wait consumed it); the others that
			// are too are taken without waiting, so no window waits behind
			// another.
			for i := 0; i < s.n; i++ {
				w := s.win[i]
				if w == nil || !w.frameArmed {
					continue
				}
				if uint32(i) == r || uint32(win32.Call(pWaitForSingleObj, s.h[i], 0, 0)) == waitObject0 {
					w.frameArmed = false
					w.q.push(Event{Kind: EvFrame, Time: Now()})
				}
			}
		case r == waitFailed:
			// A bad handle: the windows whose waitable fails fall back to
			// immediate frames.
			for i := 0; i < s.n; i++ {
				if w := s.win[i]; w != nil && uint32(win32.Call(pWaitForSingleObj, s.h[i], 0, 0)) == waitFailed {
					w.waitable = 0
				}
			}
		}
		dispatchMessages()

		if reg.app.got || timeoutNs == 0 || (timeoutNs > 0 && nanotime() >= deadline) {
			break
		}
		if anyQueued() {
			break
		}
	}
	return reg.app.got
}

// pumpMsg is the MSG the pump reads into (not on a stack: the OS writes it).
var pumpMsg msg

// dispatchMessages dispatches the thread's queued messages, each to its
// window's procedure (wndProc routes by HWND).
func dispatchMessages() {
	for win32.Bool(win32.Call(pPeekMessageW, uintptr(unsafe.Pointer(&pumpMsg)), 0, 0, 0, pmRemove)) {
		if hostKey(&pumpMsg) { // Tab, Escape, Enter a hosted control hands back (host_windows.go)
			continue
		}
		win32.Call(pTranslateMessage, uintptr(unsafe.Pointer(&pumpMsg)))
		win32.Call(pDispatchMessageW, uintptr(unsafe.Pointer(&pumpMsg)))
	}
	syncHostFocus()
}

func anyQueued() bool {
	for _, w := range openWindows() {
		if len(w.q.ev) > 0 {
			return true
		}
	}
	return false
}

// PumpStats reports how many times the app-wide pump (Pump, Wait, WaitApp)
// has waited and how many wake messages the app window took, for a test of
// an idle app's wakeups. Main thread.
func PumpStats() (pumps, wakes uint64) { return reg.app.pumps, reg.app.wakes }

// release drops the events the last Events or Wait handed out.
func (w *Window) release() {
	if w.returned {
		w.q.dropFirst(w.nOut)
		w.returned = false
	}
}

// Events hands out the events Pump queued for the window (and any pushed
// since the last call). The slice is valid until the next Pump, Wait, Events
// or Pending. Main thread.
func (w *Window) Events() []Event {
	w.release()
	w.returned, w.nOut = true, len(w.q.ev)
	return w.q.ev
}

// ---- Native modal loops ---------------------------------------------------------

// enterModal and leaveModal bracket a native modal loop on the main thread
// (the size/move loop, menu tracking; later the file dialogs and drag out):
// while one runs, the app's loop is suspended, so a timer on the app window
// gives the windows their frames and runs their frame handlers (modalTick),
// and the wake message runs the app's dispatcher. Calls nest.
func enterModal() {
	reg.app.modal++
	if reg.app.modal == 1 && reg.app.hwnd != 0 {
		win32.Call(pSetTimer, reg.app.hwnd, modalTimerID, modalTimerMs, 0)
	}
}

func leaveModal() {
	if reg.app.modal == 0 {
		return
	}
	reg.app.modal--
	if reg.app.modal == 0 && reg.app.hwnd != 0 {
		win32.Call(pKillTimer, reg.app.hwnd, modalTimerID)
	}
}

// modalTick is the modal loop's frame tick: every window whose frame was
// asked for and whose swap chain can take one (its waitable is signalled)
// gets its EvFrame and draws through its frame handler; the clipboard's time
// limits are applied.
func modalTick() {
	now := nanotime()
	for i := 0; i < reg.n; i++ {
		w := reg.open[i]
		w.reapDrags()
		if w.clipDue != 0 && now >= w.clipDue {
			w.clipStep()
		}
		if !w.frameArmed {
			continue
		}
		if w.waitable != 0 && uint32(win32.Call(pWaitForSingleObj, w.waitable, 0, 0)) != waitObject0 {
			continue // the swap chain has no room yet: the next tick
		}
		w.frameArmed = false
		w.q.push(Event{Kind: EvFrame, Time: Now()})
		w.runHandler()
	}
}
