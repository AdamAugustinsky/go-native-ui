package platform

import (
	"math/rand"
	"testing"
	"unsafe"
)

func TestCursorMapAt(t *testing.T) {
	var m cursorMap
	a := CursorRegion{Rect: [4]float32{0, 0, 100, 100}, Cursor: CursorPointer}
	b := CursorRegion{Rect: [4]float32{50, 50, 150, 150}, Cursor: CursorResizeEW}
	if !m.setRegions([]CursorRegion{a, b}) {
		t.Fatal("first list must report a change")
	}
	for _, c := range []struct {
		x, y float32
		want Cursor
	}{
		{10, 10, CursorPointer},
		{60, 60, CursorResizeEW}, // overlap: the later one is on top
		{120, 120, CursorResizeEW},
		{100, 10, CursorArrow}, // the right edge is outside
		{200, 200, CursorArrow},
		{-1, 5, CursorArrow},
	} {
		if got := m.at(c.x, c.y); got != c.want {
			t.Errorf("at(%v,%v) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	if !m.setOverride(CursorResizeNS) {
		t.Fatal("override change not reported")
	}
	if got := m.at(10, 10); got != CursorResizeNS {
		t.Errorf("override: %v", got)
	}
	if got := m.at(500, 500); got != CursorResizeNS {
		t.Errorf("override outside every region: %v", got)
	}
	if m.setOverride(CursorResizeNS) {
		t.Error("same override reported as a change")
	}
	m.setOverride(CursorArrow)
	if got := m.at(10, 10); got != CursorPointer {
		t.Errorf("override removed: %v", got)
	}
	if m.setOverride(Cursor(200)) || m.override != CursorArrow {
		t.Error("an invalid override must count as none")
	}
}

func TestRegionsDiffAndNoAlloc(t *testing.T) {
	var m cursorMap
	var d dragMap
	list := []CursorRegion{{Rect: [4]float32{0, 0, 10, 10}, Cursor: CursorPointer}, {Rect: [4]float32{5, 5, 20, 20}, Cursor: CursorText}}
	drags := []DragRegion{{Rect: [4]float32{0, 0, 100, 40}, Block: false}, {Rect: [4]float32{10, 5, 50, 35}, Block: true}}
	m.setRegions(list)
	d.set(drags)
	same := append([]CursorRegion(nil), list...) // a different slice with equal contents
	sameD := append([]DragRegion(nil), drags...)
	if n := testing.AllocsPerRun(100, func() {
		if m.setRegions(same) || d.set(sameD) {
			t.Fatal("an equal list reported as a change")
		}
		_ = m.at(7, 7)
		_ = d.at(7, 7)
	}); n != 0 {
		t.Errorf("%v allocs for an unchanged list", n)
	}
	// A changed list is copied: later edits of the caller's slice do not leak in.
	same[0].Cursor = CursorResizeNS
	if !m.setRegions(same) {
		t.Fatal("changed list not reported")
	}
	same[0].Cursor = CursorArrow
	if m.regs[0].Cursor != CursorResizeNS {
		t.Error("the list is not copied")
	}
	// Once warm, even changing lists reuse the storage.
	flip := [2][]CursorRegion{list, same}
	i := 0
	if n := testing.AllocsPerRun(100, func() { i ^= 1; m.setRegions(flip[i]) }); n != 0 {
		t.Errorf("%v allocs for changing lists", n)
	}
}

func TestDragMapAt(t *testing.T) {
	var d dragMap
	// A titlebar with a tab and a button above it, and a second block that
	// itself has a drag area drawn over it (a label that should drag again).
	d.set([]DragRegion{
		{Rect: [4]float32{0, 0, 400, 46}, Block: false},
		{Rect: [4]float32{100, 8, 200, 38}, Block: true},
		{Rect: [4]float32{300, 8, 330, 38}, Block: true},
		{Rect: [4]float32{110, 12, 130, 34}, Block: false},
	})
	for _, c := range []struct {
		x, y float32
		want bool
	}{
		{10, 10, true},
		{150, 20, false}, // on the tab
		{315, 20, false}, // on the button
		{120, 20, true},  // the drag area drawn over the tab
		{399, 45, true},
		{400, 20, false}, // outside
		{10, 46, false},  // below the band
	} {
		if got := d.at(c.x, c.y); got != c.want {
			t.Errorf("at(%v,%v) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
	if (&dragMap{}).at(1, 1) {
		t.Error("an empty map must not drag")
	}
}

// disjoint fails the test if two of the pieces overlap.
func disjoint(t *testing.T, rs []CursorRegion) {
	t.Helper()
	for i := range rs {
		for j := i + 1; j < len(rs); j++ {
			if overlaps(rs[i].Rect, rs[j].Rect) {
				t.Fatalf("pieces %d %v and %d %v overlap", i, rs[i], j, rs[j])
			}
		}
	}
}

func TestFlattenMatchesAt(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 300; iter++ {
		var m cursorMap
		n := 1 + rng.Intn(8)
		regs := make([]CursorRegion, n)
		for i := range regs {
			x0, y0 := float32(rng.Intn(40)), float32(rng.Intn(40))
			regs[i] = CursorRegion{Rect: [4]float32{x0, y0, x0 + 1 + float32(rng.Intn(30)), y0 + 1 + float32(rng.Intn(30))}, Cursor: Cursor(1 + rng.Intn(4))}
		}
		m.setRegions(regs)
		flat := m.flatten()
		disjoint(t, flat)
		// Every point has the same cursor under the topmost-wins rule and in the flat list.
		var f cursorMap
		f.regs = flat
		for y := float32(0.5); y < 75; y += 1 {
			for x := float32(0.5); x < 75; x += 1 {
				if got, want := f.at(x, y), m.at(x, y); got != want {
					t.Fatalf("iter %d at (%v,%v): flat %v, want %v\nregions %v\nflat %v", iter, x, y, got, want, regs, flat)
				}
			}
		}
	}
}

func TestFlattenExample(t *testing.T) {
	var m cursorMap
	// A divider across a pane header: the divider is later, so it cuts the header.
	m.setRegions([]CursorRegion{
		{Rect: [4]float32{0, 0, 100, 20}, Cursor: CursorPointer},
		{Rect: [4]float32{40, 0, 48, 100}, Cursor: CursorResizeEW},
	})
	flat := m.flatten()
	if len(flat) != 3 {
		t.Fatalf("%v", flat)
	}
	want := map[CursorRegion]bool{
		{Rect: [4]float32{0, 0, 40, 20}, Cursor: CursorPointer}:    true,
		{Rect: [4]float32{48, 0, 100, 20}, Cursor: CursorPointer}:  true,
		{Rect: [4]float32{40, 0, 48, 100}, Cursor: CursorResizeEW}: true,
	}
	for _, r := range flat {
		if !want[r] {
			t.Errorf("unexpected piece %v in %v", r, flat)
		}
	}
	// No overlap in the input: the list is unchanged.
	m.setRegions([]CursorRegion{{Rect: [4]float32{0, 0, 10, 10}, Cursor: CursorText}, {Rect: [4]float32{20, 0, 30, 10}, Cursor: CursorText}})
	if got := m.flatten(); len(got) != 2 || got[0] != m.regs[0] || got[1] != m.regs[1] {
		t.Errorf("%v", got)
	}
}

// WM_SETCURSOR arrives before the WM_MOUSEMOVE of the same movement: crossing
// a divider, the last position the window saw is still in the old region.
func TestPointerShapeUsesTheCurrentPosition(t *testing.T) {
	var m cursorMap
	header := CursorRegion{Rect: [4]float32{0, 0, 100, 20}, Cursor: CursorPointer}
	divider := CursorRegion{Rect: [4]float32{40, 0, 48, 100}, Cursor: CursorResizeEW}
	m.setRegions([]CursorRegion{header, divider})
	// The last move was inside the header (and outside the divider), the
	// pointer is now over the divider.
	if got := m.pointerShape(44, 50, true, 10, 10); got != CursorResizeEW {
		t.Errorf("over the divider: %v, want the resize cursor (the last known position gives %v)", got, m.at(10, 10))
	}
	// And leaving it again.
	if got := m.pointerShape(80, 50, true, 44, 50); got != CursorArrow {
		t.Errorf("past the divider: %v, want the arrow", got)
	}
	// Without a current position (the OS could not say), the last one is used.
	if got := m.pointerShape(0, 0, false, 44, 50); got != CursorResizeEW {
		t.Errorf("fallback: %v", got)
	}
	// The override still wins.
	m.setOverride(CursorText)
	if got := m.pointerShape(80, 50, true, 10, 10); got != CursorText {
		t.Errorf("override: %v", got)
	}
}

// Past the piece budget, no piece overlaps another or a hosted view.
func TestFlattenOverBudgetKeepsHoles(t *testing.T) {
	var m cursorMap
	regs := []CursorRegion{{Rect: [4]float32{0, 0, 1000, 1000}, Cursor: CursorPointer}}
	for i := 0; i < 400; i++ {
		x, y := float32(i%20*50+10), float32(i/20*50+10)
		regs = append(regs, CursorRegion{Rect: [4]float32{x, y, x + 4, y + 4}, Cursor: CursorText})
	}
	m.setRegions(regs)
	hole := [4]float32{400, 400, 600, 600}
	m.setHoles([][4]float32{hole})
	flat := m.flatten()
	disjoint(t, flat)
	if len(flat) < maxFlat {
		t.Fatalf("%d pieces: the budget was not reached", len(flat))
	}
	for _, r := range flat {
		if overlaps(r.Rect, hole) {
			t.Fatalf("piece %v over the hole %v", r, hole)
		}
	}
}

// A region list that grows a little from one frame to the next does not
// allocate each time: the storage it is copied into keeps room to grow
// (vlistbench's gate caught the cursor list growing by a few entries in a
// measured window).
func TestRegionListsGrowWithRoom(t *testing.T) {
	const n = 100
	curs := make([]CursorRegion, n+n/2)
	holes := make([][4]float32, n+n/2)
	drags := make([]DragRegion, n+n/2)
	tips := make([]TooltipRegion, n+n/2)
	for i := range curs {
		curs[i].Rect = [4]float32{float32(i), 0, float32(i + 1), 1}
		holes[i], drags[i].Rect, tips[i].Rect = curs[i].Rect, curs[i].Rect, curs[i].Rect
	}
	var m cursorMap
	var d dragMap
	var tm tooltipMap
	m.setRegions(curs[:n])
	m.setHoles(holes[:n])
	d.set(drags[:n])
	tm.set(tips[:n])
	// The storage each keeps (not the process's malloc count, which other
	// goroutines move: on Linux the appearance follower talks to the bus).
	type store struct{ c, h, d, t unsafe.Pointer }
	at := func() store {
		return store{unsafe.Pointer(unsafe.SliceData(m.regs)), unsafe.Pointer(unsafe.SliceData(m.holes)),
			unsafe.Pointer(unsafe.SliceData(d.regs)), unsafe.Pointer(unsafe.SliceData(tm.regs))}
	}
	before := at()
	m.setRegions(curs)
	m.setHoles(holes)
	d.set(drags)
	tm.set(tips)
	if after := at(); after != before {
		t.Errorf("lists grown from %d to %d entries got new storage: %v, then %v", n, n+n/2, before, after)
	}
}

func TestFlattenMergesTouchingCursors(t *testing.T) {
	var m cursorMap
	for i := 0; i < 1024; i++ {
		m.regs = append(m.regs, CursorRegion{Rect: [4]float32{0, float32(i), 10, float32(i + 1)}, Cursor: CursorText})
	}
	flat := m.flatten()
	if len(flat) != 1 || flat[0].Rect != [4]float32{0, 0, 10, 1024} {
		t.Fatalf("touching pieces: %v", flat)
	}
	if got := testing.AllocsPerRun(20, func() { m.flatten() }); got != 0 {
		t.Fatalf("warm flatten: %v allocations", got)
	}
}
