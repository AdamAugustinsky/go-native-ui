package ui

import "testing"

// A grid with MinCell fits as many columns as its width allows and shares the
// width between them (repeat(auto-fill, minmax(MinCell, 1fr))); each row is
// as tall as its tallest child, and the grid's auto height is its rows'.
func TestGridFitsColumnsToItsWidth(t *testing.T) {
	for _, tc := range []struct {
		w, wantCell float32
		wantCols    int
	}{
		{320, 100, 3}, // (320+10)/(90+10) = 3.3: three of (320-20)/3
		{200, 95, 2},
		{80, 80, 1}, // narrower than one cell: one column, the whole width
	} {
		h := newHarness(t, 800, 600)
		var g ID
		var kids [5]ID
		h.frame(func(c *Context) {
			c.NextWidth(Px(tc.w))
			g = c.BeginGrid("g", GridOpts{MinCell: 90, GapX: 10, GapY: 4})
			for i := range kids {
				c.NextSize(Px(30), Px(float32(20+10*(i%2)))) // the width is the column's
				kids[i] = c.Box(0, "k"+string(rune('0'+i)))
			}
			c.EndGrid()
		})
		h.frame(func(c *Context) {}) // GridColumns reads the last layout
		if n := tc.wantCols; h.c.boxes[g].grid.fit != int32(n) {
			t.Fatalf("width %v: %d columns, want %d", tc.w, h.c.boxes[g].grid.fit, n)
		}
		b := &h.c.boxes[kids[0]]
		if b.size[0] != tc.wantCell {
			t.Errorf("width %v: cells %v wide, want %v", tc.w, b.size[0], tc.wantCell)
		}
	}
}

func TestGridPlacesRowsAndSizesItself(t *testing.T) {
	h := newHarness(t, 800, 600)
	var g ID
	var kids [5]ID
	build := func(c *Context) {
		c.NextWidth(Px(320))
		c.NextPadding(5, 6, 5, 6)
		g = c.BeginGrid("g", GridOpts{Columns: 3, GapX: 10, GapY: 4, Cross: CrossCenter})
		for i := range kids {
			c.NextSize(Px(30), Px(float32(20+10*(i%2))))
			kids[i] = c.Box(0, "k"+string(rune('0'+i)))
		}
		c.EndGrid()
	}
	h.frame(build)
	// 310 inside, three columns of 96.67 with gaps of 10; rows of 30 and 30
	want := [5][2]float32{{5, 6 + 5}, {5 + 106.66667, 6}, {5 + 213.33334, 6 + 5}, {5, 6 + 30 + 4}, {5 + 106.66667, 6 + 34 + 5}}
	for i, k := range kids {
		if x, y := h.rel(k, g, 0), h.rel(k, g, 1); !near(x, want[i][0]) || !near(y, want[i][1]) {
			t.Errorf("child %d at %v,%v, want %v", i, x, y, want[i])
		}
	}
	if got := h.c.boxes[g].size[1]; got != 6+30+4+30+6 {
		t.Errorf("the grid is %v high, want its two rows and padding, 76", got)
	}
	if n := h.c.GridColumns(g); n != 3 {
		t.Errorf("GridColumns %d, want 3", n)
	}
}

// Grow shares the room the fixed siblings leave by weight and stretches
// across; one Grow(1) is a spacer that pushes the rest to the end.
func TestGrowSharesTheRoomLeft(t *testing.T) {
	h := newHarness(t, 800, 600)
	var p, a, g1, g2, b ID
	h.frame(func(c *Context) {
		c.NextSize(Px(400), Px(50))
		c.NextAxis(AxisX)
		c.NextGap(10)
		p = c.Open(0, "row")
		c.NextSize(Px(40), Px(20))
		a = c.Box(0, "a")
		c.NextSize(Grow(1), Px(20))
		g1 = c.Box(0, "g1")
		c.NextSize(Grow(2), Grow(1))
		g2 = c.Box(0, "g2")
		c.NextSize(Px(60), Px(20))
		b = c.Box(0, "b")
		c.Close()
	})
	// 400 - 40 - 60 - 3 gaps = 270: 90 and 180
	if w := h.c.boxes[g1].size[0]; w != 90 {
		t.Errorf("Grow(1) is %v wide, want 90", w)
	}
	if w := h.c.boxes[g2].size[0]; w != 180 {
		t.Errorf("Grow(2) is %v wide, want 180", w)
	}
	if hh := h.c.boxes[g2].size[1]; hh != 50 {
		t.Errorf("Grow across is %v high, want the row's 50", hh)
	}
	if x := h.rel(b, p, 0); x != 340 {
		t.Errorf("the last child is at %v, want at the end, 340", x)
	}
	_ = a
}

// A minimum stops shrinking (the rest gives way); a maximum caps a size,
// a percentage's or a Grow's.
func TestMinAndMaxSizes(t *testing.T) {
	h := newHarness(t, 800, 600)
	var keep, give, capped, grown ID
	h.frame(func(c *Context) {
		c.NextSize(Px(100), Px(50))
		c.NextAxis(AxisX)
		c.Open(0, "row")
		c.NextSize(Px(80).Strict(0), Px(20))
		c.NextMinSize(70, 0)
		keep = c.Box(0, "keep")
		c.NextSize(Px(80).Strict(0), Px(20))
		give = c.Box(0, "give")
		c.Close()
		c.NextSize(Px(400), Px(50))
		c.NextAxis(AxisX)
		c.Open(0, "wide")
		c.NextSize(Pct(1), Px(20))
		c.NextMaxSize(150, 0)
		capped = c.Box(0, "capped")
		c.NextSize(Grow(1), Px(20))
		c.NextMaxSize(100, 0)
		grown = c.Box(0, "grown")
		c.Close()
	})
	if w := h.c.boxes[keep].size[0]; w != 70 {
		t.Errorf("the box with a minimum shrank to %v, want 70", w)
	}
	if w := h.c.boxes[give].size[0]; w != 30 {
		t.Errorf("its sibling is %v, want the 30 left", w)
	}
	if w := h.c.boxes[capped].size[0]; w != 150 {
		t.Errorf("Pct(1) with a maximum is %v, want 150", w)
	}
	if w := h.c.boxes[grown].size[0]; w != 100 {
		t.Errorf("Grow with a maximum is %v, want 100", w)
	}
}

// NextAlignSelf overrides the parent's NextCross for one child.
func TestAlignSelf(t *testing.T) {
	h := newHarness(t, 800, 600)
	var p, mid, end ID
	h.frame(func(c *Context) {
		c.NextSize(Px(200), Px(60))
		c.NextAxis(AxisX)
		c.NextCross(CrossCenter)
		p = c.Open(0, "row")
		c.NextSize(Px(20), Px(20))
		mid = c.Box(0, "mid")
		c.NextAlignSelf(CrossEnd)
		c.NextSize(Px(20), Px(20))
		end = c.Box(0, "end")
		c.Close()
	})
	if y := h.rel(mid, p, 1); y != 20 {
		t.Errorf("the centred child is at %v, want 20", y)
	}
	if y := h.rel(end, p, 1); y != 40 {
		t.Errorf("the child aligned to the end is at %v, want 40", y)
	}
}
