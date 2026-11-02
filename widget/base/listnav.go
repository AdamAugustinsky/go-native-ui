package base

import (
	"gonativeui/ui"
)

// NavMove is a move of a list's selection by the keyboard.
type NavMove uint8

const (
	// NavNone is no move.
	NavNone NavMove = iota
	// NavUp and NavDown go to the selectable row before and after.
	NavUp
	NavDown
	// NavPageUp and NavPageDown go a page up and down.
	NavPageUp
	NavPageDown
	// NavHome and NavEnd go to the first and the last selectable row.
	NavHome
	NavEnd
)

// navKeys are the keys of the moves, in the order a frame takes them.
var navKeys = [...]struct {
	code ui.KeyCode
	m    NavMove
}{
	{ui.KeyDown, NavDown}, {ui.KeyUp, NavUp}, {ui.KeyPageDown, NavPageDown}, {ui.KeyPageUp, NavPageUp},
	{ui.KeyHome, NavHome}, {ui.KeyEnd, NavEnd},
}

// TakeNavKey takes the first of Down, Up, Page Down, Page Up, Home and End
// that went down this frame without a modifier, or with shift also with
// Shift held, and says which move it is and whether Shift was held: the keys
// of a focused list, one move per frame (the frame's other presses stay
// queued for the next ones).
func TakeNavKey(c *ui.Context, shift bool) (m NavMove, shifted bool) {
	for _, k := range navKeys {
		if c.TakeKey(k.code, 0) {
			return k.m, false
		}
		if shift && c.TakeKey(k.code, ui.ModShift) {
			return k.m, true
		}
	}
	return NavNone, false
}

// navOK reports whether row r can be selected.
func navOK(ok func(int) bool, r int) bool { return ok == nil || ok(r) }

// navFind is the first selectable row from r going d (±1) among rows rows,
// -1 if there is none before the end.
func navFind(r, rows, d int, ok func(int) bool) int {
	for ; r >= 0 && r < rows; r += d {
		if navOK(ok, r) {
			return r
		}
	}
	return -1
}

// NavStep is the row move m takes the selection sel to in a list of rows
// rows, of which ok says which can be selected (nil: every row; a section's
// header cannot): it is what every list's keyboard does, so that they agree.
//
//   - Up and Down go to the selectable row before and after sel; at an end
//     they stay, or with wrap go round to the other end.
//   - Page Up and Page Down go page rows (at least 1) away, to the nearest
//     selectable row there, or the nearest before it on the way.
//   - Home and End go to the first and the last selectable row.
//
// With no selection (sel < 0) Up, Down, Page Up and Home go to the first
// selectable row (Up with wrap to the last), Page Down a page below it, and
// End to the last. It is -1 when no row can be selected.
func NavStep(sel, rows int, ok func(int) bool, m NavMove, page int, wrap bool) int {
	if rows <= 0 {
		return -1
	}
	first := navFind(0, rows, 1, ok)
	if first < 0 {
		return -1
	}
	last := navFind(rows-1, rows, -1, ok)
	page = max(page, 1)
	if sel < 0 || sel >= rows {
		switch {
		case m == NavEnd, m == NavUp && wrap:
			return last
		case m == NavPageDown:
			sel = 0 // a page below the first row
		default:
			return first
		}
	}
	switch m {
	case NavUp:
		if r := navFind(sel-1, rows, -1, ok); r >= 0 {
			return r
		}
		if wrap {
			return last
		}
		return navStay(sel, rows, ok)
	case NavDown:
		if r := navFind(sel+1, rows, 1, ok); r >= 0 {
			return r
		}
		if wrap {
			return first
		}
		return navStay(sel, rows, ok)
	case NavPageUp:
		t := max(sel-page, 0)
		if r := navFind(t, rows, -1, ok); r >= 0 {
			return r
		}
		return navFind(t, rows, 1, ok)
	case NavPageDown:
		t := min(sel+page, rows-1)
		if r := navFind(t, rows, 1, ok); r >= 0 {
			return r
		}
		return navFind(t, rows, -1, ok)
	case NavHome:
		return first
	case NavEnd:
		return last
	}
	return sel
}

// navStay is sel when it can be selected, else the nearest row that can.
func navStay(sel, rows int, ok func(int) bool) int {
	if navOK(ok, sel) {
		return sel
	}
	if r := navFind(sel, rows, 1, ok); r >= 0 {
		return r
	}
	return navFind(sel, rows, -1, ok)
}

// TypeAhead collects what is typed into a focused list for type-to-find: the
// characters typed in a quick run make one query, and a pause of a second
// starts a new one. Its zero value is ready; it holds no pointers.
type TypeAhead struct {
	buf [findMax]byte
	n   int
	t   int64 // Input.Time of the last character
}

// Feed adds the frame's typed text (not with the primary modifier or Option
// held) and returns the query when it started or grew this frame, nil
// otherwise. A space does not start a query (it activates a row), but is
// part of one already under way. The bytes alias the TypeAhead.
func (f *TypeAhead) Feed(in *ui.Input) []byte {
	if in.NText == 0 || in.Mods&(ui.PrimaryMod()|ui.ModAlt) != 0 {
		return nil
	}
	if in.Time-f.t > findResetNs {
		f.n = 0
	}
	if f.n == 0 && in.Text[0] == ' ' {
		return nil
	}
	f.n += copy(f.buf[f.n:], in.Text[:in.NText])
	f.t = in.Time
	return f.buf[:f.n]
}

// Typing reports whether a query is under way at time now (Input.Time): a
// space then belongs to it rather than activating the row.
func (f *TypeAhead) Typing(now int64) bool { return f.n > 0 && now-f.t <= findResetNs }

// ListNav is the keyboard and pointer navigation of a list whose rows the app
// builds: any height, any content, in any scroll view, with rows that cannot
// be selected (a section's header, a note) skipped. It is what a command
// palette's results, a sidebar of cards and a picker's rows share; rexui's
// BeginNavList, BeginFilter and BeginPalette build on it.
//
// One row is selected (Sel), which the keys move:
//
//   - with the focus on the list (Begin's focused): Up and Down, Page Up and
//     Page Down by the rows' real heights, Home and End; Enter (and Space,
//     when no type-to-find query is under way) activates the row; Escape
//     cancels; typing finds the next row whose name starts with what was
//     typed, ignoring case and accents;
//   - from a text field over the list (a search box) through ui's field
//     commands (NextFieldCommander(&nav, "")): Up, Down, Page Up, Page Down
//     and Enter, which arrive as edit actions on macOS and as keys elsewhere,
//     so the field keeps its caret and the list moves. Home and End stay the
//     field's, as do Escape and Tab.
//
// The selected row, once moved, scrolls into view in the view Begin names,
// below what covers its top (ui.Context.SetObscured: a title band). A row
// under the pointer becomes the selection only when the pointer moves
// (Hover), never because the list scrolled under a pointer standing still.
//
// Each frame:
//
//	c.NextFieldCommander(&v.nav, "")               // optional: a search field steers it
//	rexui.SearchField(k, "Search", &v.query, …)
//	sv := k.BeginScroll("results", &v.nav.Scroll, base.ScrollVertical)
//	v.nav.Begin(k, len(v.rows), sv.Inner, focused)
//	for i := range v.rows {
//		… build row i's box id, with v.nav.Sel == i as its selected look
//		v.nav.Row(k, i, id, name)
//		v.nav.Hover(k, i, c.Signal(id))          // optional: the pointer moves it
//	}
//	k.CloseScrollContent(&sv); … k.CloseScroll(&sv)
//	if s := v.nav.End(k); s.Activated { v.open(s.Row) }
//
// The zero value selects the first selectable row; set Sel to -1 for none.
// A row built with Skip (a section's header) cannot be selected from the
// frame after; Selectable, when set, decides instead, from the first frame
// and for rows not built (out of view). It is read during the frame and by
// the field's commands: give it a function made once (a method value kept in
// the app's state), so that building a frame allocates nothing. The buffers
// grow to the list's size once.
type ListNav struct {
	// Sel is the selected row, -1 for none. The app may set it.
	Sel int
	// Selectable says which rows can be selected (nil: those not built
	// with Skip).
	Selectable func(row int) bool
	// Wrap makes Up and Down go round at the ends (a popup's list).
	Wrap bool
	// Scroll is the rows' scroll view's state, for the view the app opens
	// with BeginScroll (the rexui wrappers use it).
	Scroll ScrollState

	rows    int
	okFn    func(int) bool // n.ok, made once
	view    ui.ID
	pageH   float32 // the view's height less what covers it, as of Begin
	cur     uint32  // the frame Begin last ran in
	done    uint32  // the frame End last ran in
	named   uint32  // the frame the names were last reset in
	geo     []navGeo
	names   []byte
	reveal  bool
	tries   uint8
	started bool
	focused bool
	ta      TypeAhead
	sig     NavSignal
}

// navGeo is where a row was laid out in its view's content, its name and
// the frame it was recorded in.
type navGeo struct {
	top, bot float32
	off      uint32
	n        uint16
	skip     bool // built with Skip: not selectable
	frame    uint32
}

// NavSignal is what a ListNav's frame did.
type NavSignal struct {
	// Row is the selected row as the frame ends (-1: none).
	Row int
	// Changed: the selection moved (the keys, the pointer, type-to-find).
	// Keyboard: the keys or type-to-find moved it.
	Changed, Keyboard bool
	// Activated: Enter (or Space) on the selected row, from the list or its
	// field, or the app's Activate.
	Activated bool
	// Cancelled: Escape with the focus on the list.
	Cancelled bool
}

// Reset starts the list over (new rows, a new query): the first selectable
// row is selected and brought into view.
func (n *ListNav) Reset() {
	n.Sel, n.started, n.reveal, n.tries = 0, false, true, 0
	n.ta = TypeAhead{}
}

// Select selects row r (clamped; -1 for none) and brings it into view.
func (n *ListNav) Select(r int) {
	if r != n.Sel {
		n.sig.Changed = true
	}
	n.Sel, n.started, n.reveal, n.tries = r, true, r >= 0, 0
}

// Activate reports the selected row activated in this frame's signal, as
// Enter does (a click on a row of a palette).
func (n *ListNav) Activate() {
	if n.Sel >= 0 && n.Sel < n.rows {
		n.sig.Activated = true
	}
}

// Rows is the number of rows Begin was last given.
func (n *ListNav) Rows() int { return n.rows }

// Move applies move m to the selection as the keyboard does, Page Up and
// Page Down by the rows' heights in the last frame, and reports whether a
// row was there to move to.
func (n *ListNav) Move(m NavMove) bool {
	if n.rows == 0 || m == NavNone {
		return false
	}
	n.normalise()
	to := -1
	switch m {
	case NavPageUp, NavPageDown:
		to = n.pageTarget(m == NavPageDown)
	default:
		to = NavStep(n.Sel, n.rows, n.okf(), m, 0, n.Wrap)
	}
	if to < 0 {
		return false
	}
	if to != n.Sel {
		n.sig.Changed, n.sig.Keyboard = true, true
	}
	n.Sel, n.reveal, n.tries = to, true, 0
	return true
}

// FieldCommand takes a text field's Up, Down, Page Up, Page Down and Enter for
// the list (ui.FieldCommander): register the nav for the field with
// ui.Context.NextFieldCommander(&nav, ""). Enter is taken only while a row is
// selected; everything else stays the field's.
func (n *ListNav) FieldCommand(cmd ui.FieldCommand, _ rune) bool {
	switch cmd {
	case ui.FieldMoveUp:
		return n.Move(NavUp)
	case ui.FieldMoveDown:
		return n.Move(NavDown)
	case ui.FieldPageUp:
		return n.Move(NavPageUp)
	case ui.FieldPageDown:
		return n.Move(NavPageDown)
	case ui.FieldInsertNewline:
		n.normalise()
		if n.Sel >= 0 && n.Sel < n.rows {
			n.sig.Activated = true
			return true
		}
	}
	return false
}

// ok reports whether row r can be selected: Selectable's answer, else not a
// row built with Skip in the last frame or this one.
func (n *ListNav) ok(r int) bool {
	if n.Selectable != nil {
		return n.Selectable(r)
	}
	return !n.valid(r) || !n.geo[r].skip
}

// CanSelect reports whether row r can be selected: Selectable's answer, else
// whether it was built with Row rather than Skip (a row not built yet can).
func (n *ListNav) CanSelect(r int) bool { return n.ok(r) }

// okf is ok as a function value, made once.
func (n *ListNav) okf() func(int) bool {
	if n.okFn == nil {
		n.okFn = n.ok
	}
	return n.okFn
}

// normalise keeps Sel on a selectable row: the zero value's first row, a
// row past the end clamped, a header the nearest row after it (or before).
func (n *ListNav) normalise() {
	if !n.started {
		n.started = true
		if n.Sel == 0 {
			n.Sel = NavStep(-1, n.rows, n.okf(), NavHome, 0, false)
		}
	}
	if n.Sel < 0 {
		n.Sel = -1
		return
	}
	if n.rows == 0 {
		n.Sel = -1
		return
	}
	n.Sel = min(n.Sel, n.rows-1)
	if !n.ok(n.Sel) {
		n.Sel = navStay(n.Sel, n.rows, n.okf())
	}
}

// valid reports whether row r's geometry is from the frame before or this
// one.
func (n *ListNav) valid(r int) bool {
	if r < 0 || r >= len(n.geo) {
		return false
	}
	f := n.geo[r].frame
	return f != 0 && (f == n.done || f == n.cur)
}

// viewH is the height of the view less what covers it, 0 without a view.
func (n *ListNav) viewH(c *ui.Context) float32 {
	if n.view == 0 {
		return 0
	}
	r := c.Rect(n.view)
	top, bot := c.Obscured(n.view)
	return max(r[3]-r[1]-top-bot, 0)
}

// pageTarget is the row a page down (down) or up takes the selection to: the
// farthest selectable row that still shows with the selected one at the
// view's edge, by the rows' positions in the last frame; a page of ten rows
// without them.
func (n *ListNav) pageTarget(down bool) int {
	m := NavPageUp
	if down {
		m = NavPageDown
	}
	cur := n.Sel
	h := n.pageH
	if cur < 0 || !n.valid(cur) || h <= 0 {
		return NavStep(cur, n.rows, n.okf(), m, 10, false)
	}
	g := n.geo[cur]
	best := -1
	if down {
		limit := g.top + h
		for r := cur + 1; r < n.rows && n.valid(r) && n.geo[r].bot <= limit+0.5; r++ {
			if n.ok(r) {
				best = r
			}
		}
		if best < 0 {
			best = NavStep(cur, n.rows, n.okf(), NavDown, 0, false)
		}
	} else {
		limit := g.bot - h
		for r := cur - 1; r >= 0 && n.valid(r) && n.geo[r].top >= limit-0.5; r-- {
			if n.ok(r) {
				best = r
			}
		}
		if best < 0 {
			best = NavStep(cur, n.rows, n.okf(), NavUp, 0, false)
		}
	}
	return best
}

// Begin starts the list's frame: rows rows (built this frame with Row), in
// the scroll view view (its Inner box; 0 when the rows do not scroll), and
// with focused the keys of a list that has the keyboard focus are taken here
// (see ListNav). Call it after the view is opened, before the rows.
func (n *ListNav) Begin(k *Kit, rows int, view ui.ID, focused bool) {
	c := k.c
	n.rows, n.view, n.focused = rows, view, focused
	n.cur = c.Frame()
	if cap(n.geo) < rows {
		n.geo = append(n.geo[:cap(n.geo)], make([]navGeo, rows-cap(n.geo))...)
	}
	n.geo = n.geo[:max(rows, len(n.geo))]
	n.pageH = n.viewH(c)
	n.normalise()
	if !focused || rows == 0 {
		return
	}
	in := c.Input()
	if m, _ := TakeNavKey(c, false); m != NavNone {
		n.Move(m)
		c.SetFocusVisible(true)
	}
	switch {
	case c.TakeKey(ui.KeyEnter, 0), !n.ta.Typing(in.Time) && c.TakeKey(ui.KeySpace, 0):
		if n.Sel >= 0 {
			n.sig.Activated = true
		}
	case c.TakeKey(ui.KeyEscape, 0):
		n.sig.Cancelled = true
	}
	if q := n.ta.Feed(in); q != nil {
		n.find(BytesStr(q))
	}
}

// find selects the next selectable row whose name starts with q: from the
// selected row itself while the query grows, from the one after it when it
// is one character (typing a letter again steps through the rows that start
// with it).
func (n *ListNav) find(q string) {
	start := max(n.Sel, 0)
	if len(q) == 1 || n.Sel < 0 {
		start = n.Sel + 1
	}
	for i := 0; i < n.rows; i++ {
		r := (start + i) % n.rows
		if !n.valid(r) || !n.ok(r) {
			continue
		}
		g := n.geo[r]
		if int(g.off)+int(g.n) > len(n.names) {
			continue
		}
		if FoldHasPrefix(BytesStr(n.names[g.off:g.off+uint32(g.n)]), q) {
			if r != n.Sel {
				n.sig.Changed = true
			}
			n.sig.Keyboard = true
			n.Sel, n.reveal, n.tries = r, true, 0
			return
		}
	}
}

// Row records row r, built this frame as the box id, and its accessible name
// (copied; the type-to-find text): where it is, for Page Up and Page Down and
// to bring it into view. Call it after the row's box is built.
func (n *ListNav) Row(k *Kit, r int, id ui.ID, name []byte) { n.row(k, r, id, name, false) }

// Skip records row r as Row does, as a row that cannot be selected: a
// section's header, a note between the rows. The keys and type-to-find pass
// it from the next frame on (Selectable, when set, decides instead).
func (n *ListNav) Skip(k *Kit, r int, id ui.ID) { n.row(k, r, id, nil, true) }

func (n *ListNav) row(k *Kit, r int, id ui.ID, name []byte, skip bool) {
	if r < 0 || r >= len(n.geo) {
		return
	}
	c := k.c
	if n.named != n.cur {
		n.named, n.names = n.cur, n.names[:0]
	}
	g := &n.geo[r]
	g.off, g.n, g.skip = uint32(len(n.names)), uint16(min(len(name), 0xFFFF)), skip
	n.names = append(n.names, name[:g.n]...)
	rr := c.Rect(id)
	if n.view != 0 && rr[3] > rr[1] {
		vr := c.Rect(n.view)
		_, sy := c.Scroll(n.view)
		g.top = rr[1] - vr[1] + float32(sy)
		g.bot = rr[3] - vr[1] + float32(sy)
		g.frame = n.cur
	} else if n.view == 0 {
		g.top, g.bot, g.frame = rr[1], rr[3], n.cur
	} else {
		g.frame = 0 // not laid out yet
	}
	if n.reveal && r == n.Sel && n.view != 0 && g.frame != 0 {
		n.reveal = false
		c.ScrollIntoView(n.view, [4]float32{0, g.top, 1, g.bot})
	}
}

// Hover makes row r the selection when the pointer moved over it this frame
// (sig is the row's signal): a palette's and a menu's rows follow the
// pointer so, while a list scrolling under a pointer that stands still
// changes nothing.
func (n *ListNav) Hover(k *Kit, r int, sig ui.Signal) {
	if sig.Hovering && k.MouseMoved() && r != n.Sel && n.ok(r) {
		n.Sel, n.started = r, true
		n.sig.Changed = true
	}
}

// End ends the frame and returns what it did. A selected row that was not yet
// laid out is brought into view in the next frames.
func (n *ListNav) End(k *Kit) NavSignal {
	n.done = n.cur
	if n.Sel >= 0 && n.Sel < n.rows && !n.ok(n.Sel) { // a header built where the selection was: the nearest row
		n.Sel, n.reveal, n.tries = navStay(n.Sel, n.rows, n.okf()), true, 0
	}
	if n.reveal && n.Sel >= 0 && n.view != 0 {
		if n.tries < 3 {
			n.tries++
			k.c.RequestFrame()
		} else {
			n.reveal = false
		}
	}
	s := n.sig
	s.Row = n.Sel
	n.sig = NavSignal{}
	return s
}

// Focused reports whether Begin was told the list has the keyboard focus.
func (n *ListNav) Focused() bool { return n.focused }
