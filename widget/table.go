package widget

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// MaxColumns is the most columns a table has.
const MaxColumns = base.MaxColumns

// Column describes one column of a table (base.Column). The app keeps the
// columns in a slice of its own and passes it to BeginTable every frame:
// resizing a column by dragging its header edge changes Width in that slice.
type Column = base.Column

// SortState is how a table is sorted (base.SortState). The table keeps it for
// the app; the app sorts its data by it and sets TableState.Order.
type SortState = base.SortState

// TableState is the state of a table: a ListState and the sort state
// (base.TableState).
type TableState = base.TableState

// Table is an open table. See BeginTable. Its List is the table's rows: the
// same List a list has, with its fields and methods (First, Last, Changed,
// Row, TextColor …).
type Table struct {
	List
	base.TableHead
}

// TableKey is the key of the list box of the table that BeginTable builds with
// label inside the box whose key is parent: the box that takes the keyboard
// focus. An app moves the focus to a table with ui.Context.SetFocus before it
// has been built.
func TableKey(parent ui.Key, label string) ui.Key { return base.TableKey(parent, label) }

// BeginTable starts a table: a header row with the columns' titles above a
// List of rows rows. It reads like a list:
//
//	t := k.BeginTable("files", &v.table, v.cols[:], len(v.order))
//	if t.SortChanged { v.resort(t.Sort) }
//	for r := t.First; r < t.Last; r++ {
//		item := v.order[r]
//		t.Row(r, item)           // the item keys the row's boxes and its selection
//		t.BeginCell(0)           // any boxes: an icon, styled and truncated text…
//		k.Icon(v.icon(item), 0)
//		k.LabelBytes(v.name(item))
//		t.EndCell()
//		t.CellBytes(1, v.size(item))
//	}
//	k.EndTable(&t)
//
// Columns keep their IDs and widths, can be dragged wider and narrower down to
// their minimum, align their text, and fade out text that does not fit. At
// most MaxColumns columns are shown. The header state (hover, focus, an active
// drag) belongs to the column's ID, so reordering or hiding columns keeps it
// with its column.
//
// The headers are tab stops, before the rows. On a focused header Enter or
// Space sorts (a sortable column), Alt+Left and Alt+Right (Option on macOS)
// narrow and widen the column by 8 points, with Shift by 32, down to its
// minimum. A header is a ui.RoleColumnHeader that reports its sort direction
// (ui.SemSortAscending, ui.SemSortDescending) and the actions press (sortable
// columns), increment and decrement (the width) and focus.
func (k *Kit) BeginTable(label string, st *TableState, cols []Column, rows int) Table {
	t := Table{TableHead: k.BeginTableHeader(label, st, cols)}
	k.header(&t.TableHead)
	t.List.List = k.BeginTableBody(&t.TableHead, rows, base.ListOpts{Hover: hoverOn})
	return t
}

// header builds the classic look of the header row: semibold muted titles, a
// hairline below, a hover tint on sortable titles and the sort chevron.
func (k *Kit) header(t *base.TableHead) {
	c := k.c
	px, p := &k.r.Px, &k.r.Pal
	hh := t.HeaderHeight()
	c.NextBorderSides(ui.SideBottom)
	c.NextBorder(p.Line, px.BorderW)
	t.BeginHeaderRow(ui.DrawBorder)
	for i := range t.Columns() {
		hc := t.HeaderCell(i)
		var flags ui.Flags
		if hc.Hot > 0.004 {
			flags |= ui.DrawBackground
			c.NextBG(theme.WithAlpha(p.HoverOverlay, hc.Hot))
			c.NextRadius(px.RadiusSmall)
		}
		t.BeginHeaderCell(&hc, flags)
		if hc.Col.Align == ui.AlignRight { // the title (and the sort mark) at the right, over the numbers
			c.NextSize(ui.Pct(1), ui.Px(hh))
			c.BoxKeyed(0, ui.KeyN(hc.Key, 3), nil)
		}
		c.NextSize(ui.TextSize(labelSlack), ui.Px(hh))
		c.NextFont(k.r.Font.UISemi)
		c.NextTextColor(p.TextMuted)
		c.NextAlign(hc.Col.Align) // a right-aligned title ends where the cells' text ends, not a slack pixel short
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(hc.Key, 1), strBytes(hc.Col.Title))
		if hc.Sorted {
			ic := icon.SortUp
			if hc.Descending {
				ic = icon.SortDown
			}
			f, disp := k.icons.Prepare(c, ic, p.TextMuted)
			_, ih := k.icons.Size(ic)
			k.center(ih, hh)
			c.BoxKeyed(f|ui.FloatingY, ui.KeyN(hc.Key, 2), disp)
		}
		if hc.Ring > 0.004 {
			k.ringBox(hc.Key, hc.W, hh, px.RadiusSmall, hc.Ring)
		}
		t.EndHeaderCell(&hc)
	}
	t.EndHeaderRow()
}

// BeginCell starts cell col of the row being built: a box as wide as the
// column, as high as the row, clipping its children, which flow left to right.
// End it with EndCell.
func (t *Table) BeginCell(col int) ui.ID { return t.TableHead.BeginCell(&t.List.List, col) }

// CellBytes builds cell col of the row being built as one line of text, in the
// column's alignment, fading out where it does not fit. The bytes are copied.
// The text of column 0 is the accessible name of the row, unless RowNamed gave
// it one.
func (t *Table) CellBytes(col int, b []byte) {
	k := t.Kit()
	c := k.Context()
	c.NextFont(k.Theme().Font.UI)
	c.NextTextColor(t.textCol)
	c.NextAlign(t.Columns()[col].Align)
	t.TextCell(&t.List.List, col, b, ui.DrawText|ui.FadeOverflowX)
}

// CellText is CellBytes for a string.
func (t *Table) CellText(col int, s string) { t.CellBytes(col, strBytes(s)) }

// EndTable ends the table.
func (k *Kit) EndTable(t *Table) {
	k.endList(&t.List.List)
	k.CloseTable(&t.TableHead)
}
