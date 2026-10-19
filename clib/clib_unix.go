//go:build (darwin || linux) && (arm64 || amd64)

package clib

import (
	"runtime"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

const (
	supported     = true
	intArgRegs    = ffi.IntArgRegs
	floatArgRegs  = ffi.FloatArgRegs
	stackArgWords = ffi.StackArgWords
)

// The pointer table covers every integer-class argument a Frame can take.
var _ [maxPtrs - intArgRegs - stackArgWords]struct{}

// frame is the call block: internal/ffi's trampolines through
// runtime.cgocall (D3). It lives inside the heap-allocated Call, never on a
// goroutine stack.
type frame = ffi.Frame

// dlopen, dlsym and dlclose read dlerror on failure; the thread is pinned so
// that the message read is the one the failing call set on this thread.

func dlopen(path string) (uintptr, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return purego.Dlopen(path, purego.RTLD_NOW|purego.RTLD_LOCAL)
}

func dlsym(h uintptr, name string) (uintptr, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return purego.Dlsym(h, name)
}

func dlclose(h uintptr) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	return purego.Dlclose(h)
}
