package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The rows of the lists an app builds itself: a command palette's results, a
// picker's choices, a list of conversations with badges and two lines. A row
// is a box of any height and any content with the states every such list
// shares (the theme's row roles): a fill under the pointer that fades in at
// the hover rate, a deeper one while held, the selection, a hairline ring
// round it when the theme has one, and the focus ring on the selected row
// while its list has the keyboard. NavList puts rows in a scroll view and
// moves the selection with base.ListNav's keys.

// ListRowRecipe is the look of a row (BeginListRow, NavList, Palette).
type ListRowRecipe struct {
	// Hover fills a row under the pointer (at the box's hover animation),
	// Pressed one held, Selected the selected row in the key window and
	// SelectedInactive in another; Ring is a line round the selected row in
	// the key window (none when its alpha is 0), RingW wide in points (0: a
	// hairline), and Focus the ring round the selected row while its list
	// has the keyboard and the focus is visible, FocusW wide.
	Hover, Pressed, Selected, SelectedInactive, Ring, Focus Tint
	RingW, FocusW                                           float32
	// Text and Text2 colour a row's text and its secondary text (ListRow.Text).
	Text, Text2 Tint
	// Radius is a row's corner radius; PadX and PadY its padding and Gap the
	// space between its children (it lays them out left to right), in
	// points.
	Radius, PadX, PadY, Gap float32
	// Header is the colour of a section's header (NavList.Header), set in
	// HeaderStyle; HeaderPadX, HeaderTop and HeaderBottom its padding, in
	// points.
	Header                              Tint
	HeaderStyle                         TextStyle
	HeaderPadX, HeaderTop, HeaderBottom float32
}

// ListRowLook is the rows' recipe: the theme's row roles.
var ListRowLook = ListRowRecipe{
	Hover: Of(theme.RowHover), Pressed: Of(theme.RowPressed),
	Selected: Of(theme.RowSelected), SelectedInactive: Of(theme.RowSelectedInactive),
	Ring: Of(theme.RowSelectedRing), Focus: Of(theme.FocusRing), FocusW: 2,
	Text: Of(theme.Label), Text2: Of(theme.Label2),
	Radius: 6, PadX: 8, PadY: 4, Gap: 8,
	Header: Of(theme.Label2), HeaderStyle: TextCaption, HeaderPadX: 8, HeaderTop: 8, HeaderBottom: 4,
}

// ListRowOpts are a row's options, passed by value.
type ListRowOpts struct {
	// Selected shows the row selected; Focused shows the focus ring round it
	// too (the row is its list's selection and the list has the keyboard,
	// with the focus visible).
	Selected, Focused bool
	// Height is the row's height in points (0: as high as its content);
	// Radius replaces the recipe's corner radius, in points, when not 0.
	Height, Radius float32
	// Pos and Of are its place among its list's rows for assistive
	// technologies (Of 0: not given).
	Pos, Of int
	// Focusable makes the row a tab stop of its own (a row outside a
	// NavList, which takes the keyboard for its rows).
	Focusable bool
	// State adds to the row's state for assistive technologies
	// (ui.SemChecked: a picker's current choice).
	State ui.SemState
	// Vertical lays the row's children top to bottom (a card of lines),
	// from its top; otherwise left to right, centred.
	Vertical bool
	// With OwnPad, Pad is the row's padding (left, top, right, bottom) and
	// Gap the space between its children (0: none), in points; otherwise
	// the recipe's PadX, PadY and Gap.
	Pad    [4]float32
	OwnPad bool
	Gap    float32
	// Look replaces ListRowLook.
	Look *ListRowRecipe
}

// ListRow is a row being built (BeginListRow): its box and its colours.
type ListRow struct {
	base.Press
	look     *ListRowRecipe
	selected bool
}

// BeginListRow builds the box of a row with the label (its shown part is the
// accessible name: "Title###id" keeps the row's identity as its text changes)
// in the current parent, as wide as the parent, and pushes it: its children
// flow left to right. It is a list item (ui.RoleListItem) with its selected
// state, and reacts to the pointer with the theme's row roles. EndRow ends it.
//
//	row := rexui.BeginListRow(k, v.title+"###"+v.id, rexui.ListRowOpts{Selected: v.sel == i})
//	rexui.Label(k, v.title, rexui.LabelOpts{Color: row.Text()})
//	if rexui.EndListRow(k, &row).Clicked { v.sel = i }
func BeginListRow(k *base.Kit, label string, o ListRowOpts) ListRow {
	c, r := k.Context(), k.Theme()
	rc := o.Look
	if rc == nil {
		rc = &ListRowLook
	}
	p := k.PressState(label, base.HoverOn)
	row := ListRow{Press: p, look: rc, selected: o.Selected}
	v := &row.Visual
	var fill uint32
	switch {
	case o.Selected && v.Inactive:
		fill = rc.SelectedInactive.RGBA(r)
	case o.Selected:
		fill = rc.Selected.RGBA(r)
	case !v.Disabled:
		hover := rc.Hover.RGBA(r)
		fill = theme.WithAlpha(hover, v.Hot)
		if v.Down > 0 {
			fill = theme.MixRGBA(fill, rc.Pressed.RGBA(r), v.Down)
		}
	}
	sc := k.Scale()
	if fill&0xFF != 0 {
		c.NextBG(fill)
	}
	flags := ui.DrawBackground
	switch ring := rc.Ring.RGBA(r); {
	case o.Selected && o.Focused && !v.Inactive:
		c.NextBorder(rc.Focus.RGBA(r)|0xFF, pt(sc, rc.FocusW))
		flags |= ui.DrawBorder
	case o.Selected && ring&0xFF != 0 && !v.Inactive:
		w := r.Lines.Hairline
		if rc.RingW > 0 {
			w = pt(sc, rc.RingW)
		}
		c.NextBorder(ring, w)
		flags |= ui.DrawBorder
	}
	rad := rc.Radius
	if o.Radius > 0 {
		rad = o.Radius
	}
	c.NextRadius(pt(sc, rad))
	if o.Vertical {
		c.NextAxis(ui.AxisY)
	} else {
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
	}
	if o.OwnPad {
		c.NextGap(pt(sc, o.Gap))
		c.NextPadding(pt(sc, o.Pad[0]), pt(sc, o.Pad[1]), pt(sc, o.Pad[2]), pt(sc, o.Pad[3]))
	} else {
		c.NextGap(pt(sc, rc.Gap))
		px, py := pt(sc, rc.PadX), pt(sc, rc.PadY)
		c.NextPadding(px, py, px, py)
	}
	h := ui.Children(1)
	if o.Height > 0 {
		h = ui.Px(pt(sc, o.Height))
	}
	state := o.State
	if o.Selected {
		state |= ui.SemSelected
	}
	c.NextSemState(state)
	if o.Of > 0 {
		c.NextSemPos(o.Pos, o.Of)
	}
	if k.Disabled() {
		flags |= ui.Disabled
	} else {
		flags |= ui.Clickable | ui.HotAnimation | ui.ActiveAnimation
		if o.Focusable {
			flags |= ui.Focusable | ui.FocusAnimation
		}
	}
	k.BeginPress(&row.Press, ui.RoleListItem, ui.Pct(1), h, flags, nil)
	return row
}

// EndRow ends the row BeginListRow pushed and reports its signal.
func EndListRow(k *base.Kit, row *ListRow) base.Signal { return k.EndPress(&row.Press) }

// Text is the colour of the row's text, for a component's options
// (LabelOpts.Color).
func (row *ListRow) Text() Tint { return row.look.Text }

// Text2 is the colour of its secondary text (a time, a path, a shortcut).
func (row *ListRow) Text2() Tint { return row.look.Text2 }

// Selected reports whether the row shows selected.
func (row *ListRow) Selected() bool { return row.selected }

// NavListOpts are a NavList's options, passed by value.
type NavListOpts struct {
	// MaxHeight makes the list as tall as its rows, up to this many points;
	// 0 fills the parent's height (or NextSize's).
	MaxHeight float32
	// Top is the room at the top of the view that something covers (a title
	// band the rows scroll under), in pixels: the selection is brought into
	// view below it (base.Kit.NextScrollInset).
	Top float32
	// Gap is the space between rows and Pad the room round them (left, top,
	// right, bottom), in points.
	Gap float32
	Pad [4]float32
	// NoFocus keeps the list from taking the keyboard focus: a text field
	// over it steers it (a palette's search field, through
	// ui.Context.NextFieldCommander with the ListNav).
	NoFocus bool
	// FollowPointer makes a row the pointer moves over the selection, as a
	// menu's and a palette's do (never one the list scrolls under a pointer
	// that stands still).
	FollowPointer bool
	// ClickActivates makes a click on a row activate it (NavSignal.Activated),
	// as in a palette; otherwise a click selects and a double click activates.
	ClickActivates bool
}

// NavList is an open list of rows of any height that the keyboard moves
// through (base.ListNav): see BeginNavList.
type NavList struct {
	k       *base.Kit
	nav     *base.ListNav
	o       NavListOpts
	root    ui.ID
	key     ui.Key
	view    base.ScrollView
	focused bool
	ring    bool
	cur     int // the row being built, -1
	row     ListRow
	// the rows' places for assistive technologies, headers not counted: of
	// is how many can be selected, pos the next one's place, next the first
	// row not counted yet
	of, pos, next int
}

// BeginNavList starts a list of rows rows of any height and content, in a
// vertical scroll view as wide and high as its parent, whose selection is
// nav.Sel. With the keyboard focus (a click on a row, or Tab) Up and Down,
// Page Up and Page Down by the rows' heights, Home and End move it, Enter
// activates, Escape cancels and typing finds a row by its name; a text field
// steers it with ui.Context.NextFieldCommander(nav, "") (see base.ListNav).
// The selected row is brought into view, below what covers the top. Rows that
// are headers (Header) are passed.
//
//	l := rexui.BeginNavList(k, "Results", &v.nav, len(v.rows), rexui.NavListOpts{})
//	for i := range v.rows {
//		if v.rows[i].header {
//			l.Header(i, v.rows[i].title)
//			continue
//		}
//		row := l.Row(i, v.rows[i].title+"###"+v.rows[i].id, rexui.ListRowOpts{})
//		rexui.Label(k, v.rows[i].title, rexui.LabelOpts{Color: row.Text()})
//		l.EndRow()
//	}
//	if s := rexui.EndNavList(k, &l); s.Activated { v.open(s.Row) }
func BeginNavList(k *base.Kit, label string, nav *base.ListNav, rows int, o NavListOpts) NavList {
	c := k.Context()
	sc := k.Scale()
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	l := NavList{k: k, nav: nav, o: o, key: key, cur: -1}
	h := ui.Pct(1)
	if o.MaxHeight > 0 {
		h = ui.Children(1)
	}
	k.ApplySize(ui.Pct(1), h)
	c.NextRole(ui.RoleList)
	c.NextSemName(display)
	flags := ui.Clip
	if !o.NoFocus && !k.Disabled() {
		flags |= ui.Focusable | ui.FocusList
	}
	l.root = c.BoxKeyed(flags, key, nil)
	c.Push(l.root)
	l.focused = !o.NoFocus && c.Focused(l.root) && !k.Disabled()
	if o.MaxHeight > 0 {
		k.NextScrollMaxHeight(o.MaxHeight)
	}
	if o.Top > 0 {
		k.NextScrollInset(o.Top)
	}
	l.view = k.BeginScroll("rows", &nav.Scroll, base.ScrollVertical)
	nav.Begin(k, rows, l.view.Inner, l.focused)
	for r := range rows {
		if nav.CanSelect(r) {
			l.of++
		}
	}
	l.ring = (l.focused && c.FocusVisible()) || o.NoFocus && c.TextFieldFocused() && c.FocusVisible()
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextAxis(ui.AxisY)
	c.NextGap(pt(sc, o.Gap))
	c.NextPadding(pt(sc, o.Pad[0]), pt(sc, o.Pad[1]), pt(sc, o.Pad[2]), pt(sc, o.Pad[3]))
	c.Push(c.BoxKeyed(0, ui.KeyN(key, 1), nil))
	return l
}

// Row starts row r (one of the rows BeginNavList was given) with the label,
// as BeginListRow does, showing the list's selection; build its content, then
// EndRow. o's Selected and Focused are the list's to set.
func (l *NavList) Row(r int, label string, o ListRowOpts) *ListRow {
	l.EndRow()
	o.Selected = l.nav.Sel == r
	o.Focused = o.Selected && l.ring
	for ; l.next < r; l.next++ { // the rows passed: headers, notes, rows the app left out
		if l.nav.CanSelect(l.next) {
			l.pos++
		}
	}
	if o.Of == 0 {
		o.Pos, o.Of = l.pos, max(l.of, l.pos+1)
	}
	l.pos, l.next = l.pos+1, r+1
	o.Focusable = false
	l.row = BeginListRow(l.k, label, o)
	l.cur = r
	return &l.row
}

// EndRow ends the row being built, if one is, and reports its signal: a click
// selects it (and with ClickActivates activates it), a double click
// activates it.
func (l *NavList) EndRow() base.Signal {
	if l.cur < 0 {
		return base.Signal{}
	}
	r := l.cur
	l.cur = -1
	k, c := l.k, l.k.Context()
	s := EndListRow(k, &l.row)
	l.nav.Row(k, r, l.row.ID, base.StrBytes(l.row.Name))
	if l.o.FollowPointer {
		l.nav.Hover(k, r, c.Signal(l.row.ID))
	}
	if s.Clicked || s.DoubleClicked {
		l.nav.Select(r)
		if !l.o.NoFocus && !c.Focused(l.root) {
			c.SetFocus(l.key)
		}
		if l.o.ClickActivates || s.DoubleClicked {
			l.nav.Activate()
		}
	}
	return s
}

// Header builds row r as a section's header: title in the recipe's caption,
// a row the keys and type-to-find pass (base.ListNav.Skip).
func (l *NavList) Header(r int, title string) {
	l.EndRow()
	k, c := l.k, l.k.Context()
	rc := &ListRowLook
	sc := k.Scale()
	px := pt(sc, rc.HeaderPadX)
	c.NextPadding(px, pt(sc, rc.HeaderTop), px, pt(sc, rc.HeaderBottom))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextRole(ui.RoleHeading)
	c.NextSemName(title)
	id := c.BoxKeyed(0, ui.KeyN(l.key, 2<<32|uint64(uint32(r))), nil)
	c.Push(id)
	k.NextWidth(ui.Pct(1))
	Label(k, title+"###h", LabelOpts{Style: rc.HeaderStyle, Color: rc.Header})
	c.Pop()
	l.nav.Skip(k, r, id)
}

// Focused reports whether the list has the keyboard focus.
func (l *NavList) Focused() bool { return l.focused }

// Key is the key of the box that takes the list's keyboard focus: an app
// moves the focus to the list with ui.Context.SetFocus(l.Key()).
func (l *NavList) Key() ui.Key { return l.key }

// EndNavList ends the list (the rows, the scroll bar, the list) and returns
// what its frame did.
func EndNavList(k *base.Kit, l *NavList) base.NavSignal {
	l.EndRow()
	c := k.Context()
	c.Pop()
	k.CloseScrollContent(&l.view)
	ScrollBars(k, &l.view)
	k.CloseScroll(&l.view)
	c.Pop()
	return l.nav.End(k)
}
