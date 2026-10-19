//go:build windows

package platform

import (
	"strings"
	"time"
	"unicode/utf16"
	"unsafe"

	"gonativeui/internal/win32"
)

const (
	cfUnicodeText = 13
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
)

// openClipboard opens the clipboard for the window, retrying briefly: another
// process (a clipboard manager) may hold it for a moment.
func openClipboard() bool {
	if pOpenClipboard == 0 {
		return false
	}
	var owner uintptr
	if w := theWindow; w != nil {
		owner = w.hwnd
	}
	for i := 0; i < 10; i++ {
		if win32.Bool(win32.Call(pOpenClipboard, owner)) {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// Clipboard returns the plain text on the system clipboard (CF_UNICODETEXT),
// with \n line ends; "" if there is none. It reads on the calling thread, so
// an owner that renders on demand and never answers blocks it: the paste path
// (RequestPaste) reads on a worker instead, and nothing in the framework calls
// this. There is no synchronous SetClipboard on Windows for the same reason
// (EmptyClipboard waits for the previous owner); SetClipboardText copies on the
// worker. Main thread.
func Clipboard() string {
	if !openClipboard() {
		return ""
	}
	defer win32.Call(pCloseClipboard)
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
