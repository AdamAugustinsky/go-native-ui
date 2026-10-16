//go:build windows

package sysinfo

import "syscall"

var creation, exit, kernel, user syscall.Filetime

// CPUNs returns user+system CPU time of the process. It does not allocate.
// Not safe for concurrent use.
func CPUNs() int64 {
	h, err := syscall.GetCurrentProcess()
	if err != nil || syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user) != nil {
		return -1
	}
	return (int64(kernel.HighDateTime)<<32|int64(kernel.LowDateTime))*100 +
		(int64(user.HighDateTime)<<32|int64(user.LowDateTime))*100
}
