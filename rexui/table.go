package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// TableOpts are a table's options, passed by value.
type TableOpts struct {
	// Style is the table's style.
	Style ListStyle
	// Striped alternates the rows' background.
	Striped bool
	// Glyphs are the sort indicators (AddListGlyphs; zero: the standard
	// chevrons).
	Glyphs ListGlyphs
}

// TableRecipe is the look of a table's header and cells (macOS 27.2,
// NSTableView with an NSTableHeaderView); the rows are ListLook's.
type TableRecipe struct {
	// HeaderText colours the column titles (headerTextColor), set in the
	// Subheadline style (11 pt).
	HeaderText Tint
	// HeaderLine is the line under the header and Separator the one at the
	// right of each column's title (separatorColor, 1 pt).
	HeaderLine, Separator Tint
	// SeparatorH is the height of the lines between the titles, in points
	// (measured 15.5 in a 28-pt header), centred in the header above its
	// line.
	SeparatorH float32
	// SortGlyph colours the sort chevron (measured #9F9F9F over white and
	// #7A7A7A over #1E1E1E), SortRight is its centre from the title's right
	// edge and SortDY how far below the header's middle it sits, in points.
	SortGlyph         Tint
	SortRight, SortDY float32
	// CellPad is the room between a column's edge and its text, EdgePad at
	// the table's own left and right (NSTableView: the 17-pt intercell
	// spacing and the cell view's 2-pt text inset put the first column's
	// text 8 pt in, the others' 10 pt after the previous title's separator,
	// and end the last column's 9 pt before the right).
	CellPad float32
	EdgePad [2]float32
	// Corner fills the corner at the header's right above a legacy scroll
	// bar (base.TableHead.Corner, NSTableView's cornerView) over the list's
	// background: measured #FFFFFF on white, #262626 on #1E1E1E.
	Corner Tint
}

// TableLook is the tables' recipe.
var TableLook = TableRecipe{
	HeaderText: Of(theme.HeaderText),
	HeaderLine: Of(theme.Separator), Separator: Of(theme.Separator), SeparatorH: 16,
	SortGlyph: OfAlpha(theme.Text, 0.38, 0.41), SortRight: 12.5, SortDY: 0.5,
	CellPad: 10, EdgePad: [2]float32{8, 8},
	Corner: OfAlpha(theme.Text, 0, 0.036),
}

// Table is an open table: the rows are a List (Row, TextColor …), the head
// base's.
type Table struct {
	List
	base.TableHead
}

// BeginTable starts a table of the columns cols (their widths include the
// cells' padding) and rows rows, with a 28-pt header of the columns' titles:
//
//	t := rexui.BeginTable(k, "files", &v.table, v.cols[:], len(v.order), rexui.TableOpts{Striped: true})
//	if t.SortChanged { v.resort(t.Sort) }
//	for r := t.First; r < t.Last; r++ {
//		item := v.table.Item(r)
//		t.Row(r, item, nil)
//		t.CellBytes(0, v.name(item))
//		t.CellBytes(1, v.size(item))
//	}
//	rexui.EndTable(k, &t)
//
// The rows are ListLook's; headers, columns, sorting and the keyboard are
// base's (base.Kit.BeginTableHeader).
func BeginTable(k *base.Kit, label string, st *base.TableState, cols []base.Column, rows int, o TableOpts) Table {
	s := o.Style % NumListStyles
	rc := &TableLook
	t := Table{TableHead: k.BeginTableHeaderOpts(label, st, cols, base.TableOpts{
		Inset: ListLook.Inset[s], HeaderH: k.Theme().Lines.HeaderH / k.Scale(),
		CellPad: rc.CellPad, EdgePad: rc.EdgePad, Set: true,
	})}
	TableHeader(k, &t.TableHead, o.Glyphs)
	t.List = List{List: k.BeginTableBody(&t.TableHead, rows, listOpts(k, ListOpts{Style: o.Style, Striped: o.Striped}, true)),
		rowLook: rowLook{style: s, striped: o.Striped}}
	return t
}

// TableHeader builds the header row of t: the titles in the Subheadline style,
// a separator at the right of each, the sort chevron, the line under the row,
// and a focus ring on a header reached with the keyboard; beside a legacy
// scroll bar the header stops at the corner above it. AppKit's headers do
// not react to hover.
func TableHeader(k *base.Kit, t *base.TableHead, g ListGlyphs) {
	g = g.orStandard()
	c, r := k.Context(), k.Theme()
	rc := &TableLook
	hh := t.HeaderHeight()
	lw := pt(k.Scale(), 1)
	c.NextBG(ListLook.Background.RGBA(r))
	c.NextBorderSides(ui.SideBottom)
	c.NextBorder(rc.HeaderLine.RGBA(r), lw)
	t.BeginHeaderRow(ui.DrawBackground | ui.DrawBorder)
	font := r.Styles[theme.StyleSubheadline].Font
	tc, sepC := rc.HeaderText.RGBA(r), rc.Separator.RGBA(r)
	if k.Disabled() {
		tc = ListLook.LabelDisabled.RGBA(r)
	}
	body := hh // the titles are centred in the header, line included
	sh := pt(k.Scale(), rc.SeparatorH)
	for i := range t.Columns() {
		hc := t.HeaderCell(i)
		t.BeginHeaderCell(&hc, 0)
		if hc.Col.Align == ui.AlignRight { // the title at the right, over the numbers
			c.NextSize(ui.Pct(1), ui.Px(body))
			c.BoxKeyed(0, ui.KeyN(hc.Key, 3), nil)
		}
		c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(body))
		c.NextFont(font)
		c.NextTextColor(tc)
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(hc.Key, 1), base.StrBytes(hc.Col.Title))
		if hc.Sorted {
			ic := g.SortUp
			if hc.Descending {
				ic = g.SortDown
			}
			if f, disp := k.Icons().Prepare(c, ic, rc.SortGlyph.RGBA(r)); f != 0 {
				iw, ih := k.IconSize(ic)
				theme.At(c, hc.W-theme.Snap(k.Pt(rc.SortRight)+iw/2), theme.Snap((body-ih)/2+k.Pt(rc.SortDY)), iw, ih)
				c.BoxKeyed(f|base.Floating, ui.KeyN(hc.Key, 2), disp)
			}
		}
		// the separator at the column's right edge, in the room above the line
		theme.At(c, hc.W-lw, theme.Snap((body-lw-sh)/2), lw, sh)
		c.NextBG(sepC)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(hc.Key, 4), nil)
		id := c.Parent()
		t.EndHeaderCell(&hc)
		k.FocusRing(id, hc.Visual, 0)
	}
	if t.Corner() > 0 { // EndHeaderRow builds the corner above a legacy bar
		c.NextBG(theme.Over(rc.Corner.RGBA(r), ListLook.Background.RGBA(r)))
	}
	t.EndHeaderRow()
}

// Row starts display row r of the table, which shows item, with name as its
// accessible name (nil: the first cell's text), and pushes it.
func (t *Table) Row(r int, item uint32, name []byte) base.RowState { return t.List.Row(r, item, name) }

// BeginCell starts cell col of the row being built: as wide as the column, as
// high as the row, clipping its children, which flow left to right with the
// cell padding. End it with EndCell.
func (t *Table) BeginCell(col int) ui.ID { return t.TableHead.BeginCell(&t.List.List, col) }

// CellBytes builds cell col of the row being built as one line of body text in
// the row's text colour and the column's alignment, fading out where it does
// not fit. The bytes are copied. Column 0's text names the row unless Row was
// given a name.
func (t *Table) CellBytes(col int, b []byte) { t.cell(col, b, t.TextColor()) }

// CellBytes2 is CellBytes in the row's secondary text colour (a size, a date).
func (t *Table) CellBytes2(col int, b []byte) { t.cell(col, b, t.Text2Color()) }

// CellText is CellBytes for a string.
func (t *Table) CellText(col int, s string) { t.CellBytes(col, base.StrBytes(s)) }

func (t *Table) cell(col int, b []byte, color uint32) {
	k := t.List.Kit()
	c := k.Context()
	c.NextFont(k.Theme().Styles[theme.StyleBody].Font)
	c.NextTextColor(color)
	c.NextAlign(t.Columns()[col].Align)
	t.TextCell(&t.List.List, col, b, ui.DrawText|ui.FadeOverflowX)
}

// EndTable ends the table: the stripes below the rows, the rows, the scroll
// bar and the table.
func EndTable(k *base.Kit, t *Table) {
	EndList(k, &t.List)
	k.CloseTable(&t.TableHead)
}
