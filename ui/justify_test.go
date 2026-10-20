package ui

import "testing"

// origin is box id's unsnapped position relative to box p on axis a.
func (h *harness) rel(id, p ID, a int) float32 {
	return h.c.boxes[id].origin[a] - h.c.boxes[p].origin[a]
}

func TestJustifyValues(t *testing.T) {
	// A 300 px row with padding 10 and 20: 270 px inside. Children 40, 50, 30
	// with gaps of 6 take 132, so 138 are free.
	for _, tc := range []struct {
		j    Justify
		want [3]float32
	}{
		{JustifyStart, [3]float32{10, 56, 112}},
		{JustifyCenter, [3]float32{79, 125, 181}},
		{JustifyEnd, [3]float32{148, 194, 250}},
		{JustifySpaceBetween, [3]float32{10, 125, 250}},
		{JustifySpaceEvenly, [3]float32{44.5, 125, 215.5}},
	} {
		for _, axis := range []Axis{AxisX, AxisY} {
			a := 0
			if axis == AxisY {
				a = 1
			}
			h := newHarness(t, 800, 600)
			var p ID
			var ch [3]ID
			h.frame(func(c *Context) {
				c.NextSize(Px(300), Px(300))
				c.NextAxis(axis)
				c.NextPadding(10, 10, 20, 20)
				c.NextGap(6)
				c.NextJustify(tc.j)
				p = c.Open(0, "p")
				for i, w := range [3]float32{40, 50, 30} {
					c.NextSize(Px(w), Px(w))
					ch[i] = c.Box(0, "c"+string(rune('0'+i)))
				}
				c.NextFlags(FloatingX | FloatingY) // floating children take no part
				c.NextPos(3, 3)
				c.NextSize(Px(5), Px(5))
				c.Box(0, "float")
				c.Close()
			})
			for i := range ch {
				if got := h.rel(ch[i], p, a); !near(got, tc.want[i]) {
					t.Errorf("justify %d axis %d: child %d at %v, want %v", tc.j, axis, i, got, tc.want[i])
				}
				if got := h.rel(ch[i], p, 1-a); got != 10 {
					t.Errorf("justify %d axis %d: child %d across at %v, want the start (10)", tc.j, axis, i, got)
				}
			}
		}
	}
}

func TestJustifyNoFreeSpace(t *testing.T) {
	h := newHarness(t, 800, 600)
	for _, j := range []Justify{JustifyCenter, JustifyEnd, JustifySpaceBetween, JustifySpaceEvenly} {
		var over, pct, half, auto [2]ID
		var ps [4]ID
		h.frame(func(c *Context) {
			// children wider than the row (strict sizes): placed from the start,
			// never pushed out of the leading edge
			c.NextSize(Px(100), Px(40))
			c.NextAxis(AxisX)
			c.NextJustify(j)
			ps[0] = c.Open(0, "over")
			c.NextSize(Px(80), Px(10))
			over[0] = c.Box(0, "a")
			c.NextSize(Px(80), Px(10))
			over[1] = c.Box(0, "b")
			c.Close()
			// a Pct(1) child takes the free space
			c.NextSize(Px(100), Px(40))
			c.NextAxis(AxisX)
			c.NextJustify(j)
			ps[1] = c.Open(0, "pct")
			c.NextSize(Px(20), Px(10))
			pct[0] = c.Box(0, "a")
			c.NextSize(Pct(1), Px(10))
			pct[1] = c.Box(0, "b")
			c.Close()
			// percentages below the whole leave the rest
			c.NextSize(Px(100), Px(40))
			c.NextAxis(AxisX)
			c.NextJustify(j)
			ps[2] = c.Open(0, "half")
			c.NextSize(Pct(0.5), Px(10))
			half[0] = c.Box(0, "a")
			c.Close()
			// a row sized by its children has no room to share
			c.NextAxis(AxisX)
			c.NextJustify(j)
			ps[3] = c.Open(0, "auto")
			c.NextSize(Px(20), Px(10))
			auto[0] = c.Box(0, "a")
			c.NextSize(Px(30), Px(10))
			auto[1] = c.Box(0, "b")
			c.Close()
		})
		if a, b := h.rel(over[0], ps[0], 0), h.rel(over[1], ps[0], 0); a != 0 || b != 80 {
			t.Errorf("justify %d, overflow: children at %v and %v, want 0 and 80", j, a, b)
		}
		if a, b := h.rel(pct[0], ps[1], 0), h.rel(pct[1], ps[1], 0); a != 0 || b != 20 {
			t.Errorf("justify %d, Pct(1): children at %v and %v, want 0 and 20", j, a, b)
		}
		want := map[Justify]float32{JustifyCenter: 25, JustifyEnd: 50, JustifySpaceBetween: 0, JustifySpaceEvenly: 25}[j]
		if got := h.rel(half[0], ps[2], 0); got != want {
			t.Errorf("justify %d, Pct(0.5) alone: at %v, want %v", j, got, want)
		}
		if a, b := h.rel(auto[0], ps[3], 0), h.rel(auto[1], ps[3], 0); a != 0 || b != 20 {
			t.Errorf("justify %d, sized by its children: children at %v and %v, want 0 and 20", j, a, b)
		}
	}
}

func TestJustifyNested(t *testing.T) {
	h := newHarness(t, 800, 600)
	var outer, inner, a, b ID
	h.frame(func(c *Context) {
		c.NextSize(Px(200), Px(100))
		c.NextAxis(AxisX)
		c.NextJustify(JustifyCenter)
		outer = c.Open(0, "outer")
		c.NextSize(Px(60), Px(100))
		c.NextAxis(AxisY)
		c.NextJustify(JustifyEnd)
		inner = c.Open(0, "inner")
		c.NextSize(Px(10), Px(20))
		a = c.Box(0, "a")
		c.Close()
		c.NextSize(Px(40), Px(10))
		b = c.Box(0, "b")
		c.Close()
	})
	// outer: 60 + 40 in 200, centred from 50; inner: 20 at the bottom of 100
	if x := h.rel(inner, outer, 0); x != 50 {
		t.Errorf("inner column at x %v, want 50", x)
	}
	if x := h.rel(b, outer, 0); x != 110 {
		t.Errorf("b at x %v, want 110", x)
	}
	if y := h.rel(a, inner, 1); y != 80 {
		t.Errorf("a at y %v in the inner column, want 80", y)
	}
}

func TestJustifyScrolling(t *testing.T) {
	h := newHarness(t, 800, 600)
	var sc, a ID
	build := func(wb float32) func(c *Context) {
		return func(c *Context) {
			c.NextSize(Px(200), Px(50))
			c.NextAxis(AxisX)
			c.NextJustify(JustifyEnd)
			sc = c.Open(ScrollX|Clip, "sc")
			c.NextSize(Px(40), Px(10))
			a = c.Box(0, "a")
			c.NextSize(Px(wb), Px(10))
			c.Box(0, "b")
			c.Close()
		}
	}
	// content smaller than the view: justified, nothing to scroll
	h.frame(build(60))
	if x := h.rel(a, sc, 0); x != 100 {
		t.Errorf("short content: a at %v, want 100", x)
	}
	// content larger than the view: from the start, and it scrolls
	h.frame(build(300))
	h.c.SetScroll(sc, 50, 0)
	h.frame(build(300))
	h.frame(build(300))
	if x := h.rel(a, sc, 0); x != -50 {
		t.Errorf("long content scrolled by 50: a at %v, want -50", x)
	}
}

func TestJustifyIgnoredByFlows(t *testing.T) {
	// A flow has its own alignment, which follows its direction: Justify set on
	// it changes nothing, LTR or RTL.
	h := newHarness(t, 800, 600)
	for _, rtl := range []bool{false, true} {
		var f, a ID
		h.frame(func(c *Context) {
			c.NextWidth(Px(200))
			c.NextJustify(JustifyEnd)
			f = c.BeginFlow("flow", FlowOpts{RTL: rtl})
			c.NextSize(Px(30), Px(10))
			a = c.Box(0, "a")
			c.EndFlow()
		})
		want := float32(0) // FlowStart: the leading edge
		if rtl {
			want = 170
		}
		if x := h.rel(a, f, 0); x != want {
			t.Errorf("rtl %v: the child of a flow with JustifyEnd is at %v, want %v", rtl, x, want)
		}
	}
}

func TestJustifyAllocatesNothing(t *testing.T) {
	h := newHarness(t, 800, 600)
	build := func(c *Context) {
		for j := JustifyStart; j <= JustifySpaceEvenly; j++ {
			c.NextSize(Px(300), Px(40))
			c.NextAxis(AxisX)
			c.NextJustify(j)
			c.Open(0, "row"+string(rune('0'+j)))
			for i := 0; i < 4; i++ {
				c.NextSize(Px(20), Px(10))
				c.BoxKeyed(0, KeyN(c.ParentKey(), uint64(i)), nil)
			}
			c.Close()
		}
	}
	for i := 0; i < 3; i++ {
		h.frame(build)
	}
	if n := testing.AllocsPerRun(20, func() { h.frame(build) }); n != 0 {
		t.Errorf("a frame of justified rows allocates %v times", n)
	}
}
