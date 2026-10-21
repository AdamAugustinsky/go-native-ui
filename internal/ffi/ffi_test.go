//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package ffi

import (
	"math"
	"runtime"
	"slices"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	fr                                                 = new(Frame)
	fnLabs, fnFabs, fnFabsf, fnLdexp, fnSnprintf, fnQs uintptr
	cmpCB, sumCB                                       uintptr
	buf                                                = new([128]byte)
	format                                             = []byte("%d %d %d %d %d %d %d %d %d %.2f %s\x00")
	word                                               = []byte("ok\x00")
	sortArr                                            = new([2]int64)
	sink                                               uintptr
)

func lib(t testing.TB) {
	if fnLabs != 0 {
		return
	}
	var libc, libm uintptr
	var err error
	if runtime.GOOS == "darwin" {
		libc, err = purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
		libm = libc
	} else {
		libc, err = purego.Dlopen("libc.so.6", purego.RTLD_NOW)
		if err == nil {
			libm, err = purego.Dlopen("libm.so.6", purego.RTLD_NOW)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	sym := func(h uintptr, n string) uintptr {
		p, err := purego.Dlsym(h, n)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	fnLabs, fnFabs, fnFabsf = sym(libc, "labs"), sym(libm, "fabs"), sym(libm, "fabsf")
	fnLdexp, fnSnprintf, fnQs = sym(libm, "ldexp"), sym(libc, "snprintf"), sym(libc, "qsort")
	cmpCB = NewCallback(func(c *CB) {
		a, b := *(*int64)(c.Ptr(0)), *(*int64)(c.Ptr(1))
		switch {
		case a < b:
			c.Ret(uintptr(math.MaxUint32)) // int -1
		case a > b:
			c.Ret(1)
		default:
			c.Ret(0)
		}
	})
}

func TestCalls(t *testing.T) {
	lib(t)
	if got := int64(fr.Call1(fnLabs, uintptr(^uint64(41)))); got != 42 { // labs(-42)
		t.Fatalf("labs = %d", got)
	}
	if got := fr.Reset().ArgF64(-2.5).Call(fnFabs).F64(0); got != 2.5 {
		t.Fatalf("fabs = %v", got)
	}
	if got := fr.Reset().ArgF32(-1.25).Call(fnFabsf).F32(0); got != 1.25 {
		t.Fatalf("fabsf = %v", got)
	}
	// Mixed int and float registers.
	if got := fr.Reset().ArgF64(1.5).Arg32(3).Call(fnLdexp).F64(0); got != 12 {
		t.Fatalf("ldexp = %v", got)
	}
}

// snprintf exercises the variadic convention and stack arguments (11
// variadic arguments overflow the integer registers on every ABI).
func TestVariadicAndStack(t *testing.T) {
	lib(t)
	fr.Reset().ArgPtr(unsafe.Pointer(buf)).Arg(uintptr(len(buf))).ArgPtr(unsafe.Pointer(&format[0]))
	for i := 1; i <= 9; i++ {
		fr.VarArg(uintptr(i))
	}
	fr.VarArgF64(3.25).VarArg(uintptr(unsafe.Pointer(&word[0])))
	n := int(int32(fr.Call(fnSnprintf).R0()))
	want := "1 2 3 4 5 6 7 8 9 3.25 ok"
	if got := string(buf[:n]); got != want {
		t.Fatalf("snprintf = %q, want %q", got, want)
	}
	runtime.KeepAlive(format)
	runtime.KeepAlive(word)
}

func TestCallback(t *testing.T) {
	lib(t)
	sortArr[0], sortArr[1] = 9, 4
	fr.Call4(fnQs, uintptr(unsafe.Pointer(sortArr)), 2, 8, cmpCB)
	if sortArr[0] != 4 || sortArr[1] != 9 {
		t.Fatalf("qsort through callback: %v", *sortArr)
	}
}

var memStats runtime.MemStats

// allocs reads exact cumulative allocation counts. ReadMemStats flushes the
// per-P allocation caches; runtime/metrics' heap counters are credited a
// span at a time and would miss small allocations.
func allocs() (objects, bytes uint64) {
	runtime.ReadMemStats(&memStats)
	return memStats.Mallocs, memStats.TotalAlloc
}

// TestZeroAlloc asserts exact heap-allocation deltas over many calls and
// callbacks (not a truncated average). The counters are process-wide, so a
// rare one-off allocation elsewhere in the process can land in the window
// (seen once in CI: 7 objects over 8000 calls). An allocation per call or
// callback shows up thousands of times in every round, so one clean round
// out of three passes.
func TestZeroAlloc(t *testing.T) {
	lib(t)
	run := func() {
		for i := 0; i < 2000; i++ {
			sink += fr.Call1(fnLabs, uintptr(i))
			sink += uintptr(fr.Reset().ArgF64(1.5).Arg32(3).Call(fnLdexp).F64(0))
			sortArr[0], sortArr[1] = 2, 1
			fr.Call4(fnQs, uintptr(unsafe.Pointer(sortArr)), 2, 8, cmpCB)
		}
	}
	run() // warm up
	for round := 1; ; round++ {
		runtime.GC()
		o0, b0 := allocs()
		run()
		o1, b1 := allocs()
		if o1 == o0 && b1 == b0 {
			return
		}
		if round == 3 {
			t.Fatalf("allocated %d objects / %d bytes over 6000 calls + 2000 callbacks (in each of 3 rounds)", o1-o0, b1-b0)
		}
		t.Logf("round %d: %d objects / %d bytes (retrying)", round, o1-o0, b1-b0)
	}
}

func BenchmarkCall(b *testing.B) {
	lib(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = fr.Call1(fnLabs, uintptr(i))
	}
}

func BenchmarkCallMixed(b *testing.B) {
	lib(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sink = uintptr(fr.Reset().ArgF64(1.5).Arg32(3).Call(fnLdexp).F64(0))
	}
}

func BenchmarkCallback(b *testing.B) {
	lib(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sortArr[0], sortArr[1] = 2, 1
		fr.Call4(fnQs, uintptr(unsafe.Pointer(sortArr)), 2, 8, cmpCB)
	}
}

// TestAggregateSpill checks the ABI rule for a by-value double aggregate
// that no longer fits in the remaining float registers: it goes wholly on
// the stack. AAPCS64 then closes the float registers to later arguments
// (NSRN = 8); SysV leaves the remaining register to the next double.
func TestAggregateSpill(t *testing.T) {
	bits := func(v float64) uintptr { return uintptr(math.Float64bits(v)) }
	f := new(Frame)
	for range 7 {
		f.ArgF64(1)
	}
	f.ArgPoint(2, 3).ArgF64(4)
	want := []uintptr{bits(2), bits(3)}
	if runtime.GOARCH == "arm64" {
		want = append(want, bits(4))
		if f.nf != numFloatRegs {
			t.Errorf("arm64: nf = %d after spilled HFA, want %d", f.nf, numFloatRegs)
		}
	} else if f.floats[7] != uint64(bits(4)) {
		t.Errorf("amd64: last float register = %#x, want the double after the spilled struct", f.floats[7])
	}
	if got := f.stack[:f.nsb/8]; !slices.Equal(got, want) {
		t.Errorf("stack = %#x, want %#x", got, want)
	}
}
