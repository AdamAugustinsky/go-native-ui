package main

import (
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

var (
	user32           = win32.LoadSystem("user32.dll")
	pCreateWindowExW = win32.Proc(user32, "CreateWindowExW")
	pSendMessageW    = win32.Proc(user32, "SendMessageW")
	pGetStockObject  = win32.Proc(win32.LoadSystem("gdi32.dll"), "GetStockObject")
)

// newTextField is a Win32 EDIT control (a one-line, bordered text field)
// holding s, as a top-level window for NewHost to adopt; 0 on failure.
func newTextField(s string) uintptr {
	const (
		wsPopup, wsBorder, esAutoHScroll = 0x80000000, 0x00800000, 0x0080
		wmSetFont, defaultGUIFont        = 0x0030, 17
	)
	class, _ := syscall.UTF16PtrFromString("EDIT")
	text, _ := syscall.UTF16PtrFromString(s)
	h := win32.Call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(text)),
		wsPopup|wsBorder|esAutoHScroll, 0, 0, 120, 24, 0, 0, 0, 0)
	if h != 0 {
		win32.Call(pSendMessageW, h, wmSetFont, win32.Call(pGetStockObject, defaultGUIFont), 0)
	}
	return h
}
