package base

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
	shown          float64 // the scroll offset the last frame laid out (the position)
	// disp is where the last frame showed the rows: the offset plus a
	// trackpad's stretch past an edge, for finding the row under the pointer.
	disp         float64
	rowPx, padPx float32 // the row pitch and the padding above the first row, in the last frame
	goRow        int
	goPending    bool
	find         [findMax]byte
	nFind        int
	findT        int64

	mv      moveReq // a cursor move the app asked for
	revRow  int
	revWant bool
	topRow  int
	topFrac float32
	topWant bool
}

// moveReq is a pending MoveTo or MoveBy.
type moveReq struct {
	pending bool
	abs     bool // row is a display row; else a number of rows from the cursor
	row     int
	extend  bool
}

// Item is the item shown in display row r: Order[r], or r when Order is nil.
// Use it wherever the app needs the item of a row, so that a list that is not
// reordered need not keep an Order.
func (st *ListState) Item(r int) uint32 { return itemAt(st.Order, r) }

// GoTo moves the cursor to display row r, selects that row alone and scrolls
// it into view, in the next frame the list is built. The app calls it to answer
// type-to-find.
func (st *ListState) GoTo(r int) { st.goRow, st.goPending = r, true }

// MoveTo moves the cursor to display row r (clamped to the rows) in the next
// frame the list is built, whether or not the list has the keyboard: the row
// is selected alone, or with extend the range from the anchor to it is, and it
// is scrolled into view. An app that sends the arrow keys of a text field to
// the list uses it.
func (st *ListState) MoveTo(r int, extend bool) {
	st.mv = moveReq{pending: true, abs: true, row: r, extend: extend}
}

// MoveBy is MoveTo for the row n rows from the cursor's (n may be negative; the
// cursor is taken as the row before the first when there is none). Several
// moves in one frame add up when they extend alike.
func (st *ListState) MoveBy(n int, extend bool) {
	if st.mv.pending && !st.mv.abs && st.mv.extend == extend {
		n += st.mv.row
	}
	st.mv = moveReq{pending: true, row: n, extend: extend}
}

// Reveal scrolls display row r into view in the next frame the list is built:
// a far jump is instant, a near one glides. The selection does not change.
func (st *ListState) Reveal(r int) { st.revRow, st.revWant = r, true }

// TopRow is the display row at the top of the view as of the last frame, and
// how far it has scrolled out (0 up to but not including 1).
func (st *ListState) TopRow() (row int, frac float32) {
	if st.rowPx <= 0 {
		return 0, 0
	}
	f := max(st.shown, 0) / float64(st.rowPx)
	row = int(f)
	return row, float32(f - float64(row))
}

// SetTopRow scrolls so that display row r, scrolled out by frac of its height,
// is at the top, in the next frame the list is built; an app that replaced its
// rows uses it to keep the place.
func (st *ListState) SetTopRow(r int, frac float32) {
	st.topRow, st.topFrac, st.topWant = r, frac, true
}

// RowTop is the distance in pixels from the top of the list's box to the top of
// display row r as shown in the last frame (negative above the view).
func (st *ListState) RowTop(r int) float32 {
	return st.padPx + float32(r)*st.rowPx - float32(st.disp)
}

// PadPx is the room in pixels above the first row inside the list's box, as of
// the last frame.
func (st *ListState) PadPx() float32 { return st.padPx }

// RowPx is the height in pixels of a row as of the last frame.
func (st *ListState) RowPx() float32 { return st.rowPx }

// List is an open virtualised list. Build rows First to Last-1 with RowState
// and BeginRow, then CloseRows, the bar and CloseList (see BeginList). The
// other fields are what happened this frame.
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

	// Emphasised: the selection shows emphasised this frame (RowState.Emphasised).
	Emphasised bool

	k       *Kit
	st      *ListState
	hoverOn bool
	o, s    ui.ID
	key     ui.Key
	skey    ui.Key
	rowH    float32
	padT    float32
	top     float32 // the view's top inset (NextScrollInset): the rows start padT below it
	padX    float32
	inRow   bool
	hover   int // display row under the pointer, -1
	cursor  uint32
	hasCur  bool
	ring    bool
	axes    ScrollAxes
	table   bool
	tree    bool // an outline's: rows export their level
	off     bool // built in a disabled region: the rows are disabled too

	// the row being built
	row      ui.ID
	rowKey   ui.Key
	rowItem  uint32
	rowSel   bool
	rowNamed bool // the row has an accessible name
}

// ListOpts are the options of a list.
type ListOpts struct {
	// Hover makes the row under the pointer report RowState.Hover (HoverOn).
	// macOS tables and lists do not react to hover: the default is off.
	Hover Hover
	// Pad is the room inside the list's box around its rows, in points: at
	// the left and right (Pad[0]) and above the first row and below the last
	// (Pad[1]). It applies with OwnPad; without, the list keeps the theme's
	// small gap all round.
	Pad [2]float32
	// OwnPad says Pad holds the padding, zero included.
	OwnPad bool
	// Flags are drawing flags of the list's outer box, in the style the
	// recipe set with ui's Next calls before BeginList (DrawBackground and
	// NextBG for the list's own fill).
	Flags ui.Flags
}

// BeginList starts a list of rows rows of the theme's row height (NextRowHeight
// changes it), scrolling vertically, taking the keyboard and showing the
// selection of st. Only the rows in view are built, so a list of a million rows
// costs what a screenful does:
//
//	l := k.BeginList("files", &v.list, len(v.order), base.ListOpts{})
//	for r := l.First; r < l.Last; r++ {
//		rs := l.RowState(r, v.order[r])
//		… // the recipe styles the row from rs (selected, emphasised, cursor)
//		l.BeginRow(&rs, v.name(r), flags)
//		… // the row's content
//	}
//	l.CloseRows()
//	… // the scroll bar (ScrollBars on l.ScrollView())
//	k.CloseList(&l)
//
// A click selects the row (Shift extends a range from the anchor, the primary
// modifier toggles), and with the focus the arrow keys, Page Up and Page Down,
// Home and End move the cursor and select (Shift extends), the primary
// modifier with A selects all, Enter activates; typing finds (List.Find). The
// list takes the width and height of its parent unless NextSize says
// otherwise.
func (k *Kit) BeginList(label string, st *ListState, rows int, o ListOpts) List {
	return k.beginList(label, st, rows, false, o)
}

func (k *Kit) beginList(label string, st *ListState, rows int, table bool, o ListOpts) List {
	c := k.c
	px := &k.r.Px
	_, ident := ui.SplitLabel(label)
	okey := ui.HashString(c.ParentKey(), ident)
	rowH := theme.Snap(px.RowH)
	if pt := k.TakeRowHeight(); pt > 0 {
		rowH = theme.Snap(k.Pt(pt))
	}
	l := List{k: k, st: st, Rows: rows, rowH: rowH, hover: -1, key: okey, table: table, axes: ScrollVertical, off: k.Disabled(),
		hoverOn: o.Hover == HoverOn}

	k.ApplySize(ui.Pct(1), ui.Pct(1))
	skey := ui.KeyN(okey, 1)
	if pad := k.legacyReserve(skey, ScrollVertical, st.Scroll.Size); pad != [2]float32{} {
		c.NextPadding(0, 0, pad[0], pad[1]) // room for a legacy scroll bar
	}
	l.o = c.BoxKeyed(ui.Clip|o.Flags, okey, nil)
	c.Push(l.o)
	l.top = k.takeScrollInset()
	st.Scroll.top = l.top
	k.ScrollInput(okey, skey, &st.Scroll, ScrollVertical)

	l.padT = theme.Snap(px.GapSmall)
	inset := theme.Snap(px.GapSmall)
	if o.OwnPad {
		inset, l.padT = theme.Snap(k.Pt(o.Pad[0])), theme.Snap(k.Pt(o.Pad[1]))
	}
	l.padX = inset
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextPadding(inset, l.padT+l.top, inset, l.padT)
	if !table { // a table's root is its node: the header and the rows are its children
		c.NextRole(ui.RoleList)
		c.NextSemName(label)
	}
	flags := ui.ScrollY | ui.Clickable | ui.Focusable | ui.FocusAnimation
	if k.Disabled() {
		flags = ui.ScrollY | ui.Disabled
	}
	// the list takes its pointer input as one box; the row under the pointer
	// comes from the geometry
	ps := c.PeekSignal(skey)
	l.s = c.BoxKeyed(flags, skey, nil)
	c.SetObscured(l.s, l.top, 0) // what covers the inset is out of view: reveals keep rows below it
	c.Push(l.s)
	l.Focused = c.Focused(l.s) && !k.Disabled()
	l.ring = l.Focused && c.FocusVisible()
	// the selection is emphasised while the list has the focus in the key
	// window (a forced look shows it so without either)
	f := k.TakeForce()
	l.Emphasised = (l.Focused || f&LookFocused != 0) && !k.Inactive() && f&LookInactive == 0 && !l.off

	st.rowPx, st.padPx = rowH, l.padT+l.top
	l.scrollRequests()
	l.First, l.Last = c.ListRange(l.s, rows, rowH, 2)
	_, sy := c.Scroll(l.s)
	l.skey = skey

	if !k.Disabled() {
		l.input(ps, c.Rect(l.s))
	}
	l.hoverFrom(ps)
	st.shown = sy
	_, stretch := c.ScrollStretch(l.s)
	st.disp = sy + stretch
	if it, ok := st.Sel.Cursor(); ok {
		l.cursor, l.hasCur = it, true
	}
	return l
}

// Key is the key of the list's box, the one that takes the keyboard focus: an
// app moves the focus to the list with ui.Context.SetFocus(l.Key()).
func (l *List) Key() ui.Key { return l.skey }

// scrollRequests applies the app's Reveal and SetTopRow before the rows in
// view are chosen.
func (l *List) scrollRequests() {
	st, c := l.st, l.k.c
	if st.topWant {
		st.topWant = false
		span := float64(l.padT)*2 + float64(l.top) + float64(l.Rows)*float64(l.rowH) - float64(c.Rect(l.s)[3]-c.Rect(l.s)[1])
		c.SetScroll(l.s, 0, min(max((float64(st.topRow)+float64(st.topFrac))*float64(l.rowH), 0), max(span, 0)))
	}
	if st.revWant {
		st.revWant = false
		if l.Rows == 0 {
			return
		}
		r := min(max(st.revRow, 0), l.Rows-1)
		rect := c.Rect(l.s)
		view := float64(rect[3] - rect[1])
		top := float64(l.padT+l.top) + float64(r)*float64(l.rowH)
		bottom := top + float64(l.rowH)
		_, sy := c.Scroll(l.s)
		_, stretch := c.ScrollStretch(l.s)
		shown := sy + stretch // a trackpad's stretch moves the rows too
		var target float64
		switch {
		case top < shown+float64(l.top): // below the inset, not under it
			target = top - float64(l.top)
		case bottom > shown+view:
			target = bottom - view
		default:
			return
		}
		if math.Abs(target-sy) > 4*view {
			c.SetScroll(l.s, 0, target)
		} else {
			c.ScrollTo(l.s, 0, target)
		}
	}
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
	f := math.Floor((float64(y-l.padT-l.top) + l.st.disp) / float64(l.rowH))
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

// cursorRow is the display row of the cursor, -1 if there is none or it is not
// shown. A scan that was needed is remembered, so that a stale hint costs one
// scan and not one per frame.
func (l *List) cursorRow() int {
	if it, ok := l.st.Sel.Cursor(); ok {
		r := l.rowOfItem(it, l.st.curRow)
		if r >= 0 {
			l.st.curRow = r
		}
		return r
	}
	return -1
}

func (l *List) anchorRow() int {
	if it, ok := l.st.Sel.Anchor(); ok {
		r := l.rowOfItem(it, l.st.ancRow)
		if r >= 0 {
			l.st.ancRow = r
		}
		return r
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
		st.Sel.Only(st.Item(r))
		st.ancRow = r
	}
	st.curRow = r
	l.Changed = true
	l.scrollTo(r)
}

func (l *List) scrollTo(r int) {
	w := l.k.c.Rect(l.s)
	top := l.padT + l.top + float32(r)*l.rowH
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
	if m := st.mv; m.pending {
		st.mv.pending = false
		r := m.row
		if !m.abs {
			r += max(l.cursorRow(), -1) // from the row before the first when there is no cursor
		}
		l.moveTo(r, m.extend)
	}

	if ps.Pressed {
		if r := l.rowAt(ps.Mouse[1]); r < 0 {
			// the empty room below the rows: a press there deselects, as in a
			// file manager, unless the press is extending a selection
			if in.Mods&(ui.ModShift|prim) == 0 && st.Sel.Count() > 0 {
				st.Sel.Clear()
				l.Changed = true
			}
		} else {
			item := st.Item(r)
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
			l.Activated, l.Item = true, st.Item(r)
		}
	}
	if ps.RightClicked {
		if r := l.rowAt(ps.Mouse[1]); r >= 0 {
			item := st.Item(r)
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
	view := rect[3] - rect[1] - l.top // the rows under the inset are not in view
	page := max(int(view/l.rowH)-1, 1)
	cur := l.cursorRow()
	was := l.Changed
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
		l.selectAllRows()
		l.Changed = true
	}
	if l.Changed && !was { // the keyboard moved the cursor: show it
		c.SetFocusVisible(true)
	}
	if cur >= 0 && ps.Activated { // Enter, Space, or an assistive technology's press
		l.Activated, l.Item = true, st.Item(cur)
	}
	l.typeToFind(in)
}

// selectAllRows selects the items of the rows: all items 0..Rows-1 without an
// order, exactly the ones the order shows with one (items that a filter hides
// are not selected).
func (l *List) selectAllRows() {
	st := l.st
	if len(st.Order) == 0 {
		st.Sel.SelectAll(l.Rows)
		return
	}
	st.Sel.Clear()
	for _, it := range st.Order[:min(len(st.Order), l.Rows)] {
		st.Sel.Set(it, true)
	}
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

// RowState is the state of a row about to be built, as a recipe draws it.
type RowState struct {
	// Row is the display row, Item the app's stable id it shows, Key the key
	// of the row's box (derived from the item, so that its state follows it
	// through sorting and scrolling).
	Row  int
	Item uint32
	Key  ui.Key
	// Selected: the item is selected.
	Selected bool
	// PrevSelected and NextSelected: the display rows above and below are
	// selected too. A run of selected rows is one block, as in AppKit, whose
	// ends are where these are false.
	PrevSelected, NextSelected bool
	// Emphasised: the selection shows emphasised (the accent block and white
	// text): the list has the keyboard focus in the key window. Otherwise a
	// selected row shows the unemphasised selection.
	Emphasised bool
	// Hover: the pointer is over the row (with ListOpts.Hover only).
	Hover bool
	// Cursor: the row is the keyboard cursor's.
	Cursor bool
	// Ring: the cursor's row while the list has the focus and the focus is
	// visible; a recipe that marks the cursor (rather than letting the
	// emphasis show the focus, as macOS does) draws it then.
	Ring bool
	// Disabled: the list is in a disabled region.
	Disabled bool
	// Level, Expandable and Expanded are an outline row's depth in its tree
	// and whether it has children and shows them (OutlineRowState); a
	// list's rows are at level 0 and not expandable.
	Level                int
	Expandable, Expanded bool
}

// RowState ends the row before (if one is open) and reads the state of display
// row r, which shows item (the app's stable id).
func (l *List) RowState(r int, item uint32) RowState {
	l.CloseRow()
	st := l.st
	sel := st.Sel.Has(item)
	rs := RowState{
		Row: r, Item: item, Key: ui.KeyN(l.key, 1<<32|uint64(item)),
		Selected: sel, Emphasised: l.Emphasised,
		Hover: l.hoverOn && r == l.hover, Cursor: l.hasCur && l.cursor == item,
		Ring: l.ring && l.hasCur && l.cursor == item, Disabled: l.off,
	}
	if sel {
		rs.PrevSelected = r > 0 && r-1 < l.Rows && st.Sel.Has(st.Item(r-1))
		rs.NextSelected = r+1 < l.Rows && st.Sel.Has(st.Item(r+1))
	}
	return rs
}

// BeginRow builds the box of the row rs and pushes it: as high as a row and as
// wide as the list, its children flowing left to right, in the style the recipe
// set with ui's Next calls and with the drawing flags it passes. It is a
// ui.RoleListItem (ui.RoleRow in a table) with its selection and position, and
// name as its accessible name (the bytes are copied; nil for none, which the
// first cell of a table row then gives it). An assistive technology's press
// selects it. CloseRow, or the next RowState, ends it.
func (l *List) BeginRow(rs *RowState, name []byte, flags ui.Flags) ui.ID {
	c := l.k.c
	if l.off {
		flags |= ui.Disabled // no semantic actions, and a queued press is rejected
	}
	var state ui.SemState
	if rs.Selected {
		state = ui.SemSelected
	}
	if rs.Expanded {
		state |= ui.SemExpanded
	}
	role := ui.RoleListItem
	if l.table {
		role = ui.RoleRow
	}
	c.NextSize(ui.Pct(1), ui.Px(l.rowH))
	c.NextAxis(ui.AxisX)
	c.NextRole(role)
	c.NextSemState(state)
	if rs.Expandable {
		c.NextSemActions(ui.ActPress | ui.ActFocus | ui.ActScrollIntoView | ui.ActExpand | ui.ActCollapse)
	}
	c.NextSemPos(rs.Row, l.Rows)
	if l.tree {
		c.NextSemLevel(rs.Level + 1)
	}
	if name != nil {
		c.NextSemNameBytes(name)
	}
	l.row = c.BoxKeyed(flags, rs.Key, nil)
	l.rowNamed = name != nil
	l.rowKey, l.rowItem, l.rowSel = rs.Key, rs.Item, rs.Selected
	c.Push(l.row)
	l.inRow = true
	if a := c.Signal(l.row).Action; a&ui.ActPress != 0 { // an assistive technology pressed the row
		l.st.Sel.Only(rs.Item)
		l.st.curRow, l.st.ancRow = rs.Row, rs.Row
		l.Changed = true
	}
	return l.row
}

// RowSelected reports whether the row being built is selected.
func (l *List) RowSelected() bool { return l.rowSel }

// RowKey is the key of the row being built.
func (l *List) RowKey() ui.Key { return l.rowKey }

// RowID is the box of the row being built.
func (l *List) RowID() ui.ID { return l.row }

// NameRow gives the row being built its accessible name, if it has none yet
// (b is copied).
func (l *List) NameRow(b []byte) {
	if !l.rowNamed {
		l.k.c.SetSemNameBytes(l.row, b)
		l.rowNamed = true
	}
}

// CloseRow ends the row being built, if one is open.
func (l *List) CloseRow() {
	if l.inRow {
		l.k.c.Pop()
		l.inRow = false
	}
}

// RowH is the height of a row in pixels.
func (l *List) RowH() float32 { return l.rowH }

// PadX and PadY are the room in pixels inside the list's box at the sides of
// the rows and above the first row (ListOpts.Pad).
func (l *List) PadX() float32 { return l.padX }

// PadY is the room in pixels above the first row and below the last.
func (l *List) PadY() float32 { return l.padT }

// FillerRows is how many rows' worth of room the view shows below the last
// row, as of the last frame: none unless the last row is built in this
// frame. A recipe that stripes its rows, as AppKit's alternating rows do,
// stripes this room too, building as many row-high boxes after the last row.
// A trackpad's stretch past the bottom shows more of it.
func (l *List) FillerRows() int {
	if l.Last < l.Rows || l.rowH <= 0 {
		return 0
	}
	r := l.k.c.Rect(l.s)
	view := r[3] - r[1] - l.padT - l.top
	used := float32(l.Rows)*l.rowH - float32(l.st.disp)
	if view <= used {
		return 0
	}
	return int(math.Ceil(float64((view - used) / l.rowH)))
}

// FillerTop is where the ith row of the room below the last row
// (FillerRows) shows, in pixels from the top of the list's scrolling box,
// stretch included. A recipe places those rows there as floating boxes, so
// that they are not content: they never make the list scrollable.
func (l *List) FillerTop(i int) float32 {
	return l.padT + l.top + float32(l.Rows+i)*l.rowH - float32(l.st.disp)
}

// BoxKey is the key of the list's outer box; the recipe keys its own boxes in
// the list (an empty-list message) from it.
func (l *List) BoxKey() ui.Key { return l.key }

// Kit is the kit the list was begun on.
func (l *List) Kit() *Kit { return l.k }

// CloseRows ends the last row and the scrolling box: the list's outer box is
// the current parent again, for the scroll bar (ScrollView).
func (l *List) CloseRows() {
	l.CloseRow()
	l.k.c.Pop()
}

// ScrollView is the list's scroll view, for its bar (Kit.ScrollBars).
func (l *List) ScrollView() ScrollView {
	return ScrollView{Outer: l.o, Inner: l.s, State: &l.st.Scroll, Axes: ScrollVertical, TopInset: l.top}
}

// CloseList ends the list, after CloseRows and its scroll bar.
func (k *Kit) CloseList(l *List) { k.c.Pop() }
