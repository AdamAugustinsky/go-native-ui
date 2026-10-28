//go:build windows

package testlock

import (
	"os"
	"syscall"
	"unsafe"
)

// On Windows the lock is LockFileEx's exclusive lock on the file's first
// byte: the foreground window and the keyboard are the session's, and a test
// that types or activates windows (app/hotkey's, the panels') would reach
// another test process's windows.

var (
	kernel32    = syscall.NewLazyDLL("kernel32.dll")
	pLockFileEx = kernel32.NewProc("LockFileEx")
	pUnlockFile = kernel32.NewProc("UnlockFileEx")
	lockExcl    = uintptr(2) // LOCKFILE_EXCLUSIVE_LOCK
)

func lock(f *os.File) error {
	var ol syscall.Overlapped
	if r, _, err := pLockFileEx.Call(f.Fd(), lockExcl, 0, 1, 0, uintptr(unsafe.Pointer(&ol))); r == 0 {
		return err
	}
	return nil
}

func unlock(f *os.File) error {
	var ol syscall.Overlapped
	if r, _, err := pUnlockFile.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol))); r == 0 {
		return err
	}
	return nil
}
