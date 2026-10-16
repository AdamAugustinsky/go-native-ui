//go:build darwin && arm64

package ffi2

import (
	"math"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	fr          = new(Frame)
	fnLabs      uintptr
	fnFabs      uintptr
	fnQsort     uintptr
	fnMsgSend   uintptr
	clsNSNumber uintptr
	selNumDbl   uintptr
	selDblVal   uintptr
	cmp         uintptr
	arr         = new([2]int64)
	sink        uintptr
)

func init() {
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic(err)
	}
	objc, _ := purego.Dlopen("/usr/lib/libobjc.A.dylib", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	fnLabs, _ = purego.Dlsym(libc, "labs")
	fnFabs, _ = purego.Dlsym(libc, "fabs")
	fnQsort, _ = purego.Dlsym(libc, "qsort")
	fnMsgSend, _ = purego.Dlsym(objc, "objc_msgSend")
	getClass, _ := purego.Dlsym(objc, "objc_getClass")
	selReg, _ := purego.Dlsym(objc, "sel_registerName")
	clsNSNumber = fr.Call2(getClass, uintptr(unsafe.Pointer(&[]byte("NSNumber\x00")[0])), 0)
	selNumDbl = fr.Call2(selReg, uintptr(unsafe.Pointer(&[]byte("numberWithDouble:\x00")[0])), 0)
	selDblVal = fr.Call2(selReg, uintptr(unsafe.Pointer(&[]byte("doubleValue\x00")[0])), 0)
	cmp = NewCallback(func(c *CB) {
		a := *(*int64)(unsafe.Pointer(c.Int(0)))
		b := *(*int64)(unsafe.Pointer(c.Int(1)))
		switch {
		case a < b:
			c.SetInt(^uintptr(0))
		case a > b:
			c.SetInt(1)
		default:
			c.SetInt(0)
		}
	})
}

func TestCorrectness(t *testing.T) {
	fr.Reset()
	fr.Int(0, uintptr(math.MaxUint64)) // labs(-1)
	fr.Call(fnLabs)
	if fr.R0() != 1 {
		t.Fatalf("labs(-1) = %d", fr.R0())
	}
	fr.Reset()
	fr.Float(0, math.Float64bits(-2.5))
	fr.Call(fnFabs)
	if got := math.Float64frombits(fr.F0()); got != 2.5 {
		t.Fatalf("fabs(-2.5) = %v", got)
	}
	// Mixed int/float args + float return through objc_msgSend.
	fr.Reset()
	fr.Int(0, clsNSNumber)
	fr.Int(1, selNumDbl)
	fr.Float(0, math.Float64bits(3.25))
	fr.Call(fnMsgSend)
	num := fr.R0()
	fr.Reset()
	fr.Int(0, num)
	fr.Int(1, selDblVal)
	fr.Call(fnMsgSend)
	if got := math.Float64frombits(fr.F0()); got != 3.25 {
		t.Fatalf("doubleValue = %v", got)
	}
	arr[0], arr[1] = 5, 3
	fr.Reset()
	fr.Int(0, uintptr(unsafe.Pointer(arr)))
	fr.Int(1, 2)
	fr.Int(2, 8)
	fr.Int(3, cmp)
	fr.Call(fnQsort)
	if arr[0] != 3 || arr[1] != 5 {
		t.Fatalf("qsort via callback: %v", arr)
	}
}

func BenchmarkCallInt(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		fr.ints[0] = uintptr(i)
		fr.Call(fnLabs)
		sink = fr.r0
	}
}

func BenchmarkCallMixedObjc(b *testing.B) {
	fr.Reset()
	fr.Int(0, clsNSNumber)
	fr.Int(1, selNumDbl)
	fr.Float(0, math.Float64bits(3.25))
	fr.Call(fnMsgSend)
	num := fr.R0()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fr.ints[0], fr.ints[1] = num, selDblVal
		fr.Call(fnMsgSend)
		sink = uintptr(fr.f[0])
	}
}

func BenchmarkCallback(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		arr[0], arr[1] = 2, 1
		fr.ints[0], fr.ints[1], fr.ints[2], fr.ints[3] = uintptr(unsafe.Pointer(arr)), 2, 8, cmp
		fr.Call(fnQsort)
	}
}
