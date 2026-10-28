//go:build windows

package platform

import (
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// What the file dialogs (dialog_windows.go) need of OLE: the main thread as
// an OLE apartment and the app window message that runs their native modal
// loops. (Drag and drop, drag_windows.go, sets up OLE per window itself.)
//
// A file dialog's Show is modal: it runs a message loop of its own until the
// user is done. The contract (dialog.go) wants the call to return at once, so
// OpenPanel and SavePanel prepare everything, post oleRunMsg to the app
// window and return; the pump hands the message to runOLE, which runs the
// loop inside enterModal/leaveModal (the windows keep drawing through their
// frame handlers, the dispatcher runs on wakes) and queues the answer when it
// returns. One such loop runs at a time: a message that arrives while one
// runs is taken up when it ends.

var (
	pCoCreateInstance uintptr
	pCoTaskMemFree    uintptr
	pSHCreateItem     uintptr

	oleThreads [8]uint32 // the threads OleInitialize succeeded on (tests run on several)
	oleMsg     uint32    // oleRunMsg's id, registered once
	oleRunning bool      // runOLE is inside a modal loop
)

const (
	clsctxInprocServer = 1

	hrCancelled = 0x800704C7 // HRESULT_FROM_WIN32(ERROR_CANCELLED)

	// oleRunMsg's wParam.
	oleRunDialogs = 1
)

// ensureOLE makes the calling thread (the main thread) an OLE
// single-threaded apartment if it is not one of ours yet: StartApp does it
// for the app, and a program or test that opens windows without the app
// object gets it at its first dialog. It loads the OLE entry
// points and registers oleRunMsg. It reports false when OLE is unavailable
// (the thread is already a multithreaded apartment).
func ensureOLE() bool {
	if pCoCreateInstance == 0 {
		ole := win32.LoadSystem("ole32.dll")
		shell := win32.LoadSystem("shell32.dll")
		if ole32 == 0 {
			ole32 = ole
			pOleInitialize = win32.Proc(ole, "OleInitialize")
		}
		pCoCreateInstance = win32.Proc(ole, "CoCreateInstance")
		pCoTaskMemFree = win32.Proc(ole, "CoTaskMemFree")
		pSHCreateItem = win32.Proc(shell, "SHCreateItemFromParsingName")
	}
	if oleMsg == 0 {
		name := mustUTF16("GoNativeUI.OLEModal")
		oleMsg = uint32(win32.Call(pRegisterWindowMsg, uintptr(unsafe.Pointer(name))))
		if oleMsg == 0 || HandleAppMessage(oleMsg, runOLE) != nil {
			oleMsg = 0
			return false
		}
	}
	tid := uint32(win32.Call(pGetCurrentThreadID))
	if tid == reg.app.oleThread {
		return true
	}
	free := -1
	for i, t := range oleThreads {
		if t == tid {
			return true
		}
		if t == 0 && free < 0 {
			free = i
		}
	}
	if pOleInitialize == 0 || win32.HR(win32.Call(pOleInitialize, 0)) < 0 {
		return false
	}
	if free >= 0 {
		oleThreads[free] = tid
	}
	return true
}

// postOLE asks the pump to run runOLE with what (oleRunDialogs, …). Any
// thread.
func postOLE(what uintptr) {
	if h := wakeTarget.Load(); h != 0 && oleMsg != 0 {
		win32.Call(pPostMessageW, h, uintptr(oleMsg), what, 0)
	}
}

// runOLE is oleRunMsg's handler on the app window: it runs the dialogs that
// are waiting, one modal loop at a time. Main thread.
func runOLE(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	if oleRunning {
		return 0 // the loop that runs takes it up when it ends
	}
	oleRunning = true
	for runNextDialog() {
	}
	oleRunning = false
	return 0
}

// newCOM creates an object of class clsid with interface iid into out (a
// heap word).
func newCOM(clsid, iid *win32.GUID, out *uintptr) int32 {
	*out = 0
	return win32.HR(win32.Call(pCoCreateInstance, uintptr(unsafe.Pointer(clsid)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(out))))
}

// utf16z is s as a NUL-terminated UTF-16 string on the heap; nil for a
// string with a NUL.
func utf16z(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

// pwstrString converts the NUL-terminated UTF-16 string at p (owned by the
// system) to a Go string.
func pwstrString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*uint16)(unsafe.Add(win32.Ptr(p), 2*n)) != 0 {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(win32.Ptr(p)), n))
}
