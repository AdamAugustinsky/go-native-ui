//go:build darwin

// Throwaway phase-0 probe: per-call cost and heap allocations of the purego
// call paths a UI frame would use on macOS. Run:
//
//	go test ./probes/ffi -bench . -benchmem -run '^$'
package ffi

import (
	"math"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var (
	libc      uintptr
	fnLabs    uintptr
	fnFabs    uintptr
	fnQsort   uintptr
	labsRF    func(int64) int64
	fabsRF    func(float64) float64
	selHash   objc.SEL
	selIsEq   objc.SEL
	nsObject  objc.ID
	cmpCB     uintptr
	sortInput = [2]int64{2, 1}
)

func init() {
	var err error
	libc, err = purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}
	fnLabs, _ = purego.Dlsym(libc, "labs")
	fnFabs, _ = purego.Dlsym(libc, "fabs")
	fnQsort, _ = purego.Dlsym(libc, "qsort")
	purego.RegisterFunc(&labsRF, fnLabs)
	purego.RegisterFunc(&fabsRF, fnFabs)
	selHash = objc.RegisterName("hash")
	selIsEq = objc.RegisterName("isEqual:")
	nsObject = objc.ID(objc.GetClass("NSObject")).Send(objc.RegisterName("new"))
	cmpCB = purego.NewCallback(func(a, b *int64) int32 {
		if *a < *b {
			return -1
		}
		if *a > *b {
			return 1
		}
		return 0
	})
}

var sink uintptr

func BenchmarkSyscallNInt(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r, _, _ := purego.SyscallN(fnLabs, uintptr(i))
		sink = r
	}
}

func BenchmarkRegisterFuncInt(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = uintptr(labsRF(int64(i)))
	}
}

func BenchmarkRegisterFuncFloat(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = uintptr(math.Float64bits(fabsRF(float64(i))))
	}
}

func BenchmarkObjcIDSend(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = uintptr(nsObject.Send(selHash))
	}
}

func BenchmarkObjcIDSendArg(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = uintptr(nsObject.Send(selIsEq, nsObject))
	}
}

func BenchmarkObjcSendGeneric(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = objc.Send[uintptr](nsObject, selHash)
	}
}

func BenchmarkObjcMsgSendViaSyscallN(b *testing.B) {
	msgSend, _ := purego.Dlsym(libc, "objc_msgSend")
	if msgSend == 0 {
		h, _ := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		msgSend, _ = purego.Dlsym(h, "objc_msgSend")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		r, _, _ := purego.SyscallN(msgSend, uintptr(nsObject), uintptr(selHash))
		sink = r
	}
}

// One C->Go callback invocation per iteration (qsort of 2 elements = 1 compare).
func BenchmarkCallbackViaQsort(b *testing.B) {
	b.ReportAllocs()
	arr := &sortInput
	for i := 0; i < b.N; i++ {
		arr[0], arr[1] = 2, 1
		purego.SyscallN(fnQsort, uintptr(unsafe.Pointer(arr)), 2, 8, cmpCB)
	}
}
