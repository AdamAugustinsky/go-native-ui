//go:build darwin && (arm64 || amd64)

package ffi

import (
	"runtime"
	"sync/atomic"
	"testing"
	"unsafe"
)

// Callbacks from libdispatch's worker threads, which GCD creates, reuses and
// retires as it likes.

var (
	fnApplyF, fnGlobalQueue, fnQueueCreate, fnRelease uintptr
	fnGroupCreate, fnGroupAsyncF, fnGroupWait         uintptr
	gcdWorkCB, gcdApplyCB, gcdNestCB                  uintptr
	gcdRan, gcdBad                                    atomic.Int64
	gcdIDs                                            = new([1 << 15]uint64)
	gcdIDn                                            atomic.Int64
	churnLabel                                        = []byte("dev.gnui.test.churn\x00")
)

func init() {
	moreSetup = func(sym func(string) uintptr) {
		fnApplyF, fnGlobalQueue = sym("dispatch_apply_f"), sym("dispatch_get_global_queue")
		fnQueueCreate, fnRelease = sym("dispatch_queue_create"), sym("dispatch_release")
		fnGroupCreate, fnGroupAsyncF, fnGroupWait = sym("dispatch_group_create"), sym("dispatch_group_async_f"), sym("dispatch_group_wait")
		gcdWorkCB = NewCallback(func(c *CB) { gcdWork(c) })  // void (*)(void *ctx)
		gcdApplyCB = NewCallback(func(c *CB) { gcdWork(c) }) // void (*)(void *ctx, size_t i)
		gcdNestCB = NewCallback(func(c *CB) { runNest(c.Frame()) })
	}
}

// gcdWork runs on a GCD thread: a C call through the invocation's own frame,
// and the thread's id.
func gcdWork(c *CB) {
	enter()
	defer leave()
	gcdRan.Add(1)
	f := c.Frame()
	if int64(f.Call1(fnLabs, uintptr(^uint64(c.Int(0)&0xff)))) != int64(c.Int(0)&0xff)+1 {
		gcdBad.Add(1)
	}
	if i := gcdIDn.Add(1) - 1; i < int64(len(gcdIDs)) {
		f.Call2(fnThreadID, 0, uintptr(unsafe.Pointer(&gcdIDs[i])))
	}
}

var qos = [...]uintptr{0x21, 0x19, 0x15, 0x11, 0x09} // user-interactive ... background

func globalQueue(i int) uintptr { return fr.Call2(fnGlobalQueue, qos[i%len(qos)], 0) }

func distinctGCDThreads() int {
	seen := map[uint64]bool{}
	for _, id := range gcdIDs[:min(int(gcdIDn.Load()), len(gcdIDs))] {
		seen[id] = true
	}
	return len(seen)
}

// steady runs f in windows and returns the exact allocations of the first
// steady window that allocated nothing, or of the last steady one if none of
// six did. A window is steady when the runtime made no M during it (the
// thread-create profile counts every M) and the callback high-water mark did
// not rise. A C thread entering Go takes an extra M, and the runtime makes a
// new one (7 objects, 5576 bytes) whenever it hands out its last spare or
// threads wait for one; that follows the number of threads attached at once,
// which is wider than the handlers' own concurrency (an M is held before the
// handler starts and after it returns), so the high water alone missed new
// Ms (CI: 113 objects in a window that did not raise it). An allocation per
// callback is in every steady window; the runtime's own one-offs (its
// scavenger's timer heap growing on a P it had not used, 16 bytes) miss most.
// It fails if no window of 16 was steady.
func steady(t *testing.T, f func()) (objects, bytes uint64) {
	steadyN := 0
	for round := 1; round <= 16 && steadyN < 6; round++ {
		runtime.GC()
		h0, m0 := highWater.Load(), mCount()
		o0, b0 := allocs()
		f()
		o1, b1 := allocs()
		h1, m1 := highWater.Load(), mCount()
		objects, bytes = o1-o0, b1-b0
		if h1 > h0 || m1 > m0 {
			t.Logf("window %d is not steady (Ms %d -> %d, high water %d -> %d): %d objects / %d bytes", round, m0, m1, h0, h1, objects, bytes)
			continue
		}
		steadyN++
		if objects == 0 && bytes == 0 {
			return
		}
		t.Logf("steady window %d: %d objects / %d bytes", round, objects, bytes)
	}
	if steadyN == 0 {
		t.Fatal("the runtime kept making Ms: no steady window in 16")
	}
	return
}

// mCount is the number of Ms the runtime has made. It does not allocate.
func mCount() int {
	n, _ := runtime.ThreadCreateProfile(nil)
	return n
}

func resetGCD() {
	gcdRan.Store(0)
	gcdBad.Store(0)
	gcdIDn.Store(0)
}

func checkGCD(t *testing.T, want int64) {
	t.Helper()
	if got := gcdRan.Load(); got != want {
		t.Errorf("%d callbacks ran, want %d", got, want)
	}
	if gcdBad.Load() != 0 {
		t.Errorf("%d callbacks got a wrong result through their own frame", gcdBad.Load())
	}
}

func TestDispatchApply64(t *testing.T) {
	threadLib(t)
	apply := func() { fr.Call4(fnApplyF, 64, globalQueue(0), 7, gcdApplyCB) }
	apply() // warm up: GCD's width and the extra Ms
	resetGCD()
	objects, bytes := steady(t, func() {
		for k := 0; k < 20; k++ {
			apply()
		}
	})
	t.Logf("dispatch_apply 64-wide: %d callbacks on %d threads, high water %d; %d objects / %d bytes in the last window",
		gcdRan.Load(), distinctGCDThreads(), highWater.Load(), objects, bytes)
	if objects != 0 || bytes != 0 {
		t.Errorf("steady-state callbacks allocated %d objects / %d bytes", objects, bytes)
	}
	if gcdRan.Load()%(20*64) != 0 || gcdBad.Load() != 0 {
		t.Errorf("%d callbacks ran (want a multiple of %d), %d bad", gcdRan.Load(), 20*64, gcdBad.Load())
	}
}

// 10,000 callbacks from many queues at once: every global QoS queue and 16
// serial queues of our own, all in one dispatch group.
func TestManyQueues10000(t *testing.T) {
	threadLib(t)
	var serial [16]uintptr
	for i := range serial {
		serial[i] = fr.Call2(fnQueueCreate, uintptr(unsafe.Pointer(&churnLabel[0])), 0)
	}
	defer func() {
		for _, q := range serial {
			fr.Call1(fnRelease, q)
		}
	}()
	run := func() {
		g := fr.Call0(fnGroupCreate)
		for i := 0; i < 10000; i++ {
			q := serial[i%len(serial)]
			if i%3 == 0 {
				q = globalQueue(i)
			}
			fr.Call4(fnGroupAsyncF, g, q, uintptr(i), gcdWorkCB)
		}
		fr.Call2(fnGroupWait, g, ^uintptr(0))
		fr.Call1(fnRelease, g)
	}
	run()
	resetGCD()
	objects, bytes := steady(t, run)
	t.Logf("10,000 callbacks from %d queues: %d threads, high water %d; %d objects / %d bytes in the last window",
		len(serial)+len(qos), distinctGCDThreads(), highWater.Load(), objects, bytes)
	if objects != 0 || bytes != 0 {
		t.Errorf("steady-state callbacks allocated %d objects / %d bytes", objects, bytes)
	}
	if gcdRan.Load()%10000 != 0 || gcdBad.Load() != 0 {
		t.Errorf("%d callbacks ran (want a multiple of 10000), %d bad", gcdRan.Load(), gcdBad.Load())
	}
}

// Thread churn: queues created and released in a loop, mixed with fresh
// pthreads that exit, so threads come and go under the callbacks. A thread
// that leaves holds nothing, so once the high water is reached the churn
// allocates nothing.
func TestThreadChurn(t *testing.T) {
	threadLib(t)
	churn := func() {
		for k := 0; k < 40; k++ {
			q := fr.Call2(fnQueueCreate, uintptr(unsafe.Pointer(&churnLabel[0])), 0)
			g := fr.Call0(fnGroupCreate)
			for i := 0; i < 25; i++ {
				fr.Call4(fnGroupAsyncF, g, q, uintptr(i), gcdWorkCB)
				fr.Call4(fnGroupAsyncF, g, globalQueue(k+i), uintptr(i), gcdWorkCB)
			}
			fr.Call2(fnGroupWait, g, ^uintptr(0))
			fr.Call1(fnRelease, g)
			fr.Call1(fnRelease, q)
			startThreads(t, 2, false)
		}
	}
	churn()
	resetGCD()
	threadIDn.Store(0)
	objects, bytes := steady(t, churn)
	t.Logf("churn: %d GCD callbacks on %d GCD threads, %d fresh pthreads, high water %d; %d objects / %d bytes in the last window",
		gcdRan.Load(), distinctGCDThreads(), threadIDn.Load(), highWater.Load(), objects, bytes)
	if objects != 0 || bytes != 0 {
		t.Errorf("steady-state churn allocated %d objects / %d bytes", objects, bytes)
	}
	checkGCD(t, gcdRan.Load()/2000*2000)
}

// BenchmarkGCDCallback is the cost of one callback from a GCD worker thread
// (a serial queue, so each one attaches the thread to the runtime and
// detaches it again), dispatch included.
func BenchmarkGCDCallback(b *testing.B) {
	threadLib(b)
	q := fr.Call2(fnQueueCreate, uintptr(unsafe.Pointer(&churnLabel[0])), 0)
	defer fr.Call1(fnRelease, q)
	g := fr.Call0(fnGroupCreate)
	defer fr.Call1(fnRelease, g)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fr.Call4(fnGroupAsyncF, g, q, uintptr(i), gcdWorkCB)
	}
	fr.Call2(fnGroupWait, g, ^uintptr(0))
}

func TestNestedCallbacksOnAGCDThread(t *testing.T) {
	threadLib(t)
	g := fr.Call0(fnGroupCreate)
	fr.Call4(fnGroupAsyncF, g, globalQueue(1), 0, gcdNestCB)
	fr.Call2(fnGroupWait, g, ^uintptr(0))
	fr.Call1(fnRelease, g)
	checkNest(t, "GCD thread")
}
