//go:build (darwin || linux) && (arm64 || amd64)

package clib

import (
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/ffi"
)

// The fixture library (testdata/fixture.c) is built once per test binary
// with the system C compiler. clib itself needs no compiler.
var fx struct {
	once sync.Once
	dir  string
	lib  *Lib
	skip string
	err  error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if fx.dir != "" {
		os.RemoveAll(fx.dir)
	}
	os.Exit(code)
}

func buildFixture() {
	cc, err := exec.LookPath("cc")
	if err != nil {
		fx.skip = "the fixture-library tests build testdata/fixture.c with the system C compiler, and there is no cc on PATH"
		return
	}
	if fx.dir, fx.err = os.MkdirTemp("", "clib-fixture-"); fx.err != nil {
		return
	}
	args := []string{"-shared", "-O2", "-Wall", "-Werror", "-o"}
	if runtime.GOOS == "darwin" {
		arch := map[string]string{"arm64": "arm64", "amd64": "x86_64"}[runtime.GOARCH]
		args = append(args, filepath.Join(fx.dir, "libfixture.dylib"), "-arch", arch)
	} else {
		args = append(args, filepath.Join(fx.dir, "libfixture.so"), "-fPIC", "-pthread")
	}
	out := args[5]
	args = append([]string{cc}, append(args, filepath.Join("testdata", "fixture.c"))...)
	if runtime.GOOS == "darwin" {
		// Run the compiler natively even when this test runs under Rosetta,
		// where a universal cc would start as x86_64 and its toolchain
		// libraries are arm64-only. arch tries the architectures in order.
		args = append([]string{"/usr/bin/arch", "-arm64", "-x86_64"}, args...)
	}
	cmd := exec.Command(args[0], args[1:]...)
	if b, err := cmd.CombinedOutput(); err != nil {
		fx.err = fmt.Errorf("building the fixture library: %v\n%s", err, b)
		return
	}
	fx.lib, fx.err = Open(out)
}

func fixture(t testing.TB) *Lib {
	t.Helper()
	fx.once.Do(buildFixture)
	if fx.skip != "" {
		t.Skip(fx.skip)
	}
	if fx.err != nil {
		t.Fatal(fx.err)
	}
	return fx.lib
}

func fn(t testing.TB, name string) Func {
	t.Helper()
	f, err := fixture(t).Func(name)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// fxErr is the fixture's fx_err, laid out like ZapFast's zf_err.
type fxErr struct {
	Code int32
	Len  uint32
	Msg  [256]byte
}

func (e *fxErr) text() string { return string(e.Msg[:e.Len]) }

func TestLookupAndClose(t *testing.T) {
	lib := fixture(t)
	if _, err := lib.Func("fx_no_such_function"); err == nil || !strings.Contains(err.Error(), "fx_no_such_function") {
		t.Fatalf("missing symbol: %v", err)
	}
	// C strings end at the first NUL: without the check these would open
	// the fixture and resolve fx_add.
	if l, err := Open(lib.path + "\x00missing"); err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("Open(fixture path + NUL suffix) = %v, %v; want the NUL error", l, err)
	}
	if f, err := lib.Func("fx_add\x00missing"); err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("Func(\"fx_add\\x00missing\") = %v, %v; want the NUL error", f, err)
	}
	// A second handle on the same image closes independently of the first.
	again, err := Open(lib.path)
	if err != nil {
		t.Fatal(err)
	}
	add, err := again.Func("fx_add")
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
	if err := again.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := again.Func("fx_add"); err == nil {
		t.Fatal("Func on a closed Lib succeeded")
	}
	// The image stays loaded through the first handle.
	if got := NewCall().Int(2).Int(3).Do(add).Int(); got != 5 {
		t.Fatalf("fx_add after closing a second handle = %d", got)
	}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("Do(Func{}) did not panic")
			}
		}()
		NewCall().Do(Func{})
	}()
}

func TestScalars(t *testing.T) {
	c := NewCall()
	if got := c.Int(-40).Int(-2).Do(fn(t, "fx_add")).Int(); got != -42 {
		t.Errorf("fx_add = %d", got)
	}
	if got := c.Uint(1 << 40).Uint(1 << 20).Do(fn(t, "fx_mul_u64")).Uint(); got != 1<<60 {
		t.Errorf("fx_mul_u64 = %#x", got)
	}
	if got := c.Int32(math.MinInt32 + 1).Do(fn(t, "fx_neg32")).Int32(); got != math.MaxInt32 {
		t.Errorf("fx_neg32 = %d", got)
	}
	if got := c.Uint32(0x0f0f0f0f).Do(fn(t, "fx_not32")).Uint32(); got != 0xf0f0f0f0 {
		t.Errorf("fx_not32 = %#x", got)
	}
	// Narrow arguments arrive extended as C expects.
	widen := []struct {
		name string
		arg  func(*Call)
		want int32
	}{
		{"fx_widen_i8", func(c *Call) { c.Int8(-5) }, -5},
		{"fx_widen_u8", func(c *Call) { c.Uint8(250) }, 250},
		{"fx_widen_i16", func(c *Call) { c.Int16(-30000) }, -30000},
		{"fx_widen_u16", func(c *Call) { c.Uint16(65535) }, 65535},
		{"fx_widen_bool", func(c *Call) { c.Bool(true) }, 7},
		{"fx_widen_bool", func(c *Call) { c.Bool(false) }, 3},
	}
	for _, w := range widen {
		w.arg(c)
		if got := c.Do(fn(t, w.name)).Int32(); got != w.want {
			t.Errorf("%s = %d, want %d", w.name, got, w.want)
		}
	}
	// Narrow results are read from the low bits.
	if got := c.Int32(200).Do(fn(t, "fx_ret_i8")).Int8(); got != -56 {
		t.Errorf("fx_ret_i8 = %d", got)
	}
	if got := c.Int32(-1).Do(fn(t, "fx_ret_u8")).Uint8(); got != 255 {
		t.Errorf("fx_ret_u8 = %d", got)
	}
	if got := c.Int32(40000).Do(fn(t, "fx_ret_i16")).Int16(); got != -25536 {
		t.Errorf("fx_ret_i16 = %d", got)
	}
	if got := c.Int32(-1).Do(fn(t, "fx_ret_u16")).Uint16(); got != 65535 {
		t.Errorf("fx_ret_u16 = %d", got)
	}
	if !c.Int32(256).Do(fn(t, "fx_ret_bool")).Bool() || c.Int32(0).Do(fn(t, "fx_ret_bool")).Bool() {
		t.Error("fx_ret_bool")
	}
	// Pointers in and out, with C memory.
	core := c.Do(fn(t, "fx_core_new")).Ptr()
	if core == nil {
		t.Fatal("fx_core_new = nil")
	}
	free := fn(t, "fx_core_free")
	defer func() { NewCall().Ptr(core).Do(free) }()
	if got := c.Ptr(core).Uint(24).Do(fn(t, "fx_ptr_add")).Ptr(); uintptr(got)-uintptr(core) != 24 {
		t.Errorf("fx_ptr_add = %p, want %p+24", got, core)
	}
	// Floats in and out, alone and after an integer.
	if got := c.Float32(1.5).Float32(-2.25).Do(fn(t, "fx_mulf")).Float32(); got != -3.375 {
		t.Errorf("fx_mulf = %v", got)
	}
	if got := c.Float64(1).Float64(3).Do(fn(t, "fx_div")).Float64(); got != 1.0/3 {
		t.Errorf("fx_div = %v", got)
	}
	if got := c.Int32(-3).Float32(0.5).Do(fn(t, "fx_scalef")).Float32(); got != -1.5 {
		t.Errorf("fx_scalef = %v", got)
	}
	// A void function.
	n := new(int64)
	c.Ptr(unsafe.Pointer(n)).Do(fn(t, "fx_void"))
	if *n != 1 {
		t.Errorf("fx_void left %d", *n)
	}
}

type fxMixRec struct {
	A int32
	B float64
	C int64
	D float32
	E uint8
	F float64
	G bool
	H uintptr
	I float32
	J int16
}

// TestMixedRegisters interleaves integer and float parameters of every
// width, so the integer and float register sequences advance independently.
func TestMixedRegisters(t *testing.T) {
	rec, target := new(fxMixRec), new(int64)
	want := fxMixRec{A: -7, B: 0.25, C: 1 << 40, D: -1.5, E: 200, F: 1e10, G: true, H: uintptr(unsafe.Pointer(target)), I: 3.5, J: -300}
	sum := NewCall().Ptr(unsafe.Pointer(rec)).Int32(want.A).Float64(want.B).Int(want.C).Float32(want.D).Uint8(want.E).
		Float64(want.F).Bool(want.G).Ptr(unsafe.Pointer(target)).Float32(want.I).Int16(want.J).Do(fn(t, "fx_mix")).Float64()
	if *rec != want {
		t.Fatalf("fx_mix received %+v, want %+v", *rec, want)
	}
	if w := -7 + 0.25 + float64(int64(1)<<40) - 1.5 + 200 + 1e10 + 1 + 3.5 - 300; sum != w {
		t.Fatalf("fx_mix = %v, want %v", sum, w)
	}
}

type fxStackRec struct {
	R  [7]int64
	D  [8]float64
	S0 int8
	S1 int16
	S2 bool
	S3 int32
	S4 uint8
	S5 float32
	S6 int64
	S7 uint16
	S8 float64
	S9 bool
}

// TestStackArguments fills every register, then passes narrow, wide and
// float arguments on the stack, where Apple arm64 packs them at their
// natural alignment and the other ABIs give each an 8-byte slot.
func TestStackArguments(t *testing.T) {
	rec := new(fxStackRec)
	want := fxStackRec{
		R:  [7]int64{1, -2, 3, -4, 5, -6, 7},
		D:  [8]float64{0.5, 1.5, 2.5, 3.5, 4.5, 5.5, 6.5, 7.5},
		S0: -8, S1: -1600, S2: true, S3: -320000, S4: 254, S5: -0.75, S6: -1 << 50, S7: 65000, S8: 1.0 / 3, S9: true,
	}
	c := NewCall().Ptr(unsafe.Pointer(rec))
	for _, r := range want.R {
		c.Int(r)
	}
	for _, d := range want.D {
		c.Float64(d)
	}
	c.Int8(want.S0).Int16(want.S1).Bool(want.S2).Int32(want.S3).Uint8(want.S4).Float32(want.S5).
		Int(want.S6).Uint16(want.S7).Float64(want.S8).Bool(want.S9).Do(fn(t, "fx_stack"))
	if *rec != want {
		t.Fatalf("fx_stack received\n%+v\nwant\n%+v", *rec, want)
	}
}

// TestRegisterAndStackLimits makes the largest calls the frame can carry:
// every integer register and stack slot, and every float register and stack
// slot.
func TestRegisterAndStackLimits(t *testing.T) {
	n := intArgRegs + stackArgWords // the record pointer and n-1 integers
	name := map[int]string{18: "fx_ints18", 20: "fx_ints20"}[n]
	if name == "" {
		t.Fatalf("no fixture function takes %d integer arguments", n)
	}
	got := make([]int64, n-1)
	c := NewCall().Ptr(unsafe.Pointer(&got[0]))
	var sum int64
	for i := 1; i < n; i++ {
		v := int64(i) * -1000003
		c.Int(v)
		sum += v
	}
	if s := c.Do(fn(t, name)).Int(); s != sum {
		t.Errorf("%s = %d, want %d", name, s, sum)
	}
	for i, v := range got {
		if w := int64(i+1) * -1000003; v != w {
			t.Errorf("%s: argument %d = %d, want %d", name, i+1, v, w)
		}
	}

	fl := make([]float64, floatArgRegs+stackArgWords)
	c.Ptr(unsafe.Pointer(&fl[0]))
	var fsum float64
	for i := range fl {
		v := float64(i) + 0.125
		c.Float64(v)
		fsum += v
	}
	if s := c.Do(fn(t, "fx_floats20")).Float64(); s != fsum {
		t.Errorf("fx_floats20 = %v, want %v", s, fsum)
	}
	for i, v := range fl {
		if w := float64(i) + 0.125; v != w {
			t.Errorf("fx_floats20: argument %d = %v, want %v", i, v, w)
		}
	}
}

// TestThreads calls from many goroutines at once, each with its own Call.
func TestThreads(t *testing.T) {
	add, mix := fn(t, "fx_add"), fn(t, "fx_mix")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, rec := NewCall(), new(fxMixRec)
			for i := range 3000 {
				a, b := int64(g)<<32, int64(i)
				if got := c.Int(a).Int(b).Do(add).Int(); got != a+b {
					errs <- fmt.Errorf("goroutine %d: fx_add(%d, %d) = %d", g, a, b, got)
					return
				}
				f := float64(i) / 8
				c.Ptr(unsafe.Pointer(rec)).Int32(int32(g)).Float64(f).Int(b).Float32(1).Uint8(2).
					Float64(-f).Bool(false).Ptr(nil).Float32(0.5).Int16(int16(-g)).Do(mix)
				if rec.A != int32(g) || rec.B != f || rec.C != b || rec.F != -f || rec.J != int16(-g) {
					errs <- fmt.Errorf("goroutine %d: fx_mix received %+v", g, *rec)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestErrorAcrossThreadHops is the reason caller-owned error storage is the
// pattern: between two calls the goroutine moves to another OS thread, so
// a thread-local error would be read on the wrong thread, while the error
// written into the Call's own storage is always the one from that call.
func TestErrorAcrossThreadHops(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(4))
	try, thread := fn(t, "fx_try"), fn(t, "fx_thread")
	type site struct {
		call *Call
		err  *fxErr
		tid  *uint64
	}
	var hops, failures atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	token := make(chan struct{}, 1)
	token <- struct{}{}
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := site{NewCall(), new(fxErr), new(uint64)}
			for i := range 1000 {
				tag := int64(g)*1_000_000 + int64(i)
				before := s.call.Do(thread).Uint()
				// Hand a token around between the calls: blocking on it
				// parks the goroutine, and it resumes on whichever thread
				// the scheduler picks.
				<-token
				token <- struct{}{}
				s.err.Code = -1
				st := s.call.Reset().Int(tag).Ptr(unsafe.Pointer(s.err)).Ptr(unsafe.Pointer(s.tid)).Do(try).Int32()
				if *s.tid != before {
					hops.Add(1)
				}
				if tag%3 == 0 {
					if st != 0 || s.err.Code != -1 {
						errs <- fmt.Errorf("tag %d: status %d, error storage touched on success (%d)", tag, st, s.err.Code)
						return
					}
					continue
				}
				failures.Add(1)
				want := fmt.Sprintf("tag %d failed on thread %d", tag, *s.tid)
				if st != -1 || s.err.Code != int32(tag%1000)+1 || s.err.text() != want {
					errs <- fmt.Errorf("tag %d: status %d, error %d %q, want %d %q", tag, st, s.err.Code, s.err.text(), tag%1000+1, want)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	t.Logf("%d failing calls; %d calls ran on another thread than the call just before them", failures.Load(), hops.Load())
	if hops.Load() == 0 {
		t.Fatal("no goroutine changed threads between calls; the test proved nothing")
	}
}

// TestThreadLocalErrorPinned checks the documented recipe for a library
// with a thread-local "last error": pin the thread around the call and the
// read. The goroutine parks between the two, as any Go code may; pinned, it
// resumes on the same thread. (Without the pin, thousands of reads here
// return another goroutine's error.)
func TestThreadLocalErrorPinned(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(4))
	fail, last := fn(t, "fx_tls_fail"), fn(t, "fx_tls_last")
	var wg sync.WaitGroup
	var bad atomic.Int64
	token := make(chan struct{}, 1)
	token <- struct{}{}
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := NewCall()
			for i := range 2000 {
				code := int32(g*10000 + i)
				runtime.LockOSThread()
				c.Int32(code).Do(fail)
				<-token
				token <- struct{}{}
				got := c.Do(last).Int32()
				runtime.UnlockOSThread()
				if got != code {
					bad.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if n := bad.Load(); n != 0 {
		t.Fatalf("%d pinned reads returned another call's thread-local error", n)
	}
}

func xorshift(x, n uint64) uint64 {
	for range n {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
	}
	return x
}

// TestSignals runs C calls and the library's own threads while Go sends
// its signals: the CPU profiler's SIGPROF, async preemption's SIGURG (tight
// loops that only a signal can stop, against a GC that must stop them), and
// both signals aimed directly at threads that are inside a C call. The C
// side must see correct results and FP state, sleeps must be interrupted
// (proof the signals landed in C) and resumed, and the process must survive
// signals on threads Go did not create.
func TestSignals(t *testing.T) {
	spin, sleep, kill := fn(t, "fx_spin"), fn(t, "fx_sleep"), fn(t, "fx_kill")
	start, handle, stopThreads := fn(t, "fx_threads_start"), fn(t, "fx_threads_handle"), fn(t, "fx_threads_stop")
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(4))
	if err := pprof.StartCPUProfile(io.Discard); err != nil {
		t.Logf("CPU profiler not started (%v); SIGPROF is still sent directly", err)
	} else {
		defer pprof.StopCPUProfile()
	}

	const nForeign = 6
	c := NewCall()
	if st := c.Int32(nForeign).Do(start).Int32(); st != 0 {
		t.Fatalf("fx_threads_start = %d", st)
	}
	var foreign [nForeign]uint64
	for i := range foreign {
		foreign[i] = c.Int32(int32(i)).Do(handle).Uint()
	}

	var stop atomic.Bool
	var busy, gcs, eintr, sent atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	const workers = 4
	tids := new([workers]atomic.Uint64)

	// Loops without calls: only async preemption can stop them for the GC.
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := int64(0)
			for !stop.Load() {
				for i := range int64(1 << 20) {
					x += i
				}
			}
			busy.Add(x)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			runtime.GC()
			gcs.Add(1)
			time.Sleep(time.Millisecond)
		}
	}()
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, fp, started := NewCall(), new(float64), new(int32)
			tid := unsafe.Pointer(&tids[w])
			for i := uint64(0); !stop.Load(); i++ {
				seed := i*2654435761 + uint64(w) + 1
				got := c.Uint(2_000_000).Uint(seed).Ptr(unsafe.Pointer(fp)).Ptr(tid).Do(spin).Uint()
				if want := xorshift(seed, 2_000_000); got != want || *fp != 1 {
					errs <- fmt.Errorf("worker %d: fx_spin = %#x fp %v, want %#x fp 1", w, got, *fp, want)
					return
				}
				eintr.Add(int64(c.Int(int64(5 * time.Millisecond)).Ptr(tid).Ptr(unsafe.Pointer(started)).Do(sleep).Int32()))
			}
		}()
	}
	// Signal the threads that are inside C calls, and the foreign threads.
	// The runtime aims SIGURG at threads running Go code, so one lands in C
	// only by racing a call's start; sending it directly makes that race
	// routine. (On darwin each one also decrements the runtime's count of
	// pending preemption signals, which only delays exec; nothing in this
	// test binary execs after the fixture is built.)
	wg.Add(1)
	go func() {
		defer wg.Done()
		c := NewCall()
		for i := 0; !stop.Load(); i++ {
			for w := range tids {
				if tid := tids[w].Load(); tid != 0 {
					c.Uint(tid).Int32(int32(syscall.SIGURG)).Do(kill)
					c.Uint(tid).Int32(int32(syscall.SIGPROF)).Do(kill)
					sent.Add(2)
				}
			}
			// Go handles SIGPROF on a thread it did not create; SIGURG is
			// only ever sent to its own threads.
			c.Uint(foreign[i%nForeign]).Int32(int32(syscall.SIGPROF)).Do(kill)
			time.Sleep(200 * time.Microsecond)
		}
	}()

	time.Sleep(2 * time.Second)
	stop.Store(true)
	wg.Wait()
	iters := c.Do(stopThreads).Uint()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	t.Logf("%d signals sent to threads in C calls, %d sleeps interrupted, %d GCs, %d foreign-thread iterations", sent.Load(), eintr.Load(), gcs.Load(), iters)
	if sent.Load() == 0 || eintr.Load() == 0 {
		t.Error("no C call was interrupted by a signal; the test proved nothing")
	}
	if iters == 0 {
		t.Error("the foreign threads made no progress")
	}
	if gcs.Load() == 0 {
		t.Error("no GC completed alongside the tight loops")
	}
}

// TestBlockingCall checks that a long C call releases its P: with a single
// P, other goroutines and a full GC run while the call blocks.
func TestBlockingCall(t *testing.T) {
	sleep := fn(t, "fx_sleep")
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	started := new(int32)
	var done atomic.Bool
	finished := make(chan struct{})
	go func() {
		NewCall().Int(int64(time.Second)).Ptr(nil).Ptr(unsafe.Pointer(started)).Do(sleep)
		done.Store(true)
		close(finished)
	}()
	for atomic.LoadInt32(started) == 0 {
		runtime.Gosched()
	}
	t0 := time.Now()
	runtime.GC()
	ping, pong := make(chan int), make(chan int)
	go func() {
		for v := range ping {
			pong <- v + 1
		}
	}()
	for i := range 1000 {
		ping <- i
		if <-pong != i+1 {
			t.Fatal("ping-pong")
		}
	}
	close(ping)
	elapsed := time.Since(t0)
	if done.Load() {
		t.Fatalf("a GC and 1000 round trips waited for the blocking C call (%v)", elapsed)
	}
	t.Logf("a GC and 1000 round trips in %v while a C call blocked the only P's goroutine", elapsed)
	<-finished
}

var memStats runtime.MemStats

func allocs() (objects, bytes uint64) {
	runtime.ReadMemStats(&memStats)
	return memStats.Mallocs, memStats.TotalAlloc
}

// sender is the call-site pattern from the package documentation.
type sender struct {
	call *Call
	err  *fxErr
	fn   Func
}

// TestZeroAlloc asserts exact heap-allocation deltas over thousands of calls
// of every kind once the Calls exist. The counters are process-wide, so one
// clean round out of three passes; an allocation per call shows up
// thousands of times in every round.
func TestZeroAlloc(t *testing.T) {
	s := &sender{NewCall(), new(fxErr), fn(t, "fx_send")}
	c := NewCall()
	mix, strlen, newCore, freeCore := fn(t, "fx_mix"), fn(t, "fx_strlen"), fn(t, "fx_core_new"), fn(t, "fx_core_free")
	core := c.Do(newCore).Ptr()
	defer func() { NewCall().Ptr(core).Do(freeCore) }()
	buf, bad := make([]byte, 512), []byte{0xff}
	rec, str := new(fxMixRec), make([]byte, 0, 64)
	var sink float64
	run := func() {
		for i := range 2000 {
			n := 1 + i%len(buf)
			if st := s.call.Reset().Ptr(core).Ptr(unsafe.Pointer(&buf[0])).Uint(uint64(n)).Ptr(unsafe.Pointer(s.err)).Do(s.fn).Int32(); st != 0 {
				panic("fx_send failed")
			}
			if st := s.call.Reset().Ptr(core).Ptr(unsafe.Pointer(&bad[0])).Uint(1).Ptr(unsafe.Pointer(s.err)).Do(s.fn).Int32(); st == 0 || s.err.Code != 22 {
				panic("fx_send accepted a bad command")
			}
			sink += c.Ptr(unsafe.Pointer(rec)).Int32(1).Float64(2).Int(3).Float32(4).Uint8(5).Float64(6).Bool(true).
				Ptr(nil).Float32(7).Int16(8).Do(mix).Float64()
			str = CStr(str, "zero allocations")
			if c.Ptr(unsafe.Pointer(&str[0])).Do(strlen).Uint() != 16 {
				panic("fx_strlen")
			}
		}
	}
	run()
	for round := 1; ; round++ {
		runtime.GC()
		o0, b0 := allocs()
		run()
		o1, b1 := allocs()
		if o1 == o0 && b1 == b0 {
			return
		}
		if round == 3 {
			t.Fatalf("allocated %d objects / %d bytes over 8000 calls (in each of 3 rounds)", o1-o0, b1-b0)
		}
		t.Logf("round %d: %d objects / %d bytes (retrying)", round, o1-o0, b1-b0)
	}
}

func BenchmarkAdd(b *testing.B) {
	add, c := fn(b, "fx_add"), NewCall()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		c.Int(int64(i)).Int(1).Do(add)
	}
}

// BenchmarkSend is the four-argument call of the package documentation.
func BenchmarkSend(b *testing.B) {
	s := &sender{NewCall(), new(fxErr), fn(b, "fx_send")}
	c := NewCall()
	core := c.Do(fn(b, "fx_core_new")).Ptr()
	free := fn(b, "fx_core_free")
	defer func() { NewCall().Ptr(core).Do(free) }()
	buf := make([]byte, 64)
	b.ReportAllocs()
	for b.Loop() {
		if s.call.Reset().Ptr(core).Ptr(unsafe.Pointer(&buf[0])).Uint(uint64(len(buf))).Ptr(unsafe.Pointer(s.err)).Do(s.fn).Int32() != 0 {
			b.Fatal(s.err.text())
		}
	}
}

// BenchmarkMix passes ten mixed integer and float arguments.
func BenchmarkMix(b *testing.B) {
	mix, c, rec := fn(b, "fx_mix"), NewCall(), new(fxMixRec)
	b.ReportAllocs()
	for b.Loop() {
		c.Ptr(unsafe.Pointer(rec)).Int32(1).Float64(2).Int(3).Float32(4).Uint8(5).Float64(6).Bool(true).
			Ptr(nil).Float32(7).Int16(8).Do(mix)
	}
}

const holdSize = 512

// addHeldBuffer adds a fresh heap buffer as a pointer argument and returns
// its hash. Once it returns, the Call's keep table is the only Go reference
// to the buffer.
//
//go:noinline
func addHeldBuffer(c *Call) uint64 {
	b := new([holdSize]byte)
	for i := range b {
		b[i] = byte(i*7 + 1)
	}
	c.Ptr(unsafe.Pointer(b)).Uint(holdSize)
	return fnv1a(b[:])
}

func fnv1a(b []byte) uint64 {
	h := uint64(1469598103934665603)
	for _, c := range b {
		h = (h ^ uint64(c)) * 1099511628211
	}
	return h
}

// TestPointerRetention checks that a pointer argument stays alive while C
// holds it, though nothing but the Call refers to it: collections run and
// same-sized objects are allocated and overwritten while C blocks, and C
// then reads the buffer.
func TestPointerRetention(t *testing.T) {
	hold := fn(t, "fx_hold")
	c, started := NewCall(), new(int32)
	want := addHeldBuffer(c)
	var stop atomic.Bool
	var gcs atomic.Int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for atomic.LoadInt32(started) == 0 {
			runtime.Gosched()
		}
		ring := make([]*[holdSize]byte, 256)
		for !stop.Load() {
			runtime.GC()
			gcs.Add(1)
			for j := range 4096 {
				p := new([holdSize]byte)
				for k := range p {
					p[k] = 0xee
				}
				ring[j%len(ring)] = p
			}
		}
	}()
	got := c.Int(int64(300 * time.Millisecond)).Ptr(unsafe.Pointer(started)).Do(hold).Uint()
	stop.Store(true)
	<-done
	if gcs.Load() < 2 {
		t.Fatalf("only %d collections ran while C held the buffer; the test proved nothing", gcs.Load())
	}
	if got != want {
		t.Fatalf("C read a different buffer after %d collections (hash %#x, want %#x): the pointee was freed and reused", gcs.Load(), got, want)
	}
}

// TestFFIArgBoolStack calls through internal/ffi directly: bools past the
// integer registers are packed into single bytes on Apple arm64.
func TestFFIArgBoolStack(t *testing.T) {
	bools := fn(t, "fx_bools")
	fr := new(ffi.Frame)
	for i := range 8 {
		fr.Arg(uintptr(i + 1))
	}
	got := uint32(fr.ArgBool(true).ArgBool(false).ArgBool(true).ArgBool(true).Arg32(5).Call(bools.addr).R0())
	if want := uint32(1 | 4 | 8 | 5<<4 | 1<<31); got != want {
		t.Fatalf("fx_bools = %#x, want %#x", got, want)
	}
}

// TestLookupCloseRace pauses a lookup between its closed check and dlsym
// and closes the Lib meanwhile: Close must wait for the lookup, or dlsym
// would get a handle that may already be unloaded. (The fixture stays
// loaded through its first handle, so a regression fails here rather than
// crashing.)
func TestLookupCloseRace(t *testing.T) {
	lib := fixture(t)
	again, err := Open(lib.path)
	if err != nil {
		t.Fatal(err)
	}
	paused, resume := make(chan struct{}), make(chan struct{})
	lookupHook = func() {
		close(paused)
		<-resume
	}
	defer func() { lookupHook = nil }()

	looked := make(chan error, 1)
	go func() {
		_, err := again.Func("fx_add")
		looked <- err
	}()
	<-paused
	closed := make(chan error, 1)
	go func() { closed <- again.Close() }()
	select {
	case <-closed:
		close(resume)
		t.Fatal("Close finished while a lookup was between its closed check and dlsym")
	case <-time.After(200 * time.Millisecond):
	}
	lookupHook = nil
	close(resume)
	if err := <-looked; err != nil {
		t.Fatalf("lookup that began before Close: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, err := again.Func("fx_add"); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("lookup after Close = %v, want the closed error", err)
	}
}
