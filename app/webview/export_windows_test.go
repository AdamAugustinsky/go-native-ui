//go:build windows

package webview

import (
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

var (
	pGetWindowRgnBox = win32.Proc(user32, "GetWindowRgnBox")
	pGetWindowRect   = win32.Proc(user32, "GetWindowRect")
	pGetDC           = win32.Proc(user32, "GetDC")
	pReleaseDC       = win32.Proc(user32, "ReleaseDC")
	pGetPixel        = win32.Proc(gdi32, "GetPixel")
	rgnBx, winRect   [4]int32
)

// ScreenPixel is the screen's colour (0xRRGGBB) at the centre of v's window:
// what the page shows there, composed.
func ScreenPixel(v *View) uint32 {
	call(pGetWindowRect, v.be.(*view).hwnd, uintptr(unsafe.Pointer(&winRect)))
	dc := call(pGetDC, 0)
	c := uint32(call(pGetPixel, dc, uintptr((winRect[0]+winRect[2])/2), uintptr((winRect[1]+winRect[3])/2)))
	call(pReleaseDC, 0, dc)
	return c&0xFF<<16 | c&0xFF00 | c>>16&0xFF // COLORREF is 0x00BBGGRR
}

// HostWindow is v's window, the host's view (platform.Window.HostView).
func HostWindow(v *View) uintptr { return v.be.(*view).hwnd }

// LoadTime is how long the first New took to find and load the runtime (0
// before it).
func LoadTime() time.Duration { return rt.loadTime }

// Ready reports whether v's controller is there.
func Ready(v *View) bool { return v.be.(*view).ctrl != 0 }

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

// CrashPage ends v's renderer process with the DevTools protocol's
// Page.crash (ICoreWebView2::CallDevToolsProtocolMethod, slot 36), as a
// crash of the page would.
func CrashPage(v *View) {
	b := v.be.(*view)
	h, err := handler(0, hSuspend) // a completion nobody reads
	if err != nil || b.wv == 0 {
		return
	}
	comh(b.wv, 36, uintptr(unsafe.Pointer(wide("Page.crash"))), uintptr(unsafe.Pointer(wide("{}"))), h.Ptr())
	h.Release()
}

// ProgressCalls counts the downloads' progress callbacks so far.
func ProgressCalls() int { return rt.bytesCalls }
