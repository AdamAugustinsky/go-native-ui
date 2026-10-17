package text

import (
	"fmt"
	"runtime"
	"testing"
)

// strRun is a prebuilt insert: building these inside the measured window would
// count the test's own allocations.
type strRun struct {
	s  string
	b  []byte
	g  []uint16
	xs []float32
	fs []FaceID
}

func makeRuns(prefix string, n int) []strRun {
	out := make([]strRun, n)
	for i := range out {
		s := fmt.Sprintf("%s%07d", prefix, i) // 12 bytes for a 5-byte prefix
		r := &out[i]
		r.s, r.b = s, []byte(s)
		r.g, r.xs, r.fs = make([]uint16, len(s)), make([]float32, len(s)), make([]FaceID, len(s))
		for j := range s {
			r.g[j], r.xs[j], r.fs[j] = uint16(s[j]), float32(j), 1
		}
	}
	return out
}

func (r *strRun) insert(c *runCache) RunRef {
	return c.insert(1, r.b, r.g, r.xs, r.fs, float32(len(r.b)))
}

// allocFree runs setup (unmeasured) and then the function it returns, measured,
// up to five times and passes if one window allocated nothing. The runtime can
// allocate on its own (timers, background workers); an allocation in the code
// under test shows in every window.
func allocFree(t *testing.T, what string, setup func() func()) {
	t.Helper()
	var m, b uint64
	for attempt := 0; attempt < 5; attempt++ {
		run := setup()
		if m, b = allocs(run); m == 0 && b == 0 {
			return
		}
	}
	t.Fatalf("%s allocated in all 5 windows (last: %d objects / %d bytes)", what, m, b)
}

// allocs runs f and returns the exact heap allocation deltas.
func allocs(f func()) (mallocs, bytes uint64) {
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	f()
	runtime.ReadMemStats(&m1)
	return m1.Mallocs - m0.Mallocs, m1.TotalAlloc - m0.TotalAlloc
}

// After reserve, filling the cache up to the reserved counts allocates
// nothing; without it the same fill grows slabs and the table.
func TestRunCacheReserveFillIsAllocFree(t *testing.T) {
	const n = 3000
	runs := makeRuns("name_", n)
	glyphs, textBytes := 0, 0
	for i := range runs {
		glyphs += len(runs[i].g)
		textBytes += len(runs[i].b)
	}

	cold := newRunCache()
	if m, _ := allocs(func() {
		for i := range runs {
			runs[i].insert(cold)
		}
	}); m == 0 {
		t.Fatal("the unreserved fill allocated nothing: the test measures nothing")
	}

	var c *runCache
	allocFree(t, fmt.Sprintf("filling %d reserved runs", n), func() func() {
		c = newRunCache()
		c.reserve(n, glyphs, textBytes)
		return func() {
			for i := range runs {
				runs[i].insert(c)
			}
		}
	})
	if c.gen != 0 {
		t.Fatalf("the reserved fill evicted (generation %d)", c.gen)
	}
	for i := range runs {
		checkStrRun(t, c, &runs[i])
	}
}

func checkStrRun(t *testing.T, c *runCache, r *strRun) {
	t.Helper()
	ref, ok := c.lookup(1, r.b)
	if !ok {
		t.Fatalf("miss for %q", r.s)
	}
	v := c.view(ref)
	if len(v.Glyphs) != len(r.s) || v.Glyphs[0] != uint16(r.s[0]) || v.X[len(r.s)-1] != float32(len(r.s)-1) {
		t.Fatalf("%q: view %+v", r.s, v)
	}
}

// Eviction (both the compaction that keeps referenced runs and the
// drop-everything path) and the rehash it ends with allocate nothing.
func TestRunCacheEvictionIsAllocFree(t *testing.T) {
	const n = 5000
	runs := makeRuns("scan_", n)
	hot := makeRuns("hot__", 20)
	var c *runCache
	gen := uint32(0)
	allocFree(t, fmt.Sprintf("inserting %d runs through evictions", n), func() func() {
		c = newRunCache()
		c.maxRuns, c.maxGlyphs, c.maxText = 200, 200*12, 200*12
		c.reserve(200, 200*12, 200*12)
		for i := range hot {
			hot[i].insert(c)
		}
		gen = c.gen
		return func() {
			for i := range runs {
				runs[i].insert(c)
				// Referenced runs survive a compaction: touch the hot set.
				for j := range hot {
					if _, ok := c.lookup(1, hot[j].b); !ok {
						hot[j].insert(c)
					}
				}
			}
		}
	})
	if c.gen-gen < 10 {
		t.Fatalf("only %d evictions happened", c.gen-gen)
	}
	for i := range hot {
		checkStrRun(t, c, &hot[i])
	}

	// Drop-all path: every resident run referenced and more than half full.
	more := makeRuns("more_", 500)
	var c2 *runCache
	allocFree(t, "drop-all evictions", func() func() {
		c2 = newRunCache()
		c2.maxRuns = 50
		c2.reserve(50, 50*12, 50*12)
		return func() {
			for i := range more {
				more[i].insert(c2)
			}
		}
	})
	if c2.gen == 0 {
		t.Fatal("the drop-all path was not exercised")
	}
}

func TestRunCacheReserveClampsAndKeepsEntries(t *testing.T) {
	c := newRunCache()
	runs := makeRuns("keep_", 100)
	for i := range runs {
		runs[i].insert(c)
	}
	c.maxRuns, c.maxGlyphs, c.maxText = 300, 3000, 4000
	c.reserve(1<<30, 1<<30, 1<<30)
	if cap(c.ents) != 300 || cap(c.glyph) != 3000 || cap(c.xs) != 3000 || cap(c.faces) != 3000 || cap(c.text) != 4000 {
		t.Fatalf("capacities %d %d %d %d %d, want the limits 300 3000 3000 3000 4000", cap(c.ents), cap(c.glyph), cap(c.xs), cap(c.faces), cap(c.text))
	}
	if len(c.table) < 2*300 || len(c.table)&(len(c.table)-1) != 0 {
		t.Fatalf("table size %d: want a power of two >= %d", len(c.table), 2*300)
	}
	for i := range runs {
		checkStrRun(t, c, &runs[i])
	}
	c.reserve(-5, 0, 0) // negative and shrinking requests do nothing
	for i := range runs {
		checkStrRun(t, c, &runs[i])
	}
}
