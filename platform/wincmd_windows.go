package platform

import (
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Window commands on Windows (D30's F4, built for Windows by the phase 8
// plan's D68): what an app asks of one of its windows at a safe point
// (app.Win). Frames are in the space of ScreenInfo on Windows: the virtual
// screen's physical pixels, the origin at the top-left of the primary
// display, and the whole window (GetWindowRect: its frame and the invisible
// resize borders), so a frame read and set again puts the window back where
// it was.

const (
	swHide         = 0
	swShowMaximize = 3
	swShow         = 5
	swMinimize     = 6
	swRestore      = 9

	flashwTray       = 0x2
	flashwTimerNoFG  = 0xC
	gwlStyle         = -16
	monitorNearest   = 2
	swpFrameChanged  = 0x0020
	swpNoOwnerZOrder = 0x0200
	hwndTop          = 0
)

// flashWInfo is FLASHWINFO.
type flashWInfo struct {
	size    uint32
	hwnd    uintptr
	flags   uint32
	count   uint32
	timeout uint32
}

// monitorInfo is MONITORINFO.
type monitorInfo struct {
	size          uint32
	monitor, work rect
	flags         uint32
}

// windowPlacement is WINDOWPLACEMENT.
type windowPlacement struct {
	length, flags, showCmd uint32
	minPos, maxPos         point
	normal                 rect
}

var (
	pSetWindowTextW      = win32.Proc(user32, "SetWindowTextW")
	pGetWindowRect       = win32.Proc(user32, "GetWindowRect")
	pIsWindowVisible     = win32.Proc(user32, "IsWindowVisible")
	pIsIconic            = win32.Proc(user32, "IsIconic")
	pIsZoomed            = win32.Proc(user32, "IsZoomed")
	pSetForegroundWindow = win32.Proc(user32, "SetForegroundWindow")
	pGetForegroundWindow = win32.Proc(user32, "GetForegroundWindow")
	pFlashWindowEx       = win32.Proc(user32, "FlashWindowEx")
	pMonitorFromWindow   = win32.Proc(user32, "MonitorFromWindow")
	pGetMonitorInfoW     = win32.Proc(user32, "GetMonitorInfoW")
	pGetWindowPlacement  = win32.Proc(user32, "GetWindowPlacement")
	pSetWindowPlacement  = win32.Proc(user32, "SetWindowPlacement")
	pSetWindowLong       = procEither(user32, "SetWindowLongPtrW", "SetWindowLongW") // the Ptr names are 64-bit only
	pGetWindowLong       = procEither(user32, "GetWindowLongPtrW", "GetWindowLongW")
)

func procEither(dll uintptr, a, b string) uintptr {
	if p := win32.Proc(dll, a); p != 0 {
		return p
	}
	return win32.Proc(dll, b)
}

// SetTitle sets the window's title (the title bar, the taskbar button,
// Alt+Tab). Main thread.
func (w *Window) SetTitle(title string) {
	if w.hwnd == 0 {
		return
	}
	u, err := syscall.UTF16FromString(title)
	if err != nil {
		return
	}
	w.titleBuf = u
	win32.Call(pSetWindowTextW, w.hwnd, uintptr(unsafe.Pointer(&w.titleBuf[0])))
}

// FrameRect is the window's frame in the virtual screen's pixels (see above).
// Main thread.
func (w *Window) FrameRect() (x, y, width, height float64) {
	if w.hwnd == 0 || !win32.Bool(win32.Call(pGetWindowRect, w.hwnd, uintptr(unsafe.Pointer(&w.rc)))) {
		return 0, 0, 0, 0
	}
	return float64(w.rc.left), float64(w.rc.top), float64(w.rc.right - w.rc.left), float64(w.rc.bottom - w.rc.top)
}

// SetFrameRect moves and sizes the window to a frame in FrameRect's space. A
// maximized or minimized window is restored first. The minimum size
// (Options.MinWidth/MinHeight) still applies. Main thread, at a safe point
// (the resize arrives as EvResize).
func (w *Window) SetFrameRect(x, y, width, height float64) {
	if w.hwnd == 0 || width <= 0 || height <= 0 {
		return
	}
	if w.fullScreen {
		w.ToggleFullScreen()
	}
	if w.Zoomed() || w.Miniaturized() {
		win32.Call(pShowWindow, w.hwnd, swRestore)
	}
	win32.Call(pSetWindowPos, w.hwnd, 0, uintptr(int32(x)), uintptr(int32(y)), uintptr(int32(width+0.5)), uintptr(int32(height+0.5)),
		swpNoZOrder|swpNoActivate)
}

// ToggleFullScreen covers the window's monitor with its content and no frame,
// or brings the frame and the placement back. Main thread.
func (w *Window) ToggleFullScreen() {
	if w.hwnd == 0 || pSetWindowLong == 0 || pGetWindowLong == 0 {
		return
	}
	if w.fullScreen {
		w.fullScreen = false
		win32.Call(pSetWindowLong, w.hwnd, uintptr(gwlStyleArg), w.saveStyle)
		w.place.length = uint32(unsafe.Sizeof(w.place))
		win32.Call(pSetWindowPlacement, w.hwnd, uintptr(unsafe.Pointer(&w.place)))
		win32.Call(pSetWindowPos, w.hwnd, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpNoOwnerZOrder|swpFrameChanged)
		return
	}
	mon := win32.Call(pMonitorFromWindow, w.hwnd, monitorNearest)
	w.place.length = uint32(unsafe.Sizeof(w.place))
	w.mi.size = uint32(unsafe.Sizeof(w.mi))
	if mon == 0 || !win32.Bool(win32.Call(pGetWindowPlacement, w.hwnd, uintptr(unsafe.Pointer(&w.place)))) ||
		!win32.Bool(win32.Call(pGetMonitorInfoW, mon, uintptr(unsafe.Pointer(&w.mi)))) {
		return
	}
	w.fullScreen = true
	w.saveStyle = win32.Call(pGetWindowLong, w.hwnd, uintptr(gwlStyleArg))
	win32.Call(pSetWindowLong, w.hwnd, uintptr(gwlStyleArg), w.saveStyle&^wsOverlappedWindow|wsPopup|wsVisible)
	m := w.mi.monitor
	win32.Call(pSetWindowPos, w.hwnd, hwndTop, uintptr(m.left), uintptr(m.top), uintptr(m.right-m.left), uintptr(m.bottom-m.top),
		swpNoOwnerZOrder|swpFrameChanged)
}

// gwlStyleArg is GWL_STYLE as the uintptr argument (a negative index).
var gwlStyleArg = func() uintptr { i := int32(gwlStyle); return uintptr(i) }()

// FullScreen reports whether the window covers its monitor (ToggleFullScreen).
func (w *Window) FullScreen() bool { return w.fullScreen }

// Miniaturize minimizes the window to the taskbar. Main thread.
func (w *Window) Miniaturize() {
	if w.hwnd != 0 {
		win32.Call(pShowWindow, w.hwnd, swMinimize)
	}
}

// Miniaturized reports whether the window is minimized.
func (w *Window) Miniaturized() bool { return w.hwnd != 0 && win32.Bool(win32.Call(pIsIconic, w.hwnd)) }

// Zoom maximizes the window, or restores a maximized one (the caption's
// maximize button). Main thread.
func (w *Window) Zoom() {
	if w.hwnd == 0 {
		return
	}
	if w.Zoomed() {
		win32.Call(pShowWindow, w.hwnd, swRestore)
		return
	}
	win32.Call(pShowWindow, w.hwnd, swShowMaximize)
}

// Zoomed reports whether the window is maximized.
func (w *Window) Zoomed() bool { return w.hwnd != 0 && win32.Bool(win32.Call(pIsZoomed, w.hwnd)) }

// Activate brings the window to the front, restored if it was minimized and
// shown if it was hidden, and gives it the keyboard. Windows grants the
// foreground only to the app the user is working with, or one given the
// right (a hotkey's press, a tray click, a second instance's
// AllowSetForegroundWindow); without it the taskbar button flashes until
// the user switches to the window, as Windows intends. Main thread.
func (w *Window) Activate() {
	if w.hwnd == 0 {
		return
	}
	w.Show()
	if w.Miniaturized() {
		win32.Call(pShowWindow, w.hwnd, swRestore)
	}
	if win32.Bool(win32.Call(pSetForegroundWindow, w.hwnd)) && win32.Call(pGetForegroundWindow) == w.hwnd {
		return
	}
	w.flash = flashWInfo{hwnd: w.hwnd, flags: flashwTray | flashwTimerNoFG}
	w.flash.size = uint32(unsafe.Sizeof(w.flash))
	win32.Call(pFlashWindowEx, uintptr(unsafe.Pointer(&w.flash)))
}

// Hide takes the window off the screen without closing it (SW_HIDE: no
// taskbar button either); Show or Activate bring it back. Main thread.
func (w *Window) Hide() {
	if w.hwnd == 0 || w.hidden {
		return
	}
	w.hidden = true
	win32.Call(pShowWindow, w.hwnd, swHide)
}

// Visible reports whether the window is on the screen (shown and uncloaked,
// possibly covered or minimized).
func (w *Window) Visible() bool {
	return w.hwnd != 0 && !w.cloaked && !w.hidden && win32.Bool(win32.Call(pIsWindowVisible, w.hwnd))
}

// SetDocumentEdited does nothing on Windows (the caption has no such mark).
func (w *Window) SetDocumentEdited(edited bool) {}
