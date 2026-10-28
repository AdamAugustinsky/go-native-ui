//go:build windows

package webview

import (
	"time"
	"unsafe"

	"gonativeui/internal/win32"
	"gonativeui/platform"
)

var (
	pSetWindowPos     = win32.Proc(user32, "SetWindowPos")
	pShowWindow       = win32.Proc(user32, "ShowWindow")
	pGetWindowRgnBox  = win32.Proc(user32, "GetWindowRgnBox")
	parentRect, rgnBx [4]int32
)

// UseTestHost hosts each view's window over the whole client area of its
// window, in place of platform.Window.NewHost: the matrix runs before the
// window hosts child windows (W3). The host is 0.
func UseTestHost() {
	testHost = func(w *platform.Window, hwnd uintptr) (platform.HostID, error) {
		call(pGetClientRect, w.Native().HWND, uintptr(unsafe.Pointer(&parentRect)))
		call(pSetWindowPos, hwnd, 0, 0, 0, uintptr(parentRect[2]), uintptr(parentRect[3]), 0x0004|0x0010|0x0040) // SWP_NOZORDER | SWP_NOACTIVATE | SWP_SHOWWINDOW
		return 0, nil
	}
}

// LoadTime is how long the first New took to find and load the runtime (0
// before it).
func LoadTime() time.Duration { return rt.loadTime }

// Ready reports whether v's controller is there.
func Ready(v *View) bool { return v.be.(*view).ctrl != 0 }

// ShowView shows or hides v's window, as the window hides a host.
func ShowView(v *View, show bool) {
	cmd := uintptr(0) // SW_HIDE
	if show {
		cmd = 5 // SW_SHOW
	}
	call(pShowWindow, v.be.(*view).hwnd, cmd)
}

// Suspended reports whether v's web view is suspended (ICoreWebView2_3).
func Suspended(v *View) bool {
	b := v.be.(*view)
	if b.wv3 == 0 {
		return false
	}
	b.outI32 = 0
	comh(b.wv3, wvGetIsSuspended, uintptr(unsafe.Pointer(&b.outI32)))
	return b.outI32 != 0
}

// Rounded reports whether v's window has a rounded region
// (Options.CornerRadius): GetWindowRgnBox's COMPLEXREGION.
func Rounded(v *View) bool {
	return call(pGetWindowRgnBox, v.be.(*view).hwnd, uintptr(unsafe.Pointer(&rgnBx))) == 3
}

// Trace is v's debug trace.
func Trace(v *View) []string { return v.be.(*view).trace }
