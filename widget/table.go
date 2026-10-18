package widget

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
)

// MaxColumns is the most columns a table has.
const MaxColumns = 16

// defaultMinColumnPt is the least width of a column that sets none.
const defaultMinColumnPt = 48

// Column describes one column of a table. The app keeps the columns in a
// slice of its own and passes it to BeginTable every frame: resizing a column
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
	// Align aligns the text of the column's CellBytes and its header.
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

// Table is an open table. See BeginTable.
type Table struct {
	List
	// SortChanged: the user clicked a sortable header; Sort is the new state.
	// The app sorts its data (on a task when large) and sets
	// TableState.Order; the table does not reorder anything itself.
	SortChanged bool
	Sort        SortState
	// ColumnsChanged: the user resized a column (the slice has the new width).
	ColumnsChanged bool

	ts   *TableState
	cols []Column
	root ui.ID
	hkey ui.Key
	hh   float32
	pad  float32 // the padding at the sides of a cell
}

// TableKey is the key of the list box of the table that BeginTable builds with
// label inside the box whose key is parent: the box that takes the keyboard
// focus. An app moves the focus to a table with ui.Context.SetFocus before it
// has been built.
func TableKey(parent ui.Key, label string) ui.Key {
	_, ident := ui.SplitLabel(label)
	tkey := ui.HashString(parent, ident)
	return ui.KeyN(ui.HashString(tkey, ident), 1)
}

// HeaderKey is the key of the header row of the table, for ui.Context.RectOf.
func (t *Table) HeaderKey() ui.Key { return t.hkey }

// HeaderHeight is the height in pixels of the header row.
func (t *Table) HeaderHeight() float32 { return t.hh }

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
	c := k.c
	px := &k.r.Px
	if len(cols) > MaxColumns {
		cols = cols[:MaxColumns]
	}
	t := Table{ts: st, cols: cols}
	_, ident := ui.SplitLabel(label)
	tkey := ui.HashString(c.ParentKey(), ident)
	t.hh, t.pad = theme.Snap(px.RowH), px.Pad
	if k.rowPt > 0 { // compact rows keep a header a little taller than themselves, and the cells close
		t.hh, t.pad = theme.Snap(k.Pt(k.rowPt+4)), px.GapSmall
	}
	t.hkey = ui.KeyN(tkey, 2)

	k.size(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextRole(ui.RoleTable)
	c.NextSemName(label)
	t.root = c.BoxKeyed(0, tkey, nil)
	c.Push(t.root)

	// the width the columns share: the list's, from the last frame
	body := c.RectOf(ui.KeyN(ui.HashString(tkey, ident), 1))
	avail := body[2] - body[0] - 2*theme.Snap(px.GapSmall)
	t.layoutColumns(k, avail)
	t.header(k, tkey, ident)
	t.layoutColumns(k, avail) // a resize in the header

	t.List = k.beginList(label, &st.ListState, rows, true)
	return t
}

// columnStepPt is how far Alt+Left and Alt+Right (and an assistive technology's
// increment) change the width of a column, in points; with Shift, four steps.
const columnStepPt = 8

// headerKey is the key of the header cell of the column with the given ID.
func headerKey(hkey ui.Key, id uint32) ui.Key { return ui.KeyN(hkey, uint64(id)) }

// resizeKey is the key of the drag handle at the right edge of that column.
func resizeKey(hkey ui.Key, id uint32) ui.Key { return ui.KeyN(hkey, 1<<32|uint64(id)) }

// layoutColumns computes the pixel widths of the columns.
func (t *Table) layoutColumns(k *Kit, avail float32) {
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

func (t *Table) minWidth(i int) float32 {
	if m := t.cols[i].MinWidth; m > 0 {
		return m
	}
	return defaultMinColumnPt
}

// header builds the header row and applies sorting and resizing.
func (t *Table) header(k *Kit, tkey ui.Key, ident string) {
	c := k.c
	px, p := &k.r.Px, &k.r.Pal
	st := t.ts
	hh := t.hh
	hkey := t.hkey

	// resizing and sorting come from the previous frame's header boxes, which
	// are keyed by the column's ID: reordering or hiding columns moves a drag
	// or the focus with the column, not with its place in the slice
	if !k.off() {
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

	inset := theme.Snap(px.GapSmall)
	c.NextSize(ui.Pct(1), ui.Px(hh))
	c.NextAxis(ui.AxisX)
	c.NextPadding(inset, 0, inset, 0)
	c.NextBorderSides(ui.SideBottom)
	c.NextBorder(p.Line, px.BorderW)
	hrow := c.BoxKeyed(ui.DrawBorder, hkey, nil)
	c.Push(hrow)
	x := float32(0)
	for i := range t.cols {
		col := &t.cols[i]
		w := st.colPx[i]
		ckey := headerKey(hkey, col.ID)
		sorted := st.Sort.Active && st.Sort.Column == col.ID
		// every header is a tab stop (a click leaves the focus where it is, as
		// in a file manager); a sortable one is clickable
		flags := ui.Focusable | ui.KeepFocus | ui.FocusAnimation
		acts := ui.ActFocus | ui.ActIncrement | ui.ActDecrement
		if col.Sortable {
			flags |= ui.Clickable | ui.HotAnimation
			acts |= ui.ActPress
		}
		if k.off() {
			flags = ui.Disabled
		}
		var state ui.SemState
		if sorted {
			state = ui.SemSortAscending
			if st.Sort.Descending {
				state = ui.SemSortDescending
			}
		}
		hot := c.HotTOf(ckey)
		c.NextSize(ui.Px(w), ui.Px(hh))
		c.NextAxis(ui.AxisX)
		c.NextGap(px.GapSmall)
		c.NextPadding(t.pad, 0, t.pad, 0)
		c.NextRole(ui.RoleColumnHeader)
		c.NextSemName(col.Title)
		c.NextSemState(state)
		c.NextSemActions(acts)
		if hot > 0.004 {
			flags |= ui.DrawBackground
			c.NextBG(theme.WithAlpha(p.HoverOverlay, hot))
			c.NextRadius(px.RadiusSmall)
		}
		cell := c.BoxKeyed(flags|ui.Clip, ckey, nil)
		c.Push(cell)
		if col.Align == ui.AlignRight { // the title (and the sort mark) at the right, over the numbers
			c.NextSize(ui.Pct(1), ui.Px(hh))
			c.BoxKeyed(0, ui.KeyN(ckey, 3), nil)
		}
		c.NextSize(ui.TextSize(labelSlack), ui.Px(hh))
		c.NextFont(k.r.Font.UISemi)
		c.NextTextColor(p.TextMuted)
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(ckey, 1), strBytes(col.Title))
		if sorted {
			ic := icon.SortUp
			if st.Sort.Descending {
				ic = icon.SortDown
			}
			f, disp := k.icons.Prepare(c, ic, p.TextMuted)
			_, ih := k.icons.Size(ic)
			k.center(ih, hh)
			c.BoxKeyed(f|ui.FloatingY, ui.KeyN(ckey, 2), disp)
		}
		if ring := c.FocusTOf(ckey); ring > 0.004 && c.FocusVisible() && !k.off() {
			k.ringBox(ckey, w, hh, px.RadiusSmall, ring)
		}
		c.Pop()
		x += w
		// the edge between this column and the next, to drag
		grab := theme.Snap(k.Pt(8))
		theme.At(c, inset+x-grab/2, 0, grab, hh)
		c.NextCursor(ui.CursorResizeEW)
		rflags := ui.Clickable | fl
		if k.off() {
			rflags = fl
		}
		c.BoxKeyed(rflags, resizeKey(hkey, col.ID), nil)
	}
	c.Pop()
}

// BeginCell starts cell col of the row being built: a box as wide as the column,
// as high as the row, clipping its children, which flow left to right. End it
// with EndCell.
func (t *Table) BeginCell(col int) ui.ID {
	k := t.k
	c := k.c
	c.NextSize(ui.Px(t.ts.colPx[col]), ui.Pct(1))
	c.NextAxis(ui.AxisX)
	c.NextGap(k.r.Px.GapSmall)
	c.NextPadding(t.pad, 0, t.pad, 0)
	c.NextRole(ui.RoleCell)
	id := c.BoxKeyed(ui.Clip, ui.KeyN(t.rowKey, uint64(col)), nil)
	c.Push(id)
	return id
}

// EndCell ends the cell BeginCell started.
func (t *Table) EndCell() { t.k.c.Pop() }

// CellBytes builds cell col of the row being built as one line of text, in the
// column's alignment, fading out where it does not fit. The bytes are copied.
// The text of column 0 is the accessible name of the row, unless RowNamed gave
// it one.
func (t *Table) CellBytes(col int, b []byte) {
	k := t.k
	c := k.c
	c.NextSize(ui.Px(t.ts.colPx[col]), ui.Pct(1))
	c.NextPadding(t.pad, 0, t.pad, 0)
	c.NextFont(k.r.Font.UI)
	c.NextTextColor(t.textCol)
	c.NextAlign(t.cols[col].Align)
	c.NextRole(ui.RoleCell)
	c.NextSemNameBytes(b)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|ui.Clip, ui.KeyN(t.rowKey, uint64(col)), b)
	if col == 0 && !t.rowNamed { // the first cell names its row
		c.SetSemNameBytes(t.row, b)
		t.rowNamed = true
	}
}

// CellText is CellBytes for a string.
func (t *Table) CellText(col int, s string) { t.CellBytes(col, strBytes(s)) }

// ColumnWidth is the width in pixels of column col, for custom cells.
func (t *Table) ColumnWidth(col int) float32 { return t.ts.colPx[col] }

// EndTable ends the table.
func (k *Kit) EndTable(t *Table) {
	k.EndList(&t.List)
	k.c.Pop()
}
