//go:build windows

package shell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/fs"
	"gonativeui/internal/win32"
)

// Windows. The shell calls that show windows or start handlers need a COM
// apartment, so they run on a goroutine of their own, locked to its thread,
// with COM initialised for the call (the same arrangement as fs.Open).

type osSystem struct{}

const (
	coApartment = 0x2 // COINIT_APARTMENTTHREADED
	coNoOle1DDE = 0x4 // COINIT_DISABLE_OLE1DDE
	swShow      = 1   // SW_SHOWNORMAL

	// staWait bounds how long a call waits for the shell; past it the call is
	// reported as timed out and finishes, detached, on its own thread.
	staWait = 20 * time.Second

	foSilent       = 0x0004 // FOF_SILENT
	foAllowUndo    = 0x0040 // FOF_ALLOWUNDO
	foNoErrorUI    = 0x0400 // FOF_NOERRORUI
	foWantNukeWarn = 0x4000 // FOF_WANTNUKEWARNING
	foDelete       = 0x0003 // FO_DELETE
)

var (
	loadOnce                                  sync.Once
	pCoInit, pCoUninit, pCoTaskMemFree        uintptr
	pShellExecute, pParseName, pOpenAndSelect uintptr
	pFindLastID, pFileOperation               uintptr
	loadErr                                   error
)

func load() {
	ole, sh := win32.LoadSystem("ole32.dll"), win32.LoadSystem("shell32.dll")
	pCoInit, pCoUninit, pCoTaskMemFree = win32.Proc(ole, "CoInitializeEx"), win32.Proc(ole, "CoUninitialize"), win32.Proc(ole, "CoTaskMemFree")
	pShellExecute, pParseName = win32.Proc(sh, "ShellExecuteW"), win32.Proc(sh, "SHParseDisplayName")
	pOpenAndSelect, pFindLastID = win32.Proc(sh, "SHOpenFolderAndSelectItems"), win32.Proc(sh, "ILFindLastID")
	pFileOperation = win32.Proc(sh, "SHFileOperationW")
	for _, p := range []uintptr{pCoInit, pCoUninit, pCoTaskMemFree, pShellExecute, pParseName, pOpenAndSelect, pFindLastID, pFileOperation} {
		if p == 0 {
			loadErr = ErrUnavailable
		}
	}
}

func ready() error {
	loadOnce.Do(load)
	return loadErr
}

// onSTA runs f on a locked thread in a single-threaded apartment and waits for
// it, for up to staWait.
func onSTA(op, name string, f func() error) error {
	done := make(chan error, 1) // buffered: a late result is dropped, not waited for
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hr, _, _ := syscall.SyscallN(pCoInit, 0, coApartment|coNoOle1DDE)
		if int32(hr) >= 0 { // S_OK or S_FALSE: balance with CoUninitialize
			defer syscall.SyscallN(pCoUninit)
		}
		done <- f()
	}()
	t := time.NewTimer(staWait)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		return &os.PathError{Op: op, Path: name, Err: os.ErrDeadlineExceeded}
	}
}

func (osSystem) openURL(u string) error {
	if err := ready(); err != nil {
		return err
	}
	file, err := syscall.UTF16PtrFromString(u)
	if err != nil {
		return ErrInvalid
	}
	verb, _ := syscall.UTF16PtrFromString("open")
	return onSTA("open", u, func() error {
		// ShellExecuteW returns a value above 32 on success, else an SE_ERR_* code.
		r, _, _ := syscall.SyscallN(pShellExecute, 0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, swShow)
		if r > 32 {
			return nil
		}
		return &os.PathError{Op: "open", Path: u, Err: shellExecError(r)}
	})
}

func shellExecError(code uintptr) error {
	switch code {
	case 2, 3: // SE_ERR_FNF, SE_ERR_PNF
		return os.ErrNotExist
	case 5: // SE_ERR_ACCESSDENIED
		return os.ErrPermission
	case 31: // SE_ERR_NOASSOC
		return errors.New("no application is associated with this")
	}
	return fmt.Errorf("ShellExecute error %d", code)
}

// openPath is fs.Open: ShellExecuteExW "open", shared with the file browser.
func (osSystem) openPath(p string) error { return fs.Open(p) }

func (osSystem) reveal(paths []string) error {
	if err := ready(); err != nil {
		return err
	}
	// SHOpenFolderAndSelectItems selects items of one folder: one call per folder.
	var order []string
	byDir := map[string][]string{}
	for _, p := range paths {
		d := filepath.Dir(p)
		if _, ok := byDir[d]; !ok {
			order = append(order, d)
		}
		byDir[d] = append(byDir[d], p)
	}
	for _, d := range order {
		items := byDir[d]
		if err := onSTA("reveal", items[0], func() error { return selectItems(d, items) }); err != nil {
			return err
		}
	}
	return nil
}

// parse turns a path into an absolute item ID list the caller frees with
// CoTaskMemFree.
func parse(p string) (uintptr, error) {
	w, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return 0, ErrInvalid
	}
	var pidl uintptr
	hr, _, _ := syscall.SyscallN(pParseName, uintptr(unsafe.Pointer(w)), 0, uintptr(unsafe.Pointer(&pidl)), 0, 0)
	if int32(hr) < 0 || pidl == 0 {
		return 0, &os.PathError{Op: "reveal", Path: p, Err: syscall.Errno(uint32(hr) & 0xFFFF)}
	}
	return pidl, nil
}

func selectItems(dir string, items []string) error {
	free := func(p uintptr) { syscall.SyscallN(pCoTaskMemFree, p) }
	folder, err := parse(dir)
	if err != nil {
		return err
	}
	defer free(folder)
	abs := make([]uintptr, 0, len(items))
	child := make([]uintptr, 0, len(items))
	defer func() {
		for _, a := range abs {
			free(a)
		}
	}()
	for _, it := range items {
		a, err := parse(it)
		if err != nil {
			return err
		}
		abs = append(abs, a)
		last, _, _ := syscall.SyscallN(pFindLastID, a) // points into a
		child = append(child, last)
	}
	hr, _, _ := syscall.SyscallN(pOpenAndSelect, folder, uintptr(len(child)), uintptr(unsafe.Pointer(&child[0])), 0)
	runtime.KeepAlive(child)
	if int32(hr) < 0 {
		return &os.PathError{Op: "reveal", Path: dir, Err: fmt.Errorf("the shell could not show the items (HRESULT %#x)", uint32(hr))}
	}
	return nil
}

// shFileOp mirrors SHFILEOPSTRUCTW (shellapi.h) on 64-bit Windows: 56 bytes.
type shFileOp struct {
	Hwnd                 uintptr
	Func                 uint32
	From, To             *uint16
	Flags                uint16
	AnyOperationsAborted int32
	NameMappings         uintptr
	ProgressTitle        *uint16
}

var (
	_ [unsafe.Sizeof(shFileOp{}) - 56]struct{}
	_ [56 - unsafe.Sizeof(shFileOp{})]struct{}
)

// trash moves p to the Recycle Bin with SHFileOperationW. It does not pass
// FOF_NOCONFIRMATION: when the bin cannot take the file (too big, bin turned
// off for the drive) the shell would otherwise delete it for good without
// asking; with FOF_WANTNUKEWARNING it asks, and a refusal is an error.
func (osSystem) trash(p string) error {
	if err := ready(); err != nil {
		return err
	}
	from, err := syscall.UTF16FromString(p)
	if err != nil {
		return ErrInvalid
	}
	from = append(from, 0) // the list ends with a second NUL
	op := &shFileOp{
		Func:  foDelete,
		From:  &from[0],
		Flags: foAllowUndo | foSilent | foNoErrorUI | foWantNukeWarn,
	}
	r, _, _ := syscall.SyscallN(pFileOperation, uintptr(unsafe.Pointer(op)))
	runtime.KeepAlive(from)
	switch {
	case r != 0:
		return &os.PathError{Op: "trash", Path: p, Err: fmt.Errorf("the shell refused (error %#x)", uint32(r))}
	case op.AnyOperationsAborted != 0:
		return &os.PathError{Op: "trash", Path: p, Err: errors.New("canceled")}
	}
	return nil
}
