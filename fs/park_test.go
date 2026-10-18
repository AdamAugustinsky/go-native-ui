package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"gonativeui/core"
)

// waiter is how a test goroutine waits for the Service's wake: it sleeps in a
// core.Parker, as the UI thread sleeps in the platform's event wait, so the
// waiting adds no contention and no allocation of its own.
type waiter struct{ p *core.Parker }

// measureWindows runs cycles of work through s in windows, GCs first, and
// returns the process-wide Mallocs delta of each window. Each cycle lists every
// directory and collects, then releases, the results. Nothing inside a window
// allocates on the test's behalf: the result slice is preallocated and the
// counters live in preallocated arrays. With spin set the cycle polls in a
// loop instead of sleeping, which makes the UI thread contend for the
// Service's lock with the workers.
func measureWindows(t *testing.T, s *Service, w waiter, dirs []string, windows, cycles int, spin bool) []uint64 {
	t.Helper()
	buf := make([]Result, 0, 64)
	deltas := make([]uint64, windows)
	bytes := make([]uint64, windows)
	bad := false
	var m0, m1 runtime.MemStats
	cycle := func() bool {
		for i, d := range dirs {
			s.List(uint32(i+1), d, SortKey(i%4), i%2 == 0)
		}
		got := 0
		deadline := time.Now().Add(10 * time.Second)
		for got < len(dirs) {
			buf = s.Poll(buf[:0])
			for _, r := range buf {
				if r.Kind != ResultListing || r.Listing.Err != nil {
					bad = true
				}
				s.Release(r.Listing, r.Lease)
				got++
			}
			if got == len(dirs) {
				break
			}
			if time.Now().After(deadline) {
				return false
			}
			if spin {
				runtime.Gosched()
			} else {
				w.p.Park()
			}
		}
		return true
	}
	for i := 0; i < 50; i++ { // warm: pool, maps, buffers, sort scratch
		if !cycle() {
			t.Fatal("timed out in warm-up")
		}
	}
	procs := runtime.GOMAXPROCS(0)
	for win := 0; win < windows; win++ {
		// Take sudogs away: shrinking GOMAXPROCS drops the caches of the
		// removed Ps, growing it back brings them in empty, and the collection
		// empties the central list. A worker that now parks on one of those Ps
		// would have to allocate a sudog, which is what happens in a long run
		// whenever a goroutine moves to a P that has none.
		runtime.GOMAXPROCS(2)
		runtime.GOMAXPROCS(procs)
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
		runtime.ReadMemStats(&m0)
		for i := 0; i < cycles; i++ {
			if !cycle() {
				t.Fatalf("window %d: timed out after %d cycles", win, i)
			}
		}
		runtime.ReadMemStats(&m1)
		deltas[win], bytes[win] = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
	}
	if bad {
		t.Fatal("a cycle returned something other than a clean listing")
	}
	for win := range deltas {
		t.Logf("window %d: %d mallocs, %d bytes", win, deltas[win], bytes[win])
	}
	return deltas
}

func parkDirs(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	var dirs []string
	for d := 0; d < 4; d++ {
		p := filepath.Join(root, fmt.Sprintf("d%d", d))
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		for f := 0; f < 20; f++ {
			if err := os.WriteFile(filepath.Join(p, fmt.Sprintf("f%02d.txt", f)), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		dirs = append(dirs, p)
	}
	return dirs
}

// A Service whose workers sleep and wake over and over, across Ps, allocates
// nothing. A goroutine that parks through sync.Cond, a channel or a contended
// sync.Mutex takes a sudog from its P's cache and allocates one when the cache
// and the central list (which runtime.GC empties) are empty: a window then
// holds a 112-byte allocation that is not the program's. Each window starts
// from such a state (see measureWindows). The first window after the
// collection may also hold the runtime's own one-off allocations (starting an
// OS thread, the scavenger's timer), so the second and third must be exactly
// 0, and the measurement may be repeated once because the runtime's own noise
// can land in any window; an allocation caused by the Service lands in every
// attempt.
// The same with the UI thread polling in a loop while the workers run, which
// forces the Service's lock to be contended: a contended sync.Mutex parks its
// waiter on a sudog too.
func TestServiceContendedLockDoesNotAllocate(t *testing.T) {
	runParkTest(t, true)
}

func runParkTest(t *testing.T, spin bool) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(max(4, runtime.GOMAXPROCS(0))))
	w := waiter{core.NewParker()}
	defer w.p.Close()
	s := NewService(w.p.Unpark)
	defer s.Close()
	dirs := parkDirs(t)
	var deltas []uint64
	for attempt := 0; attempt < 2; attempt++ {
		deltas = measureWindows(t, s, w, dirs, 3, 1000, spin)
		if deltas[1] == 0 && deltas[2] == 0 {
			return
		}
	}
	t.Errorf("mallocs per window %v: the second and third must be 0", deltas)
}
