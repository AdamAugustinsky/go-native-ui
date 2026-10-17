// Package com calls methods of COM objects through their vtables, for the
// Windows back ends (DirectWrite in package text). It uses only
// syscall.SyscallN, so it needs neither cgo nor generated bindings, and its
// call paths do not allocate.
//
// Conventions shared with internal/win32:
//
//   - Method slots are zero-based vtable indices that include IUnknown
//     (QueryInterface 0, AddRef 1, Release 2). Callers cite the header and
//     line the number came from.
//   - Pointer arguments are passed as uintptr(unsafe.Pointer(&x)) in the call
//     expression and must point into heap memory (a field of a heap object or
//     a package variable), never at a stack local: the callee, or a callback
//     into Go made while the call is in flight, may outlive a stack move.
//
// # Floating-point arguments
//
// Win32 passes float and double parameters in vector registers, which
// syscall.SyscallN loads on amd64 (it mirrors the first four arguments into
// XMM0-3, and stack arguments are plain slots) but not on arm64 (it only
// fills x0-x7 and the stack). A method with FLOAT parameters therefore goes
// through [Frame.CallF], which on arm64 uses a small assembly trampoline
// (callf_arm64.s) that loads d0-d7 as well; on amd64 it is Call. A float32
// argument is passed as uintptr(math.Float32bits(v)).
package com

import (
	"syscall"
	"unsafe"

	"gonativeui/internal/win32"
)

// maxArgs is the largest number of arguments (excluding the object) Call and
// CallF accept.
const maxArgs = 23

// Call invokes method idx (zero-based vtable slot) of obj with the given
// arguments after the implicit `this`, and returns the raw return register
// (use [HR] for an HRESULT). It supports up to 23 arguments and does not
// allocate. Methods with FLOAT parameters must use [Frame.CallF].
func Call(obj, idx uintptr, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(win32.Ptr(obj))
	fn := *(*uintptr)(unsafe.Add(vtbl, idx*unsafe.Sizeof(uintptr(0))))
	var buf [maxArgs + 1]uintptr
	buf[0] = obj
	n := copy(buf[1:], args)
	r, _, _ := syscall.SyscallN(fn, buf[:1+n]...)
	return r
}

// CallF is Call for methods with FLOAT parameters: fmask bit i marks args[i]
// (after the implicit this) as a float32 passed as its bit pattern. The frame
// is the per-thread state of the call (see [Frame]).
func (f *Frame) CallF(obj, idx uintptr, fmask uint32, args ...uintptr) uintptr {
	if len(args) > maxArgs {
		panic("com: too many arguments")
	}
	var buf [maxArgs + 1]uintptr
	buf[0] = obj
	n := copy(buf[1:], args)
	return f.CallFn(method(obj, idx), fmask<<1, buf[:1+n]...)
}

// HR reduces a raw return register to an HRESULT.
func HR(r uintptr) int32 { return int32(r) }

// Release drops one reference. It is a no-op for a nil pointer.
func Release(obj uintptr) { win32.Release(obj) }

// method returns the code address of vtable slot idx of obj.
func method(obj, idx uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(win32.Ptr(obj))
	return *(*uintptr)(unsafe.Add(vtbl, idx*unsafe.Sizeof(uintptr(0))))
}
