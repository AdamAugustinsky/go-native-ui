//go:build windows && (amd64 || arm64)

package clib

import (
	"math"
	"runtime"
	"syscall"
	"unsafe"
)

// On Windows a call is the standard library's syscall.SyscallN: every
// argument is one positional word (amd64: RCX, RDX, R8, R9, then the
// stack; arm64: x0–x7, then the stack), so the register accounting of
// [Call] only bounds the number of arguments. The runtime's stub copies the
// first four words into XMM0–XMM3 on amd64, so float arguments work there
// by their bits, and it returns XMM0 as the second result, which is how
// [Result.Float64] reads a float result. On arm64 the stub loads no float
// register: a call with a float argument panics in [Call.Do], and float
// results read as 0.
const (
	supported    = true
	floatArgRegs = 0 // floats take integer positions (amd64) or are unsupported (arm64)
)

const stackArgWords = 16

// frame is the call block: the argument words and the two result words.
type frame struct {
	args   [intArgRegs + stackArgWords + 4]uintptr
	n      int
	float  bool // a float argument on arm64: not callable
	r1, r2 uintptr
}

func (f *frame) Reset() { f.n, f.float = 0, false }

func (f *frame) Arg(v uintptr) {
	f.args[f.n] = v
	f.n++
}

func (f *frame) Arg32(v uint32)               { f.Arg(uintptr(v)) }
func (f *frame) ArgNarrow(v uint32, size int) { f.Arg(uintptr(v)) }
func (f *frame) ArgStack(v uintptr)           { f.Arg(v) }

// FillIntRegs pads the integer registers with zero words (arm64), so the
// next word goes on the stack, as AAPCS64 places a composite that does not
// fit in the registers left.
func (f *frame) FillIntRegs() {
	for f.n < intArgRegs {
		f.Arg(0)
	}
}

func (f *frame) FillFloatRegs() { f.float = true }

func (f *frame) ArgF32(v float32) {
	f.float = f.float || runtime.GOARCH == "arm64"
	f.Arg(uintptr(math.Float32bits(v)))
}

func (f *frame) ArgF64(v float64) {
	f.float = f.float || runtime.GOARCH == "arm64"
	f.Arg(uintptr(math.Float64bits(v)))
}

func (f *frame) Call(fn uintptr) {
	if f.float {
		f.n = 0
		panic("clib: float arguments are not supported on windows/arm64")
	}
	f.r1, f.r2, _ = syscall.SyscallN(fn, f.args[:f.n]...)
	f.n = 0
}

func (f *frame) R0() uintptr { return f.r1 }

func (f *frame) F64(int) float64 {
	if runtime.GOARCH == "amd64" {
		return math.Float64frombits(uint64(f.r2)) // XMM0
	}
	return 0
}

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procLoadLibEx   = kernel32.NewProc("LoadLibraryExW")
	procGetProc     = kernel32.NewProc("GetProcAddress")
	procFreeLibrary = kernel32.NewProc("FreeLibrary")
)

// loadAlteredPath is LOAD_WITH_ALTERED_SEARCH_PATH: a DLL loaded by an
// absolute path finds its own dependencies in its folder first.
const loadAlteredPath = 0x00000008

// dlopen loads the DLL at the absolute path; its dependencies are looked for
// in its own folder first. The thread is pinned so GetLastError is the call's.
func dlopen(path string) (uintptr, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	h, _, e := syscall.SyscallN(procLoadLibEx.Addr(), uintptr(unsafe.Pointer(p)), 0, loadAlteredPath)
	if h == 0 {
		return 0, e
	}
	return h, nil
}

func dlsym(h uintptr, name string) (uintptr, error) {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return 0, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r, _, e := syscall.SyscallN(procGetProc.Addr(), h, uintptr(unsafe.Pointer(p)))
	if r == 0 {
		return 0, e
	}
	return r, nil
}

func dlclose(h uintptr) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if r, _, e := syscall.SyscallN(procFreeLibrary.Addr(), h); r == 0 {
		return e
	}
	return nil
}
