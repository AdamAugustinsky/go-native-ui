package pilot

import (
	"unsafe"

	"gonativeui/fs"
	"gonativeui/icon"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
)

// fl is the flag set of a floating box.
const fl = theme.Floating

// Layout, in points.
const (
	listRowPt    = 16  // the height of a row of the file list: dense, like a terminal's
	statusHPt    = 20  // the status line
	minPanePt    = 220 // the least width of a pane
	paneInsetPt  = 4   // the room at the sides of a pane's content: the list and the fields start 4 pt in
	paneTopPt    = 8   // and the path field this far below the pane's top edge
	paneGapPt    = 3   // between the parts of a pane
	fieldHPt     = 26  // the path and filter fields, a little lower than the theme's controls
	fieldInsetPt = 4   // and this much further in than the list
	showSizePt   = 240 // the Size column shows from this width of the list
	showDatePt   = 330 // the Modified column shows from this width of the list
	nameColPt    = 80  // the least width of the Name column
	sizeColPt    = 80  // the Size column: eight cells of 7.5 pt, the cell's padding and a little room
	dateColPt    = 132 // the Modified column: sixteen cells and the padding
	navFirstPt   = 90  // the titlebar's back button starts here, clear of the window's own buttons
	navGapPt     = 2
	titleGapPt   = 26 // between the buttons and the title
	titleRightPt = 24
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
	keyFocus    = ui.HashString(keyRoot, "focus")
	keyArea     = ui.HashString(keyRoot, "area")
	keyNav      = ui.HashString(keyRoot, "nav")
	keyTitle    = ui.HashString(keyRoot, "title")
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
	pl.poll()
	pl.update(c)
	pl.work()
	pl.draw(c)
	pl.workLeft(c)
	k.End()
	pl.prevFocus = c.FocusKey()
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

func (pl *Pilot) draw(c *ui.Context) {
	k := pl.k
	r := k.Theme()
	k.Background()
	pl.drawTitlebar(c, r)
	pl.drawPanes(c, r)
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// surface is the chrome's description of pane i's surface.
// origin is where the box the surface is built in starts in the window: the
// glass samples the window background there.
func surface(i int, rect [4]float32, origin [2]float32, focus float32) theme.Surface {
	return theme.Surface{Key: paneKey(i), Slot: i, Rect: rect, Origin: origin, Focus: focus, Opacity: 1, Top: true}
}

// drawPanes builds the splitter with the two glass surfaces.
func (pl *Pilot) drawPanes(c *ui.Context, r *theme.Resolved) {
	k := pl.k
	w, h := c.Size()
	// the panes start half a point above the titlebar's lower edge, as in the Rex reference
	ox, oy := theme.Snap(r.Px.Inset), theme.Snap(r.Px.TitlebarH-0.5*r.Scale)
	theme.At(c, ox, oy, float32(w)-2*ox, float32(h)-oy-ox)
	area := c.BoxKeyed(fl, keyArea, nil)
	c.Push(area)
	sp := k.BeginSplitter("panes", &pl.split, ui.AxisX)
	var ft [2]float32
	for i := range pl.panes {
		ft[i] = c.AnimateValue(ui.KeyN(keyFocus, uint64(i)), b2f(i == pl.active), r.Motion.Dim)
	}
	for i := range pl.panes {
		r.Chrome.SurfaceShadow(c, r, surface(i, sp.PaneRect(i), [2]float32{ox, oy}, ft[i]))
	}
	pl.frameRows = 0
	for i := range pl.panes {
		pr := sp.PaneRect(i)
		pl.geo[i].rect = [4]float32{ox + pr[0], oy + pr[1], ox + pr[2], oy + pr[3]}
		sp.Pane(i)
		pl.drawPane(c, r, i, pr[2]-pr[0], pr[3]-pr[1], [2]float32{ox + pr[0], oy + pr[1]}, ft[i])
		sp.EndPane()
	}
	k.EndSplitter(&sp)
	c.Pop()
	if pl.counting {
		pl.buildCalls++
		pl.rowsBuilt += pl.frameRows
		pl.rowsMax = max(pl.rowsMax, pl.frameRows)
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

func (pl *Pilot) drawPane(c *ui.Context, r *theme.Resolved, i int, w, h float32, origin [2]float32, ft float32) {
	p := pl.panes[i]
	s := surface(i, [4]float32{0, 0, w, h}, origin, ft)
	kg := r.Chrome.BeginSurface(c, r, s)
	// The surface's fill and edges are floating boxes, which draw above the
	// flow's, so the content is a floating box too (built after them).
	theme.At(c, 0, 0, w, h)
	wrap := c.BoxKeyed(fl, contentKey(i), nil)
	c.Push(wrap)

	pl.vbox(c, colKey(i), ui.Pct(1), ui.Pct(1), paneInsetPt, paneTopPt, paneInsetPt, 0, paneGapPt)
	pl.drawPathField(c, r, i, p)
	pl.drawList(c, r, i, p, w, ft)
	pl.drawFilterField(c, i, p)
	pl.drawStatus(c, r, i, p)
	c.Pop() // the column

	// The active pane: an accent bar along the top edge.
	if ft > 0.01 {
		sc := r.Scale
		theme.At(c, 22*sc, 0, w-44*sc, 2*sc)
		c.NextBG(theme.WithAlpha(r.Pal.Accent, ft))
		c.NextRadius(sc)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(paneKey(i), 7), nil)
	}
	c.Pop() // the wrap
	r.Chrome.EndSurface(c, r, s, kg)
}

func (pl *Pilot) drawPathField(c *ui.Context, r *theme.Resolved, i int, p *pane) {
	k := pl.k
	pl.vbox(c, pathBoxKey(i), ui.Pct(1), ui.Px(theme.Snap(k.Pt(fieldHPt))), fieldInsetPt, 0, fieldInsetPt, 0, 0)
	k.NextHeight(ui.Px(theme.Snap(k.Pt(fieldHPt))))
	k.NextMono()
	f := k.TextField("path", &p.path, "")
	if p.pathErr != "" { // the reason, inside the field at its right end
		cw := theme.Snap(k.Pt(fieldHPt))
		fm := r.Met(r.Font.Small)
		fw := c.Rect(c.Parent())
		wd := fw[2] - fw[0]
		theme.At(c, wd*0.4, theme.Snap((cw-fm.H)/2), max(wd*0.6-k.Pt(fieldInsetPt)-r.Px.Pad-r.Px.Gap, 0), fm.H)
		c.NextFont(r.Font.Small)
		c.NextTextColor(r.Pal.Danger)
		c.NextAlign(ui.AlignRight)
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|fl, ui.KeyN(pathBoxKey(i), 77), strBytes(p.pathErr))
	}
	c.Pop()
	switch {
	case f.Cancelled:
		p.path.SetString(p.dir)
		p.pathErr = ""
		pl.focusList(c)
	case f.Submitted:
		p.goTo(p.path.String())
		pl.active = i
		pl.focusList(c)
	case f.Changed:
		p.pathErr = ""
	}
	if f.Focused && f.Pressed && pl.prevFocus != pathKey(i) { // a click that brought focus here selects all
		p.path.SelectAll()
	}
}

func (pl *Pilot) drawFilterField(c *ui.Context, i int, p *pane) {
	if !p.filterOpen {
		return
	}
	k := pl.k
	pl.vbox(c, filterBoxKey(i), ui.Pct(1), ui.Px(theme.Snap(k.Pt(fieldHPt))), fieldInsetPt, 0, fieldInsetPt, 0, 0)
	k.NextHeight(ui.Px(theme.Snap(k.Pt(fieldHPt))))
	k.NextMono()
	f := k.TextField("filter", &p.filter, "Filter")
	c.Pop()
	if f.Changed {
		p.setQuery()
	}
}

// columns is the columns of pane p's list shown at list width wPx: the Name
// column always, Size from 240 pt, Modified from 330 pt.
func (pl *Pilot) columns(p *pane, wPx float32) []widget.Column {
	sc := pl.k.Theme().Scale
	n := 1
	if wPx >= showSizePt*sc {
		n = 2
	}
	if wPx >= showDatePt*sc {
		n = 3
	}
	return p.cols[:n]
}

func (p *pane) initColumns() {
	p.cols = [3]widget.Column{
		{ID: colName, Title: "Name", Width: nameColPt, MinWidth: nameColPt, Fill: true, Sortable: true},
		{ID: colSize, Title: "Size", Width: sizeColPt, MinWidth: 56, Align: ui.AlignRight, Sortable: true},
		{ID: colMod, Title: "Modified", Width: dateColPt, MinWidth: 96, Sortable: true},
	}
}

// dimmed is col as it shows in a pane that does not have the keyboard: faded
// toward the surface by the pane's focus animation.
func dimmed(r *theme.Resolved, col uint32, ft float32) uint32 {
	if ft >= 1 {
		return col
	}
	return theme.MixRGBA(col, r.Pal.Surface, 0.27*(1-ft))
}

// purple is the colour of symlinks, which the palette has no role for.
func purple(r *theme.Resolved) uint32 { return theme.MixRGBA(0x8839EFFF, 0xC3A3FFFF, r.T) }

func (pl *Pilot) kindColor(r *theme.Resolved, e *fs.Entry) uint32 {
	var col uint32
	switch {
	case e.Kind == fs.KindSymlink:
		col = purple(r)
	case e.Kind == fs.KindDir || e.Flags&fs.FlagSymlinkDir != 0:
		col = r.Pal.Accent
	case e.Flags&fs.FlagExecutable != 0:
		col = r.Pal.Success
	case e.Kind == fs.KindOther:
		col = r.Pal.Warning
	default:
		col = r.Pal.Text
	}
	if e.Flags&fs.FlagHidden != 0 {
		col = theme.MixRGBA(col, r.Pal.Surface, 0.55-0.15*r.T) // a little less faded in the dark, where the surface is close to the text
	}
	return col
}

func (pl *Pilot) kindIcon(e *fs.Entry) icon.ID {
	switch {
	case e.Kind == fs.KindSymlink:
		return pl.ico.link
	case e.Kind == fs.KindDir || e.Flags&fs.FlagSymlinkDir != 0:
		return icon.Folder
	case e.Flags&fs.FlagExecutable != 0:
		return pl.ico.exec
	case e.Kind == fs.KindOther:
		return pl.ico.other
	}
	return icon.File
}

// drawList builds the table of pane i.
func (pl *Pilot) drawList(c *ui.Context, r *theme.Resolved, i int, p *pane, paneW, ft float32) {
	k := pl.k
	v := &p.v
	g := &pl.geo[i]
	nRows := v.nRows()

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

	k.NextRowHeight(listRowPt)
	t := k.BeginTable("files", &v.ts, pl.columns(p, paneW-2*k.Pt(paneInsetPt)), nRows)
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
	g.list, g.hdr = lr, c.RectOf(t.HeaderKey())
	g.visRows = 0
	if g.rowPx > 0 {
		g.visRows = int((lr[3] - lr[1]) / g.rowPx)
	}
	x := g.hdr[0] + theme.Snap(r.Px.GapSmall)
	g.cols = [3][2]float32{}
	for ci := range pl.columns(p, paneW-2*k.Pt(paneInsetPt)) {
		wd := t.ColumnWidth(ci)
		g.cols[ci] = [2]float32{x, x + wd}
		x += wd
	}
	showSize, showDate := g.cols[1][1] > 0, g.cols[2][1] > 0

	mono, bold := r.Font.Mono, r.Font.MonoBold
	if v.l != nil {
		pl.frameRows += t.Last - t.First
		for row := t.First; row < t.Last; row++ {
			e := v.entryAt(row)
			ent := &v.l.Entries[e]
			t.RowNamed(row, uint32(e), v.l.Name(int(e)))
			selected := t.RowSelected()
			col := dimmed(r, pl.kindColor(r, ent), ft)
			font := mono
			if ent.Kind == fs.KindDir || ent.Flags&fs.FlagSymlinkDir != 0 {
				font = bold
			}
			t.BeginCell(0)
			k.NextHeight(widget.Fill)
			k.Icon(pl.kindIcon(ent), col)
			c.NextSize(ui.Pct(1), ui.Pct(1))
			c.NextFont(font)
			c.NextTextColor(col)
			c.NextRole(ui.RoleLabel)
			c.NextSemNameBytes(v.l.Name(int(e)))
			c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(c.ParentKey(), 1), v.l.Name(int(e)))
			t.EndCell()

			dim := dimmed(r, r.Pal.TextMuted, ft)
			if selected {
				dim = dimmed(r, r.Pal.Text, ft)
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
				pl.cell(c, &t, 1, mono, dim, ui.AlignRight, tx)
			}
			if showDate {
				f := c.Fmt()
				appendDate(f, ent.MTime, pl.loc)
				pl.cell(c, &t, 2, mono, dim, ui.AlignLeft, f.Bytes())
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
	t.Empty(msg, widget.TextMono)
	k.EndTable(&t)

	// the cursor row of the pane that was just built: what Build keeps for the model
	row, frac := v.ts.TopRow()
	p.topRow, p.topFrac, p.topEntry = row, frac, -1
	if v.l != nil && row >= 0 && row < nRows {
		p.topEntry = v.entryAt(row)
	}
}

// cell builds text cell col of the file row being built in a font and colour of
// its own, aligned al, fading out where it does not fit.
func (pl *Pilot) cell(c *ui.Context, t *widget.Table, col int, font text.FontKey, color uint32, al ui.Align, tx []byte) {
	t.BeginCell(col)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextFont(font)
	c.NextTextColor(color)
	c.NextAlign(al)
	c.NextRole(ui.RoleCell)
	c.NextSemNameBytes(tx)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(c.ParentKey(), 1), tx)
	t.EndCell()
}

// drawStatus builds the status line of pane i: an error or a note, or the
// counts of the listing and the selection.
func (pl *Pilot) drawStatus(c *ui.Context, r *theme.Resolved, i int, p *pane) {
	k := pl.k
	v := &p.v
	var tx []byte
	col := r.Pal.TextMuted
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
			f.Str("   ")
			appendCount(f, n)
			f.Str(" selected")
			if b := v.selBytes(); b > 0 {
				f.Str(" (")
				appendSize(f, b)
				f.Byte(')')
			}
		}
		if !p.hideHidden {
			f.Str("   hidden shown")
		}
		tx = f.Bytes()
	}
	c.NextSize(ui.Pct(1), ui.Px(theme.Snap(k.Pt(statusHPt))))
	c.NextPadding(k.Pt(paneInsetPt+2), 0, k.Pt(paneInsetPt), theme.Snap(k.Pt(2)))
	box := c.BoxKeyed(0, statusKey(i), nil)
	c.Push(box)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextFont(r.Font.Small)
	c.NextTextColor(col)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(statusKey(i), 1), tx)
	c.Pop()
}

// ---- titlebar ----

// drawTitlebar builds the titlebar: the theme's band, the back, forward and up
// buttons, and the folder's name and path.
func (pl *Pilot) drawTitlebar(c *ui.Context, r *theme.Resolved) {
	k := pl.k
	buttons := theme.ButtonsNone
	if pl.Traffic {
		buttons = theme.ButtonsTraffic
	}
	bar := k.Titlebar("", buttons, false)
	h := theme.Snap(r.Px.TitlebarH)
	bh := theme.Snap(r.Px.ControlH)
	gap := k.Pt(navGapPt)
	a := pl.panes[pl.active]

	x := max(bar.Free[0], theme.Snap(k.Pt(navFirstPt)))
	theme.At(c, x, theme.Snap((h-bh)/2), 0, bh)
	c.NextWidth(ui.Children(1))
	c.NextAxis(ui.AxisX)
	c.NextGap(gap)
	nav := c.BoxKeyed(fl, keyNav, nil)
	c.Push(nav)
	for n, b := range [3]struct {
		ic   icon.ID
		name string
		can  bool
	}{
		{icon.ChevronLeft, "Back", a.hist.canBack()},
		{icon.ChevronRight, "Forward", a.hist.canForward()},
		{icon.ChevronUp, "Enclosing folder", a.canUp()},
	} {
		k.BeginDisabled(!b.can)
		sig := k.IconButton(b.ic, b.name, widget.ButtonPlain)
		k.EndDisabled()
		if sig.Clicked {
			switch n {
			case 0:
				a.goBack()
			case 1:
				a.goForward()
			default:
				a.goUp()
			}
		}
	}
	c.Pop()

	// the folder's name over its path, left-aligned after the buttons
	tx := x + 3*bh + 2*gap + k.Pt(titleGapPt)
	room := float32(pl.lastW) - tx - k.Pt(titleRightPt)
	if room <= k.Pt(8) {
		return
	}
	fs, fm := r.Met(r.Font.UISemi), r.Met(r.Font.Small)
	top := theme.Snap((h - fs.H - fm.H) / 2)
	theme.At(c, tx, top, room, fs.H)
	c.NextFont(r.Font.UISemi)
	c.NextTextColor(r.Pal.Text)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|fl, ui.KeyN(keyTitle, 1), strBytes(a.base))
	theme.At(c, tx, top+fs.H, room, fm.H)
	c.NextFont(r.Font.Small)
	c.NextTextColor(r.Pal.TextMuted)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|fl, ui.KeyN(keyTitle, 2), strBytes(a.dir))
}

// strBytes views s as bytes without copying; the bytes must not be changed.
func strBytes(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }
