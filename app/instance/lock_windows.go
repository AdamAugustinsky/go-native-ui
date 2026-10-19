//go:build windows

package instance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"syscall"
	"unsafe"
)

// lock is a held instance lock.
type lock interface{ release() }

type mutexLock struct{ h syscall.Handle }

var (
	kernel32     = syscall.NewLazyDLL("kernel32.dll")
	createMutexW = kernel32.NewProc("CreateMutexW")
)

const errAlreadyExists = syscall.Errno(183)

// acquireLock creates a named mutex in the session's namespace, named after
// the lock file's path. got is false when it already exists, which means another
// process (or another call in this one) holds it. The OS closes the handle when
// the process ends, however it ends, and the mutex goes with its last handle.
func acquireLock(path string) (lk lock, got bool, err error) {
	sum := sha256.Sum256([]byte(path))
	name, err := syscall.UTF16PtrFromString(`Local\gnui-instance-` + hex.EncodeToString(sum[:12]))
	if err != nil {
		return nil, false, err
	}
	h, _, e := createMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, false, fmt.Errorf("instance: CreateMutexW: %w", e)
	}
	if e == errAlreadyExists {
		syscall.CloseHandle(syscall.Handle(h))
		return nil, false, nil
	}
	return mutexLock{syscall.Handle(h)}, true, nil
}

func (l mutexLock) release() { syscall.CloseHandle(l.h) }
