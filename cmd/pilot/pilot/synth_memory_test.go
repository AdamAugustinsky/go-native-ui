package pilot

import (
	"bytes"
	"runtime"
	"slices"
	"testing"

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
