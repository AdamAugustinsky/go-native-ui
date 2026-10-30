package gpu

import (
	"math"
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

// A capsule's coverage is the distance to its segment, at any angle, with
// round caps even when the segment has zero length.
func TestCapsuleCoverage(t *testing.T) {
	r := newTestRenderer(t)
	for _, end := range [][2]float32{{20, 12}, {12, 20}, {20, 20}, {12, 12}, {4, 20}} {
		x0, y0, x1, y1, radius := float32(12), float32(12), end[0], end[1], float32(3)
		x, y := min(x0, x1)-radius, min(y0, y1)-radius
		q := Quad{Mode: ModeCapsule, Rect: [4]float32{x, y, max(x0, x1) + radius, max(y0, y1) + radius},
			UV: [4]float32{x0 - x, y0 - y, x1 - x, y1 - y}, Radii: [4]float32{radius}, Color: [4]uint32{0xFFFFFF80, 0xFFFFFF80, 0xFFFFFF80, 0xFFFFFF80},
			Clip: [4]float32{5, 5, 22, 22}, FadeX: [2]float32{18, 24}}
		m := render(r, 30, 30, transparent, q)
		for py := 0; py < 30; py++ {
			for px := 0; px < 30; px++ {
				ax, ay := float64(float32(px)+.5-x0), float64(float32(py)+.5-y0)
				dx, dy := float64(x1-x0), float64(y1-y0)
				along := min(max((ax*dx+ay*dy)/max(dx*dx+dy*dy, 1e-6), 0), 1)
				cov := min(max(.5-(math.Hypot(ax-dx*along, ay-dy*along)-float64(radius)), 0), 1)
				if px < 5 || py < 5 || px >= 22 || py >= 22 {
					cov = 0
				}
				cov *= min(max((24-(float64(px)+.5))/6, 0), 1)
				want := int(math.Round(cov * 128))
				if a := m.at(px, py)[3]; a < want-1 || a > want+1 {
					t.Fatalf("end %v at %d,%d: alpha %d, want %d", end, px, py, a, want)
				}
			}
		}
	}
}
