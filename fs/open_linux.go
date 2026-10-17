//go:build linux

package fs

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// openPath opens path with the desktop's default application by starting
// xdg-open in its own session (so it outlives, and is not signalled with,
// the app). Start returns as soon as the child is forked; an error means
// xdg-open is missing, the path is unusable or the fork failed. xdg-open's
// own exit status is not reported.
func openPath(path string) error {
	if _, err := os.Lstat(path); err != nil {
		return err
	}
	// Absolute, so a name like "mailto:x" or "-h" is a file, never a URL or
	// an option for xdg-open.
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	cmd := exec.Command("xdg-open", abs)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // reap; xdg-open exits once the handler is launched
	return nil
}
