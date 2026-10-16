//go:build (darwin || linux) && arm64

package ffi

import "runtime"

const (
	intRegs         = 8
	floatResultBase = 0 // d0..d3 overlay result[0..3]
	cbEntrySize     = 8 // MOVD + B
)

// Apple arm64 packs stack arguments at their natural alignment; AAPCS64
// (Linux) uses 8-byte slots.
var appleStackPacking = runtime.GOOS == "darwin"
