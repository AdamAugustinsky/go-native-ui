//go:build windows

package main

import (
	"path/filepath"
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// What covers our window when the screen read does not show it: the first
// run on windows-11-arm read none of the probe's colours from the screen,
// while PrintWindow showed them, and P7 found a Windows.UI.Core.CoreWindow
// in the foreground there. A screen read is then not a measurement of
// trailing, and the report says so with the covering window named.

var (
	pGetClassNameW       = win32.Proc(user32, "GetClassNameW")
	pGetWindowTextW      = win32.Proc(user32, "GetWindowTextW")
	pGetWindowRect       = win32.Proc(user32, "GetWindowRect")
	pGetAncestor         = win32.Proc(user32, "GetAncestor")
	pGetForegroundWindow = win32.Proc(user32, "GetForegroundWindow")
	pGetWindowThreadPID  = win32.Proc(user32, "GetWindowThreadProcessId")
	pOpenProcess         = win32.Proc(k32, "OpenProcess")
	pQueryFullImageName  = win32.Proc(k32, "QueryFullProcessImageNameW")
)

// windowInfo names a window: class, title, process image and rectangle.
type windowInfo struct {
	Class   string   `json:"class"`
	Title   string   `json:"title,omitempty"`
	Process string   `json:"process,omitempty"`
	Rect    [4]int32 `json:"rect"`
	Ours    bool     `json:"ours"`
}

var infoBuf [512]uint16

func describe(h uintptr) *windowInfo {
	if h == 0 {
		return nil
	}
	w := &windowInfo{}
	n := call(pGetClassNameW, h, uintptr(unsafe.Pointer(&infoBuf[0])), uintptr(len(infoBuf)))
	w.Class = syscall.UTF16ToString(infoBuf[:n])
	n = call(pGetWindowTextW, h, uintptr(unsafe.Pointer(&infoBuf[0])), uintptr(len(infoBuf)))
	w.Title = syscall.UTF16ToString(infoBuf[:n])
	call(pGetWindowRect, h, uintptr(unsafe.Pointer(&w.Rect)))
	var pid uint32
	call(pGetWindowThreadPID, h, uintptr(unsafe.Pointer(&pid)))
	w.Ours = int(pid) == syscall.Getpid()
	if p := call(pOpenProcess, 0x1000, 0, uintptr(pid)); p != 0 { // PROCESS_QUERY_LIMITED_INFORMATION
		size := uint32(len(infoBuf))
		if win32.Bool(call(pQueryFullImageName, p, 0, uintptr(unsafe.Pointer(&infoBuf[0])), uintptr(unsafe.Pointer(&size)))) {
			w.Process = filepath.Base(syscall.UTF16ToString(infoBuf[:size]))
		}
		win32.CloseHandle(p)
	}
	return w
}

// covering describes the top-level window at the child's sample point and
// the foreground window.
func covering() map[string]*windowInfo {
	at := windowAt(sampleChild, childRestInsideY)
	return map[string]*windowInfo{
		"at_sample_point": describe(call(pGetAncestor, at, 2)), // GA_ROOT
		"foreground":      describe(call(pGetForegroundWindow)),
	}
}
