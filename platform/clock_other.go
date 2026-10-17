//go:build !windows

package platform

import _ "unsafe" // go:linkname

// nanotime is the runtime's monotonic clock: mach absolute time on darwin
// (the CACurrentMediaTime base) and CLOCK_MONOTONIC on linux.
//
//go:linkname nanotime runtime.nanotime
func nanotime() int64
