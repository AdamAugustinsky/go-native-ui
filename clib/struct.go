package clib

import (
	"math"
	"runtime"
	"unsafe"
)

// How each ABI passes a struct by value.
const (
	abiAAPCS64 = iota // arm64: Apple, Linux and Windows
	abiSysV           // amd64 Linux
	abiWin64          // amd64 Windows
)

var structABI = func() int {
	switch {
	case runtime.GOARCH == "arm64":
		return abiAAPCS64
	case runtime.GOOS == "windows":
		return abiWin64
	}
	return abiSysV
}()

// MaxStructBytes is the largest struct [Call.Struct] passes.
const MaxStructBytes = 96

// Struct adds a struct argument passed by value: the size bytes at p, a C
// struct whose fields are integers, pointers and bools only (no float or
// double fields; see [Call.Float32x2] for the one float struct supported).
// p follows [Call.Ptr]'s rules. Each ABI's rule is applied:
//
//   - arm64 (AAPCS64): up to 16 bytes in one or two integer registers, or
//     on the stack when they do not fit; larger, a pointer to the caller's
//     copy, which is p itself;
//   - amd64 Linux (SysV): up to 16 bytes in two integer registers when both
//     are free, otherwise on the stack; larger, on the stack;
//   - amd64 Windows: 1, 2, 4 or 8 bytes in a register; any other size, a
//     pointer to the caller's copy, p.
//
// Where the ABI passes a pointer to a copy, the callee may write to the
// copy: pass a struct the caller does not need to keep intact, or rebuild
// it before each call. Struct panics when size is 0 or over
// [MaxStructBytes], or when the call would exceed the register and stack
// limits.
func (c *Call) Struct(p unsafe.Pointer, size uintptr) *Call {
	if size == 0 || size > MaxStructBytes {
		panic("clib: Struct: size out of range")
	}
	n := int(size+7) / 8
	switch structABI {
	case abiWin64:
		switch size {
		case 1, 2, 4, 8:
			c.intArg()
			c.f.Arg(loadWord(p, size))
			return c
		}
		return c.Ptr(p)
	case abiAAPCS64:
		if size > 16 {
			return c.Ptr(p)
		}
		if c.ni+n <= intArgRegs {
			for i := 0; i < n; i++ {
				c.ni++
				c.f.Arg(structWord(p, size, i))
			}
			return c
		}
		c.ni = intArgRegs
		c.f.FillIntRegs()
	default: // SysV
		if size <= 16 && c.ni+n <= intArgRegs {
			for i := 0; i < n; i++ {
				c.ni++
				c.f.Arg(structWord(p, size, i))
			}
			return c
		}
	}
	for i := 0; i < n; i++ {
		c.stackArg()
		c.f.ArgStack(structWord(p, size, i))
	}
	return c
}

// Float32x2 adds a struct of two floats passed by value, such as
// struct { float x, y; }: a homogeneous float aggregate in two float
// registers on arm64, one SSE register on amd64 Linux, one integer register
// on amd64 Windows. On Windows on arm64 [Call.Do] panics, as for any float
// argument there.
func (c *Call) Float32x2(x, y float32) *Call {
	bits := uint64(math.Float32bits(x)) | uint64(math.Float32bits(y))<<32
	packed := uintptr(bits) // 64-bit targets only: elsewhere no call is made
	switch structABI {
	case abiWin64:
		c.intArg()
		c.f.Arg(packed)
		return c
	case abiAAPCS64:
		if c.nf+2 <= floatArgRegs {
			c.nf += 2
			c.f.ArgF32(x)
			c.f.ArgF32(y)
			return c
		}
		c.nf = floatArgRegs
		c.f.FillFloatRegs()
		c.stackArg()
		c.f.ArgStack(packed)
		return c
	}
	if c.nf < floatArgRegs {
		c.nf++
		c.f.ArgF64(math.Float64frombits(bits))
		return c
	}
	c.stackArg()
	c.f.ArgStack(packed)
	return c
}

// structWord is word i of the size bytes at p, zero-padded past the end.
func structWord(p unsafe.Pointer, size uintptr, i int) uintptr {
	off := uintptr(i) * 8
	if off+8 <= size && (uintptr(p)+off)%8 == 0 {
		return *(*uintptr)(unsafe.Add(p, off))
	}
	if off+8 <= size {
		return loadWord(unsafe.Add(p, off), 8)
	}
	return loadWord(unsafe.Add(p, off), size-off)
}

// loadWord reads n ≤ 8 bytes at p as a little-endian word.
func loadWord(p unsafe.Pointer, n uintptr) uintptr {
	var w uintptr
	for i := uintptr(0); i < n; i++ {
		w |= uintptr(*(*byte)(unsafe.Add(p, i))) << (8 * i)
	}
	return w
}
