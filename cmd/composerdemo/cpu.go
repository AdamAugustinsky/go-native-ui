//go:build !windows

package main

import "syscall"

// cpuTime is the process's user and system CPU time in nanoseconds.
func cpuTime() int64 {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return ru.Utime.Nano() + ru.Stime.Nano()
}
