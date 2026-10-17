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
	pOpenClipboard    = win32.Proc(user32, "OpenClipboard")
	pCloseClipboard   = win32.Proc(user32, "CloseClipboard")
	pEmptyClipboard   = win32.Proc(user32, "EmptyClipboard")
	pGetClipboardData = win32.Proc(user32, "GetClipboardData")
	pSetClipboardData = win32.Proc(user32, "SetClipboardData")
	pGlobalAlloc      = win32.Proc(kernel32, "GlobalAlloc")
	pGlobalFree       = win32.Proc(kernel32, "GlobalFree")
	pGlobalLock       = win32.Proc(kernel32, "GlobalLock")
	pGlobalUnlock     = win32.Proc(kernel32, "GlobalUnlock")
	pGlobalSize       = win32.Proc(kernel32, "GlobalSize")
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
// with \n line ends; "" if there is none. Main thread.
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

// SetClipboard replaces the system clipboard with the plain text s (as
// CF_UNICODETEXT, with \r\n line ends). Main thread.
func SetClipboard(s string) {
	s = strings.ReplaceAll(strings.ReplaceAll(strings.ToValidUTF8(s, "\uFFFD"), "\r\n", "\n"), "\n", "\r\n")
	u := append(utf16.Encode([]rune(s)), 0)
	mem := win32.Call(pGlobalAlloc, gmemMoveable, uintptr(len(u)*2))
	if mem == 0 {
		return
	}
	p := win32.Call(pGlobalLock, mem)
	if p == 0 {
		win32.Call(pGlobalFree, mem)
		return
	}
	copy(unsafe.Slice((*uint16)(win32.Ptr(p)), len(u)), u)
	win32.Call(pGlobalUnlock, mem)
	if !openClipboard() {
		win32.Call(pGlobalFree, mem)
		return
	}
	defer win32.Call(pCloseClipboard)
	win32.Call(pEmptyClipboard)
	if win32.Call(pSetClipboardData, cfUnicodeText, mem) == 0 {
		win32.Call(pGlobalFree, mem) // on success the system owns the memory
	}
}
