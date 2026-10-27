//go:build windows

package main

import (
	"errors"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Win32 plumbing: the host window, the RichEdit child, messages, the
// clipboard and a PrintWindow read. Constants from winuser.h, wingdi.h,
// imm.h, winbase.h and richedit.h (cited where they are defined).

var (
	user32 = win32.LoadSystem("user32.dll")
	gdi32  = win32.LoadSystem("gdi32.dll")
	k32    = win32.LoadSystem("kernel32.dll")
	ole32  = win32.LoadSystem("ole32.dll")
	imm32  = win32.LoadSystem("imm32.dll")

	pRegisterClassExW   = win32.Proc(user32, "RegisterClassExW")
	pCreateWindowExW    = win32.Proc(user32, "CreateWindowExW")
	pDefWindowProcW     = win32.Proc(user32, "DefWindowProcW")
	pPeekMessageW       = win32.Proc(user32, "PeekMessageW")
	pTranslateMessage   = win32.Proc(user32, "TranslateMessage")
	pDispatchMessageW   = win32.Proc(user32, "DispatchMessageW")
	pSendMessageW       = win32.Proc(user32, "SendMessageW")
	pShowWindow         = win32.Proc(user32, "ShowWindow")
	pUpdateWindow       = win32.Proc(user32, "UpdateWindow")
	pDestroyWindow      = win32.Proc(user32, "DestroyWindow")
	pSetFocus           = win32.Proc(user32, "SetFocus")
	pOpenClipboard      = win32.Proc(user32, "OpenClipboard")
	pEmptyClipboard     = win32.Proc(user32, "EmptyClipboard")
	pSetClipboardData   = win32.Proc(user32, "SetClipboardData")
	pCloseClipboard     = win32.Proc(user32, "CloseClipboard")
	pPrintWindow        = win32.Proc(user32, "PrintWindow")
	pGetDC              = win32.Proc(user32, "GetDC")
	pReleaseDC          = win32.Proc(user32, "ReleaseDC")
	pGetModuleHandleW   = win32.Proc(k32, "GetModuleHandleW")
	pGlobalAlloc        = win32.Proc(k32, "GlobalAlloc")
	pGlobalLock         = win32.Proc(k32, "GlobalLock")
	pGlobalUnlock       = win32.Proc(k32, "GlobalUnlock")
	pCreateCompatibleDC = win32.Proc(gdi32, "CreateCompatibleDC")
	pCreateDIBSection   = win32.Proc(gdi32, "CreateDIBSection")
	pSelectObject       = win32.Proc(gdi32, "SelectObject")
	pGdiFlush           = win32.Proc(gdi32, "GdiFlush")
	pOleInitialize      = win32.Proc(ole32, "OleInitialize")
	pImmGetContext      = win32.Proc(imm32, "ImmGetContext")
	pImmReleaseContext  = win32.Proc(imm32, "ImmReleaseContext")
	pImmIsIME           = win32.Proc(imm32, "ImmIsIME")
	pImmGetOpenStatus   = win32.Proc(imm32, "ImmGetOpenStatus")
	pImmSetOpenStatus   = win32.Proc(imm32, "ImmSetOpenStatus")
	pImmSetCompString   = win32.Proc(imm32, "ImmSetCompositionStringW")
	pImmNotifyIME       = win32.Proc(imm32, "ImmNotifyIME")
	pGetKeyboardLayout  = win32.Proc(user32, "GetKeyboardLayout")
	pGetKbdLayoutList   = win32.Proc(user32, "GetKeyboardLayoutList")
	pLoadKeyboardLayout = win32.Proc(user32, "LoadKeyboardLayoutW")
	pActivateKbdLayout  = win32.Proc(user32, "ActivateKeyboardLayout")
	pGetFocus           = win32.Proc(user32, "GetFocus")

	wndProcCB = syscall.NewCallback(wndProc)
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsVScroll          = 0x00200000
	esMultiline        = 0x0004
	esWantReturn       = 0x1000

	wmNotify   = 0x004E
	pmRemove   = 0x0001
	swShow     = 5
	cfUnicode  = 13     // CF_UNICODETEXT (winuser.h)
	gmemMove   = 0x0002 // GMEM_MOVEABLE (winbase.h)
	pwClient   = 0x0001 // PW_CLIENTONLY
	enReqSize  = 0x0701 // EN_REQUESTRESIZE (richedit.h)
	hostW      = 520
	hostH      = 420
	editW      = 480
	editH      = 300
	dibW, dibH = hostW, hostH // reads cover the host's client area
)

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

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      [2]int32
	private uint32
}

type bitmapInfo struct {
	size          uint32
	width, height int32
	planes, bits  uint16
	compression   uint32
	sizeImage     uint32
	xppm, yppm    int32
	clrUsed       uint32
	clrImportant  uint32
	colors        [1]uint32
}

// wins holds the windows and every out-parameter (package memory).
var wins struct {
	inst, top, edit  uintptr
	wc               wndClassEx
	m                msg
	bmi              bitmapInfo
	bits             uintptr
	memDC, dib       uintptr
	name, reClass    *uint16
	text             [8192]uint16
	lastResizeHeight int32
	resizeNotes      int
}

//go:uintptrescapes
func call(fn uintptr, args ...uintptr) uintptr { return win32.Call(fn, args...) }

//go:uintptrescapes
func comCall(obj, idx uintptr, args ...uintptr) uintptr { return win32.Com(obj, idx, args...) }

// send is SendMessageW to the RichEdit control.
//
//go:uintptrescapes
func send(m, wp, lp uintptr) uintptr { return win32.Call(pSendMessageW, wins.edit, m, wp, lp) }

func wndProc(hwnd, m, wp, lp uintptr) uintptr {
	if m == wmNotify && lp != 0 {
		// REQRESIZE (richedit.h): NMHDR {HWND hwndFrom; UINT_PTR idFrom;
		// UINT code} (24 bytes on 64-bit, with padding), then RECT rc.
		code := *(*uint32)(unsafe.Add(win32.Ptr(lp), 2*unsafe.Sizeof(uintptr(0))))
		if code == enReqSize {
			rc := (*[4]int32)(unsafe.Add(win32.Ptr(lp), 3*unsafe.Sizeof(uintptr(0))))
			wins.lastResizeHeight = rc[3] - rc[1]
			wins.resizeNotes++
		}
		return 0
	}
	return call(pDefWindowProcW, hwnd, m, wp, lp)
}

// makeWindows creates the host window and the RichEdit control in it.
func makeWindows() error {
	wins.inst = call(pGetModuleHandleW, 0)
	wins.name, _ = syscall.UTF16PtrFromString("GNUIProbeP5Host")
	wins.reClass, _ = syscall.UTF16PtrFromString("RICHEDIT50W") // MSFTEDIT_CLASS (richedit.h)
	wins.wc = wndClassEx{wndProc: wndProcCB, instance: wins.inst, background: 6 /* COLOR_WINDOW+1 */, className: wins.name}
	wins.wc.size = uint32(unsafe.Sizeof(wins.wc))
	if call(pRegisterClassExW, uintptr(unsafe.Pointer(&wins.wc)))&0xFFFF == 0 {
		return errors.New("RegisterClassExW failed")
	}
	wins.top = call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(wins.name)), 0, wsOverlappedWindow,
		100, 100, hostW, hostH, 0, 0, wins.inst, 0)
	if wins.top == 0 {
		return errors.New("CreateWindowExW (host) failed")
	}
	wins.edit = call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(wins.reClass)), 0,
		wsChild|wsVisible|wsVScroll|esMultiline|esWantReturn, 10, 10, editW, editH, wins.top, 1, wins.inst, 0)
	if wins.edit == 0 {
		return errors.New("CreateWindowExW (RICHEDIT50W) failed")
	}
	call(pShowWindow, wins.top, swShow)
	call(pUpdateWindow, wins.top)
	call(pSetFocus, wins.edit)
	pump()
	return nil
}

func pump() {
	for win32.Bool(call(pPeekMessageW, uintptr(unsafe.Pointer(&wins.m)), 0, 0, 0, pmRemove)) {
		call(pTranslateMessage, uintptr(unsafe.Pointer(&wins.m)))
		call(pDispatchMessageW, uintptr(unsafe.Pointer(&wins.m)))
	}
}

// utf16z is s as a NUL-terminated UTF-16 slice (heap).
func utf16z(s string) []uint16 {
	u, _ := syscall.UTF16FromString(s)
	return u
}

// setText replaces the control's text (WM_SETTEXT).
func setText(s string) {
	u := utf16z(s)
	send(0x000C, 0, uintptr(unsafe.Pointer(&u[0])))
}

// text reads the control's text (WM_GETTEXT); RichEdit reports paragraph
// ends as "\r".
func text() string {
	n := send(0x000D, uintptr(len(wins.text)), uintptr(unsafe.Pointer(&wins.text[0])))
	return syscall.UTF16ToString(wins.text[:min(int(n), len(wins.text))])
}

// putClipboard puts s on the clipboard as CF_UNICODETEXT.
func putClipboard(s string) bool {
	u := utf16z(s)
	if !win32.Bool(call(pOpenClipboard, wins.top)) {
		return false
	}
	defer call(pCloseClipboard)
	call(pEmptyClipboard)
	h := call(pGlobalAlloc, gmemMove, uintptr(len(u)*2))
	p := call(pGlobalLock, h)
	if p == 0 {
		return false
	}
	copy(unsafe.Slice((*uint16)(win32.Ptr(p)), len(u)), u)
	call(pGlobalUnlock, h)
	return call(pSetClipboardData, cfUnicode, h) != 0
}
