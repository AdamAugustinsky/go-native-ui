package ui

import (
	"runtime"
	"testing"

	"gonativeui/gpu"
)

func TestDrawImage(t *testing.T) {
	h := newHarness(t, 200, 100)
	uv := [4]float32{10, 20, 25, 31} // 15×11 texels
	h.frame(func(c *Context) {
		c.NextSize(Px(40), Px(30))
		c.NextPos(10, 10)
		c.NextTextColor(0x11223344)
		c.NextImage(7, uv, gpu.ModeMaskR8)
		c.Box(DrawImage|FloatingX|FloatingY, "mask")
		c.NextSize(Px(40), Px(30))
		c.NextPos(60, 10)
		c.NextPad(2)
		c.NextTextColor(0x112233FF)
		c.NextOpacity(0.5)
		c.NextImage(8, uv, gpu.ModeRGBA)
		c.Box(DrawImage|FloatingX|FloatingY, "rgba")
		c.NextSize(Px(40), Px(30))
		c.NextPos(110, 10)
		c.Box(DrawImage|FloatingX|FloatingY, "noimage") // no image set: nothing to draw
	})
	qs := quadsOf(h)
	if len(qs) != 2 {
		t.Fatalf("%d quads, want 2", len(qs))
	}
	// centred: (10+20)-7.5 = 22.5 → 23 (snap rounds half up), (10+15)-5.5 = 19.5 → 20
	m := qs[0]
	if m.Mode != gpu.ModeMaskR8 || m.Tex != 7 || m.UV != uv || m.Rect != [4]float32{23, 20, 38, 31} || m.Color[0] != 0x11223344 {
		t.Errorf("mask image: %+v", m)
	}
	r := qs[1]
	if r.Mode != gpu.ModeRGBA || r.Tex != 8 || r.Color[2] != 0xFFFFFF80 {
		t.Errorf("rgba image: %+v", r)
	}
}

func TestBorderSides(t *testing.T) {
	ring := func(sides Sides, flags Flags) []gpu.Quad {
		h := newHarness(t, 300, 200)
		h.frame(func(c *Context) {
			c.NextSize(Px(100), Px(60))
			c.NextPos(20, 30)
			c.NextRadius(10)
			c.NextBorder(0xFF0000FF, 2)
			c.NextBorderSides(sides)
			c.NextBG(0x00FF00FF)
			c.Box(DrawBackground|DrawBorder|FloatingX|FloatingY|flags, "b")
		})
		return quadsOf(h)
	}
	box := [4]float32{20, 30, 120, 90}

	if qs := ring(SidesAll, 0); len(qs) != 1 || qs[0].Border != 2 || qs[0].Color[0] != 0x00FF00FF {
		t.Errorf("all sides must stay one quad with fill and border: %+v", qs)
	}

	// Left and right: a fill quad and two outline strips holding the arcs of the corners.
	qs := ring(SideLeft|SideRight, 0)
	if len(qs) != 3 {
		t.Fatalf("left|right: %d quads", len(qs))
	}
	if qs[0].Border != 0 || qs[0].Color[0] != 0x00FF00FF {
		t.Errorf("fill: %+v", qs[0])
	}
	for i, wantClip := range [][4]float32{{19, 29, 30, 91}, {110, 29, 121, 91}} {
		q := qs[1+i]
		if q.Border != 2 || q.BorderColor != 0xFF0000FF || q.Color != [4]uint32{} || q.Rect != box || q.Clip != wantClip {
			t.Errorf("strip %d: %+v", i, q)
		}
	}

	// Top and bottom only: strips span the whole width, the outline is one quad each.
	qs = ring(SideTop|SideBottom, 0)
	if len(qs) != 3 || qs[1].Clip != [4]float32{19, 29, 121, 40} || qs[2].Clip != [4]float32{19, 80, 121, 91} {
		t.Errorf("top|bottom: %+v", qs)
	}

	// Left and top: the top strip starts where the left strip ends, so no pixel is drawn twice.
	qs = ring(SideLeft|SideTop, 0)
	if len(qs) != 3 || qs[1].Clip != [4]float32{19, 29, 30, 91} || qs[2].Clip != [4]float32{30, 29, 121, 40} {
		t.Errorf("left|top: %+v", qs)
	}

	// No sides: only the fill. Strips are cut by the clip of the parent.
	if qs = ring(0, 0); len(qs) != 1 {
		t.Errorf("no sides: %+v", qs)
	}
	h := newHarness(t, 300, 200)
	h.frame(func(c *Context) {
		c.NextSize(Px(200), Px(200))
		c.NextPos(0, 0)
		cl := c.Box(Clip|FloatingX|FloatingY, "clip")
		c.Push(cl)
		c.NextSize(Px(100), Px(60))
		c.NextPos(50, 30)
		c.NextRadius(10)
		c.NextBorder(0xFF0000FF, 2)
		c.NextBorderSides(SideLeft | SideRight)
		c.Box(DrawBorder|FloatingX|FloatingY, "b")
		c.Pop()
	})
	// parent clip is (0,0,200,200); the right strip [140,..,151] lies inside, the left strip too
	if qs = quadsOf(h); len(qs) != 2 || qs[0].Clip != [4]float32{49, 29, 60, 91} {
		t.Errorf("clipped: %+v", qs)
	}
}

func TestPeekSignalAndTOf(t *testing.T) {
	h := newHarness(t, 400, 300)
	key := HashString(0, "peek")
	build := func(c *Context) {
		c.NextSize(Px(100), Px(50))
		c.NextPos(10, 10)
		c.BoxKeyed(Clickable|HotAnimation|ActiveAnimation|FocusAnimation|FloatingX|FloatingY, key, nil)
	}
	h.frame(func(c *Context) {
		if s := c.PeekSignal(key); s != (Signal{}) {
			t.Errorf("unbuilt box: %+v", s)
		}
		build(c)
	})
	h.move(50, 30)
	h.frame(func(c *Context) {
		if s := c.PeekSignal(key); !s.Hovering {
			t.Errorf("hover not visible before the box is built: %+v", s)
		}
		build(c)
	})
	h.in.SetButton(ButtonLeft, true, 1)
	h.frame(func(c *Context) {
		if s := c.PeekSignal(key); !s.Pressed {
			t.Errorf("press not visible before the box is built: %+v", s)
		}
		build(c)
	})
	h.in.SetButton(ButtonLeft, false, 0)
	h.frame(func(c *Context) {
		if s := c.PeekSignal(key); !s.Clicked {
			t.Errorf("click not visible before the box is built: %+v", s)
		}
		if c.HotTOf(key) <= 0 || c.HotTOf(key) > 1 || c.ActiveTOf(key) <= 0 {
			t.Errorf("hot_t %v active_t %v", c.HotTOf(key), c.ActiveTOf(key))
		}
		build(c)
	})
	for i := 0; i < 120; i++ {
		h.frame(build)
	}
	h.frame(func(c *Context) {
		if c.HotTOf(key) != 1 || c.ActiveTOf(key) != 0 || c.FocusTOf(key) != 0 || c.HotTOf(HashString(0, "nope")) != 0 {
			t.Errorf("settled: hot %v active %v", c.HotTOf(key), c.ActiveTOf(key))
		}
		build(c)
	})
}

// Images and partial borders add no allocations to a frame.
func TestExtrasZeroAlloc(t *testing.T) {
	h := newHarness(t, 300, 200)
	build := func(c *Context) {
		c.NextSize(Px(100), Px(60))
		c.NextPos(20, 30)
		c.NextRadius(10)
		c.NextBorder(0xFF0000FF, 2)
		c.NextBorderSides(SideLeft | SideRight)
		c.NextImage(3, [4]float32{0, 0, 8, 8}, gpu.ModeMaskR8)
		c.Box(DrawBackground|DrawBorder|DrawImage|FloatingX|FloatingY, "b")
	}
	for i := 0; i < 50; i++ {
		h.frame(build)
	}
	runtime.GC()
	if n := testing.AllocsPerRun(100, func() { h.frame(build) }); n != 0 {
		t.Errorf("%v allocs per frame", n)
	}
}
