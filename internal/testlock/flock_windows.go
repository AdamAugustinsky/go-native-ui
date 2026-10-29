package testlock

import (
	"os"
	"syscall"
	"unsafe"
)

// Windows: LockFileEx on the file's first byte, exclusive, waiting.

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx = kernel32.NewProc("LockFileEx")
	procUnlockFile = kernel32.NewProc("UnlockFileEx")
)

const lockfileExclusiveLock = 0x2

func lock(f *os.File) error {
	ol := new(syscall.Overlapped)
	r, _, err := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(ol)))
	if r == 0 {
		return err
	}
	return nil
}

func unlock(f *os.File) error {
	ol := new(syscall.Overlapped)
	r, _, err := procUnlockFile.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(ol)))
	if r == 0 {
		return err
	}
	return nil
}
