//go:build windows

package fs

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

// winShellExecInfo mirrors SHELLEXECUTEINFOW (shellapi.h:322-341) on 64-bit
// Windows (amd64 and arm64): 112 bytes.
type winShellExecInfo struct {
	Size     uint32
	Mask     uint32
	Hwnd     uintptr
	Verb     *uint16
	File     *uint16
	Params   *uint16
	Dir      *uint16
	Show     int32
	InstApp  uintptr
	IDList   uintptr
	Class    *uint16
	KeyClass uintptr
	HotKey   uint32
	Icon     uintptr // union with hMonitor
	Process  uintptr
}

var (
	_ [unsafe.Sizeof(winShellExecInfo{}) - 112]struct{}
	_ [112 - unsafe.Sizeof(winShellExecInfo{})]struct{}
	_ [unsafe.Offsetof(winShellExecInfo{}.Show) - 48]struct{}
	_ [48 - unsafe.Offsetof(winShellExecInfo{}.Show)]struct{}
	_ [unsafe.Offsetof(winShellExecInfo{}.InstApp) - 56]struct{}
	_ [56 - unsafe.Offsetof(winShellExecInfo{}.InstApp)]struct{}
	_ [unsafe.Offsetof(winShellExecInfo{}.HotKey) - 88]struct{}
	_ [88 - unsafe.Offsetof(winShellExecInfo{}.HotKey)]struct{}
	_ [unsafe.Offsetof(winShellExecInfo{}.Process) - 104]struct{}
	_ [104 - unsafe.Offsetof(winShellExecInfo{}.Process)]struct{}
)

const (
	winSeeNoAsync   = 0x100 // SEE_MASK_NOASYNC (shellapi.h:267)
	winSeeNoUI      = 0x400 // SEE_MASK_FLAG_NO_UI (shellapi.h:270)
	winSwShowNormal = 1
	winCoApartment  = 0x2 // COINIT_APARTMENTTHREADED
	winCoNoOle1DDE  = 0x4 // COINIT_DISABLE_OLE1DDE

	// winOpenWait bounds how long openPath blocks its caller (the UI
	// thread): a handler that takes longer to start is assumed to be
	// starting fine.
	winOpenWait = 250 * time.Millisecond
)

var (
	winShellOnce       sync.Once
	pCoInitializeEx    uintptr
	pCoUninitialize    uintptr
	pShellExecuteExW   uintptr
	winShellLoadFailed bool
)

func winLoadShell() {
	ole := win32.LoadSystem("ole32.dll")
	sh := win32.LoadSystem("shell32.dll")
	pCoInitializeEx = win32.Proc(ole, "CoInitializeEx")
	pCoUninitialize = win32.Proc(ole, "CoUninitialize")
	pShellExecuteExW = win32.Proc(sh, "ShellExecuteExW")
	winShellLoadFailed = pCoInitializeEx == 0 || pCoUninitialize == 0 || pShellExecuteExW == 0
}

// openPath opens path with its default application (a folder in Explorer)
// via ShellExecuteExW "open". The call runs on a dedicated locked thread
// with its own COM apartment (shell verbs can need an STA), and openPath
// waits at most winOpenWait for the result, so the UI thread never blocks
// on a slow handler; an error that comes back later is not reported.
func openPath(path string) error {
	winShellOnce.Do(winLoadShell)
	if winShellLoadFailed {
		return winPathErr("open", path, winErrNotSupported)
	}
	file, ok := winAppendUTF16(nil, path)
	if !ok || len(file) == 0 {
		return winPathErr("open", path, winErrInvalidName)
	}
	file = append(file, 0)
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		done <- winShellExecute(path, file)
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(winOpenWait):
		return nil
	}
}

func winShellExecute(path string, file []uint16) error {
	hr, _, _ := syscall.SyscallN(pCoInitializeEx, 0, winCoApartment|winCoNoOle1DDE)
	if int32(hr) >= 0 { // S_OK or S_FALSE: balance with CoUninitialize
		defer syscall.SyscallN(pCoUninitialize)
	}
	verb, _ := syscall.UTF16FromString("open")
	info := &winShellExecInfo{
		Mask: winSeeNoAsync | winSeeNoUI,
		Verb: &verb[0],
		File: &file[0],
		Show: winSwShowNormal,
	}
	info.Size = uint32(unsafe.Sizeof(*info))
	r, _, e := syscall.SyscallN(pShellExecuteExW, uintptr(unsafe.Pointer(info)))
	if uint32(r) != 0 {
		return nil
	}
	// hInstApp carries an SE_ERR_* code (2 file not found, 3 path not
	// found, 5 access denied, 31 no association) that coincides with the
	// Win32 code of the same meaning; prefer the thread's last error.
	if e == 0 {
		e = syscall.Errno(info.InstApp & 0xFFFF)
	}
	if e == 0 {
		e = winErrInvalidParam
	}
	return winPathErr("open", path, e)
}
