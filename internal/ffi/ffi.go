//go:build (darwin || linux) && (arm64 || amd64)

// Package ffi calls C functions and receives C callbacks without cgo and
// without heap allocation.
//
// It sits on purego's internal/fakecgo runtime bootstrap (runtime.iscgo,
// pthread-created threads, crosscall2) and uses runtime.cgocall, so blocking
// C calls release the P. The trampolines are our own; the technique follows
// github.com/ebitengine/purego (Apache-2.0).
//
// A Frame must live in long-lived memory (a global or a heap object), never
// on a goroutine stack: a callback can grow and move the stack while C still
// holds the frame pointer. Use one Frame per OS thread. Nested use on the
// same thread (a callback making calls with the frame its caller used) is
// safe because a call consumes its arguments on entry and writes its results
// on exit.
package ffi

import (
	"math"
	"runtime"
	"unsafe"

	_ "github.com/ebitengine/purego" // fakecgo runtime bootstrap when CGO_ENABLED=0
)

const (
	numIntRegs   = intRegs // 8 on arm64, 6 on amd64
	numFloatRegs = 8
	stackWords   = 12
)

// Frame is the argument and result block of one C call.
type Frame struct {
	fn     uintptr
	ints   [8]uintptr
	floats [8]uint64
	stack  [stackWords]uintptr
	r8     uintptr // arm64 indirect result register (x8)
	nfloat uintptr // amd64: AL for variadic callees
	r0, r1 uintptr
	f      [4]uint64

	ni, nf, nsb int // builder cursors: int regs, float regs, stack bytes
}

//go:linkname runtime_cgocall runtime.cgocall
//go:noescape
func runtime_cgocall(fn uintptr, arg unsafe.Pointer) int32

var callABI0 uintptr // set in assembly

// Reset clears the argument cursors. It does not clear stale argument values;
// the callee only reads the registers/stack slots its prototype uses.
func (f *Frame) Reset() *Frame {
	f.ni, f.nf, f.nsb = 0, 0, 0
	f.r8 = 0
	return f
}

// Arg appends a pointer-sized integer argument.
func (f *Frame) Arg(v uintptr) *Frame {
	if f.ni < numIntRegs {
		f.ints[f.ni] = v
		f.ni++
		return f
	}
	f.push(v, 8)
	return f
}

// Arg32 appends a 32-bit integer argument (int, uint32, BOOL-as-int ...).
// Only differs from Arg when it lands on the Apple arm64 stack.
func (f *Frame) Arg32(v uint32) *Frame {
	if f.ni < numIntRegs {
		f.ints[f.ni] = uintptr(v)
		f.ni++
		return f
	}
	f.push(uintptr(v), 4)
	return f
}

// ArgBool appends a C BOOL/bool argument.
func (f *Frame) ArgBool(v bool) *Frame {
	var b uintptr
	if v {
		b = 1
	}
	return f.Arg(b)
}

// VarArg appends an integer argument in the variadic part of a C variadic
// call. Apple arm64 passes every variadic argument on the stack in an 8-byte
// slot; elsewhere variadic arguments follow the normal rules.
func (f *Frame) VarArg(v uintptr) *Frame {
	if appleStackPacking {
		f.push(v, 8)
		return f
	}
	return f.Arg(v)
}

// VarArgF64 appends a double in the variadic part of a C variadic call.
func (f *Frame) VarArgF64(v float64) *Frame {
	if appleStackPacking {
		f.push(uintptr(math.Float64bits(v)), 8)
		return f
	}
	return f.ArgF64(v)
}

// ArgPtr appends a pointer argument. The pointee must not be on a goroutine
// stack (see the package comment).
func (f *Frame) ArgPtr(p unsafe.Pointer) *Frame { return f.Arg(uintptr(p)) }

// ArgF64 appends a double argument.
func (f *Frame) ArgF64(v float64) *Frame {
	bits := math.Float64bits(v)
	if f.nf < numFloatRegs {
		f.floats[f.nf] = bits
		f.nf++
		return f
	}
	f.push(uintptr(bits), 8)
	return f
}

// ArgF32 appends a float argument.
func (f *Frame) ArgF32(v float32) *Frame {
	bits := math.Float32bits(v)
	if f.nf < numFloatRegs {
		f.floats[f.nf] = uint64(bits)
		f.nf++
		return f
	}
	f.push(uintptr(bits), 4)
	return f
}

// ArgRect appends a CGRect/NSRect (4 doubles) by value: an HFA in d-registers
// on arm64, MEMORY class (copied to the stack) on amd64 SysV.
func (f *Frame) ArgRect(x, y, w, h float64) *Frame {
	if runtime.GOARCH == "arm64" {
		return f.argHFA(x, y, w, h)
	}
	for _, v := range [4]float64{x, y, w, h} {
		f.push(uintptr(math.Float64bits(v)), 8)
	}
	return f
}

// ArgPoint appends a CGPoint/CGSize (2 doubles) by value: HFA on arm64, two
// SSE eightbytes on amd64. Either way it takes two float registers or, if
// fewer remain, goes wholly on the stack.
func (f *Frame) ArgPoint(x, y float64) *Frame {
	if runtime.GOARCH == "arm64" {
		return f.argHFA(x, y)
	}
	if f.nf+2 <= numFloatRegs {
		return f.ArgF64(x).ArgF64(y)
	}
	// SysV: an argument whose eightbytes don't all fit in registers goes on
	// the stack, and the remaining registers stay available for later ones.
	f.push(uintptr(math.Float64bits(x)), 8)
	f.push(uintptr(math.Float64bits(y)), 8)
	return f
}

// argHFA passes a homogeneous double aggregate (AAPCS64 C.2/C.3): in
// consecutive d-registers if all fit; otherwise wholly on the stack, and no
// later float argument may use a register (NSRN = 8).
func (f *Frame) argHFA(v ...float64) *Frame {
	if f.nf+len(v) <= numFloatRegs {
		for _, x := range v {
			f.ArgF64(x)
		}
		return f
	}
	f.nf = numFloatRegs
	for _, x := range v {
		f.push(uintptr(math.Float64bits(x)), 8)
	}
	return f
}

// SetIndirectResult sets the hidden pointer for large struct returns: x8 on
// arm64. On amd64 the pointer is the first integer argument, so callers must
// pass it with Arg before any other argument (objc_msgSend_stret convention).
func (f *Frame) SetIndirectResult(p unsafe.Pointer) *Frame {
	if runtime.GOARCH == "arm64" {
		f.r8 = uintptr(p)
	} else {
		f.Arg(uintptr(p))
	}
	return f
}

func (f *Frame) push(v uintptr, size int) {
	if appleStackPacking && size < 8 {
		f.nsb = (f.nsb + size - 1) &^ (size - 1)
		off := f.nsb
		b := (*[stackWords * 8]byte)(unsafe.Pointer(&f.stack))
		for i := 0; i < size; i++ {
			b[off+i] = byte(v >> (8 * i))
		}
		f.nsb += size
		return
	}
	f.nsb = (f.nsb + 7) &^ 7
	if f.nsb/8 >= stackWords {
		panic("ffi: too many stack arguments")
	}
	f.stack[f.nsb/8] = v
	f.nsb += 8
}

// Call calls fn with the accumulated arguments and resets the cursors.
func (f *Frame) Call(fn uintptr) *Frame {
	f.fn = fn
	f.nfloat = uintptr(f.nf)
	runtime_cgocall(callABI0, unsafe.Pointer(f))
	f.ni, f.nf, f.nsb = 0, 0, 0
	return f
}

// R0 is the first integer return register (x0 / rax).
func (f *Frame) R0() uintptr { return f.r0 }

// R1 is the second integer return register (x1 / rdx).
func (f *Frame) R1() uintptr { return f.r1 }

// Bool reads a C BOOL/bool return value.
func (f *Frame) Bool() bool { return byte(f.r0) != 0 }

// F64 reads the i-th double return register (d0..d3 on arm64, xmm0..xmm1 on amd64).
func (f *Frame) F64(i int) float64 { return math.Float64frombits(f.f[i]) }

// F32 reads the i-th float return register.
func (f *Frame) F32(i int) float32 { return math.Float32frombits(uint32(f.f[i])) }

// Call0..Call6 are shorthands for all-integer calls.
func (f *Frame) Call0(fn uintptr) uintptr { return f.Reset().Call(fn).r0 }
func (f *Frame) Call1(fn, a uintptr) uintptr {
	return f.Reset().Arg(a).Call(fn).r0
}
func (f *Frame) Call2(fn, a, b uintptr) uintptr {
	return f.Reset().Arg(a).Arg(b).Call(fn).r0
}
func (f *Frame) Call3(fn, a, b, c uintptr) uintptr {
	return f.Reset().Arg(a).Arg(b).Arg(c).Call(fn).r0
}
func (f *Frame) Call4(fn, a, b, c, d uintptr) uintptr {
	return f.Reset().Arg(a).Arg(b).Arg(c).Arg(d).Call(fn).r0
}
func (f *Frame) Call5(fn, a, b, c, d, e uintptr) uintptr {
	return f.Reset().Arg(a).Arg(b).Arg(c).Arg(d).Arg(e).Call(fn).r0
}
func (f *Frame) Call6(fn, a, b, c, d, e, g uintptr) uintptr {
	return f.Reset().Arg(a).Arg(b).Arg(c).Arg(d).Arg(e).Arg(g).Call(fn).r0
}

// ---- Callbacks ----

// MaxCallbacks is the size of the assembly entry table.
const MaxCallbacks = 32

type cbArgs struct {
	index  uintptr
	args   unsafe.Pointer // float regs, int regs, then caller stack args
	result [4]uintptr     // in: result[0] = indirect result pointer (x8 / rdi)
}

// CB is one callback invocation as seen by a handler.
type CB cbArgs

func (c *CB) words() *[64]uintptr { return (*[64]uintptr)(c.args) }

// Int returns the i-th integer argument (register or stack).
func (c *CB) Int(i int) uintptr {
	if i < numIntRegs {
		return c.words()[numFloatRegs+i]
	}
	return c.words()[numFloatRegs+numIntRegs+(i-numIntRegs)]
}

// F64 returns the i-th double argument (register arguments only).
func (c *CB) F64(i int) float64 {
	return math.Float64frombits(uint64(c.words()[i]))
}

// Ptr returns the i-th integer argument as a pointer.
func (c *CB) Ptr(i int) unsafe.Pointer { return PtrFromC(c.Int(i)) }

// Ret sets the integer return value (x0 / rax).
func (c *CB) Ret(v uintptr) { c.result[0] = v }

// Ret2 sets a two-register integer return (x0,x1 / rax,rdx), e.g. NSRange.
func (c *CB) Ret2(a, b uintptr) { c.result[0], c.result[1] = a, b }

// RetBool sets a BOOL return value.
func (c *CB) RetBool(v bool) {
	if v {
		c.result[0] = 1
	} else {
		c.result[0] = 0
	}
}

// RetF64 sets the i-th float return register (d0..d3 on arm64, xmm0..xmm1 on amd64).
func (c *CB) RetF64(i int, v float64) {
	c.result[floatResultBase+i] = uintptr(math.Float64bits(v))
}

// IndirectResult is the hidden pointer for large struct returns.
func (c *CB) IndirectResult() unsafe.Pointer { return PtrFromC(c.result[0]) }

// PtrFromC turns an address that C handed us into an unsafe.Pointer. C
// memory is not managed by the Go GC, so the uintptr→Pointer rules guard
// against nothing here; reinterpreting the word (instead of converting it)
// avoids vet's unsafeptr false positive. Never use it for Go heap addresses.
func PtrFromC(p uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&p)) }

var (
	handlers       [MaxCallbacks]func(*CB)
	nHandlers      int
	cbDispatchCall = cbDispatch
	cbTableABI0    uintptr // set in assembly
)

func cbDispatch(a *cbArgs) { handlers[a.index]((*CB)(a)) }

// NewCallback returns a C function pointer that runs h on the calling thread.
// Callbacks are never freed; register them once at startup. Not safe for
// concurrent use with itself.
func NewCallback(h func(*CB)) uintptr {
	if nHandlers == MaxCallbacks {
		panic("ffi: out of callback slots")
	}
	handlers[nHandlers] = h
	nHandlers++
	return cbTableABI0 + uintptr(nHandlers-1)*cbEntrySize
}
