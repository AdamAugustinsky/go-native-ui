//go:build windows

package sysinfo

import (
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

var (
	pGetThreadTimes = win32.Proc(kernel32, "GetThreadTimes")
	currentThread   = ^uintptr(1) // GetCurrentThread() pseudo-handle (-2)

	threadCreation, threadExit, threadKernel, threadUser syscall.Filetime
)

// ThreadCPUNs returns the user+system CPU time consumed so far by the
// calling OS thread (GetThreadTimes on the GetCurrentThread pseudo-handle),
// or -1. Its resolution is the scheduler's accounting tick, up to 15.6 ms, so
// use means over many frames. It does not allocate. The goroutine must be
// locked to its thread (runtime.LockOSThread) for deltas to mean anything.
// Not safe for concurrent use.
func ThreadCPUNs() int64 {
	if pGetThreadTimes == 0 {
		return -1
	}
	r, _, _ := syscall.SyscallN(pGetThreadTimes, currentThread,
		uintptr(unsafe.Pointer(&threadCreation)), uintptr(unsafe.Pointer(&threadExit)),
		uintptr(unsafe.Pointer(&threadKernel)), uintptr(unsafe.Pointer(&threadUser)))
	if uint32(r) == 0 {
		return -1
	}
	return int64(ft(threadKernel)+ft(threadUser)) * 100
}
