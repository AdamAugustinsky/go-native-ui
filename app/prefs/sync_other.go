//go:build !windows && !darwin

package prefs

import "os"

// syncFile flushes f; full has no meaning here, fsync is already the strongest
// flush.
func syncFile(f *os.File, _ bool) error { return f.Sync() }
