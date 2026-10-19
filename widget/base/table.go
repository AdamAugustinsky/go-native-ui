package base

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// MaxColumns is the most columns a table has.
const MaxColumns = 16

// DefaultMinColumnPt is the least width of a column that sets none, in points.
const DefaultMinColumnPt = 48

// Column describes one column of a table. The app keeps the columns in a
// slice of its own and passes it to the table every frame: resizing a column
// by dragging its header edge changes Width in that slice.
type Column struct {
	// ID identifies the column in sort changes, stably: it does not change
	// when columns are reordered or hidden.
	ID uint32
	// Title is the header text.
	Title string
	// Width is the width in points.
	Width float32
	// MinWidth is the least width the user can drag the column to, in points
	// (48 when zero).
	MinWidth float32
	// Align aligns the text of the column's cells and its header.
	Align ui.Align
	// Fill makes the column take the room the others leave, when there is
	// any; Width is then its least width.
	Fill bool
	// Sortable makes a click on the header report a sort change.
	Sortable bool
}

// SortState is how a table is sorted. The table keeps it for the app; the app
// sorts its data by it and sets TableState.Order.
type SortState struct {
	// Active: a column is sorted. Column is its ID.
	Active     bool
	Column     uint32
	Descending bool
}

// TableState is the state of a table: a ListState and the sort state.
type TableState struct {
	ListState
	// Sort is the current sort; set it to show the app's initial order.
	Sort SortState

	resizeID uint32 // the column being dragged wider or narrower, by ID
	resizing bool
	resizeW  float32
	colPx    [MaxColumns]float32
}

// TableHead is the head of an open table: its box, its columns and the header
// row of column titles, above a List of rows. BeginTableHeader starts it and
// applies the header's input; the recipe builds the header (BeginHeaderRow,
// HeaderCell, BeginHeaderCell, EndHeaderCell); BeginTableBody starts the rows
// as a List, which build like a list's, with cells (BeginCell, TextCell);
// CloseTable ends it after the list. A styled table keeps the two side by
// side:
//
//	type Table struct {
//		base.List
//		base.TableHead
//	}
//
// Columns keep their IDs and widths, can be dragged wider and narrower down to
// their minimum, and align their text. At most MaxColumns columns are shown.
// The header state (hover, focus, an active drag) belongs to the column's ID,
// so reordering or hiding columns keeps it with its column.
//
// The headers are tab stops, before the rows. On a focused header Enter or
// Space sorts (a sortable column), Alt+Left and Alt+Right (Option on macOS)
// narrow and widen the column by 8 points, with Shift by 32, down to its
// minimum. A header is a ui.RoleColumnHeader that reports its sort direction
// (ui.SemSortAscending, ui.SemSortDescending) and the actions press (sortable
// columns), increment and decrement (the width) and focus.
type TableHead struct {
	// SortChanged: the user clicked a sortable header; Sort is the new state.
	// The app sorts its data (on a task when large) and sets
	// TableState.Order; the table does not reorder anything itself.
	SortChanged bool
	Sort        SortState
	// ColumnsChanged: the user resized a column (the slice has the new width).
	ColumnsChanged bool

	tk    *Kit
	ts    *TableState
	cols  []Column
	root  ui.ID
	tkey  ui.Key
	hkey  ui.Key
	label string
	hh    float32
	pad   float32 // the padding at the sides of a cell
	edge  [2]float32 // the padding at the outer sides of the first and last cells
	inset float32 // the room at the sides of the header and the rows
	avail float32
	hx    float32 // where the next header cell starts
}

// TableOpts are a table's metrics, for BeginTableHeaderOpts, in points. With
// Set they replace BeginTableHeader's (a zero HeaderH keeps its height).
type TableOpts struct {
	// Inset is the room at the left and right of the header and the rows:
	// the list's side padding (ListOpts.Pad[0]), which BeginTableBody
	// takes from here.
	Inset float32
	// HeaderH is the header row's height.
	HeaderH float32
	// CellPad is the padding at the sides of a cell; EdgePad is the
	// padding at the left of the first column's cells (EdgePad[0]) and at
	// the right of the last's (EdgePad[1]).
	CellPad float32
	EdgePad [2]float32
	// Set says the fields hold the metrics.
	Set bool
}

// TableKey is the key of the list box of the table built with label inside the
// box whose key is parent: the box that takes the keyboard focus. An app moves
// the focus to a table with ui.Context.SetFocus before it has been built.
func TableKey(parent ui.Key, label string) ui.Key {
	_, ident := ui.SplitLabel(label)
	tkey := ui.HashString(parent, ident)
	return ui.KeyN(ui.HashString(tkey, ident), 1)
}

// HeaderKey is the key of the header row of the table, for ui.Context.RectOf.
func (t *TableHead) HeaderKey() ui.Key { return t.hkey }

// HeaderHeight is the height in pixels of the header row.
func (t *TableHead) HeaderHeight() float32 { return t.hh }

// CellPad is the padding in pixels at the sides of a cell.
func (t *TableHead) CellPad() float32 { return t.pad }

// CellPads is the padding in pixels at the left and right of column col's
// cells and header (TableOpts.CellPad, and EdgePad at the table's sides).
func (t *TableHead) CellPads(col int) (l, r float32) {
	l, r = t.pad, t.pad
	if col == 0 {
		l = t.edge[0]
	}
	if col == len(t.cols)-1 {
		r = t.edge[1]
	}
	return l, r
}

// Inset is the room in pixels at the sides of the header and the rows
// (TableOpts.Inset).
func (t *TableHead) Inset() float32 { return t.inset }

// Columns are the columns shown (at most MaxColumns).
func (t *TableHead) Columns() []Column { return t.cols }

// ColumnWidth is the width in pixels of column col, for custom cells.
func (t *TableHead) ColumnWidth(col int) float32 { return t.ts.colPx[col] }

// BeginTableHeader starts a table of the columns cols with the label (its
// accessible name) and st: the table's box (the parent's size unless NextSize
// says otherwise), the columns' widths, and the sorting and resizing the
// header's input of the previous frame asks for.
func (k *Kit) BeginTableHeader(label string, st *TableState, cols []Column) TableHead {
	return k.BeginTableHeaderOpts(label, st, cols, TableOpts{})
}

// BeginTableHeaderOpts is BeginTableHeader with the table's metrics.
func (k *Kit) BeginTableHeaderOpts(label string, st *TableState, cols []Column, o TableOpts) TableHead {
	c := k.c
	px := &k.r.Px
	if len(cols) > MaxColumns {
		cols = cols[:MaxColumns]
	}
	t := TableHead{tk: k, ts: st, cols: cols, label: label}
	_, ident := ui.SplitLabel(label)
	t.tkey = ui.HashString(c.ParentKey(), ident)
	t.hh, t.pad = theme.Snap(px.RowH), px.Pad
	if pt := k.PeekRowHeight(); pt > 0 { // compact rows keep a header a little taller than themselves, and the cells close
		t.hh, t.pad = theme.Snap(k.Pt(pt+4)), px.GapSmall
	}
	t.edge, t.inset = [2]float32{t.pad, t.pad}, theme.Snap(px.GapSmall)
	if o.Set {
		if o.HeaderH > 0 {
			t.hh = theme.Snap(k.Pt(o.HeaderH))
		}
		t.pad, t.inset = k.Pt(o.CellPad), theme.Snap(k.Pt(o.Inset))
		t.edge = [2]float32{k.Pt(o.EdgePad[0]), k.Pt(o.EdgePad[1])}
	}
	t.hkey = ui.KeyN(t.tkey, 2)

	k.ApplySize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextRole(ui.RoleTable)
	c.NextSemName(label)
	t.root = c.BoxKeyed(0, t.tkey, nil)
	c.Push(t.root)

	// the width the columns share: the list's, from the last frame
	body := c.RectOf(ui.KeyN(ui.HashString(t.tkey, ident), 1))
	t.avail = body[2] - body[0] - 2*t.inset
	t.layoutColumns(t.avail)
	t.headerInput()
	return t
}

// BeginTableBody starts the rows of the table, after its header: a List of
// rows rows, built as a list's are (RowState, BeginRow), with cells. With
// ListOpts.OwnPad the padding at the sides of the rows is the table's
// (TableOpts.Inset), so that the cells stay under their headers.
func (k *Kit) BeginTableBody(t *TableHead, rows int, o ListOpts) List {
	if o.OwnPad {
		o.Pad[0] = t.inset / k.scale
	}
	t.layoutColumns(t.avail) // a resize in the header
	return k.beginList(t.label, &t.ts.ListState, rows, true, o)
}

// CloseTable ends the table, after its list (CloseRows, the bar, CloseList).
func (k *Kit) CloseTable(t *TableHead) { k.c.Pop() }

// columnStepPt is how far Alt+Left and Alt+Right (and an assistive technology's
// increment) change the width of a column, in points; with Shift, four steps.
const columnStepPt = 8

// headerKey is the key of the header cell of the column with the given ID.
func headerKey(hkey ui.Key, id uint32) ui.Key { return ui.KeyN(hkey, uint64(id)) }

// resizeKey is the key of the drag handle at the right edge of that column.
func resizeKey(hkey ui.Key, id uint32) ui.Key { return ui.KeyN(hkey, 1<<32|uint64(id)) }

// layoutColumns computes the pixel widths of the columns.
func (t *TableHead) layoutColumns(avail float32) {
	k := t.tk
	var sum float32
	fill := -1
	for i := range t.cols {
		w := theme.Snap(k.Pt(max(t.cols[i].Width, t.minWidth(i))))
		t.ts.colPx[i] = w
		sum += w
		if t.cols[i].Fill {
			fill = i
		}
	}
	if fill >= 0 && avail > sum {
		t.ts.colPx[fill] += theme.Snap(avail - sum)
	}
}

func (t *TableHead) minWidth(i int) float32 {
	if m := t.cols[i].MinWidth; m > 0 {
		return m
	}
	return DefaultMinColumnPt
}

// headerInput applies sorting and resizing from the previous frame's header
// boxes, which are keyed by the column's ID: reordering or hiding columns
// moves a drag or the focus with the column, not with its place in the slice.
func (t *TableHead) headerInput() {
	k := t.tk
	c := k.c
	st := t.ts
	hkey := t.hkey
	if !k.Disabled() {
		for i := range t.cols {
			col := &t.cols[i]
			rk := resizeKey(hkey, col.ID)
			if ps := c.PeekSignal(rk); ps.Pressed {
				st.resizeID, st.resizing, st.resizeW = col.ID, true, col.Width
			} else if ps.Dragging && st.resizing && st.resizeID == col.ID {
				if w := max(st.resizeW+ps.DragDelta[0]/k.scale, t.minWidth(i)); w != col.Width {
					col.Width = w
					t.ColumnsChanged = true
				}
			}
			hk := headerKey(hkey, col.ID)
			hs := c.PeekSignal(hk)
			if col.Sortable && (hs.Clicked || hs.Activated) {
				s := &st.Sort
				if s.Active && s.Column == col.ID {
					s.Descending = !s.Descending
				} else {
					*s = SortState{Active: true, Column: col.ID}
				}
				t.SortChanged = true
			}
			// the keyboard (and an assistive technology) sets the width
			var steps float32
			if c.FocusKey() == hk {
				switch {
				case c.TakeKey(ui.KeyRight, ui.ModAlt):
					steps = 1
				case c.TakeKey(ui.KeyLeft, ui.ModAlt):
					steps = -1
				case c.TakeKey(ui.KeyRight, ui.ModAlt|ui.ModShift):
					steps = 4
				case c.TakeKey(ui.KeyLeft, ui.ModAlt|ui.ModShift):
					steps = -4
				}
			}
			if hs.Action&ui.ActIncrement != 0 {
				steps++
			}
			if hs.Action&ui.ActDecrement != 0 {
				steps--
			}
			if steps != 0 {
				if w := max(col.Width+steps*columnStepPt, t.minWidth(i)); w != col.Width {
					col.Width = w
					t.ColumnsChanged = true
				}
				c.RequestFrame()
			}
		}
	}
	t.Sort = st.Sort
}

// BeginHeaderRow builds the header row (as wide as the table, HeaderHeight
// high, its cells flowing left to right) in the style the recipe set with ui's
// Next calls and with its drawing flags, and pushes it. End it with
// EndHeaderRow after the cells.
func (t *TableHead) BeginHeaderRow(flags ui.Flags) ui.ID {
	c := t.tk.c
	c.NextSize(ui.Pct(1), ui.Px(t.hh))
	c.NextAxis(ui.AxisX)
	c.NextPadding(t.inset, 0, t.inset, 0)
	t.hx = 0
	id := c.BoxKeyed(flags, t.hkey, nil)
	c.Push(id)
	return id
}

// EndHeaderRow ends the header row.
func (t *TableHead) EndHeaderRow() { t.tk.c.Pop() }

// HeaderCell is the state of the header cell of a column, as a recipe draws
// it.
type HeaderCell struct {
	// Index is the column's place, Col the column.
	Index int
	Col   *Column
	// Key is the cell's key.
	Key ui.Key
	// W and H are the cell's size in pixels.
	W, H float32
	// Sorted: the table is sorted by this column; Descending which way.
	Sorted, Descending bool
	// Visual is the cell's state: Hot for a sortable column, Focus while it
	// has the keyboard focus, Ring while that focus is visible.
	Visual
}

// HeaderCell reads the header cell of column i.
func (t *TableHead) HeaderCell(i int) HeaderCell {
	k := t.tk
	c := k.c
	st := t.ts
	col := &t.cols[i]
	hc := HeaderCell{Index: i, Col: col, Key: headerKey(t.hkey, col.ID), W: st.colPx[i], H: t.hh,
		Sorted: st.Sort.Active && st.Sort.Column == col.ID}
	hc.Descending = hc.Sorted && st.Sort.Descending
	hc.Inactive = k.Inactive()
	hc.Disabled = k.Disabled()
	hc.Hot = c.HotTOf(hc.Key)
	if !hc.Disabled {
		hc.Focus = c.FocusTOf(hc.Key)
		if c.FocusVisible() {
			hc.Ring = hc.Focus
		}
	}
	return hc
}

// BeginHeaderCell builds the header cell hc (its size, clipping its children,
// which flow left to right with the theme's small gap inside the cell padding)
// in the style the recipe set with ui's Next calls and with its drawing flags,
// and pushes it. Every header is a tab stop (a click leaves the focus where it
// is, as in a file manager); a sortable one is clickable.
func (t *TableHead) BeginHeaderCell(hc *HeaderCell, flags ui.Flags) ui.ID {
	k := t.tk
	c := k.c
	var state ui.SemState
	if hc.Sorted {
		state = ui.SemSortAscending
		if hc.Descending {
			state = ui.SemSortDescending
		}
	}
	f := ui.Focusable | ui.KeepFocus | ui.FocusAnimation
	acts := ui.ActFocus | ui.ActIncrement | ui.ActDecrement
	if hc.Col.Sortable {
		f |= ui.Clickable | ui.HotAnimation
		acts |= ui.ActPress
	}
	if k.Disabled() {
		f = ui.Disabled
	}
	c.NextSize(ui.Px(hc.W), ui.Px(t.hh))
	c.NextAxis(ui.AxisX)
	c.NextGap(k.r.Px.GapSmall)
	pl, pr := t.CellPads(hc.Index)
	c.NextPadding(pl, 0, pr, 0)
	c.NextRole(ui.RoleColumnHeader)
	c.NextSemName(hc.Col.Title)
	c.NextSemState(state)
	c.NextSemActions(acts)
	id := c.BoxKeyed(f|flags|ui.Clip, hc.Key, nil)
	c.Push(id)
	return id
}

// EndHeaderCell ends the header cell and builds the edge after it, which the
// pointer drags to resize the column.
func (t *TableHead) EndHeaderCell(hc *HeaderCell) {
	k := t.tk
	c := k.c
	c.Pop()
	t.hx += hc.W
	grab := theme.Snap(k.Pt(8))
	theme.At(c, t.inset+t.hx-grab/2, 0, grab, t.hh)
	c.NextCursor(ui.CursorResizeEW)
	rflags := ui.Clickable | Floating
	if k.Disabled() {
		rflags = Floating
	}
	c.BoxKeyed(rflags, resizeKey(t.hkey, hc.Col.ID), nil)
}

// BeginCell starts cell col of the row of l being built: a box as wide as the
// column, as high as the row, clipping its children, which flow left to right.
// End it with EndCell.
func (t *TableHead) BeginCell(l *List, col int) ui.ID {
	c := t.tk.c
	c.NextSize(ui.Px(t.ts.colPx[col]), ui.Pct(1))
	c.NextAxis(ui.AxisX)
	c.NextGap(t.tk.r.Px.GapSmall)
	pl, pr := t.CellPads(col)
	c.NextPadding(pl, 0, pr, 0)
	c.NextRole(ui.RoleCell)
	id := c.BoxKeyed(ui.Clip, ui.KeyN(l.rowKey, uint64(col)), nil)
	c.Push(id)
	return id
}

// EndCell ends the cell BeginCell started.
func (t *TableHead) EndCell() { t.tk.c.Pop() }

// TextCell builds cell col of the row of l being built as one line of text b, in the
// style the recipe set with ui's Next calls (font, colour, alignment) and with
// its drawing flags (ui.DrawText and an overflow treatment): as wide as the
// column, clipped, with the cell padding. The bytes are copied. The text of
// column 0 is the accessible name of the row, unless BeginRow gave it one.
func (t *TableHead) TextCell(l *List, col int, b []byte, flags ui.Flags) {
	c := t.tk.c
	c.NextSize(ui.Px(t.ts.colPx[col]), ui.Pct(1))
	pl, pr := t.CellPads(col)
	c.NextPadding(pl, 0, pr, 0)
	c.NextRole(ui.RoleCell)
	c.NextSemNameBytes(b)
	c.BoxKeyed(flags|ui.Clip, ui.KeyN(l.rowKey, uint64(col)), b)
	if col == 0 {
		l.NameRow(b)
	}
}
