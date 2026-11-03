package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// navFixture is a search field over a NavList of rows of three heights, a
// header every ten rows.
type navFixture struct {
	nav       base.ListNav
	q         ui.TextBuffer
	names     []string
	o         rexui.NavListOpts
	last      base.NavSignal
	activated int
	cancelled bool
	// unsized makes the list's view 0 high in the first frame (a pane that
	// glides open), its rows laid out all the same
	unsized bool
}

func newNavFixture(o rexui.NavListOpts) *navFixture {
	f := &navFixture{o: o, activated: -1}
	words := []string{"Ávila", "Bento", "Coração", "Dália", "Érica", "Fábio", "Gênio", "Hélio", "Íris"}
	for i := range 40 {
		if navHeader(i) {
			f.names = append(f.names, "Section "+string(rune('A'+i/10)))
			continue
		}
		f.names = append(f.names, words[i%10-1]+" "+string(rune('a'+i/10)))
	}
	return f
}

func navHeader(i int) bool { return i%10 == 0 }

// navHeight is row i's height in points: 20, 36 or 52.
func navHeight(i int) float32 { return 20 + float32(i%3)*16 }

func (f *navFixture) build(g *rig) {
	k, c := g.k, g.k.Context()
	theme.At(c, 0, 0, 600, 400)
	c.NextAxis(ui.AxisY)
	c.Push(c.Box(base.Floating, "root"))
	c.NextFieldCommander(&f.nav, "")
	if rexui.SearchField(k, "Find", &f.q, "Find", rexui.FieldOpts{}).Cancelled {
		f.cancelled = true
	}
	h := ui.Grow(1)
	if f.unsized {
		h, f.unsized = ui.Px(0), false
	}
	k.NextSize(ui.Pct(1), h)
	l := rexui.BeginNavList(k, "Results", &f.nav, len(f.names), f.o)
	for i, name := range f.names {
		if navHeader(i) {
			l.Header(i, name)
			continue
		}
		row := l.Row(i, name, rexui.ListRowOpts{Height: navHeight(i)})
		rexui.Label(k, name, rexui.LabelOpts{Color: row.Text()})
		l.EndRow()
	}
	f.last = rexui.EndNavList(k, &l)
	if f.last.Activated {
		f.activated = f.last.Row
	}
	c.Pop()
}

// rowBounds is the window rectangle of the row named name.
func rowBounds(g *rig, name string) [4]float32 { return g.node(ui.RoleListItem, name).Bounds }

// selected is the name of the row that reports itself selected.
func selected(g *rig) string {
	g.c.Semantics(&g.sem)
	for _, n := range g.sem.Nodes {
		if n.Role == ui.RoleListItem && n.State&ui.SemSelected != 0 {
			return string(g.sem.Text(n.Name))
		}
	}
	return ""
}

// TestNavListArrowsFromAFocusedSearchField: with the caret in the search
// field, Down and Up move the list's selection, as keys (Windows, Linux) and
// as the edit actions macOS delivers, past the headers; the field's text and
// caret stay; Enter activates the selected row.
func TestNavListArrowsFromAFocusedSearchField(t *testing.T) {
	f := newNavFixture(rexui.NavListOpts{NoFocus: true})
	g := newRig(t, theme.Light, nil, f.build)
	g.settle()
	if f.nav.Sel != 1 {
		t.Fatalf("the first selection is row %d, want 1 (row 0 is a header)", f.nav.Sel)
	}
	g.click(centre(g.node(ui.RoleSearchField, "Find")))
	g.typeText("x")
	g.settle()
	f.q.SetString("")
	g.settle()
	for range 9 {
		g.key(ui.KeyDown, 0)
	}
	g.settle()
	if f.nav.Sel != 11 {
		t.Fatalf("nine Downs from row 1 reached row %d, want 11 (row 10 is a header)", f.nav.Sel)
	}
	g.in.PushEdit(ui.EditMoveUp) // macOS: moveUp: from the field's key bindings
	g.frame()
	g.settle()
	if f.nav.Sel != 9 {
		t.Errorf("Up as an edit action reached row %d, want 9", f.nav.Sel)
	}
	g.in.PushEdit(ui.EditMoveDown)
	g.frame()
	g.in.PushKey(ui.KeyEvent{Code: ui.KeyPageDown, Down: true})
	g.frame()
	g.settle()
	if f.nav.Sel <= 11 {
		t.Errorf("Page Down from the field left the selection at %d", f.nav.Sel)
	}
	if got := selected(g); got != f.names[f.nav.Sel] {
		t.Errorf("the tree says %q is selected, want %q", got, f.names[f.nav.Sel])
	}
	if f.q.String() != "" {
		t.Errorf("the keys typed %q into the field", f.q.String())
	}
	want := f.nav.Sel
	g.key(ui.KeyEnter, 0)
	g.settle()
	if f.activated != want {
		t.Errorf("Enter in the field activated row %d, want %d", f.activated, want)
	}
}

// TestNavListPagesByTheRowsHeights: Page Down and Page Up move by what the
// view shows of rows of mixed heights: the row reached ends within a view's
// height of the one left, the next would not, and it is in view.
func TestNavListPagesByTheRowsHeights(t *testing.T) {
	f := newNavFixture(rexui.NavListOpts{})
	g := newRig(t, theme.Light, nil, f.build)
	g.settle()
	g.click(centre(g.node(ui.RoleListItem, f.names[1])))
	g.settle()
	list := g.node(ui.RoleList, "Results").Bounds
	viewH := list[3] - list[1]
	for step := range 3 {
		from := f.nav.Sel
		fb := rowBounds(g, f.names[from])
		g.key(ui.KeyPageDown, 0)
		g.settle()
		to := f.nav.Sel
		if to <= from {
			t.Fatalf("page %d: Page Down from %d reached %d", step, from, to)
		}
		tb := rowBounds(g, f.names[to])
		// positions in the content: the view scrolled between the two reads
		fTop := fb[1]
		shift := rowBounds(g, f.names[from])[1] - fTop
		if tb[3]-shift-fTop > viewH+1 {
			t.Errorf("page %d: row %d ends %.0f px below row %d, more than the view's %.0f", step, to, tb[3]-shift-fTop, from, viewH)
		}
		if next := to + 1; next < len(f.names) && !navHeader(next) {
			nb := rowBounds(g, f.names[next])
			if nb[3]-shift-fTop <= viewH-1 {
				t.Errorf("page %d: row %d after %d would still fit (%.0f of %.0f px)", step, next, to, nb[3]-shift-fTop, viewH)
			}
		}
		if tb[1] < list[1]-0.5 || tb[3] > list[3]+0.5 {
			t.Errorf("page %d: row %d is out of view: %v in %v", step, to, tb, list)
		}
	}
	far := f.nav.Sel
	g.key(ui.KeyPageUp, 0)
	g.settle()
	if f.nav.Sel >= far {
		t.Errorf("Page Up from %d reached %d", far, f.nav.Sel)
	}
	g.key(ui.KeyEnd, 0)
	g.settle()
	if f.nav.Sel != len(f.names)-1 {
		t.Errorf("End reached %d", f.nav.Sel)
	}
	g.key(ui.KeyHome, 0)
	g.settle()
	if f.nav.Sel != 1 {
		t.Errorf("Home reached %d, want 1 (past the header)", f.nav.Sel)
	}
}

// TestNavListSelectedFromTheStartDoesNotScroll: a selection the app makes
// before the list's first frame (the open conversation of a sidebar) on a row
// that shows leaves the list where it is, though the view had no height in
// the first frame: the reveal waits for the view to be laid out instead of
// taking it as no height and scrolling the row to its top.
func TestNavListSelectedFromTheStartDoesNotScroll(t *testing.T) {
	f := newNavFixture(rexui.NavListOpts{})
	f.unsized = true
	f.nav.Select(3)
	g := newRig(t, theme.Light, nil, f.build)
	g.settle()
	list := g.node(ui.RoleList, "Results").Bounds
	first := rowBounds(g, f.names[1])
	if first[1] < list[1]-0.5 {
		t.Errorf("the list scrolled: the first row is at %v in %v", first, list)
	}
	if b := rowBounds(g, f.names[3]); b[1] < list[1]-0.5 || b[3] > list[3]+0.5 {
		t.Errorf("row 3 at %v is out of view in %v", b, list)
	}
}

// TestNavListKeepsTheSelectionBelowATitleBand: a row the keys select is
// brought into view below what covers the top of the view.
func TestNavListKeepsTheSelectionBelowATitleBand(t *testing.T) {
	f := newNavFixture(rexui.NavListOpts{Top: 120})
	g := newRig(t, theme.Light, nil, f.build)
	g.settle()
	g.click(centre(g.node(ui.RoleListItem, f.names[1])))
	g.key(ui.KeyEnd, 0)
	g.settle()
	list := g.node(ui.RoleList, "Results").Bounds
	for _, r := range []int{25, 3, 1} {
		for f.nav.Sel > r {
			g.key(ui.KeyUp, 0)
		}
		g.settle()
		b := rowBounds(g, f.names[f.nav.Sel])
		if b[1] < list[1]+120-0.5 || b[3] > list[3]+0.5 {
			t.Errorf("row %d at %v: not in view below the band (list %v)", f.nav.Sel, b, list)
		}
	}
}

// TestNavListTypeToFind: typing into the focused list selects the next row
// whose name starts with what was typed, ignoring case and accents; a header
// is never found.
func TestNavListTypeToFind(t *testing.T) {
	f := newNavFixture(rexui.NavListOpts{})
	g := newRig(t, theme.Light, nil, f.build)
	g.settle()
	g.click(centre(g.node(ui.RoleListItem, f.names[1])))
	g.typeText("cor")
	g.settle()
	if f.nav.Sel != 3 {
		t.Errorf("\"cor\" selected row %d (%q), want 3 (Coração)", f.nav.Sel, f.names[max(f.nav.Sel, 0)])
	}
	for range 70 { // a pause ends the query
		g.frame()
	}
	g.typeText("e")
	g.settle()
	if f.nav.Sel != 5 {
		t.Errorf("\"e\" selected row %d, want 5 (Érica)", f.nav.Sel)
	}
	for range 70 {
		g.frame()
	}
	g.typeText("e")
	g.settle()
	if f.nav.Sel != 15 {
		t.Errorf("\"e\" again selected row %d, want 15 (the next Érica)", f.nav.Sel)
	}
	for range 70 {
		g.frame()
	}
	g.typeText("sec")
	g.settle()
	if navHeader(f.nav.Sel) {
		t.Errorf("type-to-find selected header %d", f.nav.Sel)
	}
}

// TestNavListFollowsThePointerOnlyWhenItMoves: with FollowPointer the row the
// pointer moves onto is selected; when the keys scroll rows under a pointer
// that stands still, the keyboard's selection stays.
func TestNavListFollowsThePointerOnlyWhenItMoves(t *testing.T) {
	f := newNavFixture(rexui.NavListOpts{FollowPointer: true, NoFocus: true})
	g := newRig(t, theme.Light, nil, f.build)
	g.settle()
	x, y := centre(g.node(ui.RoleListItem, f.names[4]))
	g.in.MouseX, g.in.MouseY = x, y
	g.settle()
	if f.nav.Sel != 4 {
		t.Fatalf("the pointer moved onto row 4, the selection is %d", f.nav.Sel)
	}
	g.click(centre(g.node(ui.RoleSearchField, "Find")))
	g.in.MouseX, g.in.MouseY = x, y // back over the rows, then still
	g.settle()
	for range 12 {
		g.key(ui.KeyDown, 0)
	}
	g.settle()
	want := f.nav.Sel
	if want == 4 {
		t.Fatal("the keys did not move the selection")
	}
	g.frames(10)
	if f.nav.Sel != want {
		t.Errorf("rows scrolling under a still pointer moved the selection from %d to %d", want, f.nav.Sel)
	}
}

// TestNavStep: the moves every list shares, with rows that cannot be
// selected.
func TestNavStep(t *testing.T) {
	ok := func(r int) bool { return r != 0 && r != 3 && r != 9 }
	for _, c := range []struct {
		sel  int
		m    base.NavMove
		page int
		wrap bool
		want int
	}{
		{-1, base.NavDown, 0, false, 1},
		{-1, base.NavUp, 0, true, 8},
		{-1, base.NavEnd, 0, false, 8},
		{2, base.NavDown, 0, false, 4},
		{4, base.NavUp, 0, false, 2},
		{1, base.NavUp, 0, false, 1},
		{1, base.NavUp, 0, true, 8},
		{8, base.NavDown, 0, true, 1},
		{8, base.NavDown, 0, false, 8},
		{1, base.NavPageDown, 2, false, 4},
		{8, base.NavPageUp, 5, false, 2},
		{8, base.NavPageDown, 5, false, 8},
		{5, base.NavHome, 0, false, 1},
	} {
		if got := base.NavStep(c.sel, 10, ok, c.m, c.page, c.wrap); got != c.want {
			t.Errorf("NavStep(%d, move %d, page %d, wrap %v) = %d, want %d", c.sel, c.m, c.page, c.wrap, got, c.want)
		}
	}
	if got := base.NavStep(0, 3, func(int) bool { return false }, base.NavDown, 0, false); got != -1 {
		t.Errorf("no selectable row: %d", got)
	}
}

// TestFold: matching ignores case and accents, composed or not.
func TestFold(t *testing.T) {
	for _, c := range []struct {
		s, sub string
		want   bool
	}{
		{"Página de preços", "precos", true},
		{"Zé", "ze", true},
		{"Café", "café", true},
		{"ÁVILA", "avi", true},
		{"Bento", "ent", true},
		{"Bento", "x", false},
	} {
		if got := base.FoldContains(c.s, c.sub); got != c.want {
			t.Errorf("FoldContains(%q, %q) = %v", c.s, c.sub, got)
		}
	}
	if !base.FoldWordPrefix("Nova conversa", "conv") || base.FoldWordPrefix("Nova conversa", "onv") {
		t.Error("FoldWordPrefix")
	}
	if !base.FoldHasPrefix("Érica", "eri") || base.FoldHasPrefix("Érica", "ric") {
		t.Error("FoldHasPrefix")
	}
}
