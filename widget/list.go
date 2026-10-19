package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ListState is the state of a list or table, owned by the app next to the data
// it shows: the selection, the display order, the scroll state and the little
// the keyboard needs (base.ListState).
type ListState = base.ListState

// List is an open virtualised list. Build rows First to Last-1 with Row, then
// EndList. The other fields are what happened this frame (base.List).
type List struct {
	base.List
	textCol uint32
}

// BeginList starts a list of rows rows of the theme's row height, scrolling
// vertically, taking the keyboard and showing the selection of st:
//
//	l := k.BeginList("files", &v.list, len(v.order))
//	for r := l.First; r < l.Last; r++ {
//		l.Row(r, v.order[r])
//		k.Label(v.name(r))
//	}
//	k.EndList(&l)
//
// Only the rows in view are built, so a list of a million rows costs what a
// screenful does. A click selects the row (Shift extends a range from the
// anchor, the primary modifier toggles), and with the focus the arrow keys,
// Page Up and Page Down, Home and End move the cursor and select (Shift
// extends), the primary modifier with A selects all, Enter activates. The list
// takes the width and height of its parent unless NextSize says otherwise.
func (k *Kit) BeginList(label string, st *ListState, rows int) List {
	return List{List: k.Kit.BeginList(label, st, rows, base.ListOpts{Hover: hoverOn})}
}

// Row starts display row r, which shows item (the app's stable id): a box of
// the row height, as wide as the list, that shows the item's selection, hover
// and cursor, and whose children flow left to right. It ends the row before
// it. The boxes of the row are keyed by the item, so their state follows it
// through sorting and scrolling.
//
// The row has no accessible name; RowNamed gives it one, and so does the first
// cell of a Table row (Table.CellBytes).
func (l *List) Row(r int, item uint32) { rowNamed(&l.List, &l.textCol, r, item, nil) }

// RowNamed is Row with the accessible name of the row, its text (the bytes are
// copied): what an assistive technology reads for the row, and what a test
// finds it by (ui.RoleListItem, or ui.RoleRow in a table).
func (l *List) RowNamed(r int, item uint32, name []byte) {
	rowNamed(&l.List, &l.textCol, r, item, name)
}

// Empty shows msg in the body of a list that has no rows, in style st. Call it
// after the loop that builds the rows; with rows it builds nothing.
func (l *List) Empty(msg []byte, st TextStyle) { empty(&l.List, msg, st) }

// TextColor is the text colour for the row being built: the theme's, or its
// selection text colour on a selected row.
func (l *List) TextColor() uint32 { return l.textCol }

// EndList ends the list: it closes the last row and builds the scroll bar.
func (k *Kit) EndList(l *List) { k.endList(&l.List) }

// rowNamed is the classic look of a row: the translucent selection with
// rounded corners, a hover tint, and the cursor's focus ring.
func rowNamed(l *base.List, textCol *uint32, r int, item uint32, name []byte) {
	rs := l.RowState(r, item)
	b := l.Kit()
	c := b.Context()
	p := &b.Theme().Pal
	px := &b.Theme().Px
	var bg uint32
	if rs.Selected {
		bg = p.Selection
	}
	if rs.Hover {
		if rs.Selected {
			bg = theme.MixRGBA(bg, p.Selection|0xFF, 0.18)
		} else {
			bg = p.HoverOverlay
		}
	}
	flags := ui.Flags(0)
	if bg != 0 {
		flags |= ui.DrawBackground
	}
	if rs.Ring {
		flags |= ui.DrawBorder
		c.NextBorder(p.FocusRing, px.FocusRingW)
	}
	c.NextBG(bg)
	c.NextRadius(px.RadiusSmall)
	l.BeginRow(&rs, name, flags)
	*textCol = p.Text
	if rs.Selected {
		*textCol = p.SelectionText
	}
}

// empty shows msg in the body of a list that has no rows.
func empty(l *base.List, msg []byte, st TextStyle) {
	if l.Rows != 0 || len(msg) == 0 {
		return
	}
	l.CloseRow()
	b := l.Kit()
	c := b.Context()
	c.NextSize(ui.Pct(1), ui.Px(l.RowH()))
	c.NextPadding(b.Theme().Px.Pad, 0, b.Theme().Px.Pad, 0)
	c.NextFont(fontOf(b.Theme(), st))
	c.NextTextColor(colorOf(b.Theme(), st))
	c.NextAlign(b.TakeAlign(ui.AlignLeft))
	c.NextRole(ui.RoleLabel)
	c.NextSemNameBytes(msg)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(l.BoxKey(), 2), msg)
}

// endList closes the rows of l and builds its scroll bar.
func (k *Kit) endList(l *base.List) {
	l.CloseRows()
	v := l.ScrollView()
	k.scrollBars(&v)
	k.CloseList(l)
}
