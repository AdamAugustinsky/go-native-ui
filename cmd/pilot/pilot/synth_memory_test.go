package pilot

import (
	"bytes"
	"runtime"
	"slices"
	"testing"

	"gonativeui/core"
	"gonativeui/fs"
)

func TestFillSynthAppends(t *testing.T) {
	for _, n := range []int{-1, 0, 1, 997, 1000} {
		for _, variant := range []int{0, 1, 2} {
			l, tail := new(fs.Listing), new(fs.Listing)
			FillSynth(l, 3, 0)
			names := slices.Clone(l.Names)
			entries := slices.Clone(l.Entries)
			FillSynth(tail, n, variant)
			for i := range tail.Entries {
				tail.Entries[i].NameOff += uint32(len(names))
			}
			FillSynth(l, n, variant)
			if !bytes.Equal(l.Names, append(names, tail.Names...)) || !slices.Equal(l.Entries, append(entries, tail.Entries...)) {
				t.Fatalf("append changed existing data: n=%d variant=%d", n, variant)
			}
			if a := testing.AllocsPerRun(5, func() { l.Reset(); FillSynth(l, n, variant) }); a != 0 {
				t.Fatalf("warm fill: n=%d variant=%d allocs=%v", n, variant, a)
			}
		}
	}
}

func TestFillSynthColdAllocationBudget(t *testing.T) {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	l := new(fs.Listing)
	FillSynth(l, 1_000_000, 0)
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(l)
	// Known-count entry storage should not repeatedly grow and copy. Leave
	// room for the name arena and runtime noise, but not the old 108 MiB waste.
	if used := after.TotalAlloc - before.TotalAlloc; used > 150<<20 {
		t.Fatalf("cold FillSynth allocated %.2f MiB, want <=150", float64(used)/(1<<20))
	}
}

// The pool learns the larger of two repeatedly used panes without retaining
// a one-off peak. Alternating 1M and 100k listings must still reuse all buffers.
func TestLargeTwoPaneServiceReuse(t *testing.T) {
	synth := new(SynthSource)
	p := core.NewParker()
	defer p.Close()
	s := fs.NewServiceWith(p.Unpark, synth.List)
	defer s.Close()
	paths := [2]string{synthDir(1_000_000), synthDir(100_000)}
	var held [2]fs.Result
	buf := make([]fs.Result, 0, 2)
	round := func() {
		for i, path := range paths {
			s.List(uint32(i), path, fs.SortName, false)
		}
		for got := 0; got < 2; {
			buf = s.Poll(buf[:0])
			for _, r := range buf {
				s.Release(held[r.ID].Listing, held[r.ID].Lease)
				held[r.ID] = r
				got++
			}
			if got < 2 {
				p.Park()
			}
		}
	}
	for range 8 {
		round()
	}
	// Attributed, as the other gates here: under a loaded test run a parked
	// worker can make the runtime start a thread, which is not ours (D27).
	if n, sites := oursDuring(func() {
		for range 5 {
			round()
		}
	}); n != 0 {
		t.Fatalf("warm large two-pane service: %d allocations of ours:\n%s", n, sites)
	}
	for _, r := range held {
		s.Release(r.Listing, r.Lease)
	}
}
