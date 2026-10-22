package allocsite_test

import (
	"os"
	"os/exec"
	"runtime"
	"testing"

	"gonativeui/internal/allocsite"
)

type assertionSource interface{ String() string }
type assertionTarget interface{ Target() }
type assertionOther interface{ Other() }
type assertedValue struct{}

func (assertedValue) String() string { return "" }
func (assertedValue) Target()        {}

var asserted assertionTarget

//go:noinline
func assertPerFrame(v assertionSource) { asserted, _ = v.(assertionTarget) }

//go:noinline
func switchPerFrame(v assertionSource) {
	switch v := v.(type) {
	case assertionTarget:
		asserted = v
	case assertionOther:
		v.Other()
	}
}

func TestAssertionCacheIsOurs(t *testing.T) {
	if os.Getenv("GNUI_ASSERT_ALLOC_CHILD") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestAssertionCacheIsOurs$")
		cmd.Env = append(os.Environ(), "GNUI_ASSERT_ALLOC_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	defer allocsite.Enable()()
	var before, after allocsite.Snapshot
	before.Take()
	after.Take()
	runtime.GC()
	before.Take()
	for range 50000 {
		assertPerFrame(assertedValue{})
		switchPerFrame(assertedValue{})
	}
	runtime.GC()
	after.Take()
	foundAssert, foundSwitch := false, false
	for _, site := range allocsite.Diff(&before, &after) {
		if site.Class == allocsite.Ours && site.Objects > 0 {
			foundAssert = foundAssert || site.Func == "gonativeui/internal/allocsite_test.assertPerFrame"
			foundSwitch = foundSwitch || site.Func == "gonativeui/internal/allocsite_test.switchPerFrame"
		}
	}
	if !foundAssert || !foundSwitch {
		t.Fatalf("assertion/switch cache not attributed to caller: %+v", allocsite.Diff(&before, &after))
	}
}

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

// allocateAt allocates once, depth calls down: each depth is a call stack of
// its own.
//
//go:noinline
func allocateAt(depth int) {
	if depth > 0 {
		allocateAt(depth - 1)
		return
	}
	sink = append(sink, new([3]uint64))
}

// Profiling every allocation keeps the gates' counters exact: a new call
// stack's bucket is not a heap allocation. The counters are the process's,
// so another goroutine's allocation can land in an attempt (CI: 13 on a
// loaded runner); each attempt is a stack the profile has not seen, and one
// must count exactly its own allocation. A bucket that counted would be in
// every attempt.
func TestProfilingDoesNotAllocate(t *testing.T) {
	defer allocsite.Enable()()
	sink = make([]*[3]uint64, 0, 64)
	var counts []uint64
	for depth := 1; depth <= 20; depth++ {
		var m0, m1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m0)
		allocateAt(depth)
		runtime.ReadMemStats(&m1)
		d := m1.Mallocs - m0.Mallocs
		if d == 1 {
			return
		}
		if d == 0 {
			t.Fatal("an allocation was not counted")
		}
		counts = append(counts, d)
	}
	t.Fatalf("one allocation on a new stack never counted as 1: %v", counts)
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
