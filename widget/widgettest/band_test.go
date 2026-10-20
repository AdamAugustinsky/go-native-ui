package widgettest_test

import (
	"fmt"
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

// bandApp is a table that scrolls under a toolbar's band (TopInset,
// ContentEdge), as a Finder-like window does.
type bandApp struct {
	k    *widget.Kit
	st   base.TableState
	cols [2]base.Column
}

func (a *bandApp) Build(c *ui.Context) {
	k := a.k
	k.Begin(c)
	theme.At(c, 0, 0, 600, 400)
	c.Push(c.Box(theme.Floating, "pane"))
	t := rexui.BeginTable(k.Kit, "Files", &a.st, a.cols[:], 60, rexui.TableOpts{Striped: true, ClearHeader: true, TopInset: k.Pt(52)})
	for r := t.First; r < t.Last; r++ {
		t.Row(r, uint32(r), []byte(fmt.Sprintf("row %d", r)))
		t.CellBytes(0, []byte(fmt.Sprintf("row %d", r)))
		t.CellBytes(1, []byte("kind"))
	}
	rexui.EndTable(k.Kit, &t)
	c.Pop()
	tb := rexui.BeginToolbar(k.Kit, "Toolbar", rexui.ToolbarOpts{Style: theme.ToolbarUnified, Title: "Files", ContentEdge: true})
	tb.Title()
	tb.Flexible()
	tb.End()
	k.End()
}

// A row scrolled under the toolbar band is not reachable there: a click at its
// centre would land on the band, and the row would not be selected. The Player
// brings it clear of the band first and then clicks the row.
func TestPlayerClearsTheToolbarBandBeforeClicking(t *testing.T) {
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: 600, Height: 400})
	a := &bandApp{cols: [2]base.Column{{Title: "Name", Width: 300}, {Title: "Kind", Width: 200}}}
	a.k = widget.NewKit(r.Context(), r.Text(), themeRex())
	r.Run(a)
	a.st.SetTopRow(10, 0)
	r.Frames(4)
	row := r.Must(ui.RoleRow, "row 8")
	band := a.k.Pt(52)
	if row.Bounds[1] >= band {
		t.Fatalf("setup: row 8 at %v is not under the band (%v)", row.Bounds, band)
	}
	r.Play(widgettest.Click(ui.RoleRow, "row 8"))
	r.Frames(3)
	if err := r.Player().Err(); err != nil {
		t.Fatalf("the click failed: %v", err)
	}
	if n := a.st.Sel.Count(); n != 1 || !a.st.Sel.Has(8) {
		t.Errorf("selected rows after the click: %d, has row 8: %v", n, a.st.Sel.Has(8))
	}
	if got := r.Must(ui.RoleRow, "row 8").Bounds; got[1] < band {
		t.Errorf("row 8 is still under the band: %v", got)
	}
}

// Through is for a test of what covers the target: it clicks the centre of a
// row under the band as it is, and the band takes the click.
func TestThroughClicksWhatCoversTheTarget(t *testing.T) {
	r := widgettest.NewRig(t, widgettest.FakeOptions{Width: 600, Height: 400})
	a := &bandApp{cols: [2]base.Column{{Title: "Name", Width: 300}, {Title: "Kind", Width: 200}}}
	a.k = widget.NewKit(r.Context(), r.Text(), themeRex())
	r.Run(a)
	a.st.SetTopRow(10, 0)
	r.Frames(4)
	r.Play(widgettest.Click(ui.RoleRow, "row 8").Through())
	r.Frames(3)
	if a.st.Sel.Count() != 0 {
		t.Errorf("a click through the band selected %d rows", a.st.Sel.Count())
	}
	if got := r.Must(ui.RoleRow, "row 8").Bounds; got[1] >= a.k.Pt(52) {
		t.Errorf("the row was moved out from under the band: %v", got)
	}
}
