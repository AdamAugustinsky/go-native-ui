//go:build windows

package webview

import (
	"syscall"
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
	pWindowFromPoint = win32.Proc(user32, "WindowFromPoint")
	pGetAncestor     = win32.Proc(user32, "GetAncestor")
	pSetWindowPos    = win32.Proc(user32, "SetWindowPos")
	pGetClassName    = win32.Proc(user32, "GetClassNameW")
	rgnBx, winRect   [4]int32
	className        [64]uint16
)

// ScreenPixel is the screen's colour (0xRRGGBB) at the centre of v's window:
// what the page shows there, composed. Test packages run side by side on
// one desktop, so it puts v's top-level window above the others while it reads; when
// another program's window still covers the point, covered is that window's
// class.
func ScreenPixel(v *View) (px uint32, covered string) {
	hwnd := v.be.(*view).hwnd
	const gaRoot, swpNoSizeMoveActivate = 2, 0x1 | 0x2 | 0x10
	root := call(pGetAncestor, hwnd, gaRoot)
	call(pSetWindowPos, root, ^uintptr(0), 0, 0, 0, 0, swpNoSizeMoveActivate) // HWND_TOPMOST
	call(pGetWindowRect, hwnd, uintptr(unsafe.Pointer(&winRect)))
	x, y := (winRect[0]+winRect[2])/2, (winRect[1]+winRect[3])/2
	if at := call(pWindowFromPoint, uintptr(uint32(x))|uintptr(uint32(y))<<32); call(pGetAncestor, at, gaRoot) != root {
		n := call(pGetClassName, at, uintptr(unsafe.Pointer(&className[0])), uintptr(len(className)))
		covered = syscall.UTF16ToString(className[:n])
	}
	dc := call(pGetDC, 0)
	c := uint32(call(pGetPixel, dc, uintptr(x), uintptr(y)))
	call(pReleaseDC, 0, dc)
	call(pSetWindowPos, root, ^uintptr(1), 0, 0, 0, 0, swpNoSizeMoveActivate) // HWND_NOTOPMOST: not over other packages' windows
	return c&0xFF<<16 | c&0xFF00 | c>>16&0xFF, covered // COLORREF is 0x00BBGGRR
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
