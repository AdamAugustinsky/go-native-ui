package gpu

import (
	"runtime"
	"testing"
)

func TestBackdropHighWaterAfterWarmup(t *testing.T) {
	for _, warm := range []int{0, 1} {
		list := DrawList{Quads: make([]Quad, 0, 8)}
		frame := func(n int) {
			list.Reset()
			for i := range n {
				list.AddBackdrop(Quad{}, Backdrop{Sigma: float32(i + 1)})
			}
		}
		frame(warm)
		// Measure the first new high-water marks, not AllocsPerRun's warmed
		// repetitions: those would hide a one-off slice growth.
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		for _, n := range [...]int{1, 2, 0, 4, 8, 0, 8} {
			frame(n)
		}
		runtime.ReadMemStats(&after)
		if objects, bytes := after.Mallocs-before.Mallocs, after.TotalAlloc-before.TotalAlloc; objects != 0 || bytes != 0 {
			t.Errorf("warm %d: new backdrop high-water marks allocated %d objects / %d bytes", warm, objects, bytes)
		}
		if len(list.Backdrops) != 8 || len(list.Quads) != 8 {
			t.Fatalf("frame has %d backdrops / %d quads, want 8 each", len(list.Backdrops), len(list.Quads))
		}
		for i, b := range list.Backdrops {
			if q := list.Quads[i]; b.Sigma != float32(i+1) || q.Mode != ModeBackdrop || q.Tex != TextureID(i) {
				t.Fatalf("backdrop %d: recipe %+v, quad %+v", i, b, q)
			}
		}
		list.Reset()
		if len(list.Backdrops) != 0 || len(list.Quads) != 0 {
			t.Fatal("Reset retained frame contents")
		}
	}
}
