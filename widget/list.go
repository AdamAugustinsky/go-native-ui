package widget

import (
	"math"

	"gonativeui/theme"
	"gonativeui/ui"
)

// findMax is the longest type-to-find query kept.
const findMax = 32

// findResetNs is how long a pause ends a type-to-find query.
const findResetNs = 1e9

// ListState is the state of a list or table, owned by the app next to the data
// it shows: the selection, the display order, the scroll state and the little
// the keyboard needs. It holds no pointers apart from the Order slice's header.
// It must not be copied once in use.
type ListState struct {
	// Sel is the selection, by item.
	Sel Selection
	// Order maps display rows to items: row r shows item Order[r]. nil means
	// row r shows item r. The app sets it when it sorts or filters, and keeps
	// it as long as the list shows it; the list reads it for keyboard
	// navigation and range selection, where it needs the item of a row that
	// is not on screen.
	Order []uint32
	// Scroll is the state of the list's scroll bar.
	Scroll ScrollState

	curRow, ancRow int     // where the cursor and anchor were last seen (hints)
	shown          float64 // the scroll offset the last frame laid out
	goRow          int
	goPending      bool
	find           [findMax]byte
	nFind          int
	findT          int64
}

// GoTo moves the cursor to display row r, selects that row alone and scrolls
// it into view, in the next frame the list is built. The app calls it to answer
// type-to-find.
func (st *ListState) GoTo(r int) { st.goRow, st.goPending = r, true }

// List is an open virtualised list. Build rows First to Last-1 with Row, then
// EndList. The other fields are what happened this frame.
type List struct {
	// First and Last are the rows to build: those in view, widened by two.
	First, Last int
	// Rows is the number of rows.
	Rows int

	// Changed: the selection changed (a click, the keyboard).
	Changed bool
	// Activated: a row was double-clicked, or Enter or Space went down on the
	// cursor row; Item is its item.
	Activated bool
	Item      uint32
	// RightClicked: the right button was clicked on a row; ContextItem is its
	// item (selected alone first when it was not selected) and ContextAt the
	// pointer position in window pixels.
	RightClicked bool
	ContextItem  uint32
	ContextAt    [2]float32
	// Find is the type-to-find query when it grew or started in this frame, nil
	// otherwise. The app looks for the first item that starts with it and
	// calls ListState.GoTo with its row. The text aliases the state.
	Find []byte
	// Focused: the list has the keyboard focus.
	Focused bool

	k      *Kit
	st     *ListState
	o, s   ui.ID
	key    ui.Key
	rowH   float32
	padT   float32
	inRow  bool
	hover  int // display row under the pointer, -1
	cursor uint32
	hasCur bool
	ring   bool
	axes   ScrollAxes
	table  bool

	// the row being built
	row     ui.ID
	rowKey  ui.Key
	rowItem uint32
	rowSel  bool
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
	return k.beginList(label, st, rows, false)
}

func (k *Kit) beginList(label string, st *ListState, rows int, table bool) List {
	c := k.c
	px := &k.r.Px
	_, ident := ui.SplitLabel(label)
	okey := ui.HashString(c.ParentKey(), ident)
	rowH := theme.Snap(px.RowH)
	l := List{k: k, st: st, Rows: rows, rowH: rowH, hover: -1, key: okey, table: table, axes: ScrollVertical}

	k.size(ui.Pct(1), ui.Pct(1))
	l.o = c.BoxKeyed(ui.Clip, okey, nil)
	c.Push(l.o)
	skey := ui.KeyN(okey, 1)
	k.scrollInput(okey, skey, &st.Scroll, ScrollVertical)

	l.padT = theme.Snap(px.GapSmall)
	inset := theme.Snap(px.GapSmall)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextPadding(inset, l.padT, inset, l.padT)
	if !table { // a table's root is its node: the header and the rows are its children
		c.NextRole(ui.RoleList)
		c.NextSemName(label)
	}
	flags := ui.ScrollY | ui.Clickable | ui.Focusable | ui.FocusAnimation
	if k.off() {
		flags = ui.ScrollY | ui.Disabled
	}
	// the list takes its pointer input as one box; the row under the pointer
	// comes from the geometry
	ps := c.PeekSignal(skey)
	l.s = c.BoxKeyed(flags, skey, nil)
	c.Push(l.s)
	l.Focused = c.Focused(l.s) && !k.off()
	l.ring = l.Focused && c.FocusVisible()

	l.First, l.Last = c.ListRange(l.s, rows, rowH, 2)
	_, sy := c.Scroll(l.s)

	if !k.off() {
		l.input(ps, c.Rect(l.s))
	}
	l.hoverFrom(ps)
	st.shown = sy
	if it, ok := st.Sel.Cursor(); ok {
		l.cursor, l.hasCur = it, true
	}
	return l
}

// hoverFrom finds the display row under the pointer from the signal of the
// list box.
func (l *List) hoverFrom(ps ui.Signal) {
	if ps.Hovering {
		l.hover = l.rowAt(ps.Mouse[1])
	}
}

// rowAt is the display row at y pixels from the top of the list box, -1 if
// none.
func (l *List) rowAt(y float32) int {
	f := math.Floor((float64(y-l.padT) + l.st.shown) / float64(l.rowH))
	if f < 0 || f >= float64(l.Rows) {
		return -1
	}
	return int(f)
}

// rowOfItem is the display row that shows item, -1 if none. It tries the hint
// first and scans only when the hint is stale.
func (l *List) rowOfItem(item uint32, hint int) int {
	order := l.st.Order
	if hint >= 0 && hint < l.Rows && itemAt(order, hint) == item {
		return hint
	}
	if len(order) == 0 {
		if int(item) < l.Rows {
			return int(item)
		}
		return -1
	}
	for r := 0; r < l.Rows && r < len(order); r++ {
		if order[r] == item {
			return r
		}
	}
	return -1
}

func (l *List) cursorRow() int {
	if it, ok := l.st.Sel.Cursor(); ok {
		return l.rowOfItem(it, l.st.curRow)
	}
	return -1
}

func (l *List) anchorRow() int {
	if it, ok := l.st.Sel.Anchor(); ok {
		return l.rowOfItem(it, l.st.ancRow)
	}
	return -1
}

// moveTo puts the cursor on row r: selects it alone, or with extend the range
// from the anchor to it, and scrolls it into view.
func (l *List) moveTo(r int, extend bool) {
	if l.Rows == 0 {
		return
	}
	r = min(max(r, 0), l.Rows-1)
	st := l.st
	if extend {
		a := l.anchorRow()
		if a < 0 {
			a = max(l.cursorRow(), 0)
		}
		st.Sel.SelectRange(st.Order, a, r)
		st.ancRow = a
	} else {
		st.Sel.Only(itemAt(st.Order, r))
		st.ancRow = r
	}
	st.curRow = r
	l.Changed = true
	l.scrollTo(r)
}

func (l *List) scrollTo(r int) {
	w := l.k.c.Rect(l.s)
	top := l.padT + float32(r)*l.rowH
	l.k.c.ScrollIntoView(l.s, [4]float32{0, top, w[2] - w[0], top + l.rowH})
}

// input applies the pointer and keys of this frame to the selection.
func (l *List) input(ps ui.Signal, rect [4]float32) {
	c := l.k.c
	in := c.Input()
	st := l.st
	prim := ui.PrimaryMod()

	if st.goPending {
		st.goPending = false
		l.moveTo(st.goRow, false)
	}

	if ps.Pressed && l.Rows > 0 {
		if r := l.rowAt(ps.Mouse[1]); r >= 0 {
			item := itemAt(st.Order, r)
			switch {
			case in.Mods&ui.ModShift != 0:
				l.moveTo(r, true)
			case in.Mods&prim != 0:
				st.Sel.Toggle(item)
				st.curRow, st.ancRow = r, r
				l.Changed = true
			default:
				st.Sel.Only(item)
				st.curRow, st.ancRow = r, r
				l.Changed = true
			}
		}
	}
	if ps.DoubleClicked {
		if r := l.rowAt(ps.Mouse[1]); r >= 0 {
			l.Activated, l.Item = true, itemAt(st.Order, r)
		}
	}
	if ps.RightClicked {
		if r := l.rowAt(ps.Mouse[1]); r >= 0 {
			item := itemAt(st.Order, r)
			if !st.Sel.Has(item) {
				st.Sel.Only(item)
				st.curRow, st.ancRow = r, r
				l.Changed = true
			}
			l.RightClicked, l.ContextItem = true, item
			l.ContextAt = [2]float32{in.MouseX, in.MouseY}
		}
	}
	if !l.Focused || l.Rows == 0 {
		return
	}
	view := rect[3] - rect[1]
	page := max(int(view/l.rowH)-1, 1)
	cur := l.cursorRow()
	switch {
	case c.TakeKey(ui.KeyDown, 0):
		l.moveTo(cur+1, false)
	case c.TakeKey(ui.KeyDown, ui.ModShift):
		l.moveTo(max(cur, 0)+1, true)
	case c.TakeKey(ui.KeyUp, 0):
		l.moveTo(max(cur, 1)-1, false)
	case c.TakeKey(ui.KeyUp, ui.ModShift):
		l.moveTo(max(cur, 1)-1, true)
	case c.TakeKey(ui.KeyPageDown, 0):
		l.moveTo(max(cur, 0)+page, false)
	case c.TakeKey(ui.KeyPageDown, ui.ModShift):
		l.moveTo(max(cur, 0)+page, true)
	case c.TakeKey(ui.KeyPageUp, 0):
		l.moveTo(max(cur, page)-page, false)
	case c.TakeKey(ui.KeyPageUp, ui.ModShift):
		l.moveTo(max(cur, page)-page, true)
	case c.TakeKey(ui.KeyHome, 0):
		l.moveTo(0, false)
	case c.TakeKey(ui.KeyHome, ui.ModShift):
		l.moveTo(0, true)
	case c.TakeKey(ui.KeyEnd, 0):
		l.moveTo(l.Rows-1, false)
	case c.TakeKey(ui.KeyEnd, ui.ModShift):
		l.moveTo(l.Rows-1, true)
	case c.TakeKey('A', prim):
		st.Sel.SelectAll(l.itemCount())
		l.Changed = true
	}
	if cur >= 0 && ps.Activated { // Enter, Space, or an assistive technology's press
		l.Activated, l.Item = true, itemAt(st.Order, cur)
	}
	l.typeToFind(in)
}

// itemCount is the number of items select-all covers: the rows, or with an
// order the largest item it names plus one.
func (l *List) itemCount() int {
	n := l.Rows
	for _, it := range l.st.Order[:min(len(l.st.Order), l.Rows)] {
		n = max(n, int(it)+1)
	}
	return n
}

// typeToFind collects the characters typed into the focused list.
func (l *List) typeToFind(in *ui.Input) {
	st := l.st
	if in.NText == 0 || in.Mods&(ui.PrimaryMod()|ui.ModAlt) != 0 {
		return
	}
	if in.Time-st.findT > findResetNs {
		st.nFind = 0
	}
	if st.nFind == 0 && in.Text[0] == ' ' {
		return // a space starts nothing: it activates
	}
	n := copy(st.find[st.nFind:], in.Text[:in.NText])
	st.nFind += n
	st.findT = in.Time
	l.Find = st.find[:st.nFind]
}

// Row starts display row r, which shows item (the app's stable id): a box of
// the row height, as wide as the list, that shows the item's selection, hover
// and cursor, and whose children flow left to right. It ends the row before
// it. The boxes of the row are keyed by the item, so their state follows it
// through sorting and scrolling.
func (l *List) Row(r int, item uint32) {
	l.endRow()
	k := l.k
	c := k.c
	p := &k.r.Pal
	px := &k.r.Px
	key := ui.KeyN(l.key, 1<<32|uint64(item))
	sel := l.st.Sel.Has(item)
	var bg uint32
	if sel {
		bg = p.Selection
	}
	if r == l.hover {
		if sel {
			bg = theme.MixRGBA(bg, p.Selection|0xFF, 0.18)
		} else {
			bg = p.HoverOverlay
		}
	}
	flags := ui.Flags(0)
	if bg != 0 {
		flags |= ui.DrawBackground
	}
	if l.ring && l.hasCur && l.cursor == item {
		flags |= ui.DrawBorder
		c.NextBorder(p.FocusRing, px.FocusRingW)
	}
	var state ui.SemState
	if sel {
		state = ui.SemSelected
	}
	role := ui.RoleListItem
	if l.table {
		role = ui.RoleRow
	}
	c.NextBG(bg)
	c.NextRadius(px.RadiusSmall)
	c.NextSize(ui.Pct(1), ui.Px(l.rowH))
	c.NextAxis(ui.AxisX)
	c.NextRole(role)
	c.NextSemState(state)
	c.NextSemPos(r, l.Rows)
	l.row = c.BoxKeyed(flags, key, nil)
	l.rowKey, l.rowItem, l.rowSel = key, item, sel
	l.textCol = p.Text
	if sel {
		l.textCol = p.SelectionText
	}
	c.Push(l.row)
	l.inRow = true
	if a := c.Signal(l.row).Action; a&ui.ActPress != 0 { // an assistive technology pressed the row
		l.st.Sel.Only(item)
		l.st.curRow, l.st.ancRow = r, r
		l.Changed = true
	}
}

// TextColor is the text colour for the row being built: the theme's, or its
// selection text colour on a selected row.
func (l *List) TextColor() uint32 { return l.textCol }

func (l *List) endRow() {
	if l.inRow {
		l.k.c.Pop()
		l.inRow = false
	}
}

// EndList ends the list: it closes the last row and builds the scroll bar.
func (k *Kit) EndList(l *List) {
	l.endRow()
	k.c.Pop()
	k.scrollBars(l.o, l.s, &l.st.Scroll, ScrollVertical)
	k.c.Pop()
}
