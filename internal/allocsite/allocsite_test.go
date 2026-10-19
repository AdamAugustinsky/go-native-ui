package allocsite_test

import (
	"runtime"
	"testing"

	"gonativeui/internal/allocsite"
)

var (
	sink       []*[3]uint64
	extraProcs int
)

//go:noinline
func allocateHere(n int) {
	for range n {
		sink = append(sink, new([3]uint64))
	}
}

// Profiling every allocation keeps the gates' counters exact: a new call
// stack's bucket is not a heap allocation.
func TestProfilingDoesNotAllocate(t *testing.T) {
	defer allocsite.Enable()()
	sink = make([]*[3]uint64, 0, 8)
	var m0, m1 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m0)
	allocateHere(1) // a stack the profile has not seen
	runtime.ReadMemStats(&m1)
	if d := m1.Mallocs - m0.Mallocs; d != 1 {
		t.Fatalf("one allocation counted as %d", d)
	}
}

func TestDiffClassifies(t *testing.T) {
	defer allocsite.Enable()()
	var s0, s1 allocsite.Snapshot
	s0.Take()
	s1.Take()
	sink = make([]*[3]uint64, 0, 8)
	runtime.GC()
	s0.Take()
	allocateHere(5)
	// The runtime allocates a P for itself the first time there are more.
	extraProcs++
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(runtime.GOMAXPROCS(0) + extraProcs))
	runtime.GC()
	s1.Take()
	sites := allocsite.Diff(&s0, &s1)
	var ours, rt bool
	for _, s := range sites {
		switch {
		case s.Func == "gonativeui/internal/allocsite_test.allocateHere":
			ours = s.Class == allocsite.Ours && s.Objects == 5
		case s.Func == "runtime.procresize":
			rt = s.Class == allocsite.Runtime
		case s.Class == allocsite.Ours && s.Func != "gonativeui/internal/allocsite_test.TestDiffClassifies":
			t.Errorf("unexpected site of ours: %+v", s)
		}
	}
	if !ours {
		t.Errorf("the 5 allocations of allocateHere are not ours: %+v", sites)
	}
	if !rt {
		t.Errorf("the runtime's new P is not the runtime's: %+v", sites)
	}
	if n := allocsite.Objects(sites, allocsite.Ours); n < 5 {
		t.Errorf("%d objects of ours, want at least 5", n)
	}
}
