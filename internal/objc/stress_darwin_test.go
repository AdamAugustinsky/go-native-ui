//go:build darwin

package objc

import (
	"bytes"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// Callback frames stay valid while Go moves things around them: chains of
// nested callbacks on the main thread and on GCD threads, each level holding
// its frame live while its goroutine's stack grows (deep recursion), while
// it spins without calls (async preemption), and, at the deepest level,
// while it is blocked and the test forces GCs (which also shrink stacks),
// with the CPU profiler sending SIGPROF to every thread, Go's or not.

const (
	stressDepth  = 8
	stressChains = 16 // on GCD threads, plus one on the main thread
	stressMain   = stressChains
)

var (
	stressOnce     sync.Once
	stressCB       uintptr // void f(uintptr ctx): ctx = chain<<8 | level
	fnLabs         uintptr
	stressBad      atomic.Int64
	stressMoves    atomic.Int64 // handlers whose goroutine stack moved under them
	stressRan      [stressChains + 1][stressDepth]atomic.Int32
	stressArrived  atomic.Int32
	stressRelease  chan struct{}
	stressSinkLock sync.Mutex
	stressSink     uintptr
)

//go:noinline
func growStack(n int) uintptr {
	var pad [256]byte
	pad[n%len(pad)] = byte(n)
	if n == 0 {
		return uintptr(pad[0])
	}
	return growStack(n-1) + uintptr(pad[n%len(pad)])
}

func spin() uintptr {
	x := uintptr(1)
	for i := uintptr(0); i < 1000000; i++ { // no calls: only async preemption stops it
		x = x*31 + i
	}
	return x
}

func stressHandler(c *ffi.CB) {
	ctx := c.Int(0)
	chain, level := ctx>>8, int(ctx&0xff)
	f := c.Frame()
	mark := 0xab000000 | ctx
	f.Scratch()[0], f.Scratch()[1] = mark, ^mark
	check := func() {
		if f.Scratch()[0] != mark || f.Scratch()[1] != ^mark || c.Int(0) != ctx {
			stressBad.Add(1)
		}
		if f.Call1(fnLabs, uintptr(-int64(level+1))) != uintptr(level+1) {
			stressBad.Add(1)
		}
	}
	var probe byte
	before := uintptr(unsafe.Pointer(&probe))
	sink := growStack(64 + 24*level) // up to ~60 KiB: the goroutine stack grows and moves
	if uintptr(unsafe.Pointer(&probe)) != before {
		stressMoves.Add(1)
	}
	sink += spin()
	check()
	if level < stressDepth-1 {
		f.Call1(stressCB, ctx+1) // nested: our own entry, called through this frame
	} else {
		stressArrived.Add(1)
		<-stressRelease // blocked with every outer frame live while the test GCs
	}
	sink += growStack(200)
	check()
	stressRan[chain][level].Add(1)
	stressSinkLock.Lock()
	stressSink += sink
	stressSinkLock.Unlock()
}

func TestCallbackFramesUnderGCAndPreemption(t *testing.T) {
	stressOnce.Do(func() {
		h, _ := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW)
		fnLabs, _ = purego.Dlsym(h, "labs")
		stressCB = ffi.NewCallback(stressHandler)
	})
	var prof bytes.Buffer
	if err := pprof.StartCPUProfile(&prof); err != nil {
		t.Skip("CPU profiler busy:", err)
	}
	defer pprof.StopCPUProfile()

	stop := make(chan struct{})
	var garbage sync.WaitGroup
	for range 4 { // allocation pressure and GCs throughout
		garbage.Go(func() {
			var keep [][]byte
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				keep = append(keep, make([]byte, 4096))
				if len(keep) > 256 {
					keep = keep[:0]
				}
				if i%64 == 0 {
					runtime.GC()
				}
			}
		})
	}
	defer func() { close(stop); garbage.Wait() }()

	for round := 0; round < 3; round++ {
		for c := range stressRan {
			for l := range stressRan[c] {
				stressRan[c][l].Store(0)
			}
		}
		stressArrived.Store(0)
		stressRelease = make(chan struct{})
		g := F.Call0(fnGroupCreate)
		for chain := 0; chain < stressChains; chain++ {
			F.Call4(fnGroupAsyncF, g, globalQueue(F, chain), uintptr(chain<<8), stressCB)
		}
		mainDone := make(chan struct{})
		go func() { onMain(func() { F.Call1(stressCB, stressMain<<8) }); close(mainDone) }()

		deadline := time.Now().Add(20 * time.Second)
		for stressArrived.Load() < stressChains+1 {
			if time.Now().After(deadline) {
				t.Fatalf("round %d: only %d of %d chains reached their deepest level", round, stressArrived.Load(), stressChains+1)
			}
			time.Sleep(time.Millisecond)
		}
		for range 3 {
			runtime.GC()
		}
		debug.FreeOSMemory()
		close(stressRelease)
		if F.Call2(fnGroupWait, g, F.Call2(fnDispatchTime, 0, 20e9)) != 0 {
			t.Fatalf("round %d: the GCD chains did not finish", round)
		}
		F.Call1(fnDispatchRelease, g)
		<-mainDone
		for c := range stressRan {
			for l := range stressRan[c] {
				if n := stressRan[c][l].Load(); n != 1 {
					t.Fatalf("round %d: chain %d level %d ran %d times", round, c, l, n)
				}
			}
		}
	}
	if stressMoves.Load() == 0 {
		t.Error("no handler's stack moved: the test no longer exercises stack growth under live frames")
	}
	if n := stressBad.Load(); n != 0 {
		t.Errorf("%d checks found a frame, its scratch words or the callback's arguments changed", n)
	}
	pprof.StopCPUProfile()
	t.Logf("3 rounds of %d chains x %d levels; %d handlers saw their stack move; CPU profile %d bytes",
		stressChains+1, stressDepth, stressMoves.Load(), prof.Len())
}
