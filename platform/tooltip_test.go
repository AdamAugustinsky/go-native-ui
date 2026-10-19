package platform

import (
	"slices"
	"testing"
)

func TestTooltipMapDiff(t *testing.T) {
	var m tooltipMap
	a := []TooltipRegion{{[4]float32{0, 0, 10, 10}, 1}, {[4]float32{20, 0, 30, 10}, 2}}
	if !m.set(a) {
		t.Error("the first list is a change")
	}
	if m.set(slices.Clone(a)) {
		t.Error("an equal list is not a change")
	}
	b := slices.Clone(a)
	b[1].Key = 3 // same place, other tooltip
	if !m.set(b) {
		t.Error("a different key is a change")
	}
	if !m.set(nil) || m.set(nil) {
		t.Error("clearing is one change")
	}
	// The map copies: the caller may reuse its slice.
	c := slices.Clone(a)
	m.set(c)
	c[0].Key = 99
	if m.regs[0].Key != 1 {
		t.Error("the map aliases the caller's slice")
	}
	if n := testing.AllocsPerRun(100, func() { m.set(a); m.flatten() }); n != 0 {
		t.Errorf("%v allocations per steady call", n)
	}
	big := make([]TooltipRegion, MaxTooltipRegions+10)
	for i := range big {
		big[i] = TooltipRegion{[4]float32{float32(i), 0, float32(i) + 1, 1}, uint64(i + 1)}
	}
	m.set(big)
	if len(m.regs) != MaxTooltipRegions {
		t.Errorf("%d regions kept, want the first %d", len(m.regs), MaxTooltipRegions)
	}
}

// coverage is the key at each point of a grid, from the regions as given
// (the topmost wins) and from the flattened list (no two overlap, so the one
// containing the point is the only one).
func TestTooltipFlatten(t *testing.T) {
	regs := []TooltipRegion{
		{[4]float32{0, 0, 100, 100}, 1},
		{[4]float32{20, 20, 60, 60}, 2},  // inside 1, above it
		{[4]float32{50, 40, 140, 80}, 3}, // overlaps 1 and 2, above both
		{[4]float32{40, 30, 120, 70}, 0}, // no key: ignored, and it hides nothing beneath it
		{[4]float32{300, 0, 300, 10}, 4}, // empty: ignored
		{[4]float32{0, 90, 100, 110}, 5}, // overlaps the bottom of 1
		{[4]float32{10, 10, 20, 20}, 6},  // inside 1, above it
		{[4]float32{10, 10, 20, 20}, 7},  // exactly over 6: 6 is hidden
	}
	var m tooltipMap
	m.set(regs)
	flat := slices.Clone(m.flatten())
	top := func(x, y float32) uint64 {
		var k uint64
		for _, r := range regs {
			if r.Key != 0 && inRect(r.Rect, x, y) {
				k = r.Key
			}
		}
		return k
	}
	for y := float32(0.5); y < 120; y += 1 {
		for x := float32(0.5); x < 320; x += 1 {
			var hits []uint64
			for _, r := range flat {
				if inRect(r.Rect, x, y) {
					hits = append(hits, r.Key)
				}
			}
			if len(hits) > 1 {
				t.Fatalf("(%v, %v) is in %d pieces: %v", x, y, len(hits), hits)
			}
			var got uint64
			if len(hits) == 1 {
				got = hits[0]
			}
			if want := top(x, y); got != want {
				t.Fatalf("(%v, %v): key %d, want %d", x, y, got, want)
			}
		}
	}
	for _, r := range flat {
		if r.Key == 6 || r.Key == 0 || r.Key == 4 {
			t.Errorf("a hidden or ignored region survived: %+v", r)
		}
	}
}
