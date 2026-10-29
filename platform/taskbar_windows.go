package platform

import (
	"unsafe"

	"gonativeui/internal/win32"
)

// The app's presence in the taskbar (the phase 8 plan's S1, W5): the
// attention flash, the hooks app/dock uses to keep its progress and badge on
// every window's taskbar button, and what app/tray needs of the app object.
// Windows has no app-wide taskbar tile: each top-level window has its own
// button, so app-wide state is applied to every open window, and again to a
// window whose button the shell (re)creates (the registered
// "TaskbarButtonCreated" message, sent once a window's button exists and
// again after Explorer restarts).

var taskbar struct {
	buttonMsg uint32             // RegisterWindowMessageW("TaskbarButtonCreated"), 0 until OnTaskbarButton
	onButton  func(hwnd uintptr) // the hook (app/dock)
	flash     flashWInfo         // the call's argument (heap)
	attention int                // the last RequestAttention's id
	flashing  bool               // that request is not cancelled
	pid       uint32             // GetWindowThreadProcessId's out parameter (heap)
}

var (
	pGetWindowThreadPID = win32.Proc(user32, "GetWindowThreadProcessId")
	pGetCurrentProcID   = win32.Proc(kernel32, "GetCurrentProcessId")
	taskbarButtonName   = mustUTF16("TaskbarButtonCreated")
)

const (
	flashwStop = 0x0
	flashwAll  = 0x3
)

// AppStarted reports whether the application object exists (StartApp ran).
func AppStarted() bool { return reg.app.started }

// DeliverAppCommand delivers menu command id to the app (EvCommand), as a
// tray menu's commands are (app/tray). Main thread.
func DeliverAppCommand(id uint16) { deliverNow(AppEvCommand, uint64(id), 0, 0, nil) }

// OnTaskbarButton makes fn the hook called, on the main thread, when the
// shell has made a window's taskbar button (and when it makes it again after
// Explorer restarts): taskbar state set on a window before its button exists
// is lost, so a service that keeps app-wide taskbar state (app/dock's
// progress and badge) applies it to that window there. nil removes it. Main
// thread.
func OnTaskbarButton(fn func(hwnd uintptr)) {
	if taskbar.buttonMsg == 0 && pRegisterWindowMsg != 0 {
		taskbar.buttonMsg = uint32(win32.Call(pRegisterWindowMsg, uintptr(unsafe.Pointer(taskbarButtonName))))
	}
	taskbar.onButton = fn
}

func taskbarButtonCreated(hwnd uintptr) {
	if fn := taskbar.onButton; fn != nil {
		fn(hwnd)
	}
}

// TaskbarWindows appends the HWNDs of the open windows to dst: the windows
// that may have a taskbar button. Main thread.
func TaskbarWindows(dst []uintptr) []uintptr {
	for _, w := range openWindows() {
		if w.hwnd != 0 {
			dst = append(dst, w.hwnd)
		}
	}
	return dst
}

// appIsForeground reports whether the foreground window is this process's.
func appIsForeground() bool {
	fg := win32.Call(pGetForegroundWindow)
	if fg == 0 {
		return false
	}
	taskbar.pid = 0
	win32.Call(pGetWindowThreadPID, fg, uintptr(unsafe.Pointer(&taskbar.pid)))
	return taskbar.pid == uint32(win32.Call(pGetCurrentProcID))
}

// RequestAttention flashes the taskbar buttons of the app's windows
// (FlashWindowEx): once, leaving them highlighted until the app is
// activated, or (critical) until it is, and returns the request's id for
// CancelAttention; 0 when the app is in the foreground already or has no
// window. Main thread.
func RequestAttention(critical bool) int {
	if appIsForeground() || reg.n == 0 {
		return 0
	}
	flags, count := uint32(flashwTray), uint32(1)
	if critical {
		flags, count = flashwAll|flashwTimerNoFG, 0
	}
	flashAll(flags, count)
	taskbar.attention++
	taskbar.flashing = true
	return taskbar.attention
}

// CancelAttention stops the flashing of the request id, if it is the last one
// and still on. Main thread.
func CancelAttention(id int) {
	if id <= 0 || id != taskbar.attention || !taskbar.flashing {
		return
	}
	taskbar.flashing = false
	flashAll(flashwStop, 0)
}

func flashAll(flags, count uint32) {
	for _, w := range openWindows() {
		if w.hwnd == 0 {
			continue
		}
		taskbar.flash = flashWInfo{hwnd: w.hwnd, flags: flags, count: count}
		taskbar.flash.size = uint32(unsafe.Sizeof(taskbar.flash))
		win32.Call(pFlashWindowEx, uintptr(unsafe.Pointer(&taskbar.flash)))
	}
}
