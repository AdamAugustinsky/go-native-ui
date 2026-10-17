//go:build windows && !arm64

package com

import (
	"math"
	"syscall"
)

// Frame carries the state of [Frame.CallFn]. On amd64 syscall.SyscallN
// already passes floating-point arguments correctly (the first four arguments
// are mirrored into XMM0-3, the rest are stack slots), so the frame only
// remembers the float result.
type Frame struct{ f0 uint64 }

// CallFn calls the Win32 function at fn with args (the first is the object,
// for a method), where bit i of fmask marks args[i] as a float32 passed as its
// bit pattern. The mask is not needed on this architecture: the position alone
// decides the register. It returns the integer result; [Frame.F32] has the
// float one.
func (f *Frame) CallFn(fn uintptr, fmask uint32, args ...uintptr) uintptr {
	r1, r2, _ := syscall.SyscallN(fn, args...)
	f.f0 = uint64(r2)
	return r1
}

// F32 returns the float32 result (XMM0) of the last CallFn.
func (f *Frame) F32() float32 { return math.Float32frombits(uint32(f.f0)) }
