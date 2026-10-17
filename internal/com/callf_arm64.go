//go:build windows && arm64

package com

import (
	"math"
	"syscall"
	"unsafe"
)

// stackWords is the number of 8-byte stack argument slots the trampoline
// copies.
const stackWords = 16

// Frame carries the arguments of one [Frame.CallF] to the assembly
// trampoline. It must live in heap memory (a field of a heap object or a
// package variable), never on a goroutine stack, and be used by one thread at
// a time. The layout is read by callf_arm64.s through go_asm.h.
type Frame struct {
	fn     uintptr
	ints   [8]uintptr // x0-x7
	floats [8]uint64  // d0-d7 (a float32 sits in the low 32 bits: s0-s7)
	stack  [stackWords]uintptr
	r0     uintptr
	f0     uint64 // d0 on return
}

var thunkAddr uintptr // address of gonativeui_com_thunk, set in assembly

// CallFn calls the Win32 function at fn with args (the first is the object,
// for a method), where bit i of fmask marks args[i] as a float32 passed as its
// bit pattern. The Windows ARM64 calling convention (as AAPCS64) gives
// integer and float parameters separate register files: the first eight
// integer-class arguments go to x0-x7, the first eight float arguments to
// s0-s7, and the rest to 8-byte stack slots in argument order. It returns
// x0; [Frame.F32] has s0.
func (f *Frame) CallFn(fn uintptr, fmask uint32, args ...uintptr) uintptr {
	f.fn = fn
	ni, nf, ns := 0, 0, 0
	for i, a := range args {
		if fmask>>uint(i)&1 != 0 {
			if nf < len(f.floats) {
				f.floats[nf] = uint64(uint32(a))
				nf++
				continue
			}
		} else if ni < len(f.ints) {
			f.ints[ni] = a
			ni++
			continue
		}
		if ns == stackWords {
			panic("com: too many stack arguments")
		}
		f.stack[ns] = a
		ns++
	}
	syscall.SyscallN(thunkAddr, uintptr(unsafe.Pointer(f)))
	return f.r0
}

// F32 returns the float32 result (s0) of the last CallFn.
func (f *Frame) F32() float32 { return math.Float32frombits(uint32(f.f0)) }
