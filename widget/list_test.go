package widget

import (
	"slices"
	"testing"

	"gonativeui/ui"
)

// bigTable is a table over n items, for the tests: a name column, a size
// column and a flexible one.
type bigTable struct {
	n      int
	st     TableState
	cols   [3]Column
	order  []uint32
	t      Table
	sorted []SortState
	found  []byte
	active []uint32
	ctx    []uint32
}

func newBigTable(t *testing.T, n int) (*rig, *bigTable) {
	v := &bigTable{n: n}
	v.cols = [3]Column{
		{ID: 10, Title: "Name", Width: 150, Sortable: true},
		{ID: 11, Title: "Size", Width: 80, Align: ui.AlignRight, Sortable: true},
		{ID: 12, Title: "Kind", Width: 60, Fill: true},
	}
	v.st.Sel.Reserve(n)
	return newRig(t, 500, 400, v.build), v
}

func (v *bigTable) reverse() {
	if v.order == nil {
		v.order = make([]uint32, v.n)
		for i := range v.order {
			v.order[i] = uint32(i)
		}
	}
	slices.Reverse(v.order)
	v.st.Order = v.order
}

func (v *bigTable) build(k *Kit) {
	c := k.Context()
	t := k.BeginTable("files", &v.st, v.cols[:], v.n)
	if t.SortChanged {
		v.sorted = append(v.sorted, t.Sort)
		v.reverse()
	}
	if t.Activated {
		v.active = append(v.active, t.Item)
	}
	if t.RightClicked {
		v.ctx = append(v.ctx, t.ContextItem)
	}
	if t.Find != nil {
		v.found = append(v.found[:0], t.Find...)
	}
	for r := t.First; r < t.Last; r++ {
		item := uint32(r)
		if v.order != nil {
			item = v.order[r]
		}
		t.Row(r, item)
		t.BeginCell(0)
		k.LabelBytes(c.Fmt().Str("file ").Int(int(item)).Bytes())
		t.EndCell()
		t.CellBytes(1, c.Fmt().Int(int(item)*10).Bytes())
		t.CellText(2, "doc")
	}
	k.EndTable(&t)
	v.t = t
}

// rowCentre is the pixel centre of display row r when it is on screen.
func (g *rig) rowCentre(role ui.Role, r int) (x, y float32) {
	g.t.Helper()
	tree := g.semantics()
	for _, n := range tree.Nodes {
		if n.Role == role && int(n.Pos[0]) == r {
			return centre(n)
		}
	}
	g.t.Fatalf("row %d is not on screen", r)
	return
}

func TestTableVirtualRowsReportTheirPosition(t *testing.T) {
	g, _ := newBigTable(t, 1_000_000)
	tree := g.semantics()
	var rows []ui.SemNode
	for _, n := range tree.Nodes {
		if n.Role == ui.RoleRow {
			rows = append(rows, n)
		}
	}
	if len(rows) < 5 || len(rows) > 60 {
		t.Fatalf("%d rows built for a 1M-row table, want a screenful", len(rows))
	}
	for i, n := range rows {
		if int(n.Pos[0]) != i || n.Pos[1] != 1_000_000 {
			t.Fatalf("row %d reports position %v, want [%d 1000000]", i, n.Pos, i)
		}
	}
	// scroll far down: the rows built are the ones in view, with their real positions
	g.c.SetScroll(g.mustID(t, "files"), 0, 400_000*24)
	g.frames(3)
	tree = g.semantics()
	first := -1
	for _, n := range tree.Nodes {
		if n.Role == ui.RoleRow {
			if first < 0 {
				first = int(n.Pos[0])
			}
			if n.Pos[1] != 1_000_000 {
				t.Fatalf("total = %d", n.Pos[1])
			}
		}
	}
	if first < 399_990 || first > 400_000 {
		t.Fatalf("after scrolling to row 400000 the first built row is %d", first)
	}
}

// mustID finds the list box (the node that scrolls) by the table's label.
func (g *rig) mustID(t *testing.T, name string) ui.ID {
	t.Helper()
	n := g.must(ui.RoleTable, name)
	_ = n
	id, ok := g.c.BoxOf(ui.KeyN(ui.HashString(n.Key, name), 1))
	if !ok {
		t.Fatal("no list box")
	}
	return id
}

func TestTableClickShiftAndToggleSelect(t *testing.T) {
	g, v := newBigTable(t, 1000)
	x, y := g.rowCentre(ui.RoleRow, 2)
	g.click(x, y)
	if !slices.Equal(items(&v.st.Sel), []uint32{2}) {
		t.Fatalf("click on row 2 selected %v", items(&v.st.Sel))
	}
	x, y = g.rowCentre(ui.RoleRow, 5)
	g.in.Mods = ui.ModShift
	g.click(x, y)
	g.in.Mods = 0
	if got := items(&v.st.Sel); !slices.Equal(got, []uint32{2, 3, 4, 5}) {
		t.Fatalf("shift-click on row 5 selected %v, want 2..5", got)
	}
	x, y = g.rowCentre(ui.RoleRow, 9)
	g.in.Mods = ui.PrimaryMod()
	g.click(x, y)
	g.in.Mods = 0
	if got := items(&v.st.Sel); !slices.Equal(got, []uint32{2, 3, 4, 5, 9}) {
		t.Fatalf("primary-click on row 9 gave %v", got)
	}
	if n := g.rowNode(ui.RoleRow, 9); n.State&ui.SemSelected == 0 {
		t.Fatal("a selected row exports no selected state")
	}
	if n := g.rowNode(ui.RoleRow, 8); n.State&ui.SemSelected != 0 {
		t.Fatal("an unselected row exports a selected state")
	}
}

func (g *rig) rowNode(role ui.Role, r int) ui.SemNode {
	g.t.Helper()
	for _, n := range g.semantics().Nodes {
		if n.Role == role && int(n.Pos[0]) == r {
			return n
		}
	}
	g.t.Fatalf("row %d is not on screen", r)
	return ui.SemNode{}
}

func TestTableKeyboardNavigation(t *testing.T) {
	g, v := newBigTable(t, 1_000_000)
	x, y := g.rowCentre(ui.RoleRow, 0)
	g.click(x, y) // focus the list
	cursor := func() uint32 { c, _ := v.st.Sel.Cursor(); return c }
	g.key(ui.KeyDown, 0)
	g.key(ui.KeyDown, 0)
	if cursor() != 2 || !slices.Equal(items(&v.st.Sel), []uint32{2}) {
		t.Fatalf("two Downs: cursor %d, selection %v", cursor(), items(&v.st.Sel))
	}
	g.key(ui.KeyDown, ui.ModShift)
	g.key(ui.KeyDown, ui.ModShift)
	if got := items(&v.st.Sel); !slices.Equal(got, []uint32{2, 3, 4}) {
		t.Fatalf("shift+Down twice selected %v", got)
	}
	g.key(ui.KeyUp, ui.ModShift)
	if got := items(&v.st.Sel); !slices.Equal(got, []uint32{2, 3}) {
		t.Fatalf("shift+Up shrank the range to %v", got)
	}
	g.key(ui.KeyPageDown, 0)
	if c := cursor(); c < 10 || c > 40 {
		t.Fatalf("Page Down moved the cursor to %d", c)
	}
	g.key(ui.KeyEnd, 0)
	g.frames(90)
	if cursor() != 999_999 {
		t.Fatalf("End: cursor %d", cursor())
	}
	// the cursor row is scrolled into view
	if n := g.rowNode(ui.RoleRow, 999_999); n.Bounds[3] > float32(g.h)+1 {
		t.Fatalf("End left the last row at %v", n.Bounds)
	}
	g.key(ui.KeyHome, ui.ModShift)
	g.frames(5)
	if v.st.Sel.Count() != 1_000_000 {
		t.Fatalf("shift+Home from the last row selected %d, want all 1M", v.st.Sel.Count())
	}
	g.key('A', ui.PrimaryMod())
	if v.st.Sel.Count() != 1_000_000 {
		t.Fatalf("select all: %d", v.st.Sel.Count())
	}
	g.key(ui.KeyEnter, 0)
	if len(v.active) == 0 {
		t.Fatal("Enter on the cursor row did not activate")
	}
}

func TestTableSortKeepsSelectionAndKeys(t *testing.T) {
	g, v := newBigTable(t, 1000)
	x, y := g.rowCentre(ui.RoleRow, 3)
	g.click(x, y)
	g.in.Mods = ui.ModShift
	x, y = g.rowCentre(ui.RoleRow, 5)
	g.click(x, y)
	g.in.Mods = 0
	hx, hy := centre(g.must(ui.RoleColumnHeader, "Name"))
	g.click(hx, hy)
	if len(v.sorted) != 1 || !v.sorted[0].Active || v.sorted[0].Column != 10 || v.sorted[0].Descending {
		t.Fatalf("header click gave sort changes %v", v.sorted)
	}
	g.frames(3)
	// the app reversed its order; the selection is by item and did not move
	if got := items(&v.st.Sel); !slices.Equal(got, []uint32{3, 4, 5}) {
		t.Fatalf("selection after sorting: %v, want items 3..5", got)
	}
	// the rows for those items are now at the other end, and are shown selected
	for _, n := range g.semantics().Nodes {
		if n.Role == ui.RoleRow && n.State&ui.SemSelected != 0 && int(n.Pos[0]) < 990 {
			t.Fatalf("row %d is selected after the sort, its item is not", n.Pos[0])
		}
	}
	// a second click flips the direction
	g.click(hx, hy)
	if len(v.sorted) != 2 || !v.sorted[1].Descending {
		t.Fatalf("second click: %v", v.sorted)
	}
	// Shift+Down continues from the anchor by display row in the new order
	g.key(ui.KeyDown, ui.ModShift)
	if v.st.Sel.Count() < 1 {
		t.Fatal("navigation after a sort lost the selection")
	}
}

func TestTableResizeColumnHonoursMinimum(t *testing.T) {
	g, v := newBigTable(t, 100)
	hdr := g.must(ui.RoleColumnHeader, "Name")
	edge := hdr.Bounds[2]
	y := (hdr.Bounds[1] + hdr.Bounds[3]) / 2
	g.in.MouseX, g.in.MouseY = edge, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.MouseX = edge + 40
	g.frames(2)
	if w := v.cols[0].Width; w < 185 || w > 195 {
		t.Fatalf("dragging the edge 40 px right gave width %v, want about 190", w)
	}
	g.in.MouseX = edge - 400
	g.frames(2)
	if w := v.cols[0].Width; w != defaultMinColumnPt {
		t.Fatalf("dragging far left gave width %v, want the minimum %d", w, defaultMinColumnPt)
	}
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frames(2)
	if g.must(ui.RoleColumnHeader, "Name").Bounds[2]-g.must(ui.RoleColumnHeader, "Name").Bounds[0] < defaultMinColumnPt-1 {
		t.Fatal("the header cell is narrower than the minimum")
	}
}

func TestTableRightClickSelectsAndReports(t *testing.T) {
	g, v := newBigTable(t, 100)
	x, y := g.rowCentre(ui.RoleRow, 4)
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonRight, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonRight, false, 0)
	g.frame()
	if !slices.Equal(v.ctx, []uint32{4}) {
		t.Fatalf("right click reported items %v, want [4]", v.ctx)
	}
	if !v.st.Sel.Has(4) {
		t.Fatal("right click on an unselected row did not select it")
	}
}

func TestListTypeToFindReportsQuery(t *testing.T) {
	g, v := newBigTable(t, 100)
	x, y := g.rowCentre(ui.RoleRow, 0)
	g.click(x, y)
	g.typ("ab")
	if string(v.found) != "ab" {
		t.Fatalf("type-to-find query %q, want ab", v.found)
	}
	// the app answers by moving the cursor
	v.st.GoTo(7)
	g.frames(2)
	if c, _ := v.st.Sel.Cursor(); c != 7 || !slices.Equal(items(&v.st.Sel), []uint32{7}) {
		t.Fatalf("GoTo(7): cursor %d, selection %v", c, items(&v.st.Sel))
	}
}

// The rows of a disabled list are disabled: they export no actions and an
// assistive technology's press does not select them.
func TestDisabledListRowsRejectAccessibilityActions(t *testing.T) {
	var st ListState
	var changed bool
	disabled := true
	g := newRig(t, 300, 300, func(k *Kit) {
		k.BeginDisabled(disabled)
		l := k.BeginList("things", &st, 5)
		for r := l.First; r < l.Last; r++ {
			l.RowNamed(r, uint32(r), []byte("row "+string(rune('0'+r))))
		}
		k.EndList(&l)
		k.EndDisabled()
		changed = changed || l.Changed
	})
	g.frames(2)
	n := g.must(ui.RoleListItem, "row 2")
	if n.State&ui.SemDisabled == 0 || n.Actions != 0 {
		t.Fatalf("a row of a disabled list: state %v actions %v, want disabled and none", n.State, n.Actions)
	}
	for _, a := range []ui.SemAction{ui.ActPress, ui.ActFocus} {
		g.in.PushAction(ui.SemRequest{Key: n.Key, Action: a})
		g.frames(2)
	}
	if st.Sel.Count() != 0 || changed {
		t.Fatalf("a disabled row took an action: selected %d, changed %v", st.Sel.Count(), changed)
	}
	// enabled, the same request selects the row
	disabled = false
	g.frames(2)
	n = g.must(ui.RoleListItem, "row 2")
	if n.State&ui.SemDisabled != 0 || n.Actions&ui.ActPress == 0 {
		t.Fatalf("an enabled row: state %v actions %v", n.State, n.Actions)
	}
	g.in.PushAction(ui.SemRequest{Key: n.Key, Action: ui.ActPress})
	g.frames(2)
	if !st.Sel.Has(2) || st.Sel.Count() != 1 {
		t.Fatalf("press on an enabled row: selected %d", st.Sel.Count())
	}
}

// A resize drag belongs to the column's ID: reordering the columns while the
// button is down moves it with its column.
func TestTableResizeDragFollowsTheColumnWhenColumnsAreReordered(t *testing.T) {
	g, v := newBigTable(t, 100)
	hdr := g.must(ui.RoleColumnHeader, "Name")
	edge := hdr.Bounds[2]
	y := (hdr.Bounds[1] + hdr.Bounds[3]) / 2
	g.in.MouseX, g.in.MouseY = edge, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.MouseX = edge + 20
	g.frames(2)
	// the app moves the Name column to the end mid-drag
	v.cols[0], v.cols[1], v.cols[2] = v.cols[1], v.cols[2], v.cols[0]
	g.in.MouseX = edge + 40
	g.frames(3)
	width := func(id uint32) float32 {
		for _, c := range v.cols {
			if c.ID == id {
				return c.Width
			}
		}
		return -1
	}
	if w := width(10); w < 185 || w > 195 {
		t.Fatalf("Name (ID 10) is %v wide after a 40 px drag across a reorder, want about 190", w)
	}
	if width(11) != 80 || width(12) != 60 {
		t.Fatalf("another column was resized: Size %v, Kind %v", width(11), width(12))
	}
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frames(2)
}

// Every header is a tab stop, in order, before the rows; Enter and Space sort,
// Alt+arrows resize, and the semantics say what the header is and does.
func TestTableHeadersAreKeyboardOperable(t *testing.T) {
	g, v := newBigTable(t, 100)
	// Tab reaches the three headers, then the rows
	var seen []ui.Key
	for i := 0; i < 4; i++ {
		g.key(ui.KeyTab, 0)
		seen = append(seen, g.c.FocusKey())
	}
	for i, name := range []string{"Name", "Size", "Kind"} {
		if seen[i] != g.must(ui.RoleColumnHeader, name).Key {
			t.Fatalf("Tab %d did not reach the %s header", i+1, name)
		}
	}
	if seen[3] != TableKey(g.c.KeyOf(g.c.Root()), "files") {
		t.Fatal("Tab after the headers did not reach the rows")
	}
	g.key(ui.KeyTab, ui.ModShift)
	g.key(ui.KeyTab, ui.ModShift) // back to the Size header
	if g.c.FocusKey() != g.must(ui.RoleColumnHeader, "Size").Key {
		t.Fatal("Shift+Tab did not go back to the Size header")
	}
	// Space sorts the focused sortable header; the direction flips with Enter
	g.key(ui.KeySpace, 0)
	g.key(ui.KeyEnter, 0)
	if len(v.sorted) != 2 || v.sorted[0].Column != 11 || v.sorted[0].Descending || !v.sorted[1].Descending {
		t.Fatalf("keyboard sort changes: %v", v.sorted)
	}
	sz := g.must(ui.RoleColumnHeader, "Size")
	if sz.State&ui.SemSortDescending == 0 || sz.Actions&ui.ActPress == 0 || sz.State&ui.SemFocused == 0 {
		t.Fatalf("Size header state %v actions %v, want sorted descending, press, focused", sz.State, sz.Actions)
	}
	if nm := g.must(ui.RoleColumnHeader, "Name"); nm.State&(ui.SemSortAscending|ui.SemSortDescending) != 0 {
		t.Fatal("an unsorted header reports a sort direction")
	}
	// Alt+arrows resize it, Shift in bigger steps, down to the minimum
	g.key(ui.KeyRight, ui.ModAlt)
	if w := v.cols[1].Width; w != 88 {
		t.Fatalf("Alt+Right: width %v, want 88", w)
	}
	g.key(ui.KeyLeft, ui.ModAlt|ui.ModShift)
	if w := v.cols[1].Width; w != 56 {
		t.Fatalf("Alt+Shift+Left: width %v, want 56", w)
	}
	g.key(ui.KeyLeft, ui.ModAlt|ui.ModShift)
	if w := v.cols[1].Width; w != defaultMinColumnPt {
		t.Fatalf("Alt+Shift+Left past the minimum: width %v, want %d", w, defaultMinColumnPt)
	}
	// a column that cannot sort is still resizable, and does not sort
	g.key(ui.KeyTab, 0)
	if g.c.FocusKey() != g.must(ui.RoleColumnHeader, "Kind").Key {
		t.Fatal("Tab did not reach the Kind header")
	}
	kind := g.must(ui.RoleColumnHeader, "Kind")
	if kind.Actions&ui.ActPress != 0 || kind.Actions&ui.ActIncrement == 0 {
		t.Fatalf("Kind header actions %v: no press, but a width", kind.Actions)
	}
	g.key(ui.KeyEnter, 0)
	if len(v.sorted) != 2 {
		t.Fatal("Enter on a header that is not sortable sorted")
	}
	g.key(ui.KeyRight, ui.ModAlt)
	if w := v.cols[2].Width; w != 68 {
		t.Fatalf("Kind width %v after Alt+Right, want 68", w)
	}
}

// An assistive technology presses a header to sort and increments it to widen.
func TestTableHeaderAccessibilityActions(t *testing.T) {
	g, v := newBigTable(t, 100)
	name := g.must(ui.RoleColumnHeader, "Name")
	g.in.PushAction(ui.SemRequest{Key: name.Key, Action: ui.ActPress})
	g.frame()
	if len(v.sorted) != 1 || v.sorted[0].Column != 10 {
		t.Fatalf("ActPress on a header: sort changes %v", v.sorted)
	}
	g.in.PushAction(ui.SemRequest{Key: name.Key, Action: ui.ActIncrement})
	g.frame()
	g.in.PushAction(ui.SemRequest{Key: name.Key, Action: ui.ActDecrement})
	g.frame()
	g.in.PushAction(ui.SemRequest{Key: name.Key, Action: ui.ActDecrement})
	g.frame()
	if w := v.cols[0].Width; w != 142 {
		t.Fatalf("increment, decrement, decrement: width %v, want 142", w)
	}
}

// A click on a header sorts but leaves the keyboard focus in the rows.
func TestTableHeaderClickKeepsTheFocus(t *testing.T) {
	g, v := newBigTable(t, 100)
	x, y := g.rowCentre(ui.RoleRow, 2)
	g.click(x, y)
	f := g.c.FocusKey()
	g.click(centre(g.must(ui.RoleColumnHeader, "Name")))
	if len(v.sorted) != 1 || g.c.FocusKey() != f {
		t.Fatalf("sorted %d times, focus moved: %v", len(v.sorted), g.c.FocusKey() != f)
	}
}
