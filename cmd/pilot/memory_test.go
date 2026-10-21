package main

import (
	"runtime"
	"testing"
)

func TestMemoryViewExcludesBenchmarkPrewarm(t *testing.T) {
	live := func(prewarm bool) int64 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		pl := memoryPilot(100_000, prewarm)
		runtime.GC()
		runtime.ReadMemStats(&after)
		pl.Close()
		runtime.KeepAlive(pl)
		return int64(after.HeapAlloc) - int64(before.HeapAlloc)
	}
	view, fixture := live(false), live(true)
	// The ten-listing fixture must remain distinct, not merely relabelled.
	if fixture-view < 20<<20 {
		t.Fatalf("view=%d fixture=%d: benchmark prewarm was not separated", view, fixture)
	}
}
