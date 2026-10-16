//go:build (darwin || linux) && amd64

package ffi

const (
	intRegs           = 6
	floatResultBase   = 2 // xmm0, xmm1 come from result[2], result[3]
	cbEntrySize       = 5 // CALL rel32
	appleStackPacking = false
)
