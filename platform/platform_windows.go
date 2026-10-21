//go:build windows

package platform

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Native holds the OS handles the gpu package needs.
type Native struct {
	// HWND is the window's HWND.
	HWND uintptr
	// HInstance is the HINSTANCE of the executable (GetModuleHandleW(NULL)).
	HInstance uintptr
}

// ---- Win32 constants ------------------------------------------------------

const (
	wmSize          = 0x0005
	wmGetMinMaxInfo = 0x0024
	wmSetCursor     = 0x0020
	wmTimer         = 0x0113
	wmEnterSizeMove = 0x0231
	wmExitSizeMove  = 0x0232
	wmSetFocus      = 0x0007
	wmKillFocus     = 0x0008
	wmPaint         = 0x000F
	wmClose         = 0x0010
	wmEraseBkgnd    = 0x0014
	wmKeyDown       = 0x0100
	wmKeyUp         = 0x0101
	wmChar          = 0x0102
	wmImeStartComp  = 0x010D
	wmImeEndComp    = 0x010E
	wmImeComp       = 0x010F
	wmImeSetContext = 0x0281
	wmSysKeyDown    = 0x0104
	wmSysKeyUp      = 0x0105
	wmMouseMove     = 0x0200
	wmLButtonDown   = 0x0201
	wmLButtonUp     = 0x0202
	wmRButtonDown   = 0x0204
	wmRButtonUp     = 0x0205
	wmMButtonDown   = 0x0207
	wmMButtonUp     = 0x0208
	wmMouseWheel    = 0x020A
	wmXButtonDown   = 0x020B
	wmXButtonUp     = 0x020C
	wmMouseHWheel   = 0x020E
	wmCaptureChange = 0x0215
	wmMouseLeave    = 0x02A3
	wmDpiChanged    = 0x02E0
	wmWake          = 0x8000 + 1 // WM_APP+1 (WM_APP+2 is wmClip)

	sizeRestored  = 0
	sizeMinimized = 1

	wsOverlappedWindow      = 0x00CF0000
	wsExNoRedirectionBitmap = 0x00200000
	cwUseDefault            = 0x80000000
	swShowNormal            = 1
	idcArrow                = 32512
	idcIBeam                = 32513
	idcSizeNWSE             = 32642
	idcSizeNESW             = 32643
	idcSizeWE               = 32644
	idcSizeNS               = 32645
	idcSizeAll              = 32646
	idcHand                 = 32649
	htClient                = 1
	sizeMoveTimerID         = 1
	sizeMoveTimerMs         = 16 // USER_TIMER_MINIMUM is 10; ~60 Hz is plenty for requested frames

	swpNoMove     = 0x0002
	swpNoZOrder   = 0x0004
	swpNoActivate = 0x0010

	pmRemove = 0x0001

	qsAllInput          = 0x04FF
	mwmoInputAvail      = 0x0004
	waitObject0         = 0
	waitFailed          = 0xFFFFFFFF
	infinite            = 0xFFFFFFFF
	tmeLeave            = 0x00000002
	xButton1            = 1
	vkShift             = 0x10
	vkControl           = 0x11
	vkMenu              = 0x12
	vkLWin              = 0x5B
	vkRWin              = 0x5C
	defaultDPI          = 96
	wheelDelta          = 120
	wheelLinesPerNotch  = 3
	wheelLogicalPerLine = 16

	// Input method (imm32.dll).
	cfsPoint          = 0x0002
	cfsCandidatePos   = 0x0040
	niCompositionStr  = 0x0015
	cpsCancel         = 0x0004
	iscShowCompWindow = 0x80000000 // ISC_SHOWUICOMPOSITIONWINDOW: the app draws the composition itself
)

// ---- Win32 structures -------------------------------------------------------

type point struct{ x, y int32 }

type rect struct{ left, top, right, bottom int32 }

// minMaxInfo is MINMAXINFO.
type minMaxInfo struct {
	reserved, maxSize, maxPosition, minTrack, maxTrack point
}

// msg is MSG from winuser.h.
type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

// wndClassEx is WNDCLASSEXW.
type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   uintptr
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

// trackMouseEvent is TRACKMOUSEEVENT.
type trackMouseEvent struct {
	size      uint32
	flags     uint32
	hwndTrack uintptr
	hoverTime uint32
}

// ---- Entry points (resolved once; System32 only) ------------------------------

var (
	user32   = win32.LoadSystem("user32.dll")
	kernel32 = win32.LoadSystem("kernel32.dll")

	pGetModuleHandleW   = win32.Proc(kernel32, "GetModuleHandleW")
	pRegisterClassExW   = win32.Proc(user32, "RegisterClassExW")
	pUnregisterClassW   = win32.Proc(user32, "UnregisterClassW")
	pCreateWindowExW    = win32.Proc(user32, "CreateWindowExW")
	pDestroyWindow      = win32.Proc(user32, "DestroyWindow")
	pShowWindow         = win32.Proc(user32, "ShowWindow")
	pDefWindowProcW     = win32.Proc(user32, "DefWindowProcW")
	pPeekMessageW       = win32.Proc(user32, "PeekMessageW")
	pTranslateMessage   = win32.Proc(user32, "TranslateMessage")
	pDispatchMessageW   = win32.Proc(user32, "DispatchMessageW")
	pPostMessageW       = win32.Proc(user32, "PostMessageW")
	pMsgWait            = win32.Proc(user32, "MsgWaitForMultipleObjectsEx")
	pWaitForSingleObj   = win32.Proc(kernel32, "WaitForSingleObjectEx")
	pSetWindowPos       = win32.Proc(user32, "SetWindowPos")
	pGetClientRect      = win32.Proc(user32, "GetClientRect")
	pValidateRect       = win32.Proc(user32, "ValidateRect")
	pLoadCursorW        = win32.Proc(user32, "LoadCursorW")
	pSetCursor          = win32.Proc(user32, "SetCursor")
	pSetTimer           = win32.Proc(user32, "SetTimer")
	pKillTimer          = win32.Proc(user32, "KillTimer")
	pSetCapture         = win32.Proc(user32, "SetCapture")
	pReleaseCapture     = win32.Proc(user32, "ReleaseCapture")
	pGetKeyState        = win32.Proc(user32, "GetKeyState")
	pMapVirtualKeyW     = win32.Proc(user32, "MapVirtualKeyW")
	pScreenToClient     = win32.Proc(user32, "ScreenToClient")
	pGetCursorPos       = win32.Proc(user32, "GetCursorPos")
	pTrackMouseEvent    = win32.Proc(user32, "TrackMouseEvent")
	pAdjustWindowRectEx = win32.Proc(user32, "AdjustWindowRectEx")
	pSetDpiAwarenessCtx = win32.Proc(user32, "SetProcessDpiAwarenessContext") // Windows 10 1607+
	pSetProcessDPIAware = win32.Proc(user32, "SetProcessDPIAware")
	pGetDpiForWindow    = win32.Proc(user32, "GetDpiForWindow")          // Windows 10 1607+
	pGetDpiForSystem    = win32.Proc(user32, "GetDpiForSystem")          // Windows 10 1607+
	pAdjustRectExForDpi = win32.Proc(user32, "AdjustWindowRectExForDpi") // Windows 10 1607+
	imm32               = win32.LoadSystem("imm32.dll")
	pImmGetContext      = win32.Proc(imm32, "ImmGetContext")
	pImmReleaseContext  = win32.Proc(imm32, "ImmReleaseContext")
	pImmGetCompStringW  = win32.Proc(imm32, "ImmGetCompositionStringW")
	pImmSetCompWindow   = win32.Proc(imm32, "ImmSetCompositionWindow")
	pImmSetCandWindow   = win32.Proc(imm32, "ImmSetCandidateWindow")
	pImmNotifyIME       = win32.Proc(imm32, "ImmNotifyIME")
	classNameUTF16      = mustUTF16("GoNativeUIWindow")
	wndProcCallback     = syscall.NewCallback(wndProc) // created once
	theWindow           *Window                        // the single window; read by wndProc
	wakeHWND            atomic.Uintptr                 // HWND for osWake, 0 when no window
)

func mustUTF16(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		panic(err)
	}
	return p
}

// ---- Window -------------------------------------------------------------------

// Window is the single top-level window.
type Window struct {
	q eventQueue

	hwnd, hinst uintptr
	style       uint32

	w, h  int32   // client size, physical pixels
	scale float32 // pixels per logical unit

	minW, minH int // Options.MinWidth/MinHeight (logical), for WM_GETMINMAXINFO

	// Last values reported through EvResize, to avoid duplicate events.
	rw, rh int32
	rscale float32

	opening   bool   // inside Open: WM_SIZE etc. update state but push no events
	delivered bool   // q holds events already returned by Wait
	nOut      int    // how many of them
	visible   bool   // last reported visibility
	cloaked   bool   // opened with Options.Hidden and not yet shown (dwm_windows.go)
	cloakVal  uint32 // the BOOL DwmSetWindowAttribute reads
	minimized bool
	tracking  bool // TrackMouseEvent(TME_LEAVE) armed
	buttons   uint8

	frameArmed bool
	waitable   uintptr
	handles    [1]uintptr

	handler   func() // SetFrameHandler
	inHandler bool
	sizeMove  bool // inside the size/move modal loop (WM_ENTERSIZEMOVE .. WM_EXITSIZEMOVE)

	curs    cursorMap // cursor regions and override (SetCursorRegions)
	shown   Cursor    // the shape last applied with SetCursor
	ptrX    float32   // last pointer position in the content (WM_MOUSEMOVE), pixels
	ptrY    float32
	cursors [numCursors]uintptr // HCURSORs, loaded at Open

	hiSurrogate uint16
	utf8        [4]byte
	lastMods    Mods // the modifiers EvModifiers last reported

	// The clipboard requests in flight (paste_windows.go): the paste being
	// waited for, with its tag and time limit, the worker that reads it, and
	// when Wait looks at them next (0: nothing is due).
	pasteOn   bool
	pasteTag  uint64
	pasteSeq  uint64 // counts requests, so that an answer to a replaced one is recognised
	pasteGive int64
	clipDue   int64
	clip      *clipWorker // does the clipboard operations; nil until the first request

	// Input method state (ime_windows.go): the composition is on screen, the
	// caret rectangle the app reported, and storage the IME calls write into.
	composing bool
	imeRect   [4]float32
	imeU16    [512]uint16
	imeU8     []byte
	compForm  compositionForm
	candForm  candidateForm

	// preciseUntil: wheel messages until then are a precision touchpad's
	// (one of its deltas was not whole notches lately).
	preciseUntil int64

	// Syscall argument and result storage; kept in the heap-allocated Window
	// so no pointer to a stack local ever reaches the OS.
	msg msg
	rc  rect
	pt  point
	tme trackMouseEvent
	wc  wndClassEx
}

// Open creates the window. Main thread only.
func Open(o Options) (*Window, error) {
	if user32 == 0 || pCreateWindowExW == 0 || pMsgWait == 0 {
		return nil, errors.New("platform: cannot load user32.dll")
	}
	if theWindow != nil {
		return nil, fmt.Errorf("platform: a second window: %w", errors.ErrUnsupported) // one window per app here so far (D30 is macOS-only in phase 6)
	}
	nOpenStages = 0
	markOpen("start")
	setDPIAwareness()

	title, err := syscall.UTF16PtrFromString(o.Title)
	if err != nil {
		return nil, err
	}

	w := &Window{q: newEventQueue(), opening: true, style: wsOverlappedWindow, rscale: 1, minW: o.MinWidth, minH: o.MinHeight}
	w.hinst = win32.Call(pGetModuleHandleW, 0)
	theWindow = w // WndProc runs inside CreateWindowExW

	w.cursors = [numCursors]uintptr{
		CursorArrow:      win32.Call(pLoadCursorW, 0, idcArrow),
		CursorPointer:    win32.Call(pLoadCursorW, 0, idcHand),
		CursorText:       win32.Call(pLoadCursorW, 0, idcIBeam),
		CursorResizeEW:   win32.Call(pLoadCursorW, 0, idcSizeWE),
		CursorResizeNS:   win32.Call(pLoadCursorW, 0, idcSizeNS),
		CursorResizeNWSE: win32.Call(pLoadCursorW, 0, idcSizeNWSE),
		CursorResizeNESW: win32.Call(pLoadCursorW, 0, idcSizeNESW),
		CursorGrab:       win32.Call(pLoadCursorW, 0, idcHand), // Windows has no hand that grabs
		CursorGrabbing:   win32.Call(pLoadCursorW, 0, idcSizeAll),
	}
	w.wc = wndClassEx{
		size:      uint32(unsafe.Sizeof(w.wc)),
		wndProc:   wndProcCallback,
		instance:  w.hinst,
		cursor:    w.cursors[CursorArrow],
		className: classNameUTF16,
	}
	if win32.Call(pRegisterClassExW, uintptr(unsafe.Pointer(&w.wc)))&0xFFFF == 0 {
		theWindow = nil
		return nil, errors.New("platform: RegisterClassExW failed")
	}

	dpi := uint32(defaultDPI)
	if pGetDpiForSystem != 0 {
		dpi = uint32(win32.Call(pGetDpiForSystem))
	}
	ow, oh := w.outerSize(o.Width, o.Height, dpi)
	// WS_EX_NOREDIRECTIONBITMAP: the window has no GDI surface of its own
	// (what it shows is the swap chain's, and DWM's frame). With one, DWM
	// could compose the uncloaked window once with that surface, never
	// painted, before it had taken in the first frame: a white client area
	// on the screen for one pass (TestFirstFrameIsContent on the runners,
	// about 1 in 100 fresh processes under load; a probe that painted the
	// surface blue saw blue there instead). Nothing here draws with GDI.
	w.hwnd = win32.Call(pCreateWindowExW,
		wsExNoRedirectionBitmap, uintptr(unsafe.Pointer(classNameUTF16)), uintptr(unsafe.Pointer(title)), uintptr(w.style),
		cwUseDefault, cwUseDefault, uintptr(ow), uintptr(oh), 0, 0, w.hinst, 0)
	runtime.KeepAlive(title)
	if w.hwnd == 0 {
		win32.Call(pUnregisterClassW, uintptr(unsafe.Pointer(classNameUTF16)), w.hinst)
		theWindow = nil
		return nil, errors.New("platform: CreateWindowExW failed")
	}
	markOpen("window")
	// The window may have landed on a monitor with a different DPI.
	if pGetDpiForWindow != 0 {
		if d := uint32(win32.Call(pGetDpiForWindow, w.hwnd)); d != 0 && d != dpi {
			dpi = d
			ow, oh = w.outerSize(o.Width, o.Height, dpi)
			win32.Call(pSetWindowPos, w.hwnd, 0, 0, 0, uintptr(ow), uintptr(oh), swpNoMove|swpNoZOrder|swpNoActivate)
		}
	}
	w.scale = float32(dpi) / defaultDPI

	if o.Hidden { // content-first: shown, but cloaked until the first frame is presented
		w.cloaked = w.setCloak(true)
		markOpen("cloak")
	}
	win32.Call(pShowWindow, w.hwnd, swShowNormal)
	markOpen("shown")
	w.updateClientSize()
	w.rw, w.rh, w.rscale = w.w, w.h, w.scale
	w.opening = false
	wakeHWND.Store(w.hwnd)

	w.visible = true
	w.q.push(Event{Kind: EvVisible, Flags: FlagDown, Time: Now()})
	if takeWake() { // a Wake before Open found no window to post to
		w.q.push(Event{Kind: EvWake, Time: Now()})
	}
	return w, nil
}

// outerSize returns the window rectangle size (including frame) whose client
// area is the logical size scaled by dpi.
func (w *Window) outerSize(lw, lh int, dpi uint32) (int32, int32) {
	cw := int32(float32(lw)*float32(dpi)/defaultDPI + 0.5)
	ch := int32(float32(lh)*float32(dpi)/defaultDPI + 0.5)
	w.rc = rect{0, 0, cw, ch}
	if pAdjustRectExForDpi != 0 {
		win32.Call(pAdjustRectExForDpi, uintptr(unsafe.Pointer(&w.rc)), uintptr(w.style), 0, 0, uintptr(dpi))
	} else {
		win32.Call(pAdjustWindowRectEx, uintptr(unsafe.Pointer(&w.rc)), uintptr(w.style), 0, 0)
	}
	return w.rc.right - w.rc.left, w.rc.bottom - w.rc.top
}

func (w *Window) updateClientSize() {
	if win32.Bool(win32.Call(pGetClientRect, w.hwnd, uintptr(unsafe.Pointer(&w.rc)))) {
		w.w, w.h = w.rc.right-w.rc.left, w.rc.bottom-w.rc.top
	}
}

// emitResize reports the current size/scale unless it was already reported.
func (w *Window) emitResize() {
	if w.opening || w.w <= 0 || w.h <= 0 {
		return
	}
	if w.w == w.rw && w.h == w.rh && w.scale == w.rscale {
		return
	}
	w.rw, w.rh, w.rscale = w.w, w.h, w.scale
	w.q.push(Event{Kind: EvResize, W: w.w, H: w.h, Scale: w.scale, Time: Now()})
}

// SetFrameHandler registers fn to run inside the size/move modal loop, which
// DefWindowProc enters when the user drags the frame or title bar and in
// which Wait does not return. It runs from WM_SIZE (a size change; the
// swap chain must be resized from the EvResize in Pending before drawing)
// and from a ~16 ms WM_TIMER while a frame is requested (RequestFrame), so
// animations keep running during a drag.
func (w *Window) SetFrameHandler(fn func()) { w.handler = fn }

// Pending returns and clears the events queued so far without pumping
// messages. The slice is valid until the next Pending or Wait call.
func (w *Window) Pending() []Event {
	if w.delivered {
		w.q.dropFirst(w.nOut)
		w.delivered = false
	}
	return w.q.takePending()
}

// runHandler calls the frame handler unless it is already running. Called
// from wndProc, i.e. from inside Wait's DispatchMessage or a modal loop.
func (w *Window) runHandler() {
	if w.handler == nil || w.inHandler || w.opening {
		return
	}
	w.inHandler = true
	w.handler()
	w.inHandler = false
}

// SetCursorRegions sets the cursor regions: where in the content the pointer
// shows which shape (later entries are on top; elsewhere the arrow). Main
// thread. It copies into storage the window owns and does nothing when the
// list equals the previous one, so it can be called every frame without
// allocating.
//
// WM_SETCURSOR (HTCLIENT) looks the shape up at the pointer's position (the
// override first, then the topmost region, then the arrow), so a divider needs
// no mouse event from the app; a change of the lists applies at once when the
// pointer is over the content.
func (w *Window) SetCursorRegions(r []CursorRegion) {
	if w.curs.setRegions(r) {
		w.applyCursor()
	}
}

// SetCursorOverride shows c over the whole content while it is not
// CursorArrow, whatever the regions say (a drag keeps its cursor wherever the
// pointer goes). CursorArrow removes the override. Main thread.
func (w *Window) SetCursorOverride(c Cursor) {
	if w.curs.setOverride(c) {
		w.applyCursor()
	}
}

// pointerNow is the pointer's position in the client area right now, in
// pixels: GetCursorPos and ScreenToClient, into storage the Window owns.
func (w *Window) pointerNow(hwnd uintptr) (x, y float32, ok bool) {
	if !win32.Bool(win32.Call(pGetCursorPos, uintptr(unsafe.Pointer(&w.pt)))) {
		return 0, 0, false
	}
	if !win32.Bool(win32.Call(pScreenToClient, hwnd, uintptr(unsafe.Pointer(&w.pt)))) {
		return 0, 0, false
	}
	return float32(w.pt.x), float32(w.pt.y), true
}

func (w *Window) applyCursor() {
	if !w.tracking { // the pointer is over the content: apply now, not at the next mouse move
		return
	}
	x, y, ok := w.pointerNow(w.hwnd)
	if c := w.curs.pointerShape(x, y, ok, w.ptrX, w.ptrY); c != w.shown {
		w.shown = c
		win32.Call(pSetCursor, w.cursors[c])
	}
}

// SetDragRegions is a no-op on Windows: the window keeps its system titlebar.
func (w *Window) SetDragRegions([]DragRegion) {}

// Text returns the UTF-8 bytes of an EvText event.
func (w *Window) Text(r TextRef) []byte { return w.q.textOf(r) }

// RequestFrame arms one EvFrame for the next frame-latency opportunity.
func (w *Window) RequestFrame() { w.frameArmed = true }

// Size returns the client size in physical pixels and the pixels per logical unit.
func (w *Window) Size() (int32, int32, float32) { return w.w, w.h, w.scale }

// Native returns the OS handles for the gpu package.
func (w *Window) Native() Native { return Native{HWND: w.hwnd, HInstance: w.hinst} }

// SetFrameSource installs the DXGI frame-latency waitable handle.
func (w *Window) SetFrameSource(h uintptr) {
	w.waitable = h
	if h != 0 {
		// The waitable starts signalled; consume it before the first frame as
		// Microsoft's DirectXLatency sample does, or every later frame runs one
		// frame of latency behind.
		win32.Call(pWaitForSingleObj, h, 1000, 1)
	}
}

// Close destroys the window. The clipboard worker is told to stop and Close
// waits for it only a moment: a worker inside a native clipboard call ends when
// the call returns. It touches nothing of the Window after Close (it holds only
// its own state and, under its lock, the window handle, which Close zeroes
// first), sends no message, and publishes no copy: it checks that under its
// lock before SetClipboardData. A stale handle can at worst make an open that
// was already under way fail or be closed unused.
func (w *Window) Close() {
	if w.hwnd == 0 {
		return
	}
	wakeHWND.Store(0)
	if w.clip != nil { // ends now, or when the native call it may be in returns; publishes nothing either way
		w.clip.close()
		w.clip = nil
	}
	hwnd := w.hwnd
	w.hwnd = 0
	win32.Call(pDestroyWindow, hwnd)
	win32.Call(pUnregisterClassW, uintptr(unsafe.Pointer(classNameUTF16)), w.hinst)
	theWindow = nil
}

// osWake posts a message to the window thread; safe from any thread.
func osWake() {
	h := wakeHWND.Load()
	if h == 0 {
		return // Open picks up the pending wake
	}
	if !win32.Bool(win32.Call(pPostMessageW, h, wmWake, 0, 0)) {
		takeWake() // don't leave the coalescing flag stuck
	}
}

// Wait blocks until at least one event is available, a wake or frame slot
// arrives, or the timeout elapses (negative = no timeout, 0 = poll). The
// returned slice is valid until the next Wait.
func (w *Window) Wait(timeoutNs int64) []Event {
	if w.delivered {
		w.q.dropFirst(w.nOut) // what the app asked for since (RequestPaste) stays
		w.delivered = false
	}
	var deadline int64
	if timeoutNs > 0 {
		deadline = nanotime() + timeoutNs
	}
	for {
		if w.clipDue != 0 && nanotime() >= w.clipDue {
			w.clipStep()
		}
		if w.frameArmed && w.waitable == 0 {
			w.frameArmed = false
			w.q.push(Event{Kind: EvFrame, Time: Now()})
		}

		ms := uintptr(infinite)
		switch {
		case timeoutNs == 0 || len(w.q.ev) > 0:
			ms = 0
		case timeoutNs > 0:
			ms = 0
			if rem := deadline - nanotime(); rem > 0 {
				ms = uintptr(min((rem+999_999)/1_000_000, infinite-1))
			}
		}
		if w.clipDue != 0 { // do not sleep past the next try at the clipboard
			d := uintptr(0)
			if rem := w.clipDue - nanotime(); rem > 0 {
				d = uintptr((rem + 999_999) / 1_000_000)
			}
			ms = min(ms, d)
		}
		n := uintptr(0)
		if w.frameArmed {
			w.handles[0] = w.waitable
			n = 1
		}
		r := uint32(win32.Call(pMsgWait, n, uintptr(unsafe.Pointer(&w.handles[0])), ms, qsAllInput, mwmoInputAvail))
		if n == 1 {
			switch r {
			case waitObject0:
				w.frameArmed = false
				w.q.push(Event{Kind: EvFrame, Time: Now()})
			case waitFailed:
				w.waitable = 0 // bad handle: fall back to immediate frames
			}
		}

		for win32.Bool(win32.Call(pPeekMessageW, uintptr(unsafe.Pointer(&w.msg)), 0, 0, 0, pmRemove)) {
			win32.Call(pTranslateMessage, uintptr(unsafe.Pointer(&w.msg)))
			win32.Call(pDispatchMessageW, uintptr(unsafe.Pointer(&w.msg)))
		}

		if len(w.q.ev) > 0 || timeoutNs == 0 || (timeoutNs > 0 && nanotime() >= deadline) {
			break
		}
	}
	w.delivered, w.nOut = true, len(w.q.ev)
	return w.q.ev
}

// ---- Window procedure ---------------------------------------------------------

func loword(v uintptr) int32  { return int32(int16(uint16(v))) }
func hiword(v uintptr) int32  { return int32(int16(uint16(v >> 16))) }
func uloword(v uintptr) int32 { return int32(uint16(v)) }
func uhiword(v uintptr) int32 { return int32(uint16(v >> 16)) }

func keyIsDown(vk uintptr) bool {
	return uint16(win32.Call(pGetKeyState, vk))&0x8000 != 0
}

func currentMods() Mods {
	var m Mods
	if keyIsDown(vkShift) {
		m |= ModShift
	}
	if keyIsDown(vkControl) {
		m |= ModCtrl
	}
	if keyIsDown(vkMenu) {
		m |= ModAlt
	}
	if keyIsDown(vkLWin) || keyIsDown(vkRWin) {
		m |= ModSuper
	}
	return m
}

func defProc(hwnd, m, wp, lp uintptr) uintptr {
	return win32.Call(pDefWindowProcW, hwnd, m, wp, lp)
}

func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	w := theWindow
	if w == nil {
		return defProc(hwnd, m, wp, lp)
	}
	switch m {
	case wmWake:
		if takeWake() {
			w.q.push(Event{Kind: EvWake, Time: Now()})
		}
		return 0

	case wmClip:
		w.clipCollect()
		return 0

	case wmClose:
		w.q.push(Event{Kind: EvClose, Time: Now()})
		return 0

	case wmEraseBkgnd:
		return 1

	case wmPaint:
		win32.Call(pValidateRect, hwnd, 0)
		if !w.opening && !w.visible && !w.minimized {
			w.visible = true
			w.q.push(Event{Kind: EvVisible, Flags: FlagDown, Time: Now()})
		}
		return 0

	case wmSize:
		if wp == sizeMinimized {
			w.minimized = true
			if w.visible && !w.opening {
				w.visible = false
				w.q.push(Event{Kind: EvVisible, Time: Now()})
			}
			return 0
		}
		w.w, w.h = uloword(lp), uhiword(lp)
		if w.minimized {
			w.minimized = false
			if !w.opening {
				w.visible = true
				w.q.push(Event{Kind: EvVisible, Flags: FlagDown, Time: Now()})
			}
		}
		w.emitResize()
		if w.sizeMove {
			w.runHandler() // draw in step with the frame being dragged
		}
		return 0

	case wmEnterSizeMove:
		w.sizeMove = true
		win32.Call(pSetTimer, hwnd, sizeMoveTimerID, sizeMoveTimerMs, 0)
		return 0

	case wmExitSizeMove:
		w.sizeMove = false
		win32.Call(pKillTimer, hwnd, sizeMoveTimerID)
		return 0

	case wmTimer:
		if wp != sizeMoveTimerID {
			return defProc(hwnd, m, wp, lp)
		}
		if w.sizeMove && w.frameArmed {
			w.frameArmed = false
			w.q.push(Event{Kind: EvFrame, Time: Now()})
			w.runHandler()
		}
		return 0

	case wmSetCursor:
		if uint32(lp&0xFFFF) == htClient {
			x, y, ok := w.pointerNow(hwnd)
			w.shown = w.curs.pointerShape(x, y, ok, w.ptrX, w.ptrY)
			win32.Call(pSetCursor, w.cursors[w.shown])
			return 1
		}
		return defProc(hwnd, m, wp, lp)

	case wmGetMinMaxInfo:
		// The minimum client size at the current DPI, as a window size.
		// Sent before the DPI is known too (inside CreateWindowExW).
		if (w.minW > 0 || w.minH > 0) && w.scale > 0 {
			if mmi := (*minMaxInfo)(win32.Ptr(lp)); mmi != nil {
				ow, oh := w.outerSize(w.minW, w.minH, uint32(w.scale*defaultDPI+0.5))
				mmi.minTrack = point{ow, oh}
				return 0
			}
		}
		return defProc(hwnd, m, wp, lp)

	case wmDpiChanged:
		w.scale = float32(uloword(wp)) / defaultDPI
		if r := (*rect)(win32.Ptr(lp)); r != nil {
			win32.Call(pSetWindowPos, hwnd, 0,
				uintptr(r.left), uintptr(r.top), uintptr(r.right-r.left), uintptr(r.bottom-r.top),
				swpNoZOrder|swpNoActivate)
		}
		w.updateClientSize()
		w.emitResize()
		return 0

	case wmSetFocus:
		t := Now()
		w.q.push(Event{Kind: EvFocus, Flags: FlagDown, Time: t})
		focusModifiers(&w.q, &w.lastMods, currentMods(), t) // keys pressed while another window had the focus
		return 0
	case wmKillFocus:
		t := Now()
		w.q.push(Event{Kind: EvFocus, Time: t})
		focusModifiers(&w.q, &w.lastMods, 0, t) // a release elsewhere never arrives
		return 0

	case wmKeyDown, wmKeyUp, wmSysKeyDown, wmSysKeyUp:
		var f Flags
		if m == wmKeyDown || m == wmSysKeyDown {
			f = FlagDown
			if lp&(1<<30) != 0 {
				f |= FlagRepeat
			}
		}
		code, k, ok := winKey(wp, lp)
		if !ok {
			return 0 // processed by an input method (VK_PROCESSKEY)
		}
		cur := currentMods()
		if vkIsModifier(wp) { // a modifier on its own is EvModifiers, once per change
			if cur != w.lastMods {
				w.lastMods = cur
				w.q.push(Event{Kind: EvModifiers, Mods: cur, Code: code, Time: Now()})
			}
		} else {
			var mapped uint32
			if winNeedsChar(wp) {
				mapped = uint32(win32.Call(pMapVirtualKeyW, wp, 2 /* MAPVK_VK_TO_CHAR */))
			}
			w.q.push(Event{Kind: EvKey, Key: k, Char: winChar(wp, mapped), Code: code, Mods: cur, Flags: f, Time: Now()})
			w.lastMods = cur // (EvModifiers reports changes against what the app last saw)
		}
		if m == wmSysKeyDown || m == wmSysKeyUp {
			return defProc(hwnd, m, wp, lp) // keeps Alt+F4 and Alt+Space working
		}
		return 0

	case wmChar:
		w.onChar(uint16(wp))
		return 0

	case wmImeSetContext:
		// The app draws the composition itself (EvPreedit); the candidate
		// list stays the system's.
		return defProc(hwnd, m, wp, lp&^iscShowCompWindow)
	case wmImeStartComp:
		w.imePlace()
		return 0
	case wmImeEndComp:
		w.imeEnded()
		return 0
	case wmImeComp:
		w.onImeComposition(lp)
		return 0

	case wmMouseMove:
		if !w.tracking {
			w.tme = trackMouseEvent{size: uint32(unsafe.Sizeof(w.tme)), flags: tmeLeave, hwndTrack: hwnd}
			if win32.Bool(win32.Call(pTrackMouseEvent, uintptr(unsafe.Pointer(&w.tme)))) {
				w.tracking = true
			}
		}
		w.ptrX, w.ptrY = float32(loword(lp)), float32(hiword(lp))
		w.q.push(Event{Kind: EvPointerMove, X: w.ptrX, Y: w.ptrY, Time: Now()})
		return 0

	case wmMouseLeave:
		w.tracking = false
		w.q.push(Event{Kind: EvPointerLeave, Time: Now()})
		return 0

	case wmLButtonDown:
		w.button(hwnd, ButtonLeft, true, lp)
		return 0
	case wmLButtonUp:
		w.button(hwnd, ButtonLeft, false, lp)
		return 0
	case wmRButtonDown:
		w.button(hwnd, ButtonRight, true, lp)
		return 0
	case wmRButtonUp:
		w.button(hwnd, ButtonRight, false, lp)
		return 0
	case wmMButtonDown:
		w.button(hwnd, ButtonMiddle, true, lp)
		return 0
	case wmMButtonUp:
		w.button(hwnd, ButtonMiddle, false, lp)
		return 0
	case wmXButtonDown, wmXButtonUp:
		b := ButtonForward
		if uhiword(wp) == xButton1 {
			b = ButtonBack
		}
		w.button(hwnd, b, m == wmXButtonDown, lp)
		return 1 // TRUE: XBUTTON messages require it

	case wmCaptureChange:
		w.buttons = 0
		return 0

	case wmMouseWheel, wmMouseHWheel:
		w.pt = point{loword(lp), hiword(lp)} // screen coordinates
		win32.Call(pScreenToClient, hwnd, uintptr(unsafe.Pointer(&w.pt)))
		raw := hiword(wp)
		if m == wmMouseWheel {
			if amt, pinch := winPinch(wp, keyIsDown(vkControl)); pinch {
				w.q.push(Event{Kind: EvMagnify, Amount: amt, X: float32(w.pt.x), Y: float32(w.pt.y), Mods: currentMods(), Time: Now()})
				return 0
			}
		}
		delta := float32(raw) / wheelDelta * wheelLinesPerNotch * wheelLogicalPerLine * w.scale
		e := Event{Kind: EvScroll, X: float32(w.pt.x), Y: float32(w.pt.y), Mods: currentMods(), Time: Now()}
		// A precision touchpad sends pixel-sized deltas (not whole notches)
		// and no gesture phases: its scrolling is precise but not elastic.
		// A high-resolution (free-spinning) wheel sends fractions of a notch
		// too and is treated the same: precise, applied at once, no glide.
		if raw%wheelDelta != 0 {
			w.preciseUntil = e.Time + 300e6
		}
		if e.Time < w.preciseUntil {
			e.Flags = FlagPrecise
		}
		e.Flags |= FlagPos
		if m == wmMouseWheel {
			e.DY = delta // wheel forward = scroll up = content moves down
		} else {
			e.DX = -delta // wheel tilted right = content moves left
		}
		w.q.push(e)
		return 0
	}
	return defProc(hwnd, m, wp, lp)
}

func (w *Window) button(hwnd uintptr, b Button, down bool, lp uintptr) {
	bit := uint8(1) << b
	var f Flags
	if down {
		f = FlagDown
		if w.buttons == 0 {
			win32.Call(pSetCapture, hwnd)
		}
		w.buttons |= bit
	} else {
		w.buttons &^= bit
		if w.buttons == 0 {
			win32.Call(pReleaseCapture)
		}
	}
	w.q.push(Event{Kind: EvPointerButton, Button: b, Flags: f, Mods: currentMods(),
		X: float32(loword(lp)), Y: float32(hiword(lp)), Time: Now()})
}

// onChar converts one UTF-16 code unit from WM_CHAR to UTF-8 text.
func (w *Window) onChar(u uint16) {
	var r rune
	switch {
	case u >= 0xD800 && u < 0xDC00: // high surrogate: wait for the low half
		w.hiSurrogate = u
		return
	case u >= 0xDC00 && u < 0xE000: // low surrogate
		hi := w.hiSurrogate
		w.hiSurrogate = 0
		if hi == 0 {
			return
		}
		r = (rune(hi)-0xD800)<<10 + (rune(u) - 0xDC00) + 0x10000
	default:
		w.hiSurrogate = 0
		if u < 0x20 || u == 0x7F { // control characters are keys, not text
			return
		}
		r = rune(u)
	}
	n := 0
	switch {
	case r < 0x80:
		w.utf8[0] = byte(r)
		n = 1
	case r < 0x800:
		w.utf8[0] = 0xC0 | byte(r>>6)
		w.utf8[1] = 0x80 | byte(r)&0x3F
		n = 2
	case r < 0x10000:
		w.utf8[0] = 0xE0 | byte(r>>12)
		w.utf8[1] = 0x80 | byte(r>>6)&0x3F
		w.utf8[2] = 0x80 | byte(r)&0x3F
		n = 3
	default:
		w.utf8[0] = 0xF0 | byte(r>>18)
		w.utf8[1] = 0x80 | byte(r>>12)&0x3F
		w.utf8[2] = 0x80 | byte(r>>6)&0x3F
		w.utf8[3] = 0x80 | byte(r)&0x3F
		n = 4
	}
	w.q.pushText(w.utf8[:n], Now())
}
