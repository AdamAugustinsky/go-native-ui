//go:build (darwin || linux) && (arm64 || amd64)

package ffi

import (
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Callbacks on threads Go never created. With CGO_ENABLED=0 a C thread
// enters Go through purego's fakecgo: crosscall2 finds no g and takes an
// extra M (runtime.needm), and gives it back when the callback returns
// (fakecgo binds no M to the thread). These tests check that such a thread
// can run a handler and make C calls with the invocation's own frame, at any
// concurrency and nesting, and measure what it allocates.

var (
	threadOnce                     sync.Once
	fnPthreadCreate, fnPthreadJoin uintptr
	fnThreadID                     uintptr // darwin: pthread_threadid_np
	threadStartCB                  uintptr // void *(*)(void *)
	threadRan, threadFrameBad      atomic.Int64
	threadIDs                      = new([1024]uint64) // filled by darwin threads
	threadIDn                      atomic.Int64
	pthreads                       = new([128]uintptr)
	threadGate                     *gate // when set, a started thread waits here
)

// inFlight counts handlers running now; highWater is its maximum.
var inFlight, highWater atomic.Int64

func enter() {
	n := inFlight.Add(1)
	for {
		h := highWater.Load()
		if n <= h || highWater.CompareAndSwap(h, n) {
			return
		}
	}
}

func leave() { inFlight.Add(-1) }

// gate holds callbacks inside their handlers until opened. A waiter blocks
// in read(2) on a pipe, outside the Go scheduler: parking a goroutine (a
// channel, a WaitGroup) takes a runtime sudog, which allocates when the P's
// cache is empty and would be counted against the callbacks.
type gate struct {
	arrived atomic.Int64
	r, w    int
}

var (
	theGate  gate
	gateOnce sync.Once
	gateFill = new([1024]byte)
)

func newGate() *gate {
	gateOnce.Do(func() {
		var p [2]int
		if err := syscall.Pipe(p[:]); err != nil {
			panic(err)
		}
		theGate.r, theGate.w = p[0], p[1]
	})
	theGate.arrived.Store(0)
	return &theGate
}

func (g *gate) wait() {
	g.arrived.Add(1)
	var b [1]byte
	for {
		if n, err := syscall.Read(g.r, b[:]); n == 1 || err != syscall.EINTR {
			return
		}
	}
}

// open waits until n callbacks are inside, then lets them all go.
func (g *gate) open(t testing.TB, n int) {
	deadline := time.Now().Add(10 * time.Second)
	for g.arrived.Load() < int64(n) {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d callbacks arrived", g.arrived.Load(), n)
		}
		runtime.Gosched() // not time.Sleep: a P's first timer grows its timer heap (an allocation)
	}
	for left := gateFill[:n]; len(left) > 0; {
		k, err := syscall.Write(g.w, left)
		if err != nil && err != syscall.EINTR {
			t.Fatal(err)
		}
		left = left[max(k, 0):]
	}
}

func threadLib(t testing.TB) {
	lib(t)
	threadOnce.Do(func() {
		var h uintptr
		var err error
		if runtime.GOOS == "darwin" {
			h, err = purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
		} else {
			h, err = purego.Dlopen("libc.so.6", purego.RTLD_NOW)
		}
		if err != nil {
			panic(err)
		}
		sym := func(n string) uintptr {
			p, err := purego.Dlsym(h, n)
			if err != nil {
				panic(err)
			}
			return p
		}
		fnPthreadCreate, fnPthreadJoin = sym("pthread_create"), sym("pthread_join")
		if runtime.GOOS == "darwin" {
			fnThreadID = sym("pthread_threadid_np")
		}
		threadStartCB = NewCallback(threadStart)
		nestCB = NewCallback(nest)
		nestThreadCB = NewCallback(func(c *CB) { runNest(c.Frame()); c.Ret(0) })
		if moreSetup != nil {
			moreSetup(sym)
		}
	})
}

// moreSetup registers the OS-specific tests' callbacks with the others, so
// that TestCallbackTableEnds can fill the table whatever the test order.
var moreSetup func(sym func(string) uintptr)

// threadStart is a pthread start routine. It makes C calls with its own
// frame and records the thread's system-wide id (darwin).
func threadStart(c *CB) {
	enter()
	defer leave()
	threadRan.Add(1)
	f := c.Frame()
	if int64(f.Call1(fnLabs, uintptr(^uint64(6)))) != 7 { // labs(-7)
		threadFrameBad.Add(1)
	}
	if fnThreadID != 0 {
		if i := threadIDn.Add(1) - 1; i < int64(len(threadIDs)) {
			f.Call2(fnThreadID, 0, uintptr(unsafe.Pointer(&threadIDs[i])))
		}
	}
	if g := threadGate; g != nil {
		g.wait()
	}
	c.Ret(0)
}

// startThreads creates n fresh pthreads (n <= len(pthreads)) and joins them.
// With together set they all wait for each other inside the callback, so n
// threads are in Go at once.
func startThreads(t testing.TB, n int, together bool) {
	if !together {
		for i := 0; i < n; i++ {
			if e := fr.Call4(fnPthreadCreate, uintptr(unsafe.Pointer(&pthreads[0])), 0, threadStartCB, 0); e != 0 {
				t.Fatalf("pthread_create: %d", e)
			}
			fr.Call2(fnPthreadJoin, pthreads[0], 0)
		}
		return
	}
	threadGate = newGate()
	defer func() { threadGate = nil }()
	for i := 0; i < n; i++ {
		if e := fr.Call4(fnPthreadCreate, uintptr(unsafe.Pointer(&pthreads[i])), 0, threadStartCB, 0); e != 0 {
			t.Fatalf("pthread_create: %d", e)
		}
	}
	threadGate.open(t, n)
	for i := 0; i < n; i++ {
		fr.Call2(fnPthreadJoin, pthreads[i], 0)
	}
}

// measure runs f in up to three windows and returns the first window's
// exact allocation counts that allocated nothing, or the last one's. The
// counters are process-wide; see TestZeroAlloc.
func measure(t testing.TB, f func()) (objects, bytes uint64) {
	for round := 1; round <= 3; round++ {
		runtime.GC()
		o0, b0 := allocs()
		f()
		o1, b1 := allocs()
		objects, bytes = o1-o0, b1-b0
		if objects == 0 && bytes == 0 {
			break
		}
		t.Logf("window %d: %d objects / %d bytes", round, objects, bytes)
	}
	return
}

// The first callback on a fresh pthread attaches it to the runtime; the
// handler runs and calls C with its own frame. Fresh threads one after
// another reuse the extra M the previous one gave back, so after the first
// they allocate nothing.
func TestColdThreadAttach(t *testing.T) {
	threadLib(t)
	threadRan.Store(0)
	threadIDn.Store(0)
	o0, b0 := allocs()
	startThreads(t, 1, false)
	o1, b1 := allocs()
	t.Logf("first cold thread: %d objects / %d bytes", o1-o0, b1-b0)

	const n = 100
	objects, bytes := measure(t, func() {
		startThreads(t, n, false)
	})
	t.Logf("%d more cold threads, one at a time: %d objects / %d bytes", n, objects, bytes)
	if objects != 0 || bytes != 0 {
		t.Errorf("fresh threads after the first allocated %d objects / %d bytes", objects, bytes)
	}
	if threadFrameBad.Load() != 0 {
		t.Errorf("%d threads got a wrong result through their own frame", threadFrameBad.Load())
	}
	if got := threadRan.Load(); got < 1+n {
		t.Errorf("%d thread callbacks ran, want at least %d", got, 1+n)
	}
	if runtime.GOOS == "darwin" {
		seen := map[uint64]bool{}
		for _, id := range threadIDs[:min(int(threadIDn.Load()), len(threadIDs))] {
			seen[id] = true
		}
		t.Logf("%d distinct thread ids", len(seen))
		if len(seen) < 1+n {
			t.Errorf("%d distinct threads, want %d fresh ones", len(seen), 1+n)
		}
	}
}

// Many fresh threads inside Go at once: the runtime makes one extra M per
// new concurrency high (a Go allocation). Repeating the same concurrency
// allocates nothing.
func TestConcurrentColdThreads(t *testing.T) {
	threadLib(t)
	const n = 64
	h0 := highWater.Load()
	o0, b0 := allocs()
	startThreads(t, n, true)
	o1, b1 := allocs()
	h1 := highWater.Load()
	if h1 < n {
		t.Fatalf("high water %d, want %d threads in callbacks at once", h1, n)
	}
	t.Logf("first %d fresh threads in callbacks at once: high water %d -> %d, %d objects / %d bytes (%.1f objects, %.0f bytes per thread above the old high)",
		n, h0, h1, o1-o0, b1-b0, float64(o1-o0)/float64(max(h1-h0, 1)), float64(b1-b0)/float64(max(h1-h0, 1)))
	objects, bytes := measure(t, func() { startThreads(t, n, true) })
	t.Logf("%d fresh threads again, at the same high water: %d objects / %d bytes", n, objects, bytes)
	if objects != 0 || bytes != 0 {
		t.Errorf("a repeat at the high-water mark allocated %d objects / %d bytes", objects, bytes)
	}
	if threadFrameBad.Load() != 0 {
		t.Errorf("%d threads got a wrong result through their own frame", threadFrameBad.Load())
	}
}

// Each invocation's frame is its own: nested callbacks on one thread get
// different frames, and every outer frame's call in progress and its scratch
// words survive the nested ones. Nesting is bounded by the thread's native
// stack (each level takes the callback's C frame, about 0.5 KiB, plus
// qsort's and the runtime's frames), not by ffi; 32 levels run on a Go
// thread, a fresh pthread and a GCD thread.
const nestLevels = 32

var (
	nestCB, nestThreadCB uintptr
	nestSeen             [nestLevels]uintptr
	nestArrs             [nestLevels][2]int64 // qsort's arrays, in static memory
	nestDepth            int
	nestBad              atomic.Int64
)

// nest is a qsort comparator that sorts again from inside, nestLevels deep.
func nest(c *CB) {
	f := c.Frame()
	level := nestDepth
	nestSeen[level] = uintptr(unsafe.Pointer(f))
	f.Scratch()[0] = 0x5eed0000 + uintptr(level)
	if level < nestLevels-1 {
		nestDepth++
		arr := &nestArrs[level] // not new: an array only C sees stays on the stack, which moves as it grows
		arr[0], arr[1] = 2, 1
		f.Call4(fnQs, uintptr(unsafe.Pointer(arr)), 2, 8, nestCB)
		nestDepth--
		if arr[0] != 1 || f.Scratch()[0] != 0x5eed0000+uintptr(level) {
			nestBad.Add(1)
		}
	}
	c.Ret(1) // the pair is out of order: qsort swaps it
}

// runNest sorts through nest from frame f and checks every level's frame.
func runNest(f *Frame) {
	nestDepth = 0
	clear(nestSeen[:])
	f.Call4(fnQs, uintptr(unsafe.Pointer(&nestArrs[nestLevels-1])), 2, 8, nestCB)
	seen := map[uintptr]bool{}
	for _, p := range nestSeen {
		if p == 0 || seen[p] {
			nestBad.Add(1)
		}
		seen[p] = true
	}
}

func checkNest(t *testing.T, where string) {
	t.Helper()
	if n := nestBad.Swap(0); n != 0 {
		t.Errorf("%s: %d levels of %d shared a frame or lost an outer call's state", where, n, nestLevels)
	}
}

func TestNestedCallbacksHaveTheirOwnFrames(t *testing.T) {
	threadLib(t)
	runNest(fr)
	checkNest(t, "Go thread")
	if e := fr.Call4(fnPthreadCreate, uintptr(unsafe.Pointer(&pthreads[0])), 0, nestThreadCB, 0); e != 0 {
		t.Fatalf("pthread_create: %d", e)
	}
	fr.Call2(fnPthreadJoin, pthreads[0], 0)
	checkNest(t, "fresh pthread")
}

// Every entry of the callback table reaches its own handler, the last one
// included, and one more is refused.
func TestCallbackTableEnds(t *testing.T) {
	threadLib(t)
	if testing.Short() {
		t.Skip("fills the process's callback table")
	}
	first := Callbacks()
	entries := make([]uintptr, 0, MaxCallbacks-first)
	for i := first; i < MaxCallbacks; i++ {
		entries = append(entries, NewCallback(func(c *CB) { c.Ret(uintptr(i)<<1 | c.Int(0)) }))
	}
	for k, e := range entries {
		if got, want := fr.Call1(e, 1), uintptr(first+k)<<1|1; got != want {
			t.Fatalf("entry %d returned %d, want %d", first+k, got, want)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("a callback beyond MaxCallbacks did not panic")
		}
	}()
	NewCallback(func(*CB) {})
}
