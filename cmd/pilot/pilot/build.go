package pilot

import (
	"unsafe"

	"gonativeui/fs"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"
)

// fl is the flag set of a floating box.
const fl = theme.Floating

// Layout, in points.
const (
	minPanePt  = 240 // the least width of a pane
	showDatePt = 240 // the Date Modified column shows from this width of the list
	showSizePt = 330 // the Size column shows from this width of the list
	nameColPt  = 120 // the least width of the Name column
	dateColPt  = 140 // the Date Modified column: sixteen digits and the cells' padding
	sizeColPt  = 80  // the Size column: "123.4 MB" and the padding
	grabPt     = 9   // the panes' divider: its grab area across the one-point line
)

// The columns of the file list. A column's ID is the fs.SortKey it sorts by,
// plus one (0 is not an ID).
const (
	colName uint32 = uint32(fs.SortName) + 1
	colSize        = uint32(fs.SortSize) + 1
	colMod         = uint32(fs.SortModified) + 1
)

func sortKeyOf(id uint32) fs.SortKey { return fs.SortKey(id - 1) }

var (
	keyRoot     = ui.HashString(0, "pilot")
	keyArea     = ui.HashString(keyRoot, "area")
	keyPaneRoot = ui.HashString(keyRoot, "pane")
	loadingTx   = []byte("Loading…")
	emptyTx     = []byte("Empty folder")
	noMatchTx   = []byte("No matches")
	dashTx      = []byte("--")
)

// The keys of a pane's parts. The widgets derive theirs from their parents', so
// these are enough to name the table and the fields before they exist (a key
// press that moves the focus to the filter field opens it in the same frame).
func paneKey(i int) ui.Key      { return ui.KeyN(keyPaneRoot, uint64(i)) }
func contentKey(i int) ui.Key   { return ui.KeyN(paneKey(i), 1) }
func colKey(i int) ui.Key       { return ui.KeyN(paneKey(i), 2) }
func pathBoxKey(i int) ui.Key   { return ui.KeyN(paneKey(i), 3) }
func filterBoxKey(i int) ui.Key { return ui.KeyN(paneKey(i), 4) }
func statusKey(i int) ui.Key    { return ui.KeyN(paneKey(i), 5) }
func barKey(i int) ui.Key       { return ui.KeyN(paneKey(i), 6) }

// listKey is the key of the file list, which takes the keyboard focus.
func listKey(i int) ui.Key   { return widget.TableKey(colKey(i), "files") }
func pathKey(i int) ui.Key   { return widget.FieldKey(pathBoxKey(i), "path") }
func filterKey(i int) ui.Key { return widget.FieldKey(filterBoxKey(i), "filter") }

// geometry is what the model and the benchmark's script need of the last
// frame's layout, in window pixels: the pane, the list under the header, the
// header, the row pitch, and the columns shown (x0, x1; zero when hidden).
type geometry struct {
	rect, list, hdr [4]float32
	rowPx           float32
	visRows         int
	cols            [3][2]float32
}

// Build builds the frame: the kit's frame, the model's work, then every box. It
// implements app.Builder.
func (pl *Pilot) Build(c *ui.Context) {
	w, h := c.Size()
	if w <= 0 || h <= 0 || c.Scale() <= 0 {
		return
	}
	k := pl.k
	k.Begin(c)
	pl.lastW, pl.lastH = w, h
	pl.sys.setScale(c.Scale())
	if c.Input().FocusGained {
		pl.sys.refreshStyle()
	}
	if pl.sys.cache.Poll() { // icons decoded: the uploads are for the next frame
		c.RequestFrame()
	}
	pl.poll()
	pl.applyScroll()
	pl.update(c)
	pl.work()
	pl.draw(c)
	pl.workLeft(c)
	k.End()
	pl.prevFocus = c.FocusKey()
}

// applyScroll scrolls the left list under the toolbar when Options.ScrollLeft
// asks, once its rows are there (130 pt, as the finder scene's scrolled
// capture).
func (pl *Pilot) applyScroll() {
	if !pl.opt.ScrollLeft || pl.scrollDone {
		return
	}
	if v := &pl.panes[0].v; v.nRows() > 8 && v.ts.RowPx() > 0 { // the row height is known from the first frame on
		top := float32(130-5) / 24
		v.ts.SetTopRow(int(top), top-float32(int(top)))
		pl.scrollDone = true
	}
}

// work advances the panes' bounded background-style work by one slice. It runs
// before the frame is drawn, so that the frame shows what the work finished (a
// reconciled listing swapped in, the rows of a filter); work done after the
// draw would leave the old state on screen until something else asked for a
// frame.
func (pl *Pilot) work() {
	for _, p := range pl.panes {
		p.work()
	}
}

// workLeft asks for another frame while any pane has work left, including work
// the draw itself started (a changed filter, a submitted path).
func (pl *Pilot) workLeft(c *ui.Context) {
	for _, p := range pl.panes {
		if p.rc.active || p.jobBusy {
			c.RequestFrame()
		}
	}
}

// draw builds the window as the finder scene has it: the sidebar's glass full
// height at the left, the lists to its right running under the toolbar, and the
// toolbar over them with its band clear (the lists draw the scroll edge, and
// their headers lie in its pocket).
func (pl *Pilot) draw(c *ui.Context) {
	k := pl.bk
	r := k.Theme()
	w, h := c.Size()
	W, H := float32(w), float32(h)
	tbH := theme.Snap(r.Window.Toolbars[toolbarStyle].Height)
	var sbW float32
	if pl.sidebar {
		sbW = theme.Snap(sidebarPt * r.Scale)
		theme.At(c, 0, 0, sbW, H)
		c.Push(c.Box(base.Floating, "sidebar"))
		pl.drawSidebar(tbH)
		c.Pop()
	}
	// The lists run under the toolbar's band, their scroll views inset by it
	// (TableOpts.TopInset), and draw the scroll edge themselves: the toolbar
	// comes after them, over them, with its band clear (ContentEdge).
	pl.tbH = tbH
	theme.At(c, sbW, 0, W-sbW, H)
	area := c.BoxKeyed(fl, keyArea, nil)
	c.Push(area)
	pl.drawPanes(c, r, sbW, tbH)
	c.Pop()
	pl.drawToolbar(c, sbW)
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// drawPanes builds the splitter with the two lists, and the line between them.
func (pl *Pilot) drawPanes(c *ui.Context, r *theme.Resolved, ox, oy float32) {
	k := pl.bk
	sp := k.BeginSplitter("panes", &pl.split, ui.AxisX)
	pl.frameRows = 0
	for i := range pl.panes {
		pr := sp.PaneRect(i)
		pl.geo[i].rect = [4]float32{ox + pr[0], max(pr[1], oy), ox + pr[2], pr[3]} // below the band: the toolbar's presses are not the pane's
		sp.Pane(i)
		pl.drawPane(c, r, i, pr[2]-pr[0], pr[3]-pr[1])
		sp.EndPane()
	}
	for i := 0; i < sp.Dividers(); i++ {
		pl.drawDivider(c, r, &sp, i)
	}
	k.CloseSplitter(&sp)
	if pl.counting {
		pl.buildCalls++
		pl.rowsBuilt += pl.frameRows
		pl.rowsMax = max(pl.rowsMax, pl.frameRows)
	}
}

// drawDivider builds the line between the panes (the separator colour, the
// gutter's one point) and the grab area over it, with the resize cursor; the
// divider shows a focus ring when it has the keyboard.
func (pl *Pilot) drawDivider(c *ui.Context, r *theme.Resolved, sp *base.Splitter, i int) {
	k := pl.bk
	d := sp.Divider(i)
	top := pl.tbH
	theme.At(c, d.Gutter[0], top, d.Gutter[2]-d.Gutter[0], d.Bounds[3]-top)
	c.NextBG(r.Colors[theme.Separator])
	c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(d.Key, 9101), nil)
	gx := (d.Gutter[0] + d.Gutter[2]) / 2
	gw := theme.Snap(grabPt * r.Scale)
	theme.At(c, theme.Snap(gx-gw/2), top, gw, d.Bounds[3]-top)
	k.NextDivider(sp, &d)
	c.NextCursor(ui.CursorResizeEW)
	c.BoxKeyed(ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|fl, d.Key, nil)
	if d.Ring > 0.004 {
		lw := theme.Snap(r.Px.FocusRingW)
		theme.At(c, theme.Snap(gx-lw/2), top, lw, d.Bounds[3]-top)
		c.NextBG(theme.WithAlpha(r.Colors[theme.FocusRing], d.Ring))
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(d.Key, 9100), nil)
	}
}

// vbox builds a box that stacks its children top to bottom, pushes it, and
// returns it; the caller pops it. Sizes are in points where they are numbers.
func (pl *Pilot) vbox(c *ui.Context, key ui.Key, w, h ui.Size, padL, padT, padR, padB, gap float32) ui.ID {
	k := pl.k
	c.NextSize(w, h)
	c.NextAxis(ui.AxisY)
	c.NextGap(k.Pt(gap))
	c.NextPadding(k.Pt(padL), k.Pt(padT), k.Pt(padR), k.Pt(padB))
	id := c.BoxKeyed(0, key, nil)
	c.Push(id)
	return id
}

// ---- one pane ----

// drawPane builds pane i: the list down to the bars at its foot (the filter
// when it is open, the path bar, the status line).
func (pl *Pilot) drawPane(c *ui.Context, r *theme.Resolved, i int, w, h float32) {
	p := pl.panes[i]
	s := r.Scale
	pl.vbox(c, colKey(i), ui.Pct(1), ui.Pct(1), 0, 0, 0, 0, 0)
	foot := theme.Snap(pathBarPt*s) + theme.Snap(statusPt*s)
	if p.filterOpen {
		foot += theme.Snap(filterPt * s)
	}
	pl.drawList(c, r, i, p, w, max(h-foot, 0))
	pl.drawFilterField(c, r, i, p, w)
	pl.drawPathBar(c, r, i, p, w)
	pl.drawStatus(c, r, i, p, w)
	c.Pop() // the column
}

func (pl *Pilot) pathSignals(c *ui.Context, i int, p *pane, f widget.FieldSignal) {
	switch {
	case f.Cancelled:
		p.path.SetString(p.dir)
		p.pathErr = ""
		p.pathEdit = false
		pl.focusList(c)
	case f.Submitted:
		p.goTo(p.path.String())
		p.pathEdit = false
		pl.active = i
		pl.focusList(c)
	case f.Changed:
		p.pathErr = ""
	}
	if f.Focused && f.Pressed && pl.prevFocus != pathKey(i) { // a click that brought focus here selects all
		p.path.SelectAll()
	}
	if pl.prevFocus == pathKey(i) && c.FocusKey() != pathKey(i) && p.pathEdit { // left the field (Tab): the edit is dropped, as Esc does
		p.path.SetString(p.dir)
		p.pathErr = ""
		p.pathEdit = false
	}
}

func (pl *Pilot) drawFilterField(c *ui.Context, r *theme.Resolved, i int, p *pane, w float32) {
	if !p.filterOpen {
		return
	}
	s := r.Scale
	pl.band(c, r, ui.KeyN(paneKey(i), 22), w, theme.Snap(filterPt*s), true)
	theme.At(c, theme.Snap(8*s), theme.Snap(5*s), max(w-theme.Snap(16*s), 0), theme.Snap(24*s))
	c.Push(c.BoxKeyed(base.Floating, filterBoxKey(i), nil))
	f := rexui.SearchField(pl.bk, "filter", &p.filter, "Filter", rexui.FieldOpts{})
	c.Pop()
	c.Pop() // the band
	if f.Changed {
		p.setQuery()
	}
}

// columns is the columns of pane p's list shown at list width wPx: the Name
// column always, Date Modified from 240 pt, Size from 330 pt.
func (pl *Pilot) columns(p *pane, wPx float32) []widget.Column {
	sc := pl.k.Theme().Scale
	n := 1
	if wPx >= showDatePt*sc {
		n = 2
	}
	if wPx >= showSizePt*sc {
		n = 3
	}
	return p.cols[:n]
}

func (p *pane) initColumns() {
	p.cols = [3]widget.Column{
		{ID: colName, Title: "Name", Width: nameColPt, MinWidth: nameColPt, Fill: true, Sortable: true},
		{ID: colMod, Title: "Date Modified", Width: dateColPt, MinWidth: 110, Sortable: true},
		{ID: colSize, Title: "Size", Width: sizeColPt, MinWidth: 56, Align: ui.AlignRight, Sortable: true},
	}
}

// drawList builds the table of pane i, h pixels high.
func (pl *Pilot) drawList(c *ui.Context, r *theme.Resolved, i int, p *pane, paneW, h float32) {
	k := pl.bk
	v := &p.v
	g := &pl.geo[i]
	nRows := v.nRows()
	s := r.Scale

	v.ts.Order = v.order()
	v.ts.Sort = widget.SortState{Active: true, Column: uint32(p.sortKey) + 1, Descending: p.desc}
	if p.scrollRow >= 0 { // a swapped-in listing: the place the view had
		v.ts.SetTopRow(p.scrollRow, p.scrollOff)
		p.scrollRow = -1
	}
	if p.reveal >= 0 && nRows > 0 {
		v.ts.Reveal(p.reveal)
		p.reveal = -1
	}

	cols := pl.columns(p, paneW)
	k.NextSize(ui.Px(paneW), ui.Px(h))
	t := rexui.BeginTable(k, "files", &v.ts, cols, nRows, rexui.TableOpts{Striped: true, ClearHeader: true, TopInset: pl.tbH})
	if t.SortChanged {
		p.sortBy(sortKeyOf(t.Sort.Column))
		pl.setActive(c, i)
	}
	if t.Activated {
		p.openEntry(int32(t.Item))
		pl.setActive(c, i)
	}
	g.rowPx = v.ts.RowPx()
	lr := c.RectOf(t.Key())
	g.list, g.hdr = lr, c.RectOf(t.HeaderKey()) // the header is built in EndTable: the last frame's
	if g.hdr[2] <= g.hdr[0] {
		g.hdr = [4]float32{lr[0], lr[1] + pl.tbH, lr[2], lr[1] + pl.tbH + theme.Snap(r.Lines.HeaderH)}
	}
	g.visRows = 0
	if g.rowPx > 0 {
		g.visRows = int((lr[3] - g.hdr[1]) / g.rowPx) // the header's top to the foot: what a page of rows is
	}
	g.cols = [3][2]float32{}
	x := g.hdr[0]
	for ci := range cols {
		wd := t.ColumnWidth(ci)
		g.cols[cols[ci].ID-1] = [2]float32{x, x + wd} // by sort key: name, size, modified
		x += wd
	}
	showDate, showSize := len(cols) > 1, len(cols) > 2

	body := r.Styles[theme.StyleBody].Font
	if pl.opt.Mono {
		body = r.Font.Mono
	}
	iconW := theme.Snap(fileIconPt * s)
	if v.l != nil {
		pl.frameRows += t.Last - t.First
		for row := t.First; row < t.Last; row++ {
			e := v.entryAt(row)
			ent := &v.l.Entries[e]
			name := v.l.Name(int(e))
			t.Row(row, uint32(e), name)
			text1, text2 := t.TextColor(), t.Text2Color()
			if ent.Flags&fs.FlagHidden != 0 {
				text1 = text2
			}

			// the name: the system's icon in its 16 pt square, the text 5 pt
			// after it (AppKit's image view and text field). The icons differ
			// in ink as the system's do, so the column audit does not apply.
			c.NextAuditExempt(ui.AuditColumn.Mask())
			t.BeginCell(0)
			cell := c.Parent()
			padL, padR := t.CellPads(0)
			c.NextSize(ui.Px(iconW+theme.Snap(5*s)-r.Px.GapSmall), ui.Pct(1))
			c.Push(c.BoxKeyed(0, ui.KeyN(c.KeyOf(cell), 1), nil))
			pl.fileIcon(c, i, ent, name, iconW, t.RowH())
			c.Pop()
			c.NextSize(ui.Px(max(t.ColumnWidth(0)-padL-padR-iconW-theme.Snap(5*s), 0)), ui.Pct(1)) // (this frame's width: a new row's cell has no rectangle yet)
			c.NextFont(body)
			c.NextTextColor(text1)
			c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(c.KeyOf(cell), 2), name)
			t.EndCell()

			if showDate {
				f := c.Fmt()
				appendDate(f, ent.MTime, pl.loc)
				pl.cell(c, &t, 1, body, text2, f.Bytes())
			}
			if showSize {
				var tx []byte
				if ent.Kind == fs.KindDir {
					tx = dashTx
				} else {
					f := c.Fmt()
					appendSize(f, ent.Size)
					tx = f.Bytes()
				}
				if pl.opt.Mono || ent.Kind == fs.KindDir { // Menlo's digits are one width already
					pl.cell(c, &t, 2, body, text2, tx)
				} else {
					pl.sizeCell(c, &t, 2, body, text2, tx)
				}
			}
		}
	}
	// the body of a list without rows
	var msg []byte
	switch {
	case p.loading():
		msg = loadingTx
	case p.jobBusy:
	case len(p.q) > 0:
		msg = noMatchTx
	default:
		msg = emptyTx
	}
	t.Empty(msg)
	rexui.EndTable(k, &t)

	// the cursor row of the pane that was just built: what Build keeps for the model
	row, frac := v.ts.TopRow()
	p.topRow, p.topFrac, p.topEntry = row, frac, -1
	if v.l != nil && row >= 0 && row < nRows {
		p.topEntry = v.entryAt(row)
	}
}

// cell builds text cell col of the file row being built in font and colour of
// its own, in the column's alignment, with an ellipsis where it does not fit.
func (pl *Pilot) cell(c *ui.Context, t *rexui.Table, col int, font text.FontKey, color uint32, tx []byte) {
	c.NextFont(font)
	c.NextTextColor(color)
	c.NextAlign(t.Columns()[col].Align)
	t.TableHead.TextCell(&t.List.List, col, tx, ui.DrawText|ui.EllipsisOverflowX)
}

// sizeCell builds cell col of the file row being built as a size in tabular
// digits (the system font's digits are proportional, so a column of sizes
// would not line up): each digit takes the widest digit's advance, the
// number ends at the cell's right edge and its unit follows it, as Finder's
// monospaced-digit text does.
func (pl *Pilot) sizeCell(c *ui.Context, t *rexui.Table, col int, font text.FontKey, color uint32, tx []byte) {
	k := pl.bk
	if pl.digitFont != font {
		pl.digitFont, pl.digitW = font, 0
		for d := byte('0'); d <= '9'; d++ {
			pl.digitW = max(pl.digitW, k.TextWidth(font, string(rune(d))))
		}
	}
	n := 0
	for n < len(tx) && (tx[n] >= '0' && tx[n] <= '9' || tx[n] == '.') {
		n++
	}
	c.NextSemNameBytes(tx)
	// the number is boxes of whole pixels, each digit in its own cell: its ink
	// ends within a pixel of the column's, which the column audit (0.5 px) does
	// not take for the same edge
	c.NextAuditExempt(ui.AuditColumn.Mask())
	t.BeginCell(col)
	key := c.ParentKey()
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisX)
	c.NextGap(0)
	c.NextJustify(ui.JustifyEnd)
	c.NextCross(ui.CrossCenter)
	c.Push(c.BoxKeyed(0, ui.KeyN(key, 1), nil))
	for i := 0; i < n; i++ {
		if tx[i] == '.' {
			c.NextSize(ui.TextSize(0), ui.Pct(1))
			c.NextAlign(ui.AlignRight)
		} else {
			c.NextSize(ui.Px(pl.digitW), ui.Pct(1))
			c.NextAlign(ui.AlignCenter)
		}
		c.NextFont(font)
		c.NextTextColor(color)
		c.BoxKeyed(ui.DrawText, ui.KeyN(key, 100+uint64(i)), tx[i:i+1])
	}
	if n < len(tx) {
		c.NextSize(ui.TextSize(0), ui.Pct(1))
		c.NextAlign(ui.AlignRight) // (the audit reads a column's alignment from its widest text: the unit)
		c.NextFont(font)
		c.NextTextColor(color)
		c.BoxKeyed(ui.DrawText, ui.KeyN(key, 99), tx[n:])
	}
	c.Pop()
	t.EndCell()
}

// drawStatus builds the status line of pane i, centred in its band at the
// foot: an error or a note, or the counts of the listing and the selection.
func (pl *Pilot) drawStatus(c *ui.Context, r *theme.Resolved, i int, p *pane, w float32) {
	v := &p.v
	var tx []byte
	col := r.Colors[theme.Label2]
	switch {
	case p.msg != "":
		tx = strBytes(p.msg)
		if p.msgErr {
			col = r.Pal.Danger
		}
	case p.loading():
		tx = loadingTx
	default:
		f := c.Fmt()
		appendCount(f, v.nRows())
		if p.jobBusy {
			f.Str("…")
		}
		f.Str(" items")
		if n := v.nSel(); n > 0 {
			f.Str(", ")
			appendCount(f, n)
			f.Str(" selected")
			if b := v.selBytes(); b > 0 {
				f.Str(" (")
				appendSize(f, b)
				f.Byte(')')
			}
		}
		if !p.hideHidden {
			f.Str(", hidden shown")
		}
		tx = f.Bytes()
	}
	s := r.Scale
	pl.band(c, r, statusKey(i), w, theme.Snap(statusPt*s), false)
	theme.At(c, theme.Snap(8*s), 0, max(w-theme.Snap(16*s), 0), theme.Snap(statusPt*s))
	st := &r.Styles[theme.StyleSubheadline]
	c.NextFont(st.Font)
	c.NextTextColor(col)
	c.NextAlign(ui.AlignCenter)
	c.NextCross(ui.CrossCenter)
	c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX|fl, ui.KeyN(statusKey(i), 2), tx)
	c.Pop()
}

// strBytes views s as bytes without copying; the bytes must not be changed.
func strBytes(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }
