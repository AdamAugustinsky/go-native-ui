package platform

import (
	"unsafe"

	"gonativeui/internal/win32"
)

// Content-first startup. A window opened with Options.Hidden is created,
// cloaked with DWMWA_CLOAK, and then shown: to the window system it is
// visible (it gets its size, focus, paint and swap-chain presents as any
// window), but DWM does not put it on the screen. The gpu surface presents the
// first frame and then calls Show, which uncloaks it: the first thing on the
// screen is the app's content, not an empty client area.
const dwmaCloak = 13 // DWMWA_CLOAK; the value is a BOOL

var (
	dwmapi             = win32.LoadSystem("dwmapi.dll")
	pDwmSetWindowAttrW = win32.Proc(dwmapi, "DwmSetWindowAttribute")
)

// setCloak cloaks or uncloaks the window and reports whether DWM took it
// (it does not where composition is off, or where the call is missing).
func (w *Window) setCloak(on bool) bool {
	if pDwmSetWindowAttrW == 0 || w.hwnd == 0 {
		return false
	}
	w.cloakVal = 0
	if on {
		w.cloakVal = 1
	}
	return win32.HR(win32.Call(pDwmSetWindowAttrW, w.hwnd, dwmaCloak, uintptr(unsafe.Pointer(&w.cloakVal)), 4)) >= 0
}

// Show uncloaks a window opened with Options.Hidden (typically called by the
// GPU surface after its first present: Surface.RevealOnFirstFrame), and shows
// one that Hide took off the screen (activating it, as Windows does). It
// does nothing for a window that is already on the screen. A panel is
// brought to the top, and takes the foreground unless it is NonActivating
// (showPanel). Main thread.
func (w *Window) Show() {
	if w.panel != nil {
		w.showPanel() // panel_windows.go
		return
	}
	if w.cloaked {
		w.cloaked = false
		w.setCloak(false)
	}
	if w.hidden && w.hwnd != 0 {
		w.hidden = false
		win32.Call(pShowWindow, w.hwnd, swShow)
	}
}
