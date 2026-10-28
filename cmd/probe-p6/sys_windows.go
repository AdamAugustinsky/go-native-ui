package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// Plumbing local to the probe (as probe-p4's): calls whose pointer
// arguments are moved to the heap (uintptrescapes), GUIDs from their
// registry form, HRESULT text, the thread's message pump, the OS version.

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

// utf16 is s as a NUL-terminated UTF-16 slice (s holds no NUL).
func utf16(s string) []uint16 {
	u, err := syscall.UTF16FromString(s)
	if err != nil {
		panic(err)
	}
	return u
}

var (
	user32   = win32.LoadSystem("user32.dll")
	ole32    = win32.LoadSystem("ole32.dll")
	shell32  = win32.LoadSystem("shell32.dll")
	advapi32 = win32.LoadSystem("advapi32.dll")
	ntdll    = win32.LoadSystem("ntdll.dll")

	pPeekMessageW     = win32.Proc(user32, "PeekMessageW")
	pTranslateMessage = win32.Proc(user32, "TranslateMessage")
	pDispatchMessageW = win32.Proc(user32, "DispatchMessageW")
	pCoCreateInstance = win32.Proc(ole32, "CoCreateInstance")
	pSetAppID         = win32.Proc(shell32, "SetCurrentProcessExplicitAppUserModelID")
	pRegCreateKeyExW  = win32.Proc(advapi32, "RegCreateKeyExW")
	pRegSetValueExW   = win32.Proc(advapi32, "RegSetValueExW")
	pRegCloseKey      = win32.Proc(advapi32, "RegCloseKey")
	pRegDeleteTreeW   = win32.Proc(advapi32, "RegDeleteTreeW")
	pRtlGetVersion    = win32.Proc(ntdll, "RtlGetVersion")
	msg               [48]byte // MSG (winuser.h), 48 bytes on 64-bit
)

// pump dispatches every queued message of the thread (the STA's calls).
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

// registerAUMID writes HKCU\Software\Classes\AppUserModelId\<id> with its
// DisplayName, as a3-kb's C# client does (apps/windows/src/Notifier.cs).
func registerAUMID(id, name string) int32 {
	const keyWrite, regSZ = 0x20006, 1
	var hk uintptr
	sub := utf16(`Software\Classes\AppUserModelId\` + id)
	if rc := call(pRegCreateKeyExW, uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(&sub[0])), 0, 0, 0, keyWrite, 0,
		uintptr(unsafe.Pointer(&hk)), 0); rc != 0 {
		return int32(0x80070000 | uint32(rc))
	}
	defer call(pRegCloseKey, hk)
	v, n := utf16("DisplayName"), utf16(name)
	rc := call(pRegSetValueExW, hk, uintptr(unsafe.Pointer(&v[0])), 0, regSZ, uintptr(unsafe.Pointer(&n[0])), uintptr(2*len(n)))
	if rc != 0 {
		return int32(0x80070000 | uint32(rc))
	}
	return 0
}

// unregisterAUMID removes the key registerAUMID wrote.
func unregisterAUMID(id string) int32 {
	sub := utf16(`Software\Classes\AppUserModelId\` + id)
	if rc := call(pRegDeleteTreeW, uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(&sub[0]))); rc != 0 {
		return int32(0x80070000 | uint32(rc))
	}
	return 0
}
