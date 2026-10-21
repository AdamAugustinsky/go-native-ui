package clib

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

// ErrUnsupported is returned by [Open] on systems where clib cannot load
// libraries yet (everything but macOS on Apple silicon and Linux on arm64
// and amd64).
// errors.Is(err, errors.ErrUnsupported) also holds.
var ErrUnsupported = fmt.Errorf("clib: native libraries on %s/%s: %w", runtime.GOOS, runtime.GOARCH, errors.ErrUnsupported)

// Lib is a loaded native library.
type Lib struct {
	h    uintptr
	path string

	mu     sync.Mutex // orders lookups against Close
	closed bool
}

// lookupHook, when set by a test, runs in Func between the closed check and
// dlsym.
var lookupHook func()

// Open loads the library at path, which must be absolute, with
// RTLD_NOW|RTLD_LOCAL: every symbol is bound now, so a missing dependency
// fails here rather than at a later call, and the library's symbols do not
// leak into the process's global namespace. Opening the same path again
// returns another Lib for the same loaded image. A path containing a NUL
// byte is rejected (C would see only the part before it). The error carries
// the loader's message (dlerror, read on the same thread).
func Open(path string) (*Lib, error) {
	if !supported {
		return nil, ErrUnsupported
	}
	if strings.IndexByte(path, 0) >= 0 {
		return nil, fmt.Errorf("clib: open %q: path contains a NUL byte", path)
	}
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("clib: open %q: not an absolute path", path)
	}
	h, err := dlopen(path)
	if err != nil {
		return nil, fmt.Errorf("clib: open %s: %w", path, err)
	}
	return &Lib{h: h, path: path}, nil
}

// Func looks up the C function name in l. Look functions up once, at setup.
// A name containing a NUL byte is rejected. Func is safe to call
// concurrently with itself and with [Lib.Close]: after Close it returns an
// error.
func (l *Lib) Func(name string) (Func, error) {
	if strings.IndexByte(name, 0) >= 0 {
		return Func{}, fmt.Errorf("clib: %q in %s: name contains a NUL byte", name, l.path)
	}
	// Held across dlsym, so the handle cannot be closed under the lookup.
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Func{}, fmt.Errorf("clib: %s in %s: library closed", name, l.path)
	}
	if lookupHook != nil {
		lookupHook()
	}
	p, err := dlsym(l.h, name)
	if err != nil {
		return Func{}, fmt.Errorf("clib: %s in %s: %w", name, l.path, err)
	}
	return Func{addr: p}, nil
}

// Close releases l. Only close a library when nothing can still call into
// it: its Funcs become dangling, and calling one may crash the process.
// Closing twice does nothing. Close waits for lookups in progress.
func (l *Lib) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if err := dlclose(l.h); err != nil {
		return fmt.Errorf("clib: close %s: %w", l.path, err)
	}
	return nil
}

// BundleFramework returns the absolute path where an app bundle keeps the
// native library name, relative to the running executable: on macOS
// Contents/Frameworks/name (the executable is in Contents/MacOS); on Linux
// ../lib/name, as in an AppDir's usr/bin and usr/lib; on Windows the
// executable's directory. Symlinks to the executable are resolved first. If
// name contains a NUL byte or the executable cannot be found, it returns
// name unchanged, which [Open] rejects.
func BundleFramework(name string) string {
	if strings.IndexByte(name, 0) >= 0 {
		return name
	}
	exe, err := os.Executable()
	if err != nil {
		return name
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	dir := filepath.Dir(exe)
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(dir, "..", "Frameworks", name)
	case "windows":
		return filepath.Join(dir, name)
	default:
		return filepath.Join(dir, "..", "lib", name)
	}
}

// Func is a C function of a [Lib]. The zero Func is not callable.
type Func struct{ addr uintptr }

// maxPtrs bounds the pointer arguments of one call: every integer-class
// argument can be a pointer, in registers or on the stack.
const maxPtrs = 8 + 12

// Call builds and makes C calls. Allocate one per call site with [NewCall]
// at setup and reuse it: building and calling allocate nothing. A Call is
// used by one goroutine at a time.
//
// Add the arguments in C parameter order, then call [Call.Do]:
//
//	r := c.Reset().Ptr(p).Uint(uint64(n)).Float64(x).Do(fn)
//
// An argument method panics when the call would exceed the register and
// stack limits (see the package documentation).
type Call struct {
	f          frame
	ni, nf, ns int // integer and float registers used, stack slots used
	nkeep      int
	keep       [maxPtrs]unsafe.Pointer // pointer arguments, reachable until Do returns
}

// NewCall returns a Call with no arguments.
func NewCall() *Call { return new(Call) }

// Reset drops any arguments added since the last [Call.Do]. Do leaves the
// Call empty, so Reset is needed only after a call was built and abandoned,
// but starting each call with it keeps a call site self-evidently clean.
func (c *Call) Reset() *Call {
	c.f.Reset()
	c.ni, c.nf, c.ns = 0, 0, 0
	c.release()
	return c
}

func (c *Call) intArg() {
	if c.ni < intArgRegs {
		c.ni++
		return
	}
	c.stackArg()
}

func (c *Call) floatArg() {
	if c.nf < floatArgRegs {
		c.nf++
		return
	}
	c.stackArg()
}

// stackArg counts a stack argument. Each takes at most one 8-byte slot,
// alignment included, so counting arguments bounds the bytes.
func (c *Call) stackArg() {
	if c.ns == stackArgWords {
		panic("clib: too many arguments for one call: the registers and the stack slots are full")
	}
	c.ns++
}

// Int adds a 64-bit signed integer argument: int64_t, intptr_t, ssize_t,
// and long on 64-bit Unix.
func (c *Call) Int(v int64) *Call {
	c.intArg()
	c.f.Arg(uintptr(v))
	return c
}

// Uint adds a 64-bit unsigned integer argument: uint64_t, size_t,
// uintptr_t, and unsigned long on 64-bit Unix. It also passes an opaque
// handle that C declares as an integer.
func (c *Call) Uint(v uint64) *Call {
	c.intArg()
	c.f.Arg(uintptr(v))
	return c
}

// Int32 adds an int32_t (C int) argument.
func (c *Call) Int32(v int32) *Call {
	c.intArg()
	c.f.Arg32(uint32(v))
	return c
}

// Uint32 adds a uint32_t (C unsigned int) argument.
func (c *Call) Uint32(v uint32) *Call {
	c.intArg()
	c.f.Arg32(v)
	return c
}

// Int16 adds an int16_t (C short) argument.
func (c *Call) Int16(v int16) *Call {
	c.intArg()
	c.f.ArgNarrow(uint32(int32(v)), 2)
	return c
}

// Uint16 adds a uint16_t argument.
func (c *Call) Uint16(v uint16) *Call {
	c.intArg()
	c.f.ArgNarrow(uint32(v), 2)
	return c
}

// Int8 adds an int8_t (signed char) argument.
func (c *Call) Int8(v int8) *Call {
	c.intArg()
	c.f.ArgNarrow(uint32(int32(v)), 1)
	return c
}

// Uint8 adds a uint8_t (unsigned char) argument.
func (c *Call) Uint8(v uint8) *Call {
	c.intArg()
	c.f.ArgNarrow(uint32(v), 1)
	return c
}

// Bool adds a C bool (_Bool) argument.
func (c *Call) Bool(v bool) *Call {
	var b uint32
	if v {
		b = 1
	}
	c.intArg()
	c.f.ArgNarrow(b, 1)
	return c
}

// Ptr adds a pointer argument. p points to Go heap memory or C memory,
// never to a goroutine stack; the Call keeps p reachable until [Call.Do]
// returns, so passing the address of a local moves the local to the heap.
// The memory must hold no Go pointers, and C must not keep p after the call.
func (c *Call) Ptr(p unsafe.Pointer) *Call {
	c.intArg()
	c.keep[c.nkeep] = p
	c.nkeep++
	c.f.Arg(uintptr(p))
	return c
}

// Float32 adds a float argument.
func (c *Call) Float32(v float32) *Call {
	c.floatArg()
	c.f.ArgF32(v)
	return c
}

// Float64 adds a double argument.
func (c *Call) Float64(v float64) *Call {
	c.floatArg()
	c.f.ArgF64(v)
	return c
}

// Do calls fn with the arguments added since the last call, waits for it to
// return, and leaves the Call empty for the next one. The goroutine's P is
// released for the duration of the call, as for a cgo call. Do panics on a
// zero Func.
func (c *Call) Do(fn Func) Result {
	if fn.addr == 0 {
		panic("clib: call of a zero Func")
	}
	c.f.Call(fn.addr)
	c.ni, c.nf, c.ns = 0, 0, 0
	c.release()
	return Result{r: c.f.R0(), f: math.Float64bits(c.f.F64(0))}
}

func (c *Call) release() {
	clear(c.keep[:c.nkeep])
	c.nkeep = 0
}

// Result is the return value of a C call. Read it with the method for the C
// return type; for a void function, ignore it. A narrow integer is read
// from the low bits of the register, since C leaves the rest unspecified.
type Result struct {
	r uintptr // x0 / rax
	f uint64  // d0 / xmm0
}

// Int reads an int64_t (or intptr_t, ssize_t, long) result.
func (r Result) Int() int64 { return int64(r.r) }

// Uint reads a uint64_t (or size_t, uintptr_t, unsigned long) result.
func (r Result) Uint() uint64 { return uint64(r.r) }

// Int32 reads an int32_t (C int) result.
func (r Result) Int32() int32 { return int32(r.r) }

// Uint32 reads a uint32_t result.
func (r Result) Uint32() uint32 { return uint32(r.r) }

// Int16 reads an int16_t result.
func (r Result) Int16() int16 { return int16(r.r) }

// Uint16 reads a uint16_t result.
func (r Result) Uint16() uint16 { return uint16(r.r) }

// Int8 reads an int8_t result.
func (r Result) Int8() int8 { return int8(r.r) }

// Uint8 reads a uint8_t result.
func (r Result) Uint8() uint8 { return uint8(r.r) }

// Bool reads a C bool result.
func (r Result) Bool() bool { return uint8(r.r) != 0 }

// Ptr reads a pointer result. Use it only for addresses of C memory (or
// nil); a Go pointer that went through C is not a valid Go pointer.
func (r Result) Ptr() unsafe.Pointer {
	// Reinterpret the word rather than convert it: C memory is invisible to
	// the GC, so the uintptr-to-Pointer rules guard nothing here.
	return *(*unsafe.Pointer)(unsafe.Pointer(&r.r))
}

// Float32 reads a float result.
func (r Result) Float32() float32 { return math.Float32frombits(uint32(r.f)) }

// Float64 reads a double result.
func (r Result) Float64() float64 { return math.Float64frombits(r.f) }

// CStr copies s and a terminating NUL into dst's storage and returns the
// copy, for a const char * argument:
//
//	c.buf = clib.CStr(c.buf, name)
//	call.Ptr(unsafe.Pointer(&c.buf[0]))
//
// It allocates only when cap(dst) < len(s)+1, so a buffer kept with the
// Call makes it allocation-free. C reads up to the first NUL, so a string
// containing NUL bytes arrives truncated.
func CStr(dst []byte, s string) []byte {
	dst = append(dst[:0], s...)
	return append(dst, 0)
}
