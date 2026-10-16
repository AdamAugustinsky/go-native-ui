//go:build darwin || linux

package sysinfo

import "syscall"

var ru syscall.Rusage

// CPUNs returns user+system CPU time of the process. It does not allocate.
// Not safe for concurrent use.
func CPUNs() int64 {
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return -1
	}
	return ru.Utime.Nano() + ru.Stime.Nano()
}
