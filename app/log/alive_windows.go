package log

import "syscall"

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259
	errorInvalidParameter          = syscall.Errno(87) // OpenProcess: no process has that pid
)

// processAlive reports whether a process with pid is running. Anything it cannot
// tell (access denied, a failed query) counts as alive, so that a running
// instance's file is never taken.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return err != errorInvalidParameter
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}
