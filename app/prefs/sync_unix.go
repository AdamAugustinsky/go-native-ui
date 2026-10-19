//go:build !windows

package prefs

import "os"

// syncDir flushes the directory so a completed rename survives a power loss.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
