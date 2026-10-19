package ui

import (
	"fmt"
	"testing"

	"gonativeui/gpu"
)

// flowRig builds one flow of Px-sized chips named c0, c1, … under the root and
// remembers their boxes.
type flowRig struct {
	*harness
	width float32 // the flow's width (Px), 0 for the default (the parent's)
	opts  FlowOpts
	pad   float32
	w, h  []float32 // the chips' sizes
	flags Flags     // the chips' flags (Focusable|Clickable for the focus tests)
	flow  ID
	chips []ID
	after ID // a box built after the flow, in the root column
}

func newFlowRig(t *testing.T, width float32, o FlowOpts, w ...float32) *flowRig {
	r := &flowRig{harness: newHarness(t, 600, 400), width: width, opts: o, w: w}
	r.h = make([]float32, len(w))
	for i := range r.h {
		r.h[i] = 10
	}
	r.run()
	return r
}

func (r *flowRig) build(c *Context) {
	if r.width > 0 {
		c.NextWidth(Px(r.width))
	}
	if r.pad > 0 {
		c.NextPad(r.pad)
	}
	r.flow = c.BeginFlow("flow", r.opts)
	r.chips = r.chips[:0]
	for i := range r.w {
		c.NextSize(Px(r.w[i]), Px(r.h[i]))
		id := c.Box(r.flags, fmt.Sprintf("c%d", i))
		r.chips = append(r.chips, id)
	}
	c.EndFlow()
	c.NextSize(Px(5), Px(7))
	r.after = c.Box(0, "after")
}

// run builds and lays out two frames (the second sees the first's results).
func (r *flowRig) run() {
	r.frame(r.build)
	r.frame(r.build)
}

// pos is the top left of chip i.
func (r *flowRig) pos(i int) [2]float32 {
	b := r.rect(r.chips[i])
	return [2]float32{b[0], b[1]}
}

func (r *flowRig) wantPos(want ...[2]float32) {
	r.t.Helper()
	for i, w := range want {
		if got := r.pos(i); got != w {
			r.t.Errorf("chip %d at %v, want %v", i, got, w)
		}
	}
}

func TestFlowWrapsAtExactFit(t *testing.T) {
	// 45 + 10 + 45 is exactly 100: the second chip stays on the line.
	r := newFlowRig(t, 100, FlowOpts{GapX: 10, GapY: 4}, 45, 45, 20, 100, 1)
	r.wantPos([2]float32{0, 0}, [2]float32{55, 0}, [2]float32{0, 14}, [2]float32{0, 28}, [2]float32{0, 42})
	// the 100-wide chip fills a line on its own, so the 1-wide one after it wraps
	if got := r.rect(r.flow); got[3] != 52 {
		t.Errorf("flow bottom %v, want 52 (4 lines of 10 and 3 gaps of 4)", got[3])
	}
	if b := r.rect(r.after); b[1] != 52 {
		t.Errorf("the box after the flow is at %v, want 52", b[1])
	}
	if r.c.FlowLines(r.flow) != 4 || r.c.FlowOverflow(r.flow) != 0 {
		t.Errorf("lines %d overflow %d", r.c.FlowLines(r.flow), r.c.FlowOverflow(r.flow))
	}

	// one pixel short and the second chip wraps
	r = newFlowRig(t, 100, FlowOpts{GapX: 10}, 46, 45)
	r.wantPos([2]float32{0, 0}, [2]float32{0, 10})
	// no gap, exactly three of 20 in 60
	r = newFlowRig(t, 60, FlowOpts{}, 20, 20, 20, 20)
	r.wantPos([2]float32{0, 0}, [2]float32{20, 0}, [2]float32{40, 0}, [2]float32{0, 10})
}

func TestFlowDefaultWidthIsTheParents(t *testing.T) {
	r := newFlowRig(t, 0, FlowOpts{GapX: 5}, 280, 280, 280, 280)
	// the root is 600 wide: 280+5+280 fits, a third does not
	r.wantPos([2]float32{0, 0}, [2]float32{285, 0}, [2]float32{0, 10}, [2]float32{285, 10})
	if b := r.rect(r.flow); b[2] != 600 {
		t.Errorf("flow right %v, want 600", b[2])
	}
}

func TestFlowChildWiderThanTheContainer(t *testing.T) {
	r := newFlowRig(t, 100, FlowOpts{GapX: 4, GapY: 2}, 30, 150, 30)
	// a Px chip keeps its width on a line of its own and overflows
	r.wantPos([2]float32{0, 0}, [2]float32{0, 12}, [2]float32{0, 24})
	if b := r.rect(r.chips[1]); b[2]-b[0] != 150 {
		t.Errorf("strict chip is %v wide, want 150", b[2]-b[0])
	}
	if r.c.FlowLines(r.flow) != 3 {
		t.Errorf("lines %d", r.c.FlowLines(r.flow))
	}

	// a chip that gives way is shrunk to the line, by its strictness
	h := newHarness(t, 600, 400)
	var soft, half, hard ID
	build := func(c *Context) {
		c.NextWidth(Px(100))
		c.BeginFlow("f", FlowOpts{GapX: 4})
		c.NextSize(Px(150).Strict(0), Px(10))
		soft = c.Box(0, "soft")
		c.NextSize(Px(150).Strict(0.8), Px(10))
		half = c.Box(0, "half")
		c.NextSize(Px(150).Strict(1), Px(10))
		hard = c.Box(0, "hard")
		c.EndFlow()
	}
	h.frame(build)
	h.frame(build)
	for _, c := range []struct {
		id   ID
		want float32
	}{{soft, 100}, {half, 120}, {hard, 150}} {
		if b := h.rect(c.id); !near(b[2]-b[0], c.want) {
			t.Errorf("chip %d is %v wide, want %v", c.id, b[2]-b[0], c.want)
		}
	}

	// a Clip flow clips the overflow
	r2 := newHarness(t, 600, 400)
	var l gpu.DrawList
	r2.c.Begin(&r2.in, 600, 400, 1, 1.0/60)
	r2.c.NextWidth(Px(100))
	r2.c.NextFlags(Clip)
	r2.c.BeginFlow("f", FlowOpts{})
	r2.c.NextSize(Px(150), Px(10))
	r2.c.NextBG(0xFF0000FF)
	r2.c.Box(DrawBackground, "wide")
	r2.c.EndFlow()
	r2.c.End(&l)
	found := false
	for _, q := range l.Quads {
		if q.Rect[2]-q.Rect[0] == 150 {
			found = true
			if q.Clip[2] != 100 {
				t.Errorf("the wide chip is clipped to x=%v, want 100", q.Clip[2])
			}
		}
	}
	if !found {
		t.Error("the wide chip was not drawn")
	}
}

func TestFlowPaddingAndGaps(t *testing.T) {
	r := &flowRig{harness: newHarness(t, 600, 400), width: 120, opts: FlowOpts{GapX: 8, GapY: 6}, pad: 10, w: []float32{40, 40, 40, 30}}
	r.h = []float32{10, 20, 10, 10}
	r.run()
	// content width 100: 40+8+40 = 88, a third 40 would be 136
	r.wantPos([2]float32{10, 10}, [2]float32{58, 10}, [2]float32{10, 36}, [2]float32{58, 36})
	// height: 10 + 20 (line 1) + 6 + 10 (line 2) + 10 = 56
	if b := r.rect(r.flow); b[3]-b[1] != 56 {
		t.Errorf("flow height %v, want 56", b[3]-b[1])
	}
}

// xs are the x of every chip.
func (r *flowRig) xs() []float32 {
	xs := make([]float32, len(r.chips))
	for i := range xs {
		xs[i] = r.pos(i)[0]
	}
	return xs
}

func sameFloats(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !near(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestFlowLineAlignment(t *testing.T) {
	// 100 wide: 40+40 on line 1 (room 20), 30+30 on line 2 (room 40)
	w := []float32{40, 40, 30, 30}
	for _, c := range []struct {
		name  string
		align FlowAlign
		rtl   bool
		want  []float32 // the x of the four chips
	}{
		{"start", FlowStart, false, []float32{0, 40, 0, 30}},
		{"center", FlowCenter, false, []float32{10, 50, 20, 50}},
		{"end", FlowEnd, false, []float32{20, 60, 40, 70}},
		{"justify", FlowJustify, false, []float32{0, 60, 0, 30}}, // the last line stays at the start
		{"rtl start", FlowStart, true, []float32{60, 20, 70, 40}},
		{"rtl center", FlowCenter, true, []float32{50, 10, 50, 20}},
		{"rtl end", FlowEnd, true, []float32{40, 0, 30, 0}},
		{"rtl justify", FlowJustify, true, []float32{60, 0, 70, 40}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newFlowRig(t, 100, FlowOpts{Align: c.align, RTL: c.rtl}, w...)
			if got := r.xs(); !sameFloats(got, c.want) {
				t.Errorf("x = %v, want %v", got, c.want)
			}
			// the second line is below the first in every case
			if r.pos(2)[1] != 10 || r.pos(0)[1] != 0 {
				t.Errorf("lines at y %v and %v", r.pos(0)[1], r.pos(2)[1])
			}
		})
	}

	t.Run("justify with a gap and a lone child", func(t *testing.T) {
		// line 1: 30, 30 and 30 with gap 5 = 100 exactly: no room, gaps stay 5
		// line 2: a lone 20-wide chip
		r := newFlowRig(t, 100, FlowOpts{GapX: 5, Align: FlowJustify}, 30, 30, 30, 20, 20)
		if got, want := r.xs(), []float32{0, 35, 70, 0, 25}; !sameFloats(got, want) {
			t.Errorf("x = %v, want %v", got, want)
		}
		// a single-child line is not stretched even when more lines follow
		r = newFlowRig(t, 100, FlowOpts{GapX: 5, Align: FlowJustify}, 90, 50, 20)
		if got, want := r.xs(), []float32{0, 0, 55}; !sameFloats(got, want) {
			t.Errorf("x = %v, want %v", got, want)
		}
	})

	t.Run("an oversize line is at the start whatever the alignment", func(t *testing.T) {
		for _, a := range []FlowAlign{FlowCenter, FlowEnd, FlowJustify} {
			r := newFlowRig(t, 100, FlowOpts{Align: a}, 150)
			if x := r.pos(0)[0]; x != 0 {
				t.Errorf("align %d: oversize chip at x=%v", a, x)
			}
		}
		r := newFlowRig(t, 100, FlowOpts{RTL: true}, 150)
		if b := r.rect(r.chips[0]); b[2] != 100 || b[0] != -50 {
			t.Errorf("RTL oversize chip spans %v..%v, want -50..100 (anchored at the right)", b[0], b[2])
		}
	})
}

func TestFlowCrossAlignment(t *testing.T) {
	for _, c := range []struct {
		cross Cross
		want  []float32 // y of the chips of line 1 (heights 10, 30, 20) and line 2
	}{
		{CrossStart, []float32{0, 0, 0, 36}},
		{CrossCenter, []float32{10, 0, 5, 36}},
		{CrossEnd, []float32{20, 0, 10, 36}},
	} {
		r := &flowRig{harness: newHarness(t, 600, 400), width: 100, opts: FlowOpts{Cross: c.cross, GapY: 6}, w: []float32{30, 30, 30, 30}}
		r.h = []float32{10, 30, 20, 8}
		r.run()
		for i, want := range c.want {
			if got := r.pos(i)[1]; got != want {
				t.Errorf("cross %d: chip %d at y=%v, want %v", c.cross, i, got, want)
			}
		}
	}
}

func TestFlowMaxLinesAndOverflow(t *testing.T) {
	// lines: [40 40] [30 30 30] [30]
	r := newFlowRig(t, 100, FlowOpts{MaxLines: 2, GapY: 3}, 40, 40, 30, 30, 30, 30)
	r.flags = Focusable | Clickable
	r.run()
	if r.c.FlowLines(r.flow) != 2 || r.c.FlowOverflow(r.flow) != 1 {
		t.Fatalf("lines %d, overflow %d; want 2 and 1", r.c.FlowLines(r.flow), r.c.FlowOverflow(r.flow))
	}
	if b := r.rect(r.flow); b[3]-b[1] != 23 {
		t.Errorf("height %v, want the two visible lines: 10+3+10", b[3]-b[1])
	}
	if b := r.rect(r.after); b[1] != 23 {
		t.Errorf("the box after the flow is at %v, want 23", b[1])
	}
	for i, c := range r.chips {
		if hid := r.c.boxes[c].hid; hid != (i == 5) {
			t.Errorf("chip %d hidden=%v", i, hid)
		}
	}
	if b := r.rect(r.chips[5]); b[2] > b[0] || b[3] > b[1] {
		t.Errorf("the hidden chip has a rectangle: %v", b)
	}

	// MaxLines 1: five chips are hidden; 0 means no limit
	r.opts.MaxLines = 1
	r.run()
	if r.c.FlowOverflow(r.flow) != 4 || r.c.FlowLines(r.flow) != 1 {
		t.Errorf("MaxLines 1: overflow %d lines %d", r.c.FlowOverflow(r.flow), r.c.FlowLines(r.flow))
	}
	r.opts.MaxLines = 0
	r.run()
	if r.c.FlowOverflow(r.flow) != 0 || r.c.FlowLines(r.flow) != 3 {
		t.Errorf("no limit: overflow %d lines %d", r.c.FlowOverflow(r.flow), r.c.FlowLines(r.flow))
	}
	// more lines than the children need
	r.opts.MaxLines = 9
	r.run()
	if r.c.FlowOverflow(r.flow) != 0 || r.c.FlowLines(r.flow) != 3 {
		t.Errorf("MaxLines 9: overflow %d lines %d", r.c.FlowOverflow(r.flow), r.c.FlowLines(r.flow))
	}
}

func TestFlowOverflowChangeAsksForAFrame(t *testing.T) {
	r := newFlowRig(t, 100, FlowOpts{MaxLines: 1}, 60, 60, 60)
	for i := 0; i < 3; i++ {
		r.frame(r.build)
	}
	if r.c.Animating() {
		t.Fatal("a stable flow keeps asking for frames")
	}
	r.w = append(r.w, 60) // one more hidden chip
	r.h = append(r.h, 10)
	r.frame(r.build)
	if !r.c.Animating() {
		t.Error("the overflow changed but no frame was asked for")
	}
	if got := r.c.FlowOverflow(r.flow); got != 3 {
		t.Errorf("overflow %d, want 3", got)
	}
	r.frame(r.build)
	if r.c.Animating() {
		t.Error("the flow went on asking for frames after the overflow settled")
	}
}

func TestFlowHiddenChildrenTakeNoInput(t *testing.T) {
	h := newHarness(t, 600, 400)
	keys := map[string]Key{}
	var sem SemTree
	build := func(c *Context) {
		c.NextWidth(Px(100))
		c.BeginFlow("flow", FlowOpts{MaxLines: 1})
		for _, n := range []string{"a", "b", "c"} {
			c.NextSize(Px(60), Px(10))
			c.NextRole(RoleButton)
			c.NextSemName(n)
			id := c.Open(Clickable|Focusable, n)
			keys[n] = c.KeyOf(id)
			// a focusable box inside, which is hidden along with its parent
			c.NextSize(Px(10), Px(10))
			in := c.Box(Clickable|Focusable, n+"-inner")
			keys[n+"-inner"] = c.KeyOf(in)
			c.Close()
		}
		c.EndFlow()
	}
	h.frame(build)
	h.frame(build)
	// a and its inner are the visible ones; b, c and their inners are hidden
	for _, n := range []string{"b", "c", "b-inner", "c-inner"} {
		id, _ := h.c.BoxOf(keys[n])
		if !h.c.boxes[id].hid {
			t.Errorf("%s is not hidden", n)
		}
	}
	for _, n := range []string{"a", "a-inner"} {
		id, _ := h.c.BoxOf(keys[n])
		if h.c.boxes[id].hid {
			t.Errorf("%s is hidden", n)
		}
	}

	// Tab visits the visible ones only, and wraps
	var got []string
	name := func(k Key) string {
		for n, kk := range keys {
			if kk == k {
				return n
			}
		}
		return ""
	}
	for i := 0; i < 5; i++ {
		h.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
		h.frame(build)
		got = append(got, name(h.c.FocusKey()))
	}
	want := []string{"a", "a-inner", "a", "a-inner", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Tab visited %v, want %v", got, want)
		}
	}

	// nothing is hot beside the visible chip, and the pointer on it hits its inner box
	h.move(70, 5)
	h.frame(build)
	if h.c.HotKey() != 0 {
		t.Errorf("hot is %q beside the visible chip", name(h.c.HotKey()))
	}
	h.move(5, 5)
	h.frame(build)
	if k := name(h.c.HotKey()); k != "a-inner" {
		t.Errorf("the pointer over a is on %q", k)
	}

	// the accessibility tree has the visible button only
	h.c.Semantics(&sem)
	buttons := 0
	for _, n := range sem.Nodes {
		if n.Role == RoleButton {
			buttons++
		}
	}
	if buttons != 1 {
		t.Errorf("%d buttons in the tree, want 1", buttons)
	}

	// a focused chip that becomes hidden loses the focus
	h.c.SetFocus(keys["b"])
	h.frame(build)
	if h.c.FocusKey() != 0 {
		t.Errorf("the hidden box keeps the focus: %q", name(h.c.FocusKey()))
	}

	// hidden boxes are not drawn
	h.frame(func(c *Context) {
		c.NextWidth(Px(100))
		c.BeginFlow("flow", FlowOpts{MaxLines: 1})
		for _, n := range []string{"a", "b"} {
			c.NextSize(Px(60), Px(10))
			c.NextBG(0xFF0000FF)
			c.Box(DrawBackground, n)
		}
		c.EndFlow()
	})
	if n := len(h.list.Quads); n != 1 {
		t.Errorf("%d quads drawn, want 1 (the second chip is hidden)", n)
	}
}

func TestFlowFocusAndAccessibilityOrderIsVisualOrder(t *testing.T) {
	for _, rtl := range []bool{false, true} {
		t.Run(fmt.Sprint("rtl=", rtl), func(t *testing.T) {
			// four lines: [40 40] [30 30 30] [50] [20 20]
			w := []float32{40, 40, 30, 30, 30, 50, 20, 20}
			r := newFlowRig(t, 100, FlowOpts{GapX: 0, GapY: 2, RTL: rtl, Align: FlowCenter}, w...)
			r.flags = Focusable | Clickable
			r.opts = FlowOpts{GapY: 2, RTL: rtl, Align: FlowCenter}
			r.run()
			// the visual reading order: lines top to bottom, within a line from
			// the leading edge (the left, or the right with RTL)
			var tab []int
			for i := 0; i < len(w); i++ {
				r.in.PushKey(KeyEvent{Code: KeyTab, Down: true})
				r.frame(r.build)
				for j, id := range r.chips {
					if r.c.FocusKey() == r.c.KeyOf(id) {
						tab = append(tab, j)
					}
				}
			}
			for i := 1; i < len(tab); i++ {
				a, b := r.pos(tab[i-1]), r.pos(tab[i])
				sameLine := a[1] == b[1]
				switch {
				case b[1] < a[1]:
					t.Fatalf("Tab went up: %v", tab)
				case sameLine && !rtl && b[0] <= a[0], sameLine && rtl && b[0] >= a[0]:
					t.Fatalf("Tab went against the line direction: %v", tab)
				}
			}
			if len(tab) != len(w) {
				t.Fatalf("Tab visited %v", tab)
			}

			// the accessibility tree lists the chips in the same order
			var sem SemTree
			r.frame(func(c *Context) {
				c.NextWidth(Px(100))
				c.BeginFlow("flow", FlowOpts{GapY: 2, RTL: rtl})
				for i, wi := range w {
					c.NextSize(Px(wi), Px(10))
					c.NextRole(RoleButton)
					c.NextSemName(fmt.Sprint(i))
					c.Box(0, fmt.Sprint("b", i))
				}
				c.EndFlow()
			})
			r.c.Semantics(&sem)
			var prev SemNode
			n := 0
			for _, nd := range sem.Nodes {
				if nd.Role != RoleButton {
					continue
				}
				if n > 0 {
					same := nd.Bounds[1] == prev.Bounds[1]
					if nd.Bounds[1] < prev.Bounds[1] || (same && !rtl && nd.Bounds[0] <= prev.Bounds[0]) || (same && rtl && nd.Bounds[0] >= prev.Bounds[0]) {
						t.Errorf("accessibility node %d at %v follows %v", n, nd.Bounds, prev.Bounds)
					}
				}
				prev = nd
				n++
			}
			if n != len(w) {
				t.Errorf("%d buttons in the tree, want %d", n, len(w))
			}
		})
	}
}

func TestFlowHitTesting(t *testing.T) {
	for _, rtl := range []bool{false, true} {
		r := newFlowRig(t, 100, FlowOpts{GapX: 10, GapY: 4, RTL: rtl}, 45, 45, 30, 30)
		r.flags = Clickable
		r.run()
		for i := range r.chips {
			b := r.rect(r.chips[i])
			r.move((b[0]+b[2])/2, (b[1]+b[3])/2)
			r.frame(r.build)
			if r.c.HotKey() != r.c.KeyOf(r.chips[i]) {
				t.Errorf("rtl=%v: the pointer on chip %d (%v) hits another box", rtl, i, b)
			}
		}
		// the gap between two chips is not a chip
		b0, b1 := r.rect(r.chips[0]), r.rect(r.chips[1])
		gx := (b0[2] + b1[0]) / 2
		if rtl {
			gx = (b0[0] + b1[2]) / 2
		}
		r.move(gx, 5)
		r.frame(r.build)
		if r.c.HotKey() != 0 {
			t.Errorf("rtl=%v: the gap hits box %v", rtl, r.c.HotKey())
		}
	}
}

func TestFlowInAColumn(t *testing.T) {
	h := newHarness(t, 600, 400)
	var col, head, flow, foot ID
	var chips [5]ID
	build := func(c *Context) {
		c.NextSize(Px(100), Children(1))
		c.NextPad(10)
		c.NextGap(6)
		col = c.Open(0, "col")
		c.NextSize(Px(20), Px(12))
		head = c.Box(0, "head")
		flow = c.BeginFlow("flow", FlowOpts{GapX: 5, GapY: 5}) // 80 wide: Pct(1) of the content
		for i := range chips {
			c.NextSize(Px(35), Px(10))
			chips[i] = c.Box(0, fmt.Sprint("c", i))
		}
		c.EndFlow()
		c.NextSize(Px(20), Px(12))
		foot = c.Box(0, "foot")
		c.Close()
	}
	h.frame(build)
	h.frame(build)
	// 80 wide: 35+5+35 = 75 fits, a third wraps: lines [2] [2] [1]
	if b := h.rect(flow); b[2]-b[0] != 80 || b[3]-b[1] != 40 {
		t.Errorf("flow is %vx%v, want 80x40", b[2]-b[0], b[3]-b[1])
	}
	if b := h.rect(foot); b[1] != 10+12+6+40+6 {
		t.Errorf("the box below the flow is at %v, want %v", b[1], 10+12+6+40+6)
	}
	if b := h.rect(col); b[3]-b[1] != 10+12+6+40+6+12+10 {
		t.Errorf("the column is %v high, want %v", b[3]-b[1], 10+12+6+40+6+12+10)
	}
	if b := h.rect(chips[4]); b[0] != 10 || b[1] != 10+12+6+30 {
		t.Errorf("the last chip is at %v", b)
	}
	_ = head

	// a narrower column wraps more, and the column follows
	h.w = 600
	build2 := func(c *Context) {
		c.NextSize(Px(60), Children(1))
		col = c.Open(0, "col")
		flow = c.BeginFlow("flow", FlowOpts{GapX: 5, GapY: 5})
		for i := range chips {
			c.NextSize(Px(35), Px(10))
			chips[i] = c.Box(0, fmt.Sprint("c", i))
		}
		c.EndFlow()
		c.Close()
	}
	h.frame(build2)
	h.frame(build2)
	if b := h.rect(col); b[3]-b[1] != 5*10+4*5 {
		t.Errorf("a column too narrow for two chips is %v high, want 70", b[3]-b[1])
	}
}

func TestFlowInARowShrinksAndWraps(t *testing.T) {
	h := newHarness(t, 600, 400)
	var row, flow, btn ID
	build := func(c *Context) {
		c.NextSize(Px(200), Children(1))
		c.NextAxis(AxisX)
		row = c.Open(0, "row")
		flow = c.BeginFlow("flow", FlowOpts{GapX: 4, GapY: 4}) // Pct(1), gives way to the button
		for i := 0; i < 6; i++ {
			c.NextSize(Px(36), Px(10))
			c.Box(0, fmt.Sprint("c", i))
		}
		c.EndFlow()
		c.NextSize(Px(40), Px(10).Strict(1))
		btn = c.Box(0, "btn")
		c.Close()
	}
	h.frame(build)
	h.frame(build)
	// the flow gets 200-40 = 160: 4 chips of 36 and 3 gaps are 156, so 4 fit, then 2
	if b := h.rect(flow); b[2]-b[0] != 160 || b[3]-b[1] != 24 {
		t.Errorf("flow %vx%v, want 160x24", b[2]-b[0], b[3]-b[1])
	}
	if b := h.rect(row); b[3]-b[1] != 24 {
		t.Errorf("the row is %v high, want the flow's 24", b[3]-b[1])
	}
	if b := h.rect(btn); b[0] != 160 {
		t.Errorf("the button is at %v, want 160", b[0])
	}
}

func TestFlowSizedByChildrenWrapsOnlyWhenShrunk(t *testing.T) {
	h := newHarness(t, 600, 400)
	var flow ID
	build := func(parentW float32, strict float32) func(c *Context) {
		return func(c *Context) {
			c.NextSize(Px(parentW), Children(1))
			c.Open(0, "p")
			c.NextWidth(Children(strict))
			flow = c.BeginFlow("flow", FlowOpts{GapX: 4})
			for i := 0; i < 3; i++ {
				c.NextSize(Px(30), Px(10))
				c.Box(0, fmt.Sprint("c", i))
			}
			c.EndFlow()
			c.Close()
		}
	}
	h.frame(build(200, 1))
	h.frame(build(200, 1))
	if b := h.rect(flow); b[2]-b[0] != 98 || b[3]-b[1] != 10 {
		t.Errorf("a Children(1) flow is %vx%v, want 98x10 (one line)", b[2]-b[0], b[3]-b[1])
	}
	h.frame(build(70, 0)) // the parent is too narrow and the flow gives way
	h.frame(build(70, 0))
	if b := h.rect(flow); b[2]-b[0] != 70 || b[3]-b[1] != 20 {
		t.Errorf("a shrunk Children(0) flow is %vx%v, want 70x20 (2+1 chips)", b[2]-b[0], b[3]-b[1])
	}
}

func TestFlowInAScrollArea(t *testing.T) {
	h := newHarness(t, 300, 300)
	var area, flow ID
	var chips [9]ID
	build := func(c *Context) {
		c.NextSize(Px(100), Px(50))
		area = c.Open(ScrollY|Clip, "area")
		flow = c.BeginFlow("flow", FlowOpts{GapY: 5}) // 100 wide
		for i := range chips {
			c.NextSize(Px(60), Px(10))
			chips[i] = c.Box(0, fmt.Sprint("c", i))
		}
		c.EndFlow()
		c.Close()
	}
	h.frame(build)
	h.frame(build)
	content, view := h.c.ScrollExtent(area)
	if content[1] != 9*10+8*5 || view[1] != 50 {
		t.Errorf("scroll extent %v view %v, want a content height of 130 in 50", content, view)
	}
	if b := h.rect(flow); b[3]-b[1] != 130 {
		t.Errorf("flow height %v, want 130 (a scroll area does not shrink it)", b[3]-b[1])
	}
	h.c.SetScroll(area, 0, 30)
	h.frame(build)
	if b := h.rect(chips[2]); b[1] != 2*15-30 {
		t.Errorf("chip 2 at y=%v after scrolling 30, want %v", b[1], 2*15-30)
	}
	h.c.SetScroll(area, 0, 1000)
	h.frame(build)
	if _, y := h.c.Scroll(area); y != 80 {
		t.Errorf("the scroll is clamped to %v, want 80", y)
	}
}

func TestFlowFloatingChildrenAreOutsideTheLines(t *testing.T) {
	h := newHarness(t, 300, 300)
	var f, a, b, c3 ID
	build := func(c *Context) {
		c.NextWidth(Px(100))
		c.NextPad(4)
		f = c.BeginFlow("flow", FlowOpts{})
		c.NextSize(Px(40), Px(10))
		a = c.Box(0, "a")
		c.NextSize(Px(30), Px(10))
		c.NextPos(50, 60)
		b = c.Box(FloatingX|FloatingY, "float")
		c.NextSize(Px(40), Px(10))
		c3 = c.Box(0, "c")
		c.EndFlow()
	}
	h.frame(build)
	h.frame(build)
	if r := h.rect(a); r[0] != 4 || r[1] != 4 {
		t.Errorf("a at %v", r)
	}
	if r := h.rect(c3); r[0] != 44 || r[1] != 4 {
		t.Errorf("c at %v, want it next to a (the floating box takes no place)", r)
	}
	if r := h.rect(b); r[0] != 50 || r[1] != 60 {
		t.Errorf("the floating box at %v, want 50,60", r)
	}
	if r := h.rect(f); r[3]-r[1] != 18 {
		t.Errorf("flow height %v, want 10 + padding 8", r[3]-r[1])
	}
}

func TestFlowEmptyAndSingle(t *testing.T) {
	h := newHarness(t, 300, 300)
	var f ID
	build := func(c *Context) {
		f = c.BeginFlow("flow", FlowOpts{GapX: 5, GapY: 5, MaxLines: 1})
		c.EndFlow()
	}
	h.frame(build)
	h.frame(build)
	if r := h.rect(f); r[3]-r[1] != 0 || h.c.FlowLines(f) != 0 || h.c.FlowOverflow(f) != 0 {
		t.Errorf("an empty flow is %v high with %d lines", r[3]-r[1], h.c.FlowLines(f))
	}
}

// steadyFlow builds a busy flow tree: a column with two flows (one limited,
// one RTL and justified) of focusable chips with text, and a nested flow.
func steadyFlow(c *Context, labels []string) {
	c.NextSize(Px(300), Children(1))
	c.Open(0, "col")
	c.NextFlags(Clip)
	c.BeginFlow("a", FlowOpts{GapX: 6, GapY: 6, MaxLines: 2, Align: FlowCenter, Cross: CrossCenter})
	for _, l := range labels {
		c.Box(DrawText|Clickable|Focusable, l)
	}
	c.EndFlow()
	c.BeginFlow("b", FlowOpts{GapX: 6, GapY: 6, RTL: true, Align: FlowJustify})
	for i, l := range labels {
		if i%5 == 0 {
			c.BeginFlow(l+"##inner", FlowOpts{GapX: 2})
			c.Box(DrawText, "x")
			c.Box(DrawText, "y")
			c.EndFlow()
			continue
		}
		c.Box(DrawText|Clickable, l)
	}
	c.EndFlow()
	c.Close()
}

func TestFlowAllocatesNothing(t *testing.T) {
	h := newHarness(t, 600, 800)
	labels := make([]string, 40)
	for i := range labels {
		labels[i] = fmt.Sprint("chip number ", i)
	}
	frame := func() { h.frame(func(c *Context) { steadyFlow(c, labels) }) }
	for i := 0; i < 6; i++ {
		frame()
	}
	if h.c.FlowOverflow(h.firstFlow()) == 0 {
		t.Fatal("the limited flow hides nothing: the test does not cover the hidden path")
	}
	if n := testing.AllocsPerRun(50, frame); n != 0 {
		t.Errorf("%v allocations per frame, want 0", n)
	}
}

// firstFlow is the first flow box of the last frame.
func (h *harness) firstFlow() ID {
	for _, id := range h.c.order {
		if h.c.boxes[id].axis == AxisFlow {
			return id
		}
	}
	return 0
}

// Example of a flow of tags: they wrap at 100 px, only two lines are shown, and
// the caller reads how many did not fit (for "+2 more"). A box's rectangle is
// the previous frame's while it is being built, so the example builds twice.
func ExampleContext_BeginFlow() {
	c := New(newFake())
	var in Input
	in.Inside = true
	var list gpu.DrawList
	tags := []string{"go", "ui", "native", "flow", "wrap", "chips"}
	var flow ID
	for frame := 0; frame < 2; frame++ {
		c.Begin(&in, 160, 100, 1, 1.0/60)
		c.NextWidth(Px(100))
		flow = c.BeginFlow("tags", FlowOpts{GapX: 4, GapY: 4, MaxLines: 2})
		for _, t := range tags {
			c.NextSize(Px(float32(8*len(t)+8)), Px(14))
			id := c.Box(0, t)
			if r := c.Rect(id); frame == 1 && r[2] > r[0] { // shown
				fmt.Printf("%-6s x=%2.0f y=%2.0f\n", t, r[0], r[1])
			}
		}
		c.EndFlow()
		c.End(&list)
	}
	fmt.Println("lines", c.FlowLines(flow), "+", c.FlowOverflow(flow), "more")
	// Output:
	// go     x= 0 y= 0
	// ui     x=28 y= 0
	// native x= 0 y=18
	// flow   x=60 y=18
	// lines 2 + 2 more
}
