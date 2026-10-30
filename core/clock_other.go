//go:build !windows

package core

import _ "unsafe" // go:linkname

// systemNanotime is the runtime's monotonic clock: mach absolute time on darwin
// (the CACurrentMediaTime base) and CLOCK_MONOTONIC on linux.
//
//go:linkname systemNanotime runtime.nanotime
func systemNanotime() int64
