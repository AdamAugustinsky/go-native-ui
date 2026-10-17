//go:build linux

package sysinfo

import (
	"syscall"
	"unsafe"
)

const clockThreadCPUTimeID = 3 // CLOCK_THREAD_CPUTIME_ID

var threadTS syscall.Timespec

// ThreadCPUNs returns the user+system CPU time consumed so far by the
// calling OS thread (clock_gettime(CLOCK_THREAD_CPUTIME_ID) as a raw
// syscall: the vDSO does not serve per-thread CPU clocks), or -1. It does
// not allocate. The goroutine must be locked to its thread
// (runtime.LockOSThread) for deltas to mean anything. Not safe for
// concurrent use.
func ThreadCPUNs() int64 {
	if _, _, e := syscall.Syscall(syscall.SYS_CLOCK_GETTIME, clockThreadCPUTimeID, uintptr(unsafe.Pointer(&threadTS)), 0); e != 0 {
		return -1
	}
	return threadTS.Sec*1e9 + threadTS.Nsec
}
