// Package winproc holds the process plumbing app/install and app/selfupdate
// share on Windows: a handle to this process that a helper inherits and
// waits on (so a reused process id cannot fool it), starting a helper that
// outlives the app, and a file's rename with the retries that a virus
// scanner's brief hold needs.
package winproc

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
	"unsafe"

	"gonativeui/internal/win32"
)

const (
	synchronize    = 0x00100000 // SYNCHRONIZE
	queryLimited   = 0x1000     // PROCESS_QUERY_LIMITED_INFORMATION
	terminate      = 0x0001     // PROCESS_TERMINATE
	createNoWindow = 0x08000000 // CREATE_NO_WINDOW
	createNewGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	waitObject0    = 0          // WAIT_OBJECT_0
)

var (
	kernel32         = win32.LoadSystem("kernel32.dll")
	pGetCurrentProc  = win32.Proc(kernel32, "GetCurrentProcess")
	pDuplicateHandle = win32.Proc(kernel32, "DuplicateHandle")
	pWaitForSingle   = win32.Proc(kernel32, "WaitForSingleObject")
	pOpenProcess     = win32.Proc(kernel32, "OpenProcess")
	pTerminate       = win32.Proc(kernel32, "TerminateProcess")
	pQueryImageName  = win32.Proc(kernel32, "QueryFullProcessImageNameW")
	pGetExitCode     = win32.Proc(kernel32, "GetExitCodeProcess")
)

// SelfHandle is an inheritable handle to this process that allows waiting
// on it; the helper gets it through Start and its value on its command line.
func SelfHandle() (syscall.Handle, error) {
	cur, _, _ := syscall.SyscallN(pGetCurrentProc)
	var h syscall.Handle
	ok, _, err := syscall.SyscallN(pDuplicateHandle, cur, cur, cur, uintptr(unsafe.Pointer(&h)), synchronize|queryLimited, 1, 0)
	if ok == 0 {
		return 0, err
	}
	return h, nil
}

// ParseHandle reads a handle value Start passed on the command line.
func ParseHandle(s string) (syscall.Handle, error) {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n == 0 {
		return 0, errors.New("winproc: not a handle")
	}
	return syscall.Handle(n), nil
}

// Wait waits up to timeout for the process (or other object) h to end.
func Wait(h syscall.Handle, timeout time.Duration) bool {
	ms := uint32(timeout / time.Millisecond)
	r, _, _ := syscall.SyscallN(pWaitForSingle, uintptr(h), uintptr(ms))
	return r == waitObject0
}

// Start starts exe with args as a process of its own (its own process
// group, no console window), which inherits the handles in inherit and only
// those, and outlives this one.
func Start(exe string, args []string, dir string, inherit ...syscall.Handle) (*os.Process, error) {
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags:              createNewGroup | createNoWindow,
		AdditionalInheritedHandles: inherit,
		NoInheritHandles:           len(inherit) == 0,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// Open opens process pid to wait for it, read its image and end it.
func Open(pid int) (syscall.Handle, error) {
	h, _, err := syscall.SyscallN(pOpenProcess, synchronize|queryLimited|terminate, 0, uintptr(pid))
	if h == 0 {
		return 0, err
	}
	return syscall.Handle(h), nil
}

// Running reports whether the process h has not exited.
func Running(h syscall.Handle) bool {
	var code uint32
	ok, _, _ := syscall.SyscallN(pGetExitCode, uintptr(h), uintptr(unsafe.Pointer(&code)))
	return ok != 0 && code == 259 // STILL_ACTIVE
}

// Image is the full path of the executable process h runs.
func Image(h syscall.Handle) string {
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	ok, _, _ := syscall.SyscallN(pQueryImageName, uintptr(h), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if ok == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

// Terminate ends process h with exit code code.
func Terminate(h syscall.Handle, code uint32) {
	syscall.SyscallN(pTerminate, uintptr(h), uintptr(code))
}

// Rename moves from to to (replacing nothing), retrying for up to 5 s while
// the file is held (a scanner reading a new executable, an image the system
// has not yet let go of).
func Rename(from, to string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		f, err1 := syscall.UTF16PtrFromString(from)
		t, err2 := syscall.UTF16PtrFromString(to)
		if err1 != nil || err2 != nil {
			return errors.New("winproc: a path holds a NUL")
		}
		err := syscall.MoveFile(f, t)
		if err == nil {
			return nil
		}
		if !held(err) || time.Now().After(deadline) {
			return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// held reports an error that means another process holds the file for now.
func held(err error) bool {
	return errors.Is(err, syscall.ERROR_ACCESS_DENIED) || errors.Is(err, syscall.Errno(32)) || // ERROR_SHARING_VIOLATION
		errors.Is(err, syscall.Errno(33)) // ERROR_LOCK_VIOLATION
}

// RemoveAll is os.RemoveAll with the same retries as Rename.
func RemoveAll(path string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := os.RemoveAll(path)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
}
