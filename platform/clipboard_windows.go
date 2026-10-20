//go:build windows

package platform

import (
	"runtime"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"gonativeui/internal/win32"
)

const (
	cfDIB         = 8
	cfUnicodeText = 13
	cfHDrop       = 15
	cfDIBV5       = 17
	gmemMoveable  = 0x0002
)

var (
	pOpenClipboard              = win32.Proc(user32, "OpenClipboard")
	pCloseClipboard             = win32.Proc(user32, "CloseClipboard")
	pEmptyClipboard             = win32.Proc(user32, "EmptyClipboard")
	pGetClipboardData           = win32.Proc(user32, "GetClipboardData")
	pCountClipboardFormats      = win32.Proc(user32, "CountClipboardFormats")
	pIsClipboardFormatAvailable = win32.Proc(user32, "IsClipboardFormatAvailable")
	pSetClipboardData           = win32.Proc(user32, "SetClipboardData")
	pGlobalAlloc                = win32.Proc(kernel32, "GlobalAlloc")
	pGlobalFree                 = win32.Proc(kernel32, "GlobalFree")
	pGlobalLock                 = win32.Proc(kernel32, "GlobalLock")
	pGlobalUnlock               = win32.Proc(kernel32, "GlobalUnlock")
	pGlobalSize                 = win32.Proc(kernel32, "GlobalSize")
	pRegisterClipboardFormatW   = win32.Proc(user32, "RegisterClipboardFormatW")
	pMultiByteToWideChar        = win32.Proc(kernel32, "MultiByteToWideChar")
	pGetACP                     = win32.Proc(kernel32, "GetACP")

	clipClassName = mustUTF16("STATIC") // the clipboard worker's window: a system class, so no window procedure of ours runs on its thread
)

const hwndMessage = ^uintptr(2) // HWND_MESSAGE (-3): the parent of a message-only window

// clipSession is set from a successful OpenClipboard of this process to its
// CloseClipboard, and while the clipboard worker takes its window's messages
// (clipWorker.pump). The process does its clipboard calls on one thread at a
// time: the system frees the process's clipboard memory in callbacks on the
// thread of the owner window, which must not overlap another thread's
// GetClipboardData; a worker whose window was closed may still be inside its
// session when the next window's worker starts; and Windows lets a second
// thread open the clipboard with the window that already has it open, which
// takes the first thread's session over. A session of the process in progress
// counts as a busy clipboard. Only the worker empties or sets the clipboard
// inside a session: EmptyClipboard sends the owner (the worker's window)
// WM_DESTROYCLIPBOARD, which the worker answers only in its own session.
var clipSession atomic.Bool

// tryOpenClipboard opens the clipboard for owner unless it is busy (open in
// another process, or in another thread of this one, or a closed worker's
// window is not destroyed yet: clipRetiring). After a true result the
// caller must call closeClipboard.
func tryOpenClipboard(owner uintptr) bool {
	if clipRetiring.Load() != 0 || !clipSession.CompareAndSwap(false, true) {
		return false
	}
	if clipRetiring.Load() != 0 { // a worker began retiring meanwhile: its window must go first
		clipSession.Store(false)
		return false
	}
	if win32.Bool(win32.Call(pOpenClipboard, owner)) {
		return true
	}
	clipSession.Store(false)
	return false
}

// closeClipboard ends the session tryOpenClipboard began.
func closeClipboard() {
	win32.Call(pCloseClipboard)
	clipSession.Store(false)
}

// openClipboard opens the clipboard for the window, retrying briefly: another
// process (a clipboard manager), or the window's clipboard worker, may hold it
// for a moment.
func openClipboard() bool {
	if pOpenClipboard == 0 {
		return false
	}
	var owner uintptr
	if w := theWindow; w != nil {
		owner = w.hwnd
	}
	for i := 0; i < 10; i++ {
		if tryOpenClipboard(owner) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// Clipboard returns the plain text on the system clipboard (CF_UNICODETEXT),
// with \n line ends; "" if there is none, or if the clipboard stays busy (in
// another process, or in the clipboard worker) for a moment. It reads on the calling thread, so
// an owner that renders on demand and never answers blocks it: the paste path
// (RequestPaste) reads on a worker instead, and nothing in the framework calls
// this. There is no synchronous SetClipboard on Windows for the same reason
// (EmptyClipboard waits for the previous owner); SetClipboardText copies on the
// worker. Main thread.
func Clipboard() string {
	if !openClipboard() {
		return ""
	}
	defer closeClipboard()
	h := win32.Call(pGetClipboardData, cfUnicodeText)
	if h == 0 {
		return ""
	}
	p := win32.Call(pGlobalLock, h)
	if p == 0 {
		return ""
	}
	defer win32.Call(pGlobalUnlock, h)
	n := int(win32.Call(pGlobalSize, h)) / 2
	u := unsafe.Slice((*uint16)(win32.Ptr(p)), n)
	for i, c := range u { // NUL-terminated
		if c == 0 {
			u = u[:i]
			break
		}
	}
	return strings.ReplaceAll(string(utf16.Decode(u)), "\r\n", "\n")
}

// acpToUTF8 appends the path p, in the system's ANSI code page (CP_ACP), to dst
// as UTF-8: what an ANSI CF_HDROP holds. A path the code page cannot convert
// appends nothing.
func acpToUTF8(dst, p []byte) []byte {
	if len(p) == 0 {
		return dst
	}
	n := int(int32(win32.Call(pMultiByteToWideChar, 0 /* CP_ACP */, 0, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), 0, 0)))
	runtime.KeepAlive(p)
	if n <= 0 {
		return dst
	}
	u := make([]uint16, n)
	m := int(int32(win32.Call(pMultiByteToWideChar, 0, 0, uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)), uintptr(unsafe.Pointer(&u[0])), uintptr(n))))
	runtime.KeepAlive(p)
	runtime.KeepAlive(u)
	for _, r := range utf16.Decode(u[:max(m, 0)]) {
		dst = utf8.AppendRune(dst, r)
	}
	return dst
}
