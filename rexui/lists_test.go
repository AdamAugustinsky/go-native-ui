package rexui_test

import (
	"slices"
	"sort"
	"testing"
	"time"

	"gonativeui/gpu"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// A small tree in pre-order:
//
//	0 A
//	1   A1
//	2     A1a
//	3   A2
//	4 B
//	5   B1
//	6 C
var (
	smallSize  = []uint32{4, 2, 1, 1, 2, 1, 1}
	smallDepth = []uint16{0, 1, 2, 1, 0, 1, 0}
)

// outlineView is an outline of tree in a 260 × 240 pt box at the window's
// top left, full width, so that row r spans y 48r to 48r+48 px.
type outlineView struct {
	tree base.Tree
	st   base.OutlineState
	ol   rexui.Outline
}

func newOutlineView(size []uint32, depth []uint16) *outlineView {
	v := &outlineView{tree: base.Tree{Size: size, Depth: depth}}
	v.st.Reserve(len(size))
	return v
}

func (v *outlineView) build(g *rig) {
	k := g.k
	c := k.Context()
	c.NextSize(ui.Px(k.Pt(260)), ui.Px(k.Pt(240)))
	c.Push(c.Box(0, "frame"))
	v.ol = rexui.BeginOutline(k, "Files", &v.st, &v.tree, rexui.OutlineOpts{Style: rexui.ListFullWidth})
	for r := v.ol.First; r < v.ol.Last; r++ {
		v.ol.Row(r, nil)
		v.ol.TextBytes([]byte("node"))
	}
	rexui.EndOutline(k, &v.ol)
	c.Pop()
}

func (v *outlineView) rows() []uint32 { return slices.Clone(v.st.Rows()) }

func (v *outlineView) selected() []uint32 {
	var out []uint32
	for it, ok := v.st.Sel.NextSet(0); ok; it, ok = v.st.Sel.NextSet(it + 1) {
		out = append(out, it)
	}
	return out
}

// The outline's keys and disclosures expand and collapse as NSOutlineView's:
// Right expands, Left collapses or goes to the parent, Option the whole
// subtree; a disclosure click does not select; a collapse deselects what it
// hides and selects the node instead.
func TestOutlineExpandsAndCollapses(t *testing.T) {
	v := newOutlineView(smallSize, smallDepth)
	g := newRig(t, theme.Light, nil, v.build)
	want := func(step string, rows, sel []uint32) {
		t.Helper()
		g.settle()
		if got := v.rows(); !slices.Equal(got, rows) {
			t.Fatalf("%s: rows %v, want %v", step, got, rows)
		}
		if got := v.selected(); !slices.Equal(got, sel) {
			t.Fatalf("%s: selection %v, want %v", step, got, sel)
		}
	}
	want("at first", []uint32{0, 4, 6}, nil)

	g.click(200, 24) // row 0, A
	want("click A", []uint32{0, 4, 6}, []uint32{0})
	g.key(ui.KeyRight, 0)
	want("Right", []uint32{0, 1, 3, 4, 6}, []uint32{0})
	g.key(ui.KeyDown, 0)
	g.key(ui.KeyRight, ui.ModAlt)
	want("Option-Right on A1", []uint32{0, 1, 2, 3, 4, 6}, []uint32{1})
	g.key(ui.KeyDown, 0)
	g.key(ui.KeyLeft, 0)
	want("Left on the leaf A1a", []uint32{0, 1, 2, 3, 4, 6}, []uint32{1})
	g.key(ui.KeyLeft, 0)
	want("Left on A1", []uint32{0, 1, 3, 4, 6}, []uint32{1})

	// a disclosure click expands B without touching the selection
	g.click(17, 3*48+24)
	want("click B's disclosure", []uint32{0, 1, 3, 4, 5, 6}, []uint32{1})

	// collapsing A hides the selected A1: A takes the selection
	g.key(ui.KeyRight, ui.ModAlt) // A1 and A1a open again
	g.click(17, 24)
	want("click A's disclosure", []uint32{0, 4, 5, 6}, []uint32{0})
	if !v.st.Expanded(1) {
		t.Fatal("collapsing A collapsed A1 too")
	}

	// Option-Left collapses the whole subtree
	g.key(ui.KeyRight, 0)
	g.key(ui.KeyLeft, ui.ModAlt)
	want("Option-Left on A", []uint32{0, 4, 5, 6}, []uint32{0})
	if v.st.Expanded(1) {
		t.Fatal("Option-Left left A1 expanded")
	}
}

// listView is a list of n rows with rows 1 and 2 selected, in style s, in a
// 300 × 240 pt box at the window's top left.
type listView struct {
	st    base.ListState
	style rexui.ListStyle
	n     int
}

func (v *listView) build(g *rig) {
	k := g.k
	c := k.Context()
	c.NextSize(ui.Px(k.Pt(300)), ui.Px(k.Pt(240)))
	c.Push(c.Box(0, "frame"))
	l := rexui.BeginList(k, "Notes", &v.st, v.n, rexui.ListOpts{Style: v.style})
	for r := l.First; r < l.Last; r++ {
		l.Row(r, v.st.Item(r), nil)
		rexui.LabelBytes(k, []byte("note"), rexui.LabelOpts{Color: l.Text()})
	}
	rexui.EndList(k, &l)
	c.Pop()
}

// selection is the shapes of the colour col in the draw list, top to bottom,
// and the colours of the glyphs drawn over them.
func selection(g *rig, col uint32) (shapes []gpu.Quad, text []uint32) {
	for _, q := range g.list.Quads {
		if q.Mode == gpu.ModeShape && q.Color[0]>>8 == col>>8 && q.Color[0]&0xFF != 0 {
			shapes = append(shapes, q)
		}
	}
	sort.Slice(shapes, func(i, j int) bool { return shapes[i].Rect[1] < shapes[j].Rect[1] })
	for _, q := range g.list.Quads {
		if q.Mode != gpu.ModeMaskR8 {
			continue
		}
		for _, s := range shapes {
			if q.Rect[1] >= s.Rect[1] && q.Rect[3] <= s.Rect[3] {
				text = append(text, q.Color[0])
				break
			}
		}
	}
	return shapes, text
}

// The selection is emphasised (the accent, white text) only while the list
// has the focus in the key window; an inset block of two rows is rounded at
// its ends only, with a line between them; nothing reacts to hover.
func TestListSelectionEmphasis(t *testing.T) {
	v := &listView{n: 6}
	v.st.Sel.Reserve(v.n)
	v.st.Sel.SelectRange(nil, 1, 2)
	g := newRig(t, theme.Light, nil, v.build)
	r := g.k.Theme()
	emph, unemph := r.Colors[theme.Selection], r.Colors[theme.SelectionUnemph]
	onSel, label := r.Colors[theme.OnSelection], r.Colors[theme.Label]

	check := func(step string, fill, fg uint32, other uint32) {
		t.Helper()
		g.settle()
		shapes, text := selection(g, fill)
		if len(shapes) != 2 {
			t.Fatalf("%s: %d selected rows in the selection's colour, want 2", step, len(shapes))
		}
		if o, _ := selection(g, other); len(o) != 0 {
			t.Fatalf("%s: %d rows in the other selection colour", step, len(o))
		}
		rad := shapes[0].Radii[0]
		if rad == 0 || shapes[0].Radii[3] != 0 || shapes[1].Radii[0] != 0 || shapes[1].Radii[3] != rad {
			t.Fatalf("%s: radii %v and %v, want rounded at the block's ends only", step, shapes[0].Radii, shapes[1].Radii)
		}
		if shapes[0].Rect[3] != shapes[1].Rect[1] {
			t.Fatalf("%s: rows %v and %v do not meet", step, shapes[0].Rect, shapes[1].Rect)
		}
		if len(text) == 0 {
			t.Fatalf("%s: no labels on the selection", step)
		}
		for _, c := range text {
			if c>>8 != fg>>8 {
				t.Fatalf("%s: label %08X on the selection, want %08X", step, c, fg)
			}
		}
	}
	check("unfocused", unemph, label, emph)

	g.click(200, 4*48) // an unselected row: the list takes the focus
	v.st.Sel.SelectRange(nil, 1, 2)
	g.settle()
	if !v.st.Sel.Has(1) || !v.st.Sel.Has(2) {
		t.Fatal("the selection did not take rows 1 and 2")
	}
	check("focused", emph, onSel, unemph)

	// hovering a row changes nothing
	n := len(g.list.Quads)
	g.in.MouseX, g.in.MouseY = 200, 20+5*48
	g.settle()
	if len(g.list.Quads) != n {
		t.Fatalf("hovering a row: %d quads, then %d", n, len(g.list.Quads))
	}

	g.in.Focused = false
	check("inactive window", unemph, label, emph)
	g.in.Focused = true
	check("key again", emph, onSel, unemph)

	// at rest, once the overlay scroll bar has faded, it asks for no frame
	g.frames(60)
	g.settle()
	if g.c.Animating() || g.c.WakeAfter() != 0 {
		t.Fatalf("at rest: animating %v, wake after %d ns", g.c.Animating(), g.c.WakeAfter())
	}
}

// bigTree is a tree of n nodes: roots of 999 children each.
func bigTree(n int) (size []uint32, depth []uint16) {
	size, depth = make([]uint32, n), make([]uint16, n)
	for i := 0; i < n; i += 1000 {
		size[i] = uint32(min(1000, n-i))
		for j := i + 1; j < i+int(size[i]); j++ {
			size[j], depth[j] = 1, 1
		}
	}
	return
}

// An outline of a million nodes, all expanded, builds its frames without
// allocating and in well under 4 ms: it builds the shown rows only, and
// makes the display order again only when the expansion changes.
func TestBigOutline(t *testing.T) {
	size, depth := bigTree(1_000_000)
	v := newOutlineView(size, depth)
	for i := 0; i < len(size); i += 1000 {
		v.st.Expand(&v.tree, uint32(i), true, false)
	}
	v.st.Sel.Only(500_000)
	g := newRig(t, theme.Light, nil, v.build)
	if n := len(v.st.Rows()); n != len(size) {
		t.Fatalf("%d rows shown, want %d", n, len(size))
	}
	g.k.Context().SetFocus(v.ol.Key())
	g.frames(3)
	if n := testing.AllocsPerRun(30, func() {
		g.in.PushKey(ui.KeyEvent{Code: ui.KeyDown, Down: true})
		g.frame()
	}); n != 0 {
		t.Fatalf("%.1f allocations per frame", n)
	}
	if cur, _ := v.st.Sel.Cursor(); cur <= 500_000 || v.ol.First < 400_000 {
		t.Fatalf("the cursor is at %d and the first row built %d: the keys did not reach the outline", cur, v.ol.First)
	}
	if testing.Short() {
		return
	}
	var times []time.Duration
	for range 40 {
		g.in.PushKey(ui.KeyEvent{Code: ui.KeyDown, Down: true})
		t0 := time.Now()
		g.frame()
		times = append(times, time.Since(t0))
	}
	slices.Sort(times)
	med := times[len(times)/2]
	t.Logf("frame: median %v, max %v", med, times[len(times)-1])
	if med > 4*time.Millisecond {
		t.Fatalf("median frame %v, want under 4 ms", med)
	}
	// a frame that makes the display order again, of a million rows
	v.st.TreeChanged()
	t0 := time.Now()
	g.frame()
	t.Logf("a frame that reorders a million rows: %v", time.Since(t0))
}
