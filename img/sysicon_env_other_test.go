//go:build !linux && !windows

package img

import "testing"

// sysIconEnv prepares the system's icons for a test: nothing off Linux,
// where the system is what it is.
func sysIconEnv(testing.TB) {}
