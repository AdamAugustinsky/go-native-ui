package ui

import "testing"

func tipBox(c *Context, x, y, w, h float32, name, tip string) ID {
	c.NextSize(Px(w), Px(h))
	c.NextPos(x, y)
	id := c.Box(Hoverable|FloatingX|FloatingY, name)
	c.NativeTooltip(id, tip)
	return id
}

func TestTooltipRegions(t *testing.T) {
	h := newHarness(t, 400, 300)
	var a, clipped, edge, noText ID
	h.frame(func(c *Context) {
		a = tipBox(c, 10, 10, 100, 40, "a", "First")
		noText = tipBox(c, 150, 10, 30, 30, "none", "") // no text: not a region
		c.NextSize(Px(40), Px(40))
		c.NextPos(300, 200)
		cl := c.Box(Clip|FloatingX|FloatingY, "clip")
		c.Push(cl)
		clipped = tipBox(c, 20, 0, 100, 40, "in-clip", "Clipped") // 20 px of 100 show
		tipBox(c, 60, 0, 10, 10, "out-of-clip", "Hidden")         // entirely outside the clip: dropped
		c.Pop()
		edge = tipBox(c, 380, 280, 100, 100, "edge", "Edge") // clipped by the window
	})
	got := h.c.TooltipRegions(nil)
	want := []TooltipRegion{
		{[4]float32{10, 10, 110, 50}, uint64(h.c.KeyOf(a))},
		{[4]float32{320, 200, 340, 240}, uint64(h.c.KeyOf(clipped))},
		{[4]float32{380, 280, 400, 300}, uint64(h.c.KeyOf(edge))},
	}
	if len(got) != len(want) {
		t.Fatalf("%d regions %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("region %d: %+v, want %+v", i, got[i], want[i])
		}
	}
	_ = noText
	// The text is asked for by key, on demand.
	for _, w := range []struct {
		id   ID
		text string
	}{{a, "First"}, {clipped, "Clipped"}, {edge, "Edge"}} {
		if s := string(h.c.TooltipText(uint64(h.c.KeyOf(w.id)), nil)); s != w.text {
			t.Errorf("text of %q: %q", w.text, s)
		}
	}
	if s := h.c.TooltipText(12345, []byte("x")); string(s) != "x" {
		t.Errorf("an unknown key changed dst: %q", s)
	}
	// A frame that declares nothing has no regions and no texts.
	h.frame(func(c *Context) { c.Box(0, "plain") })
	if r := h.c.TooltipRegions(nil); len(r) != 0 {
		t.Errorf("regions after an empty frame: %+v", r)
	}
	if s := h.c.TooltipText(uint64(h.c.KeyOf(a)), nil); len(s) != 0 {
		t.Errorf("text after an empty frame: %q", s)
	}
}

// A modal overlay makes the boxes under it inert, and their tooltips with
// them; a tooltip inside the modal stays.
func TestTooltipRegionsModal(t *testing.T) {
	h := newHarness(t, 400, 300)
	var under, inside ID
	build := func(modal bool) func(c *Context) {
		return func(c *Context) {
			under = tipBox(c, 10, 10, 100, 40, "under", "Under")
			if modal {
				c.NextSize(Px(200), Px(100))
				c.BeginOverlay(KeyN(0, 7), OverlayModal, [4]float32{150, 100, 350, 200}, PlaceBelow)
				c.NextSize(Px(80), Px(30))
				inside = c.Box(Hoverable, "inside")
				c.NativeTooltip(inside, "Inside")
				c.EndOverlay()
			}
		}
	}
	h.frame(build(false))
	if r := h.c.TooltipRegions(nil); len(r) != 1 || r[0].Key != uint64(h.c.KeyOf(under)) {
		t.Fatalf("without the modal: %+v", r)
	}
	h.frame(build(true))
	r := h.c.TooltipRegions(nil)
	if len(r) != 1 || r[0].Key != uint64(h.c.KeyOf(inside)) {
		t.Errorf("with the modal: %+v, want only the tooltip inside it", r)
	}
}

func TestNativeTooltipsFlag(t *testing.T) {
	h := newHarness(t, 100, 100)
	if h.c.NativeTooltips() {
		t.Error("native tooltips are on by default")
	}
	h.c.SetNativeTooltips(true)
	if !h.c.NativeTooltips() {
		t.Error("SetNativeTooltips(true)")
	}
}

// Declaring and reading tooltips allocates nothing in a steady frame.
func TestTooltipRegionsAllocs(t *testing.T) {
	h := newHarness(t, 400, 300)
	build := func(c *Context) {
		for i := range 20 {
			tipBox(c, float32(i*15), 10, 12, 12, "t"+string(rune('a'+i)), "Tooltip text of the box")
		}
	}
	var dst []TooltipRegion
	var buf []byte
	h.frame(build)
	dst = h.c.TooltipRegions(dst)
	buf = h.c.TooltipText(dst[0].Key, buf[:0])
	if n := testing.AllocsPerRun(100, func() {
		h.frame(build)
		dst = h.c.TooltipRegions(dst)
		buf = h.c.TooltipText(dst[3].Key, buf[:0])
	}); n != 0 {
		t.Errorf("%v allocations per frame with native tooltips", n)
	}
	if len(dst) != 20 {
		t.Errorf("%d regions, want 20", len(dst))
	}
}
