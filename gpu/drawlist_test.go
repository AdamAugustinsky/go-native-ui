package gpu

import (
	"runtime"
	"testing"
)

func TestInstanceCapacityHysteresis(t *testing.T) {
	var d instanceDemand
	if n := d.capacity(0, 0); n != 0 {
		t.Fatal("clear-only frame allocated")
	}
	if n := d.capacity(0, 65); n != 128 {
		t.Fatalf("first demand class = %d, want 128", n)
	}
	for range instanceCalmFrames - 1 {
		if n := d.capacity(4096, 100); n != 4096 {
			t.Fatal("shrunk during a short lull")
		}
	}
	if n := d.capacity(4096, 2048); n != 4096 {
		t.Fatal("burst resized sufficient capacity")
	}
	for range instanceCalmFrames - 1 {
		if n := d.capacity(4096, 100); n != 4096 {
			t.Fatal("burst did not reset the calm period")
		}
	}
	if n := d.capacity(4096, 100); n != 256 {
		t.Fatalf("sustained demand class = %d, want 256", n)
	}
}

func TestTextureBatchesPreserveOrder(t *testing.T) {
	quads := make([]Quad, 217)
	for i := range quads {
		id := TextureID(100 + i%2)
		if i >= 200 {
			id = TextureID(200 + i)
		}
		quads[i] = Quad{Mode: ModeMaskR8, Tex: id, Rect: [4]float32{float32(i), 0, float32(i + 1), 1}}
	}
	quads[2].Mode = ModeShape // shapes keep Tex (shape flags), and join the run
	inst := make([]Quad, len(quads))
	copy(inst, quads)
	batches := textureBatches(nil, inst[:200])
	if len(batches) != 1 {
		t.Fatalf("200 alternating textures: %d batches, want 1", len(batches))
	}
	frame := func() {
		copy(inst, quads)
		batches = textureBatches(batches, inst)
	}
	frame()
	if len(batches) != 3 {
		t.Fatalf("table overflow: %d batches, want 3", len(batches))
	}
	next := 0
	for _, b := range batches {
		if b.first != next || b.count <= 0 || b.n > runTextures {
			t.Fatalf("invalid batch: %+v", b)
		}
		for i := b.first; i < b.first+b.count; i++ {
			q := inst[i]
			if q.Mode != ModeShape {
				if int(q.Tex) >= b.n {
					t.Fatal("texture slot outside the batch")
				}
				q.Tex = b.textures[q.Tex]
			}
			if q != quads[i] {
				t.Fatalf("quad %d changed after resolving its texture slot", i)
			}
		}
		next += b.count
	}
	if next != len(quads) {
		t.Fatal("batching lost quads")
	}
	if n := testing.AllocsPerRun(100, frame); n != 0 {
		t.Fatalf("warm batching: %g allocations", n)
	}
}

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
