//go:build !windows

package log

import "syscall"

// processAlive reports whether a process with pid exists (signal 0 delivers
// nothing; EPERM means it exists but belongs to someone else).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
