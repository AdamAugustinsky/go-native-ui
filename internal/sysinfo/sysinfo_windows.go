//go:build windows

package sysinfo

import (
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

var (
	kernel32                 = win32.LoadSystem("kernel32.dll")
	pGetSystemTimePrecise    = win32.Proc(kernel32, "GetSystemTimePreciseAsFileTime") // Windows 8+
	pK32GetProcessMemoryInfo = win32.Proc(kernel32, "K32GetProcessMemoryInfo")        // Windows 7+
	currentProcess           = ^uintptr(0)                                            // GetCurrentProcess() pseudo-handle (-1)
)

// processMemoryCountersEx is PROCESS_MEMORY_COUNTERS_EX (psapi.h).
type processMemoryCountersEx struct {
	cb                         uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
	privateUsage               uintptr
}

// ft converts a FILETIME (100 ns ticks since 1601-01-01) to a tick count.
func ft(f syscall.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }

// ProcessAgeNs returns nanoseconds since the OS created this process, -1 if unknown.
func ProcessAgeNs() int64 {
	var creation, exit, kernel, user, now syscall.Filetime
	if err := syscall.GetProcessTimes(syscall.Handle(currentProcess), &creation, &exit, &kernel, &user); err != nil {
		return -1
	}
	if pGetSystemTimePrecise != 0 {
		// Direct SyscallN call: the compiler keeps &now valid across the call.
		syscall.SyscallN(pGetSystemTimePrecise, uintptr(unsafe.Pointer(&now)))
	} else {
		syscall.GetSystemTimeAsFileTime(&now)
	}
	c, n := ft(creation), ft(now)
	if n < c {
		return -1
	}
	return int64(n-c) * 100
}

// Read returns current process metrics. Footprint is the private (committed)
// bytes of the process; RSS is the working set.
func Read() Snapshot {
	var s Snapshot
	var creation, exit, kernel, user syscall.Filetime
	if syscall.GetProcessTimes(syscall.Handle(currentProcess), &creation, &exit, &kernel, &user) == nil {
		s.CPUNs = int64(ft(kernel)+ft(user)) * 100
	}
	if pK32GetProcessMemoryInfo != 0 {
		var pmc processMemoryCountersEx
		pmc.cb = uint32(unsafe.Sizeof(pmc))
		r, _, _ := syscall.SyscallN(pK32GetProcessMemoryInfo, currentProcess, uintptr(unsafe.Pointer(&pmc)), uintptr(pmc.cb))
		if uint32(r) != 0 {
			s.Footprint = uint64(pmc.privateUsage)
			s.RSS = uint64(pmc.workingSetSize)
		}
	}
	return s
}
