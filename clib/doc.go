// Package clib loads a native library and calls its plain C functions
// without cgo and without allocating per call (DECISIONS.md D35).
//
// It is for apps: an app that ships its own native library (ZapFast's Rust
// core, D49) drives it from Go with clib. The framework itself still loads
// only OS libraries (D2); what an app loads is the app's decision.
//
// # Use
//
// Look everything up once, at setup, and keep one [Call] per call site:
//
//	lib, err := clib.Open(clib.BundleFramework("libzapfast_core.dylib"))
//	send, err := lib.Func("zf_send")
//	s := &sender{call: clib.NewCall(), err: new(zfErr), fn: send}
//
// Each call then builds its arguments in C parameter order, calls, and reads
// the result:
//
//	// int32_t zf_send(zf_core *c, const uint8_t *cmd, size_t len, zf_err *err);
//	st := s.call.Reset().Ptr(core).Ptr(unsafe.Pointer(&buf[0])).Uint(uint64(n)).
//		Ptr(unsafe.Pointer(s.err)).Do(s.fn).Int32()
//	if st != 0 {
//		return s.err.toError() // allocates only on failure
//	}
//
// Each argument method names the C parameter type it passes: [Call.Int] and
// [Call.Uint] for 64-bit integers (int64_t, uint64_t, size_t, intptr_t and
// long on LP64), [Call.Int32] to [Call.Uint8] for the narrower ones,
// [Call.Bool] for C bool, [Call.Ptr] for any pointer, and [Call.Float32] and
// [Call.Float64] for float and double. Use the method for the exact C type:
// the calling conventions place a narrow integer or a float differently
// from a 64-bit integer once the registers run out. [Result] reads the
// return value the same way.
//
// # What it is not
//
//   - No callbacks. A Go function is never passed to C, and a library that
//     calls back into Go is not supported.
//   - No structs by value, as arguments or results. Pass a pointer.
//   - No variadic functions (printf-style "..."): their conventions differ.
//   - Integer and pointer arguments travel in 8 registers on arm64 and 6 on
//     amd64, float arguments in 8; up to 12 more arguments of any kind go on
//     the stack. Building a call with more panics.
//
// # Threading
//
// A Call is used by one goroutine at a time; any goroutine may use it, and
// different Calls may run concurrently. A [Lib] and its [Func] values are
// safe for concurrent use. A call releases its P (as runtime.cgocall does
// for cgo), so a C function that blocks for seconds does not stall other
// goroutines or the garbage collector. A blocking function still occupies
// an OS thread for as long as it blocks.
//
// # Errors come back in the same call
//
// A goroutine can move to another OS thread between two calls, even between
// two lines of Go. So thread-local error state (errno, dlerror, a library's
// "last error" function) cannot be read reliably after a call: by then the
// goroutine may be on a different thread, reading another call's error or
// none.
//
// A library designed for clib therefore returns its error through storage
// the caller owns and passes in the call itself, as ZapFast's core does with
// its zf_err (a code and a 256-byte message). That storage lives in Go heap
// memory, in the same struct as the Call that passes it, so it follows the
// Call's rule (one caller at a time) and reading it allocates nothing until
// it becomes a Go error. The library writes it before returning, so the
// error is complete when [Call.Do] returns, on whatever thread that was.
//
// [Open] and [Lib.Func] read dlerror themselves with the thread pinned. A
// caller that must read a thread-local error from an existing library pins
// the thread around both the call and the read:
//
//	runtime.LockOSThread()
//	st := c.Reset().Int32(fd).Do(fnClose).Int32()
//	code := c.Reset().Do(fnLastError).Int32() // same thread as the call
//	runtime.UnlockOSThread()
//
// # Memory
//
// A pointer argument points to Go heap memory or C memory, never to a
// goroutine stack. Taking the address of a local and passing it makes the
// local escape to the heap (one allocation), because [Call.Ptr] keeps the
// pointer reachable until the call returns. The pointee holds no Go
// pointers, and C must not keep the pointer after the call returns. [CStr]
// makes NUL-terminated copies of Go strings into a caller buffer.
//
// A library's own threads (a Rust async runtime, for one) may run in the
// process; Go's signal handling tolerates them. This is tested with a
// fixture library whose threads spin and block while Go runs with the CPU
// profiler on and preempts goroutines asynchronously.
//
// # Platforms
//
// macOS and Linux on arm64 and amd64. On other systems, Windows included,
// [Open] returns [ErrUnsupported].
//
// # Lifetime
//
// Close a library only when nothing can still call into it. Apps normally
// never close.
package clib
