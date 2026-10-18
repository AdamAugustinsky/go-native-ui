package widget

import (
	"testing"

	"gonativeui/ui"
)

// shownList is a table of rows rows showing the items of order (nil: item r in
// row r), for the tests of what an app can ask of a list.
type shownList struct {
	st     TableState
	order  []uint32
	rows   int
	rowPt  float32
	cols   [1]Column
	parent ui.Key
	empty  []byte
	last   Table
}

func newShownList(t *testing.T, rows int, order []uint32) (*rig, *shownList) {
	v := &shownList{rows: rows, order: order, cols: [1]Column{{ID: 1, Title: "Name", Width: 100, Fill: true}}}
	v.st.Order = order
	v.st.Sel.Reserve(1 << 16)
	return newRig(t, 400, 300, v.build), v
}

func (v *shownList) build(k *Kit) {
	c := k.Context()
	v.parent = c.ParentKey()
	if v.rowPt > 0 {
		k.NextRowHeight(v.rowPt)
	}
	t := k.BeginTable("list", &v.st, v.cols[:], v.rows)
	for r := t.First; r < t.Last; r++ {
		item := uint32(r)
		if v.order != nil {
			item = v.order[r]
		}
		t.Row(r, item)
		t.CellBytes(0, c.Fmt().Str("item ").Int(int(item)).Bytes())
	}
	t.Empty(v.empty, TextMuted)
	k.EndTable(&t)
	v.last = t
}

// MoveTo and MoveBy move the cursor in the next frame whether or not the list
// has the keyboard, and extend from the anchor when asked.
func TestMoveToAndMoveByWithoutTheKeyboard(t *testing.T) {
	g, v := newShownList(t, 100, nil)
	if g.focusKey() == TableKey(v.parent, "list") {
		t.Fatal("setup: the list has the focus")
	}
	v.st.MoveTo(5, false)
	g.frame()
	if c, ok := v.st.Sel.Cursor(); !ok || c != 5 || v.st.Sel.Count() != 1 || !v.st.Sel.Has(5) {
		t.Fatalf("MoveTo(5): cursor %d,%v, %d selected", c, ok, v.st.Sel.Count())
	}
	v.st.MoveBy(2, true)
	g.frame()
	if c, _ := v.st.Sel.Cursor(); c != 7 || v.st.Sel.Count() != 3 || !v.st.Sel.Has(5) || !v.st.Sel.Has(7) {
		t.Fatalf("MoveBy(2, extend): cursor %d, %d selected, want 5..7", c, v.st.Sel.Count())
	}
	v.st.MoveBy(-1, true)
	v.st.MoveBy(-1, true) // moves in one frame add up
	g.frame()
	if c, _ := v.st.Sel.Cursor(); c != 5 || v.st.Sel.Count() != 1 {
		t.Fatalf("two MoveBy(-1): cursor %d, %d selected, want the anchor's row alone", c, v.st.Sel.Count())
	}
	v.st.MoveTo(1000, false) // clamped
	g.frame()
	if c, _ := v.st.Sel.Cursor(); c != 99 {
		t.Errorf("MoveTo past the end: cursor %d, want 99", c)
	}
}

// Reveal scrolls a row into view, SetTopRow puts a row at the top, and TopRow
// reads where the view is.
func TestRevealAndTopRow(t *testing.T) {
	g, v := newShownList(t, 100_000, nil)
	v.st.SetTopRow(500, 0.5)
	g.frames(2)
	if row, frac := v.st.TopRow(); row != 500 || frac < 0.45 || frac > 0.55 {
		t.Fatalf("SetTopRow(500, 0.5): top row %d + %v", row, frac)
	}
	v.st.Reveal(90_000) // far: at once
	g.frame()
	if y := v.st.RowTop(90_000); y < 0 || y > 300 {
		t.Errorf("after Reveal(90000) the row is at y = %v, not in the 300 px view", y)
	}
	if v.st.Sel.Count() != 0 {
		t.Error("Reveal selected something")
	}
	v.st.SetTopRow(0, 0)
	g.frames(2)
	if row, frac := v.st.TopRow(); row != 0 || frac != 0 {
		t.Errorf("top row %d + %v, want 0", row, frac)
	}
	v.st.Reveal(2) // already in view: nothing moves
	g.frames(2)
	if row, _ := v.st.TopRow(); row != 0 {
		t.Errorf("Reveal of a visible row scrolled to row %d", row)
	}
}

// Select all selects the rows that are shown, not the items a filter hides, and
// a press in the room below the rows deselects.
func TestSelectAllOfAFilteredListAndClickingBelow(t *testing.T) {
	g, v := newShownList(t, 3, []uint32{40, 7, 19}) // three of many items
	x, y := g.rowCentre(ui.RoleRow, 1)
	g.click(x, y)
	g.key('A', ui.PrimaryMod())
	if v.st.Sel.Count() != 3 || !v.st.Sel.Has(40) || !v.st.Sel.Has(7) || !v.st.Sel.Has(19) || v.st.Sel.Has(8) {
		t.Fatalf("select all: %d selected, items %v", v.st.Sel.Count(), v.st.Sel.Items(nil))
	}
	g.click(x, 250) // below the three rows
	if v.st.Sel.Count() != 0 {
		t.Errorf("a click below the rows left %d selected", v.st.Sel.Count())
	}
	g.click(x, y)
	g.in.Mods = ui.ModShift
	g.click(x, 250) // extending a selection is not a click on nothing
	g.in.Mods = 0
	if v.st.Sel.Count() != 1 {
		t.Errorf("a shift-click below the rows changed the selection: %d selected", v.st.Sel.Count())
	}
}

// NextRowHeight sets the pitch of the next list's rows, and Empty fills a list
// without rows.
func TestRowHeightAndEmptyMessage(t *testing.T) {
	g, v := newShownList(t, 10, nil)
	tree := g.semantics()
	var ys []float32
	for _, n := range tree.Nodes {
		if n.Role == ui.RoleRow {
			ys = append(ys, n.Bounds[1])
		}
	}
	if len(ys) < 3 {
		t.Fatal("setup: rows not built")
	}
	def := ys[1] - ys[0]
	v.rowPt = 16
	g.frame()
	ys = ys[:0]
	for _, n := range g.semantics().Nodes {
		if n.Role == ui.RoleRow {
			ys = append(ys, n.Bounds[1])
		}
	}
	if got := ys[1] - ys[0]; got != 16 || got >= def {
		t.Errorf("rows are %v px apart with NextRowHeight(16), %v without", got, def)
	}

	v.rows, v.empty = 0, []byte("Empty folder")
	g.frames(2)
	if _, ok := g.find(ui.RoleLabel, "Empty folder"); !ok {
		t.Error("the empty list shows no message")
	}
	v.rows = 5
	g.frames(2)
	if _, ok := g.find(ui.RoleLabel, "Empty folder"); ok {
		t.Error("a list with rows shows the empty message")
	}
}

// TableKey and FieldKey name the boxes that take the focus before they are
// built.
func TestFocusKeysNameTheWidgets(t *testing.T) {
	var buf ui.TextBuffer
	var parent ui.Key
	var st TableState
	cols := []Column{{ID: 1, Title: "Name", Width: 100, Fill: true}}
	g := newRig(t, 400, 300, func(k *Kit) {
		parent = k.Context().ParentKey()
		k.TextField("name", &buf, "")
		t := k.BeginTable("files", &st, cols, 3)
		for r := t.First; r < t.Last; r++ {
			t.Row(r, uint32(r))
			t.CellText(0, "x")
		}
		k.EndTable(&t)
	})
	x, y := g.rowCentre(ui.RoleRow, 1)
	g.click(x, y)
	if got, want := g.focusKey(), TableKey(parent, "files"); got != want {
		t.Errorf("a click in the table focuses key %v, TableKey says %v", got, want)
	}
	nx, ny := centre(g.must(ui.RoleTextField, "name"))
	g.click(nx, ny)
	if got, want := g.focusKey(), FieldKey(parent, "name"); got != want {
		t.Errorf("a click in the field focuses key %v, FieldKey says %v", got, want)
	}
}
