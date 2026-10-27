package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Plumbing local to the probe: calls whose pointer arguments are moved to
// the heap (the internal/win32 convention, by uintptrescapes), GUIDs parsed
// from their registry form, HRESULT text, a plain visible window and its
// message pump.

// call calls fn; what a pointer argument converted to uintptr in the call
// expression points to is moved to the heap.
//
//go:uintptrescapes
func call(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(fn, args...)
	return r
}

// cm calls vtable slot idx of the COM object obj, with call's rule.
//
//go:uintptrescapes
func cm(obj, idx uintptr, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(win32.Ptr(obj))
	fn := *(*uintptr)(unsafe.Add(vtbl, idx*unsafe.Sizeof(uintptr(0))))
	var buf [16]uintptr
	buf[0] = obj
	n := copy(buf[1:], args)
	r, _, _ := syscall.SyscallN(fn, buf[:1+n]...)
	return r
}

// guid parses "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx" (braces optional).
func guid(s string) win32.GUID {
	s = strings.Trim(s, "{}")
	p := strings.Split(s, "-")
	if len(p) != 5 || len(p[0]) != 8 || len(p[1]) != 4 || len(p[2]) != 4 || len(p[3]) != 4 || len(p[4]) != 12 {
		panic("probe: bad GUID " + s)
	}
	u := func(h string, bits int) uint64 {
		v, err := strconv.ParseUint(h, 16, bits)
		if err != nil {
			panic("probe: bad GUID " + s)
		}
		return v
	}
	g := win32.GUID{Data1: uint32(u(p[0], 32)), Data2: uint16(u(p[1], 16)), Data3: uint16(u(p[2], 16))}
	rest := p[3] + p[4]
	for i := range g.Data4 {
		g.Data4[i] = byte(u(rest[2*i:2*i+2], 8))
	}
	return g
}

// hrText is an HRESULT as 0x%08X.
func hrText(hr int32) string { return fmt.Sprintf("0x%08X", uint32(hr)) }

var (
	kernel32 = win32.LoadSystem("kernel32.dll")
	user32   = win32.LoadSystem("user32.dll")
	gdi32    = win32.LoadSystem("gdi32.dll")
	ole32    = win32.LoadSystem("ole32.dll")
	ntdll    = win32.LoadSystem("ntdll.dll")

	pGetModuleHandleW  = win32.Proc(kernel32, "GetModuleHandleW")
	pRegisterClassExW  = win32.Proc(user32, "RegisterClassExW")
	pCreateWindowExW   = win32.Proc(user32, "CreateWindowExW")
	pDestroyWindow     = win32.Proc(user32, "DestroyWindow")
	pDefWindowProcW    = win32.Proc(user32, "DefWindowProcW")
	pPeekMessageW      = win32.Proc(user32, "PeekMessageW")
	pTranslateMessage  = win32.Proc(user32, "TranslateMessage")
	pDispatchMessageW  = win32.Proc(user32, "DispatchMessageW")
	pInvalidateRect    = win32.Proc(user32, "InvalidateRect")
	pUpdateWindow      = win32.Proc(user32, "UpdateWindow")
	pSetClassLongPtrW  = win32.Proc(user32, "SetClassLongPtrW")
	pCreateSolidBrush  = win32.Proc(gdi32, "CreateSolidBrush")
	pDeleteObject      = win32.Proc(gdi32, "DeleteObject")
	pCoTaskMemFree     = win32.Proc(ole32, "CoTaskMemFree")
	pRtlGetVersion     = win32.Proc(ntdll, "RtlGetVersion")
	msg                [48]byte // MSG (winuser.h), 48 bytes on 64-bit
	wndClass           wndClassEx
	className, winName []uint16
)

// wndClassEx is WNDCLASSEXW (winuser.h).
type wndClassEx struct {
	size, style            uint32
	wndProc                uintptr
	clsExtra, wndExtra     int32
	instance, icon, cursor uintptr
	background             uintptr
	menuName, className    *uint16
	iconSm                 uintptr
}

// newWindow opens a plain visible 640×480 window whose procedure is
// DefWindowProcW itself (no Go callback): enough for capture.
func newWindow(title string) uintptr {
	className, _ = syscall.UTF16FromString("GnuiProbeP4")
	winName, _ = syscall.UTF16FromString(title)
	inst := call(pGetModuleHandleW, 0)
	wndClass = wndClassEx{wndProc: pDefWindowProcW, instance: inst, background: call(pCreateSolidBrush, 0x00C08040), className: &className[0]}
	wndClass.size = uint32(unsafe.Sizeof(wndClass))
	call(pRegisterClassExW, uintptr(unsafe.Pointer(&wndClass)))
	const wsOverlappedWindow, wsVisible, cwUseDefault = 0x00CF0000, 0x10000000, 0x80000000
	hwnd := call(pCreateWindowExW, 0, uintptr(unsafe.Pointer(&className[0])), uintptr(unsafe.Pointer(&winName[0])),
		wsOverlappedWindow|wsVisible, cwUseDefault, cwUseDefault, 640, 480, 0, 0, inst, 0)
	call(pUpdateWindow, hwnd)
	return hwnd
}

// repaint gives the window a new background colour and invalidates it, so
// the compositor has a new frame to deliver.
func repaint(hwnd uintptr, i int) {
	const gclpHbrBackground = ^uintptr(9) // -10
	c := uintptr(0x00402000 + (i*37%200)<<8 + i*13%200)
	if old := call(pSetClassLongPtrW, hwnd, gclpHbrBackground, call(pCreateSolidBrush, c)); old != 0 {
		call(pDeleteObject, old)
	}
	call(pInvalidateRect, hwnd, 0, 1)
}

// pump dispatches every queued message of the thread.
func pump() {
	for call(pPeekMessageW, uintptr(unsafe.Pointer(&msg)), 0, 0, 0, 1) != 0 {
		call(pTranslateMessage, uintptr(unsafe.Pointer(&msg)))
		call(pDispatchMessageW, uintptr(unsafe.Pointer(&msg)))
	}
}

// osVersion is RtlGetVersion's major, minor and build.
func osVersion() [3]uint32 {
	var v struct {
		size, major, minor, build, platform uint32
		csd                                 [128]uint16
	}
	v.size = uint32(unsafe.Sizeof(v))
	call(pRtlGetVersion, uintptr(unsafe.Pointer(&v)))
	return [3]uint32{v.major, v.minor, v.build}
}
