package widget_test

import (
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// buildRows builds the rows of a list through a helper that takes a
// *widget.List, as app code written against the phase-5 API does.
func buildRows(l *widget.List, names [][]byte, colour *[8]uint32) {
	for r := l.First; r < l.Last; r++ {
		l.RowNamed(r, uint32(r), names[r])
		colour[r] = l.TextColor()
	}
}

// A table's List is a widget.List backed by the table's own rows: helpers that
// take *widget.List, and t.List.Row and t.List.TextColor, keep working and see
// the table's selection.
func TestTableListIsAWidgetList(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ts := fakeui.New()
	c := ui.New(ts)
	c.Reserve(4096)
	c.AtlasSync(&fakeui.Uploader{})
	k := widget.NewKit(c, ts, rex.Theme())
	var st widget.TableState
	st.Sel.Only(1)
	cols := []widget.Column{{ID: 1, Title: "Name", Width: 120}}
	names := [][]byte{[]byte("zero"), []byte("one"), []byte("two")}
	var viaHelper, viaField [8]uint32
	var first, last int
	pass := 0
	var in ui.Input
	in.Inside, in.Focused = true, true
	var list gpu.DrawList
	for i := 0; i < 4; i++ {
		in.Time += 16_666_667
		c.Begin(&in, 400, 300, 1, 1.0/60)
		k.Begin(c)
		tb := k.BeginTable("files", &st, cols, len(names))
		if pass%2 == 0 {
			buildRows(&tb.List, names, &viaHelper)
		} else {
			for r := tb.List.First; r < tb.List.Last; r++ {
				tb.List.Row(r, uint32(r))
				tb.CellBytes(0, names[r])
				viaField[r] = tb.List.TextColor()
			}
		}
		first, last = tb.List.First, tb.List.Last
		if first != tb.First || last != tb.Last {
			t.Fatalf("t.List shows rows %d..%d, the table %d..%d", first, last, tb.First, tb.Last)
		}
		k.EndTable(&tb)
		k.End()
		c.End(&list)
		in.ClearFrame()
		pass++
	}
	if first != 0 || last != 3 {
		t.Fatalf("rows %d..%d, want 0..3", first, last)
	}
	pal := &k.Theme().Pal
	for _, got := range [][8]uint32{viaHelper, viaField} {
		if got[0] != pal.Text || got[1] != pal.SelectionText || got[2] != pal.Text {
			t.Errorf("row text colours %x, want the selection's on row 1 only", got[:3])
		}
	}
	// the rows the helper built are the table's rows
	var sem ui.SemTree
	sem.Reserve(1024, 1<<14)
	in.Time += 16_666_667
	c.Begin(&in, 400, 300, 1, 1.0/60)
	k.Begin(c)
	tb := k.BeginTable("files", &st, cols, len(names))
	buildRows(&tb.List, names, &viaHelper)
	k.EndTable(&tb)
	k.End()
	c.End(&list)
	c.Semantics(&sem)
	found := false
	for _, n := range sem.Nodes {
		if n.Role == ui.RoleRow && string(sem.Text(n.Name)) == "one" {
			found = n.State&ui.SemSelected != 0
		}
	}
	if !found {
		t.Error("the helper's row \"one\" is not a selected row of the table")
	}
}
