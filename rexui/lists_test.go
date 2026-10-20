package rexui_test

import (
	"slices"
	"sort"
	"strconv"
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
	tree  base.Tree
	st    base.OutlineState
	ol    rexui.Outline
	names [][]byte // the nodes' accessible names, if set
	// changed: the selection changed in a frame
	changed bool
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
		var name []byte
		if v.names != nil {
			name = v.names[v.st.Rows()[r]]
		}
		v.ol.Row(r, name)
		v.ol.TextBytes([]byte("node"))
	}
	v.changed = v.changed || v.ol.Changed
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

// stretchTop pulls the view past its top with a trackpad, the fingers still
// down, by more than a row; it returns the stretch.
func stretchTop(t *testing.T, g *rig, list ui.Key, rowPx float32) float64 {
	t.Helper()
	g.in.MouseX, g.in.MouseY = 200, 150
	g.in.AddScrollEvent(ui.ScrollEvent{Phase: ui.PhaseMayBegin, Precise: true})
	g.frame()
	for i := 0; i < 30; i++ {
		p := ui.PhaseChanged
		if i == 0 {
			p = ui.PhaseBegan
		}
		g.in.AddScrollEvent(ui.ScrollEvent{DY: 40, Phase: p, Precise: true})
		g.frame()
	}
	id, _ := g.c.BoxOf(list)
	_, s := g.c.ScrollStretch(id)
	if -s < float64(rowPx) {
		t.Fatalf("stretch %v: not past a row", s)
	}
	return s
}

// groups is a tree of n collapsed groups of two leaves each: display row r
// shows group node 3r.
func groups(n int) (size []uint32, depth []uint16) {
	for range n {
		size = append(size, 3, 1, 1)
		depth = append(depth, 0, 1, 1)
	}
	return
}

// While a trackpad stretches an outline past its top, a click on a
// disclosure as shown expands that row's node, and the keys reveal the
// cursor's row where the stretch shows it.
func TestStretchedOutline(t *testing.T) {
	v := newOutlineView(groups(30))
	v.st.Sel.Only(0)
	g := newRig(t, theme.Light, nil, v.build)
	g.c.SetFocus(v.ol.Key())
	g.settle()
	rowPx := v.st.RowPx()
	stretchTop(t, g, v.ol.Key(), rowPx)
	top := g.c.RectOf(v.ol.Key())[1]
	if v.st.RowTop(3) < v.st.PadPx()+4*rowPx {
		t.Fatalf("row 3 shows at %v: the stretch does not move it a row down", v.st.RowTop(3))
	}
	g.click(17, top+v.st.RowTop(3)+rowPx/2) // row 3's disclosure, as shown
	if !v.st.Expanded(9) {
		for n := uint32(0); n < 90; n += 3 {
			if v.st.Expanded(n) {
				t.Fatalf("clicked row 3's disclosure as shown, expanded node %d", n)
			}
		}
		t.Fatal("clicked row 3's disclosure as shown, nothing expanded")
	}
	if got := v.selected(); !slices.Equal(got, []uint32{0}) {
		t.Fatalf("the disclosure click changed the selection to %v", got)
	}

	// the last row the offset shows whole, which the stretch pushes out
	r := g.c.RectOf(v.ol.Key())
	view := r[3] - r[1]
	row := int((view-v.st.PadPx())/rowPx) - 1
	if b := v.st.RowTop(row) + rowPx; b <= view {
		t.Fatalf("row %d ends at %v in a %v view: the stretch does not hide it", row, b, view)
	}
	v.st.Sel.Only(v.st.Rows()[row-1])
	v.st.Sel.SetCursor(v.st.Rows()[row-1])
	g.key(ui.KeyDown, 0)
	g.frames(30) // the fingers stay down
	if got := v.selected(); !slices.Equal(got, []uint32{v.st.Rows()[row]}) {
		t.Fatalf("Down selected %v, want row %d's node %d", got, row, v.st.Rows()[row])
	}
	if top := v.st.RowTop(row); top < 0 || top+rowPx > view+0.5 {
		t.Fatalf("row %d shows at %v–%v in a %v view", row, top, top+rowPx, view)
	}
}

// listView is a list of n rows with rows 1 and 2 selected, in style s, in a
// 300 × 240 pt box at the window's top left.
type listView struct {
	st      base.ListState
	style   rexui.ListStyle
	striped bool
	n       int
	key     ui.Key // the list's, as of the last frame
}

func (v *listView) build(g *rig) {
	k := g.k
	c := k.Context()
	c.NextSize(ui.Px(k.Pt(300)), ui.Px(k.Pt(240)))
	c.Push(c.Box(0, "frame"))
	l := rexui.BeginList(k, "Notes", &v.st, v.n, rexui.ListOpts{Style: v.style, Striped: v.striped})
	for r := l.First; r < l.Last; r++ {
		l.Row(r, v.st.Item(r), nil)
		rexui.LabelBytes(k, []byte("note"), rexui.LabelOpts{Color: l.Text()})
	}
	v.key = l.Key()
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

// With the system's legacy scrollers ("Show scroll bars: Always") a list
// that overflows shows its bar at rest, in room beside its rows; with
// overlay scrollers the bar is hidden at rest and the rows take the width.
func TestListFollowsScrollerStyle(t *testing.T) {
	v := &listView{n: 40, style: rexui.ListFullWidth}
	v.st.Sel.Reserve(v.n)
	legacy := theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: theme.ScrollerLegacy}}
	g := newRig(t, theme.Light, &legacy, v.build)
	rowWidth := func() (w float32, bar bool) {
		r := g.k.Theme()
		for _, q := range g.list.Quads {
			if q.Mode != gpu.ModeShape || q.Color[0]&0xFF == 0 {
				continue
			}
			if c := q.Color[0] >> 8; c == r.Pal.ScrollThumb>>8 || c == rexui.ListLook.LegacyKnob.RGBA(r)>>8 {
				bar = true
			}
		}
		rect := g.c.RectOf(v.key)
		return rect[2] - rect[0], bar
	}
	g.frames(90)
	g.settle()
	wl, bar := rowWidth()
	if !bar {
		t.Fatal("legacy scrollers: no bar at rest")
	}
	g.k.PinSystem(&theme.SystemValues{Known: true})
	g.frames(90)
	g.settle()
	wo, bar := rowWidth()
	if bar {
		t.Fatal("overlay scrollers: a bar at rest")
	}
	if bw := theme.Snap(g.k.Theme().Px.ScrollbarW); wo-wl != bw {
		t.Fatalf("the rows are %v wide beside a legacy bar and %v without, want %v less", wl, wo, bw)
	}
}

// The stripes below the last row of a striped list are not content: an empty
// (or short) list is not scrollable, has no scroll bar even with legacy
// scrollers, and still shows its stripes.
func TestFillerStripesAreNotContent(t *testing.T) {
	for _, n := range []int{0, 3} {
		v := &listView{n: n, striped: true}
		v.st.Sel.Reserve(max(n, 1))
		legacy := theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: theme.ScrollerLegacy}}
		g := newRig(t, theme.Light, &legacy, v.build)
		g.settle()
		id, ok := g.c.BoxOf(v.key)
		if !ok {
			t.Fatal("no list box")
		}
		content, view := g.c.ScrollExtent(id)
		if content[1] > view[1] {
			t.Fatalf("%d rows: %v px of content in a %v px view: the stripes scroll", n, content[1], view[1])
		}
		r := g.k.Theme()
		stripe, knob := rexui.ListLook.Stripe.RGBA(r)>>8, rexui.ListLook.LegacyKnob.RGBA(r)>>8
		stripes := 0
		for _, q := range g.list.Quads {
			if q.Mode != gpu.ModeShape || q.Color[0]&0xFF == 0 {
				continue
			}
			switch q.Color[0] >> 8 {
			case knob:
				t.Fatalf("%d rows: a scroll bar", n)
			case stripe:
				stripes++
			}
		}
		if stripes < 3 {
			t.Fatalf("%d rows: %d stripes in a 240 pt list", n, stripes)
		}
	}
}

// named gives the nodes of v the names n0, n1 …
func (v *outlineView) named() *outlineView {
	for i := range v.tree.Size {
		v.names = append(v.names, []byte("n"+strconv.Itoa(i)))
	}
	return v
}

// An assistive technology's expand and collapse requests reach a styled
// outline's rows: the row of a collapsed node offers ActExpand, and the
// request expands it; ActCollapse collapses it again.
func TestOutlineTakesExpandAndCollapseRequests(t *testing.T) {
	v := newOutlineView(smallSize, smallDepth).named()
	g := newRig(t, theme.Light, nil, v.build)
	g.settle()
	for _, step := range []struct {
		act  ui.SemAction
		open bool
	}{{ui.ActExpand, true}, {ui.ActCollapse, false}} {
		n := g.node(ui.RoleListItem, "n0")
		if n.Actions&step.act == 0 {
			t.Fatalf("the row of node 0 does not offer action %v (it offers %v)", step.act, n.Actions)
		}
		g.in.PushAction(ui.SemRequest{Key: n.Key, Action: step.act})
		g.frame()
		g.settle()
		if v.st.Expanded(0) != step.open {
			t.Fatalf("after action %v node 0 is expanded: %v", step.act, v.st.Expanded(0))
		}
	}
}

// An outline's rows tell assistive technologies their depth in the tree
// (SemNode.Level, 1 for a root) and whether they show their children
// (SemExpanded); a list's rows are in no tree.
func TestOutlineRowsExportTheirLevel(t *testing.T) {
	v := newOutlineView(smallSize, smallDepth).named()
	v.st.Expand(&v.tree, 0, true, false)
	v.st.Expand(&v.tree, 1, true, false)
	g := newRig(t, theme.Light, nil, v.build)
	g.settle()
	for _, w := range []struct {
		name     string
		level    int32
		expanded bool
	}{{"n0", 1, true}, {"n1", 2, true}, {"n2", 3, false}, {"n3", 2, false}, {"n4", 1, false}, {"n6", 1, false}} {
		n := g.node(ui.RoleListItem, w.name)
		if n.Level != w.level || (n.State&ui.SemExpanded != 0) != w.expanded {
			t.Errorf("%s: level %d, expanded %v; want %d, %v", w.name, n.Level, n.State&ui.SemExpanded != 0, w.level, w.expanded)
		}
	}

	l := &listView{n: 3}
	l.st.Sel.Reserve(3)
	lg := newRig(t, theme.Light, nil, l.build)
	lg.settle()
	lg.c.Semantics(&lg.sem)
	for _, n := range lg.sem.Nodes {
		if n.Role == ui.RoleListItem && n.Level != 0 {
			t.Fatalf("a list's row reports level %d", n.Level)
		}
	}
}

// Collapsing a node whose hidden child is selected moves the selection to
// the node, and the outline reports the change (Changed), whichever way the
// collapse comes: a disclosure click or an assistive technology's request.
func TestOutlineCollapseReportsTheSelectionChange(t *testing.T) {
	for _, how := range []string{"disclosure", "action"} {
		v := newOutlineView(smallSize, smallDepth).named()
		v.st.Expand(&v.tree, 0, true, false)
		v.st.Sel.Only(1)
		v.st.Sel.SetCursor(1)
		g := newRig(t, theme.Light, nil, v.build)
		g.settle()
		v.changed = false
		if how == "disclosure" {
			g.click(17, 24) // node 0's disclosure, row 0
		} else {
			g.in.PushAction(ui.SemRequest{Key: g.node(ui.RoleListItem, "n0").Key, Action: ui.ActCollapse})
			g.frames(3)
		}
		if v.st.Expanded(0) {
			t.Fatalf("%s: node 0 did not collapse", how)
		}
		if got := v.selected(); !slices.Equal(got, []uint32{0}) {
			t.Fatalf("%s: selection %v, want the collapsed node", how, got)
		}
		if !v.changed {
			t.Fatalf("%s: the selection changed, and Changed never said so", how)
		}
	}
}
