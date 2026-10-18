package ui

import "testing"

func TestKeysAndLabels(t *testing.T) {
	for _, c := range []struct{ in, disp, ident string }{
		{"plain", "plain", "plain"},
		{"Name##a", "Name", "Name##a"},
		{"Name###id", "Name", "id"},
		{"##hidden", "", "##hidden"},
		{"###only", "", "only"},
		{"a#b", "a#b", "a#b"},
		{"x##y###z", "x", "x##y###z"},
	} {
		d, id := SplitLabel(c.in)
		// For "x##y###z" the first "##" starts a plain "##": identity is the whole label.
		if d != c.disp || id != c.ident {
			t.Errorf("SplitLabel(%q) = %q, %q; want %q, %q", c.in, d, id, c.disp, c.ident)
		}
	}

	h := newHarness(t, 400, 300)
	var a1, a2, b1, c1 ID
	h.frame(func(c *Context) {
		a1 = c.Box(0, "Item##1")
		a2 = c.Box(0, "Item##2")
		b1 = c.Box(0, "Name v1###stable")
		c.Open(0, "panel")
		c1 = c.Box(0, "Item##1") // same label, different parent
		c.Close()
	})
	k := func(id ID) Key { return h.c.KeyOf(id) }
	if k(a1) == k(a2) || k(a1) == k(c1) {
		t.Fatal("distinct identities collided")
	}
	stable := k(b1)
	h.frame(func(c *Context) {
		c.Box(0, "Item##1")
		c.Box(0, "Item##2")
		b2 := c.Box(0, "Name v2 changed###stable")
		if c.KeyOf(b2) != stable || b2 != b1 {
			t.Errorf("### identity changed with the display text: %v vs %v", c.KeyOf(b2), stable)
		}
	})

	// Duplicate labels in one frame get distinct boxes, stable across frames.
	var d1, d2 ID
	h.frame(func(c *Context) { d1 = c.Box(0, "dup"); d2 = c.Box(0, "dup") })
	if d1 == d2 || k(d1) == k(d2) {
		t.Fatal("duplicate labels share a box")
	}
	f1, f2 := k(d1), k(d2)
	h.frame(func(c *Context) {
		x, y := c.Box(0, "dup"), c.Box(0, "dup")
		if c.KeyOf(x) != f1 || c.KeyOf(y) != f2 {
			t.Error("duplicate keys not stable across frames")
		}
	})
}

func TestDisplayTextOnlyWithDrawText(t *testing.T) {
	h := newHarness(t, 400, 300)
	var a, b ID
	h.frame(func(c *Context) {
		a = c.Box(DrawText, "hello##x")
		b = c.Box(0, "hello##y")
	})
	if w := h.rect(a)[2] - h.rect(a)[0]; w != 40 {
		t.Errorf("text box width %v, want 40 (5 glyphs × 8)", w)
	}
	if r := h.rect(b); r[2] != r[0] {
		t.Errorf("box without DrawText has width %v", r[2]-r[0])
	}
}

func TestPruningAndState(t *testing.T) {
	h := newHarness(t, 400, 300)
	var x ID
	build := func(c *Context) {
		x = c.Box(ScrollY|Hoverable, "x")
	}
	h.frame(build)
	h.c.SetScroll(x, 0, 50)
	live := h.c.Live()
	h.frame(build)
	if _, sy := h.c.Scroll(x); sy != 0 { // clamped: nothing to scroll
		t.Fatalf("scroll %v", sy)
	}
	// State persists while built; box pool reuses the record.
	h.c.boxes[x].persist.hotT = 0.5
	h.frame(build)
	if h.c.boxes[x].hotT != 0.5 {
		t.Fatal("state lost while built every frame")
	}
	h.frame(nil) // not built
	if got := h.c.Live(); got != live-1 {
		t.Fatalf("live boxes after omission: %d, want %d", got, live-1)
	}
	if _, ok := h.c.table.Get(uint64(h.c.boxes[x].key)); ok {
		t.Fatal("pruned key still in the table")
	}
	h.frame(build)
	if h.c.boxes[x].hotT != 0 {
		t.Fatal("state survived pruning")
	}
	if h.c.Live() != live {
		t.Fatalf("live %d want %d", h.c.Live(), live)
	}
}

func TestLayoutRowAndPadding(t *testing.T) {
	h := newHarness(t, 800, 600)
	var row, a, b, cc ID
	h.frame(func(c *Context) {
		c.NextSize(Px(500), Px(100))
		c.NextAxis(AxisX)
		c.NextGap(10)
		c.NextPadding(5, 6, 7, 8)
		row = c.Box(0, "row")
		c.Push(row)
		c.NextSize(Px(100), Pct(1))
		a = c.Box(0, "a")
		c.NextSize(Px(50), Px(20))
		b = c.Box(0, "b")
		c.NextSize(Pct(0.5), Pct(0.5))
		cc = c.Box(0, "c")
		c.Pop()
	})
	check := func(name string, id ID, want [4]float32) {
		if got := h.rect(id); got != want {
			t.Errorf("%s rect = %v, want %v", name, got, want)
		}
	}
	check("row", row, [4]float32{0, 0, 500, 100})
	check("a", a, [4]float32{5, 6, 105, 92})              // height 100% of 100-6-8
	check("b", b, [4]float32{115, 6, 165, 26})            // after gap 10
	check("c", cc, [4]float32{175, 6, 175 + 244, 6 + 43}) // 50% of (500-12)=244, 50% of 86
}

func TestLayoutColumnTextAndChildren(t *testing.T) {
	h := newHarness(t, 800, 600)
	var box, l1, l2 ID
	h.frame(func(c *Context) {
		c.NextPad(4)
		c.NextGap(2)
		c.NextSize(Children(1), Children(1))
		box = c.Box(0, "box")
		c.Push(box)
		l1 = c.Label("hello")
		c.NextPad(1)
		l2 = c.Label("hi")
		c.Pop()
	})
	// l1: 40×14, l2: 16+2 wide, 14+2 tall; column: width max(40,18)+8, height 14+16+2+8
	if r := h.rect(l1); r != [4]float32{4, 4, 44, 18} {
		t.Errorf("l1 %v", r)
	}
	if r := h.rect(l2); r != [4]float32{4, 20, 22, 36} {
		t.Errorf("l2 %v", r)
	}
	if r := h.rect(box); r != [4]float32{0, 0, 48, 40} {
		t.Errorf("box %v", r)
	}
}

func TestViolations(t *testing.T) {
	cases := []struct {
		name   string
		strict [2]float32
		want   [2]float32 // widths
	}{
		{"strict first, flexible second", [2]float32{1, 0}, [2]float32{200, 100}},
		{"both flexible share the overflow", [2]float32{0, 0}, [2]float32{150, 150}},
		{"both strict overflow", [2]float32{1, 1}, [2]float32{200, 200}},
		{"half strict", [2]float32{0.5, 0}, [2]float32{200 - 100/3.0, 200 - 200/3.0}}, // fix 100 and 200 share 100
	}
	for _, tc := range cases {
		h := newHarness(t, 800, 600)
		var a, b ID
		h.frame(func(c *Context) {
			c.NextSize(Px(300), Px(100))
			c.NextAxis(AxisX)
			r := c.Box(0, "r")
			c.Push(r)
			c.NextSize(Px(200).Strict(tc.strict[0]), Px(10))
			a = c.Box(0, "a")
			c.NextSize(Px(200).Strict(tc.strict[1]), Px(10))
			b = c.Box(0, "b")
			c.Pop()
		})
		ra, rb := h.rect(a), h.rect(b)
		if wa, wb := ra[2]-ra[0], rb[2]-rb[0]; (wa-tc.want[0] > 1 || tc.want[0]-wa > 1) || (wb-tc.want[1] > 1 || tc.want[1]-wb > 1) {
			t.Errorf("%s: widths %v, %v; want %v", tc.name, wa, wb, tc.want)
		}
	}

	// Cross axis: a tall flexible child is cut to the parent, a strict one is not.
	h := newHarness(t, 800, 600)
	var tall, strict, scroller, inScroll ID
	h.frame(func(c *Context) {
		c.NextSize(Px(300), Px(200))
		c.NextAxis(AxisX)
		r := c.Box(0, "r")
		c.Push(r)
		c.NextSize(Px(50), Px(500).Strict(0))
		tall = c.Box(0, "tall")
		c.NextSize(Px(50), Px(500))
		strict = c.Box(0, "strict")
		c.Pop()

		c.NextSize(Px(100), Px(100))
		scroller = c.Box(ScrollY, "scroller")
		c.Push(scroller)
		c.NextSize(Px(100), Px(400).Strict(0))
		inScroll = c.Box(0, "inScroll")
		c.Pop()
	})
	if r := h.rect(tall); r[3]-r[1] != 200 {
		t.Errorf("flexible cross-axis child height %v, want 200", r[3]-r[1])
	}
	if r := h.rect(strict); r[3]-r[1] != 500 {
		t.Errorf("strict cross-axis child height %v, want 500", r[3]-r[1])
	}
	if r := h.rect(inScroll); r[3]-r[1] != 400 {
		t.Errorf("child of a scroll box was shrunk to %v", r[3]-r[1])
	}
}

func TestParentPctFollowsShrunkParent(t *testing.T) {
	h := newHarness(t, 800, 600)
	var p, ch ID
	h.frame(func(c *Context) {
		c.NextSize(Px(100), Px(100))
		outer := c.Box(0, "outer")
		c.Push(outer)
		c.NextSize(Px(300).Strict(0), Px(50)) // wants 300, outer only has 100
		p = c.Box(0, "p")
		c.Push(p)
		c.NextSize(Pct(0.5), Px(10))
		ch = c.Box(0, "ch")
		c.Pop()
		c.Pop()
	})
	if w := h.rect(p); w[2]-w[0] != 100 {
		t.Fatalf("p width %v", w[2]-w[0])
	}
	if r := h.rect(ch); r[2]-r[0] != 50 {
		t.Errorf("percentage child width %v, want 50 (half of the shrunk parent)", r[2]-r[0])
	}
}

func TestFloating(t *testing.T) {
	h := newHarness(t, 800, 600)
	var par, fl, after ID
	h.frame(func(c *Context) {
		c.NextSize(Children(1), Children(1))
		c.NextPad(2)
		par = c.Box(0, "par")
		c.Push(par)
		c.NextSize(Px(30), Px(30))
		c.Box(0, "in-flow")
		c.NextSize(Px(500), Px(500))
		c.NextPos(50, 60)
		fl = c.Box(FloatingX|FloatingY, "floating")
		c.NextSize(Px(10), Px(10))
		after = c.Box(0, "after")
		c.Pop()
	})
	if r := h.rect(par); r != [4]float32{0, 0, 34, 34 + 10} {
		t.Errorf("parent %v: floating child must not count in the sum", r)
	}
	if r := h.rect(fl); r[0] != 50 || r[1] != 60 || r[2] != 550 || r[3] != 560 {
		t.Errorf("floating %v", r)
	}
	if r := h.rect(after); r[1] != 32 {
		t.Errorf("sibling after a floating box at y=%v, want 32 (flow ignores it)", r[1])
	}

	// A floating child of the root is placed in window coordinates.
	var pop ID
	h.frame(func(c *Context) {
		c.Push(c.Root())
		c.NextSize(Px(40), Px(40))
		c.NextPos(300, 200)
		pop = c.Box(FloatingX|FloatingY, "pop")
		c.Pop()
	})
	if r := h.rect(pop); r != [4]float32{300, 200, 340, 240} {
		t.Errorf("popup %v", r)
	}
}

func TestSnapping(t *testing.T) {
	h := newHarness(t, 800, 600)
	var ids [4]ID
	h.frame(func(c *Context) {
		c.NextSize(Px(100), Px(30))
		c.NextAxis(AxisX)
		r := c.Box(0, "row")
		c.Push(r)
		for i := range ids {
			c.NextSize(Px(100.0/3), Px(10.4))
			ids[i] = c.Box(0, string(rune('a'+i)))
		}
		c.Pop()
	})
	for i := range ids {
		r := h.rect(ids[i])
		for _, v := range r {
			if v != float32(int(v)) {
				t.Fatalf("rect %v not on whole pixels", r)
			}
		}
		if i > 0 && h.rect(ids[i-1])[2] != r[0] {
			t.Errorf("gap between boxes %d and %d", i-1, i)
		}
	}
}

func TestPassOrderChildrenOfPercent(t *testing.T) {
	// A column sized by its children, holding a fixed box and a text label.
	h := newHarness(t, 800, 600)
	var col ID
	h.frame(func(c *Context) {
		c.NextSize(Children(1), Children(1))
		c.NextAxis(AxisX)
		c.NextGap(5)
		col = c.Box(0, "col")
		c.Push(col)
		c.NextSize(Px(20), Px(20))
		c.Box(0, "a")
		inner := c.Open(0, "inner")
		_ = inner
		c.Label("abc")
		c.Close()
		c.Pop()
	})
	// inner has no explicit size: children sum = 24 × 14.
	if r := h.rect(col); r != [4]float32{0, 0, 20 + 5 + 24, 20} {
		t.Errorf("col %v", r)
	}
}

// NextCross places the flow children across the layout axis; the default and
// CrossStart leave them at the start, and a child larger than the room stays
// there.
func TestCrossAxisPlacement(t *testing.T) {
	h := newHarness(t, 800, 600)
	var row, a, b, big [3]ID
	build := func(cr Cross) func(c *Context) {
		return func(c *Context) {
			for i, cross := range [3]Cross{cr, CrossStart, cr} {
				c.NextSize(Px(300), Px(100))
				c.NextAxis(AxisX)
				c.NextPadding(4, 6, 4, 10)
				c.NextCross(cross)
				row[i] = c.Box(0, "row"+string(rune('0'+i)))
				c.Push(row[i])
				c.NextSize(Px(40), Px(20))
				a[i] = c.Box(0, "a")
				c.NextSize(Px(40), Px(41))
				b[i] = c.Box(0, "b")
				c.NextSize(Px(40), Px(200)) // taller than the row
				big[i] = c.Box(0, "big")
				c.Pop()
			}
		}
	}
	// the padded area of a row is 84 px high: y 6 to 90 inside the row
	h.frame(build(CrossCenter))
	rel := func(i int, id ID) float32 { return h.rect(id)[1] - h.rect(row[i])[1] }
	if got := rel(0, a[0]); got != 6+32 { // (84-20)/2 = 32
		t.Errorf("centred a at %v, want 38", got)
	}
	if got := rel(0, b[0]); got != 6+snap(float32(84-41)/2) { // 21.5 rounds to 22
		t.Errorf("centred b at %v, want 28", got)
	}
	if got := rel(0, big[0]); got != 6 {
		t.Errorf("a child taller than the room is at %v, want the start (6)", got)
	}
	if got := rel(1, a[1]); got != 6 {
		t.Errorf("CrossStart a at %v, want 6", got)
	}
	h.frame(build(CrossEnd))
	if got := rel(0, a[0]); got != 6+64 {
		t.Errorf("end-aligned a at %v, want 70", got)
	}
	if got := rel(2, b[2]); got != 6+43 {
		t.Errorf("end-aligned b at %v, want 49", got)
	}
	var plain, centred ID
	h.frame(func(c *Context) {
		c.NextSize(Px(300), Px(100)) // the default: the start
		c.NextAxis(AxisX)
		c.Push(c.Box(0, "plain"))
		c.NextSize(Px(40), Px(20))
		plain = c.Box(0, "x")
		c.Pop()
		c.NextSize(Px(300), Px(100)) // a column centres the widths
		c.NextCross(CrossCenter)
		c.Push(c.Box(0, "col"))
		c.NextSize(Px(100), Px(10))
		centred = c.Box(0, "x")
		c.Pop()
	})
	if got := h.rect(plain)[1]; got != 0 {
		t.Errorf("without NextCross the child is at y %v, want 0", got)
	}
	if got := h.rect(centred)[0]; got != 100 {
		t.Errorf("a centred column puts a 100 px child at x %v, want 100", got)
	}
}
