//go:build darwin && arm64

// Throwaway phase-0 probe: a zero-allocation C call + C->Go callback path,
// layered on purego's fakecgo runtime bootstrap (CGO_ENABLED=0).
// The assembly is written for this project; the overall technique
// (runtime.cgocall + crosscall2) follows github.com/ebitengine/purego (Apache-2.0).
package ffi2

import (
	"unsafe"

	_ "github.com/ebitengine/purego" // fakecgo: iscgo, crosscall2, pthread-backed Ms
)

// Frame is the argument block for one C call. It must not live on a goroutine
// stack (a callback could grow and move the stack while C holds the pointer),
// so callers keep one per OS thread in long-lived memory.
type Frame struct {
	fn     uintptr
	ints   [8]uintptr
	floats [8]uint64
	stack  [8]uintptr
	r8     uintptr
	r0, r1 uintptr
	f      [4]uint64
}

//go:linkname runtime_cgocall runtime.cgocall
//go:noescape
func runtime_cgocall(fn uintptr, arg unsafe.Pointer) int32

var trampABI0 uintptr // set in assembly

func (f *Frame) Call(fn uintptr) {
	f.fn = fn
	runtime_cgocall(trampABI0, unsafe.Pointer(f))
}

func (f *Frame) Reset()                  { *f = Frame{} }
func (f *Frame) Int(i int, v uintptr)    { f.ints[i] = v }
func (f *Frame) Float(i int, v uint64)   { f.floats[i] = v }
func (f *Frame) R0() uintptr             { return f.r0 }
func (f *Frame) F0() uint64              { return f.f[0] }
func (f *Frame) Call2(fn, a, b uintptr) uintptr {
	f.ints[0], f.ints[1] = a, b
	f.Call(fn)
	return f.r0
}

// Callbacks.

type cbArgs struct {
	index  uintptr
	args   unsafe.Pointer // F0..F7, X0..X7, then caller stack args
	result [4]uintptr
}

// CB is the view a handler gets of one callback invocation.
type CB cbArgs

func (c *CB) Int(i int) uintptr   { return (*[24]uintptr)(c.args)[8+i] }
func (c *CB) Float(i int) uint64  { return uint64((*[24]uintptr)(c.args)[i]) }
func (c *CB) SetInt(v uintptr)    { c.result[0] = v }
func (c *CB) SetFloat(v uint64)   { c.result[0] = uintptr(v) }

const maxCB = 8

var (
	handlers       [maxCB]func(*CB)
	nHandlers      int
	cbDispatchCall = cbDispatch
	cbTableABI0    uintptr // set in assembly
)

func cbDispatch(a *cbArgs) { handlers[a.index]((*CB)(a)) }

// NewCallback returns a C function pointer that invokes h. Each entry in the
// assembly table is 8 bytes (MOVD + B).
func NewCallback(h func(*CB)) uintptr {
	if nHandlers == maxCB {
		panic("ffi2: out of callbacks")
	}
	handlers[nHandlers] = h
	nHandlers++
	return cbTableABI0 + uintptr(nHandlers-1)*8
}
