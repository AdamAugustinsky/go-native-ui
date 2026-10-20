//go:build darwin

package objc

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
	stressEntered  [stressChains + 1]atomic.Int32 // levels of each chain entered
	stressAt       [stressChains + 1]atomic.Int64 // when each chain reached its deepest level (UnixNano)
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
	stressEntered[chain].Add(1)
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
		stressAt[chain].Store(time.Now().UnixNano())
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

// stallExcuse is the evidence that the machine, not the callbacks, held the
// lagging chains back, or "": they all run at background QoS, which macOS
// defers while anything else wants the CPU, and the load average is at
// least the number of CPUs. A chain of any other class that stalls, or a
// background one on an idle machine, fails.
func stallExcuse(lag []int, load float64, cpus int) string {
	if len(lag) == 0 || load < float64(cpus) {
		return ""
	}
	for _, c := range lag {
		if c >= stressChains || qosClasses[c%len(qosClasses)] != qosBackground {
			return ""
		}
	}
	return fmt.Sprintf("only background QoS chains lag, with a load average of %.1f on %d CPUs: macOS defers them while the machine is busy", load, cpus)
}

const qosBackground = 0x09 // QOS_CLASS_BACKGROUND

// loadAverage is the one-minute load average (vm.loadavg: three fixed-point
// values, then their scale), or 0.
func loadAverage() float64 {
	v, err := syscall.Sysctl("vm.loadavg")
	b := []byte(v)
	if err != nil || len(b) < 16 {
		return 0
	}
	b = append(b, 0) // Sysctl drops a trailing NUL byte
	scale := float64(binary.LittleEndian.Uint64(b[16:]))
	if len(b) < 24 || scale == 0 {
		return 0
	}
	return float64(binary.LittleEndian.Uint32(b)) / scale
}

// The excuse takes background chains on a loaded machine only.
func TestStallExcuse(t *testing.T) {
	var bg, other int = -1, -1
	for c := range stressChains {
		if qosClasses[c%len(qosClasses)] == qosBackground {
			bg = c
		} else {
			other = c
		}
	}
	if bg < 0 || other < 0 {
		t.Fatal("the chains have no background QoS one, or only those")
	}
	for _, c := range []struct {
		name string
		lag  []int
		load float64
		want bool
	}{
		{"background, loaded", []int{bg}, 12, true},
		{"background, idle", []int{bg}, 1, false},
		{"another class, loaded", []int{bg, other}, 12, false},
		{"the main thread, loaded", []int{stressMain}, 12, false},
		{"nothing lags", nil, 12, false},
	} {
		if got := stallExcuse(c.lag, c.load, 10) != ""; got != c.want {
			t.Errorf("%s: excused %v, want %v", c.name, got, c.want)
		}
	}
	if l := loadAverage(); !(l > 0) {
		t.Errorf("load average %v", l)
	}
}

// stallLimit is how long the chains may make no progress at all.
const stallLimit = 60 * time.Second

// stressProgress counts the levels entered and the chains arrived so far.
func stressProgress() int {
	n := int(stressArrived.Load())
	for c := range stressEntered {
		n += int(stressEntered[c].Load())
	}
	return n
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
					// Collections throughout, but not every CPU spinning: on
					// CI's three vCPUs four of these left the background QoS
					// chains, which macOS defers while anything else wants
					// the CPU, no time at all.
					time.Sleep(time.Millisecond)
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
		for c := range stressEntered {
			stressEntered[c].Store(0)
		}
		stressRelease = make(chan struct{})
		g := F.Call0(fnGroupCreate)
		for chain := 0; chain < stressChains; chain++ {
			F.Call4(fnGroupAsyncF, g, globalQueue(F, chain), uintptr(chain<<8), stressCB)
		}
		mainDone := make(chan struct{})
		go func() { onMain(func() { F.Call1(stressCB, stressMain<<8) }); close(mainDone) }()

		// The chains on the background QoS queue are the slow ones: macOS
		// lets them wait while anything else wants the CPU (here the test's
		// own collectors and, on CI, the other packages' tests), seconds
		// when the machine is busy. So the wait fails on no progress at all
		// for stallLimit, or at the binary's deadline, not on a total time.
		t0 := time.Now()
		end, hasEnd := t.Deadline()
		progress, progressAt := -1, t0
		for stressArrived.Load() < stressChains+1 {
			now := time.Now()
			if p := stressProgress(); p != progress {
				progress, progressAt = p, now
			}
			if now.Sub(progressAt) > stallLimit || hasEnd && end.Sub(now) < 30*time.Second {
				var b strings.Builder
				var lag []int
				for c := range stressEntered {
					if n := stressEntered[c].Load(); n < stressDepth {
						lag = append(lag, c)
						q := "main thread"
						if c < stressChains {
							q = fmt.Sprintf("QoS %#x", qosClasses[c%len(qosClasses)])
						}
						fmt.Fprintf(&b, " chain %d (%s) entered %d of %d levels;", c, q, n, stressDepth)
					}
				}
				close(stressRelease) // let the chains that are in finish
				if why := stallExcuse(lag, loadAverage(), runtime.NumCPU()); why != "" {
					t.Skipf("round %d: after %v (%v without progress), %s:%s", round, now.Sub(t0).Round(time.Millisecond), now.Sub(progressAt).Round(time.Millisecond), why, b.String())
				}
				t.Fatalf("round %d: after %v (%v without progress), only %d of %d chains reached their deepest level:%s",
					round, now.Sub(t0).Round(time.Millisecond), now.Sub(progressAt).Round(time.Millisecond), stressArrived.Load(), stressChains+1, b.String())
			}
			time.Sleep(time.Millisecond)
		}
		var slow [len(qosClasses) + 1]time.Duration
		for c := range stressAt {
			k := len(qosClasses)
			if c < stressChains {
				k = c % len(qosClasses)
			}
			slow[k] = max(slow[k], time.Duration(stressAt[c].Load()-t0.UnixNano()))
		}
		t.Logf("round %d: slowest arrival per QoS class %#x, then the main thread: %v", round, qosClasses, slow)
		for range 3 {
			runtime.GC()
		}
		debug.FreeOSMemory()
		close(stressRelease)
		if F.Call2(fnGroupWait, g, F.Call2(fnDispatchTime, 0, uintptr(stallLimit))) != 0 {
			var lag []int
			for c := range stressChains {
				if stressRan[c][0].Load() == 0 { // the outermost level finishes last
					lag = append(lag, c)
				}
			}
			if why := stallExcuse(lag, loadAverage(), runtime.NumCPU()); why != "" {
				t.Skipf("round %d: the GCD chains %v did not finish in %v: %s", round, lag, stallLimit, why)
			}
			t.Fatalf("round %d: the GCD chains %v did not finish in %v (load average %.1f on %d CPUs)", round, lag, stallLimit, loadAverage(), runtime.NumCPU())
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
