package pilot

import (
	"gonativeui/cmd/internal/rexkit"
	"gonativeui/fs"
	"gonativeui/ui"
)

const fl = rexkit.Fl

// Layout, in points.
const (
	padPt     = 8
	pathYPt   = 8
	pathHPt   = 26
	hdrYPt    = 40
	hdrHPt    = 20
	listYPt   = 61
	statusHPt = 22
	filterHPt = 28
	iconCXPt  = 14
	nameXPt   = 26
	edgePt    = 12
	gutterPt  = 7
	minPanePt = 220
	dimRate   = 20
	thumbWPt  = 4
	thumbHit  = 12
)

var (
	keyRoot   = ui.HashString(0, "pilot")
	keyFocus  = ui.HashString(keyRoot, "focus")
	keyDiv    = ui.HashString(keyRoot, "divider")
	keyBack   = ui.HashString(keyRoot, "back")
	keyFwd    = ui.HashString(keyRoot, "forward")
	keyUp     = ui.HashString(keyRoot, "up")
	keyTitle  = ui.HashString(keyRoot, "title")
	loadingTx = []byte("Loading…")
	emptyTx   = []byte("Empty folder")
	noMatchTx = []byte("No matches")
	dashTx    = []byte("--")
	hdrName   = []byte("Name")
	hdrSize   = []byte("Size")
	hdrMod    = []byte("Modified")
	arrowUp   = []byte("▲")
	arrowDown = []byte("▼")
)

func paneUID(i int) uint32     { return uint32(i + 1) }
func paneKey(i int) ui.Key     { return rexkit.PaneKey(paneUID(i)) }
func listKey(i int) ui.Key     { return ui.KeyN(paneKey(i), 3) }
func thumbKey(i int) ui.Key    { return ui.KeyN(paneKey(i), 4) }
func hdrKey(i, col int) ui.Key { return ui.KeyN(paneKey(i), uint64(10+col)) }
func pathWrap(i int) ui.Key    { return ui.KeyN(paneKey(i), 1) }
func filterWrap(i int) ui.Key  { return ui.KeyN(paneKey(i), 2) }

// pathKey and filterKey are the keys of the text fields (TextField hashes its
// label with the parent's key).
func pathKey(i int) ui.Key   { return ui.HashString(pathWrap(i), "path") }
func filterKey(i int) ui.Key { return ui.HashString(filterWrap(i), "filter") }

// geometry is what the model needs of the last frame's layout: the pane and
// list rectangles (window pixels), the row pitch and the scroll offset.
type geometry struct {
	rect, list [4]float32
	rowPx      float32
	scrollY    float64
	visRows    int
	hover      int // row under the pointer, -1
}

// paneLayout positions the parts of a pane (pixels; x, y of rects relative to
// the pane, columns relative to the list's left edge).
type paneLayout struct {
	path, hdr, list, filter, status [4]float32
	iconCX, nameX, nameW            float32
	sizeR, dateX                    float32
	showSize, showDate              bool
}

func (pl *Pilot) layoutPane(w, h float32, filter bool) paneLayout {
	s := pl.Scale
	var L paneLayout
	pad := padPt * s
	L.path = [4]float32{pad, pathYPt * s, w - pad, (pathYPt + pathHPt) * s}
	L.hdr = [4]float32{pad / 2, hdrYPt * s, w - pad/2, (hdrYPt + hdrHPt) * s}
	bottom := h - statusHPt*s
	if filter {
		L.filter = [4]float32{pad, bottom - filterHPt*s + 2*s, w - pad, bottom - 2*s}
		bottom -= filterHPt * s
	}
	L.list = [4]float32{pad / 2, listYPt * s, w - pad/2, max(bottom, listYPt*s)}
	L.status = [4]float32{pad, h - statusHPt*s, w - pad, h}

	lw := L.list[2] - L.list[0]
	L.iconCX, L.nameX = iconCXPt*s, nameXPt*s
	right := lw - edgePt*s
	dateW, sizeW := 16*pl.Adv, 8*pl.Adv
	if lw >= 330*s {
		L.showDate = true
		L.dateX = right - dateW
		right = L.dateX - 12*s
	}
	if lw >= 240*s {
		L.showSize = true
		L.sizeR = right
		right = L.sizeR - sizeW - 8*s
	}
	L.nameW = max(right-L.nameX, 8*s)
	return L
}

// Build builds the frame: it applies the frame's input to the model, then
// builds every box. It implements app.Builder.
func (pl *Pilot) Build(c *ui.Context) {
	w, h := c.Size()
	if w <= 0 || h <= 0 || c.Scale() <= 0 {
		return
	}
	if s := c.Scale(); s != pl.Scale {
		pl.SetScale(s)
	}
	pl.lastW, pl.lastH = w, h
	pl.Fld.Build(w, h, pl.K)
	pl.poll()
	pl.update(c)
	pl.work()
	pl.draw(c)
	pl.workLeft(c)
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

func (pl *Pilot) workArea() [4]float32 {
	s := pl.Scale
	W, H := float32(pl.lastW), float32(pl.lastH)
	return [4]float32{rexkit.Snap(7.5 * s), rexkit.Snap(45.5 * s), W - rexkit.Snap(7.5*s), H - rexkit.Snap(7.5*s)}
}

// paneRects splits the work area at the divider.
func (pl *Pilot) paneRects() (l, g, r [4]float32) {
	s := pl.Scale
	wa := pl.workArea()
	gw := rexkit.Snap(gutterPt * s)
	avail := wa[2] - wa[0] - gw
	lw := rexkit.Snap(avail * pl.ratio)
	lw = min(max(lw, minPanePt*s), max(avail-minPanePt*s, minPanePt*s))
	l = [4]float32{wa[0], wa[1], wa[0] + lw, wa[3]}
	g = [4]float32{l[2], wa[1], l[2] + gw, wa[3]}
	r = [4]float32{g[2], wa[1], wa[2], wa[3]}
	return
}

func (pl *Pilot) draw(c *ui.Context) {
	pl.DrawBackground(c)
	pl.drawPanes(c)
	pl.drawTitlebar(c)
	if pl.Traffic {
		pl.DrawTrafficLights(c)
	}
}

func (pl *Pilot) drawPanes(c *ui.Context) {
	lr, gut, rr := pl.paneRects()
	rects := [2][4]float32{lr, rr}
	var ft [2]float32
	for i := range pl.panes {
		foc := float32(0)
		if i == pl.active {
			foc = 1
		}
		ft[i] = c.AnimateValue(ui.KeyN(keyFocus, uint64(i)), foc, dimRate)
	}
	for i := range pl.panes {
		pl.DrawShadow(c, paneUID(i), rects[i], ft[i], 1)
	}
	pl.frameRows = 0
	for i := range pl.panes {
		pl.drawPane(c, i, rects[i], ft[i])
	}
	wa := pl.workArea()
	pl.Divider(c, keyDiv, ui.AxisX, wa, gut)
	if pl.counting {
		pl.buildCalls++
		pl.rowsBuilt += pl.frameRows
		pl.rowsMax = max(pl.rowsMax, pl.frameRows)
	}
}

// ---- one pane ----

func (pl *Pilot) drawPane(c *ui.Context, i int, d [4]float32, ft float32) {
	p := pl.panes[i]
	s, k := pl.Scale, pl.K
	w, h := d[2]-d[0], d[3]-d[1]
	pal := rexkit.MixPalette(ft)
	key := paneKey(i)
	L := pl.layoutPane(w, h, p.filterOpen)
	g := &pl.geo[i]
	g.rect = d
	g.list = [4]float32{d[0] + L.list[0], d[1] + L.list[1], d[0] + L.list[2], d[1] + L.list[3]}

	kg := pl.BeginPane(c, key, i, d, ft, 1, true)
	pk := rexkit.KeyGen{Base: ui.KeyN(key, 900)}

	// The active pane: an accent bar along the top edge.
	if ft > 0.01 {
		rexkit.At(c, 22*s, 0, w-44*s, 2*s)
		c.NextBG(rexkit.ColA(pal.Blue, ft))
		c.NextRadius(s)
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
	}

	pl.drawPathField(c, i, p, &L, &pal, ft)
	pl.drawHeader(c, i, p, &L, &pal)
	pl.drawFilterField(c, i, p, &L, &pal, ft)
	pl.drawList(c, i, p, &L, &pal, ft, &pk)
	pl.drawStatus(c, p, &L, &pal)
	_ = k
	pl.EndPane(c, &kg, d, ft)
}

func (pl *Pilot) fieldStyle(c *ui.Context, pal *rexkit.Palette, focused bool, ft float32) {
	s := pl.Scale
	lh := pl.FM[pl.FMono].H
	c.NextFont(pl.FMono)
	c.NextTextColor(rexkit.Col(pal.FG))
	c.NextPadding(8*s, max((pathHPt*s-lh)/2, 0), 8*s, max((pathHPt*s-lh)/2, 0))
	c.NextHeight(ui.Px(pathHPt * s))
	c.NextRadius(6 * s)
	c.NextBG(rexkit.ColA(0xFFFFFF, 0.7))
	border := rexkit.ColA(pal.Line, 0.16)
	if focused {
		border = rexkit.ColA(pal.Blue, 0.9)
	}
	c.NextBorder(border, max(1, pl.K))
}

func (pl *Pilot) drawPathField(c *ui.Context, i int, p *pane, L *paneLayout, pal *rexkit.Palette, ft float32) {
	r := L.path
	rexkit.At(c, r[0], r[1], r[2]-r[0], r[3]-r[1])
	wrap := c.BoxKeyed(fl, pathWrap(i), nil)
	c.Push(wrap)
	focused := c.FocusKey() == pathKey(i)
	pl.fieldStyle(c, pal, focused, ft)
	f := c.TextField("path", &p.path)
	c.Pop()
	switch {
	case f.Cancelled:
		p.path.SetString(p.dir)
		p.pathErr = ""
		c.SetFocus(0)
	case f.Submitted:
		p.goTo(p.path.String())
		c.SetFocus(0)
		pl.active = i
	case f.Changed:
		p.pathErr = ""
	}
	if f.Focused && f.Pressed && pl.prevFocus != pathKey(i) { // a click that brought focus here selects all
		p.path.SelectAll()
	}
	if p.pathErr != "" {
		// the reason, inside the field at its right end
		kg := rexkit.KeyGen{Base: ui.KeyN(pathWrap(i), 77)}
		c.Push(wrap)
		tx := rexkit.StrBytes(p.pathErr)
		wd := float32(len(tx)) * pl.Adv
		if wd > (r[2]-r[0])*0.6 {
			wd = (r[2] - r[0]) * 0.6
		}
		base := rexkit.Snap((pathHPt*pl.Scale-pl.FM[pl.FSub].H)/2) + pl.FM[pl.FSub].Off
		pl.PutBytes(c, &kg, pl.FSub, tx, (r[2]-r[0])-wd-10*pl.Scale, base, rexkit.Col(0xCF3E4B), wd, ui.FadeOverflowX)
		c.Pop()
	}
}

func (pl *Pilot) drawFilterField(c *ui.Context, i int, p *pane, L *paneLayout, pal *rexkit.Palette, ft float32) {
	if !p.filterOpen {
		return
	}
	r := L.filter
	rexkit.At(c, r[0], r[1], r[2]-r[0], r[3]-r[1])
	wrap := c.BoxKeyed(fl, filterWrap(i), nil)
	c.Push(wrap)
	focused := c.FocusKey() == filterKey(i)
	pl.fieldStyle(c, pal, focused, ft)
	f := c.TextField("filter", &p.filter)
	c.Pop()
	if f.Changed {
		p.setQuery()
	}
}

func (pl *Pilot) drawHeader(c *ui.Context, i int, p *pane, L *paneLayout, pal *rexkit.Palette) {
	s := pl.Scale
	r := L.hdr
	kg := rexkit.KeyGen{Base: ui.KeyN(paneKey(i), 950)}
	listL := L.list[0]
	cols := [3]struct {
		show   bool
		x0, x1 float32 // relative to the pane
		label  []byte
		key    fs.SortKey
		right  bool
	}{
		{true, listL + L.nameX, listL + L.nameX + L.nameW, hdrName, fs.SortName, false},
		{L.showSize, listL + L.sizeR - 8*pl.Adv, listL + L.sizeR, hdrSize, fs.SortSize, true},
		{L.showDate, listL + L.dateX, listL + L.dateX + 16*pl.Adv, hdrMod, fs.SortModified, false},
	}
	base := rexkit.Snap((r[3]-r[1]-pl.FM[pl.FSub].H)/2) + pl.FM[pl.FSub].Off
	for ci := range cols {
		cl := &cols[ci]
		if !cl.show {
			continue
		}
		active := p.sortKey == cl.key
		t := c.HotTOf(hdrKey(i, ci))
		x0, x1 := cl.x0-6*s, cl.x1+6*s
		if ci == 0 {
			x0 = listL + 2*s
		}
		rexkit.At(c, x0, r[1], x1-x0, r[3]-r[1])
		c.NextBG(rexkit.ColA(0x2A2030, 0.06*t))
		c.NextRadius(5 * s)
		c.NextCursor(ui.CursorPointer)
		hb := c.BoxKeyed(ui.DrawBackground|ui.Clickable|ui.HotAnimation|fl, hdrKey(i, ci), nil)
		c.Push(hb)
		col := pal.Dim
		if active {
			col = pal.FG
		}
		font := pl.FSub
		wd := float32(len(cl.label)) * pl.Adv * 0.9
		wd = pl.TS.Run(pl.TS.Shape(font, cl.label)).Advance
		x := cl.x0 - x0
		if cl.right {
			x = cl.x1 - x0 - wd
			if active {
				x -= 12 * s
			}
		}
		pl.PutBytes(c, &kg, font, cl.label, x, base, rexkit.Col(col), 0, 0)
		if active {
			arrow := arrowUp
			if p.desc {
				arrow = arrowDown
			}
			pl.PutBytes(c, &kg, pl.FSub, arrow, x+wd+3*s, base, rexkit.Col(pal.Blue), 0, 0)
		}
		c.Pop()
	}
	// hairline under the header
	rexkit.At(c, L.list[0]+2*s, listYPt*s-1, L.list[2]-L.list[0]-4*s, max(1, pl.K))
	c.NextBG(rexkit.ColA(pal.Line, 0.1))
	c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
}

func (pl *Pilot) kindColor(pal *rexkit.Palette, e *fs.Entry) uint32 {
	var col uint32
	switch {
	case e.Kind == fs.KindSymlink:
		col = pal.Purple
	case e.Kind == fs.KindDir || e.Flags&fs.FlagSymlinkDir != 0:
		col = pal.Blue
	case e.Flags&fs.FlagExecutable != 0:
		col = pal.Green
	case e.Kind == fs.KindOther:
		col = pal.Orange
	default:
		col = pal.FG
	}
	if e.Flags&fs.FlagHidden != 0 {
		col = rexkit.MixRGB(col, 0xE6E2E8, 0.55)
	}
	return col
}

func kindIcon(e *fs.Entry) iconID {
	switch {
	case e.Kind == fs.KindSymlink:
		return icLink
	case e.Kind == fs.KindDir || e.Flags&fs.FlagSymlinkDir != 0:
		return icFolder
	case e.Flags&fs.FlagExecutable != 0:
		return icExec
	case e.Kind == fs.KindOther:
		return icOther
	}
	return icFile
}

func (pl *Pilot) drawList(c *ui.Context, i int, p *pane, L *paneLayout, pal *rexkit.Palette, ft float32, pk *rexkit.KeyGen) {
	s := pl.Scale
	v := &p.v
	g := &pl.geo[i]
	rowPx := pl.RowPitch
	g.rowPx = rowPx
	lw, lh := L.list[2]-L.list[0], L.list[3]-L.list[1]
	rexkit.At(c, L.list[0], L.list[1], lw, lh)
	c.NextCursor(ui.CursorArrow)
	lk := listKey(i)
	list := c.BoxKeyed(ui.ScrollY|ui.Clip|ui.Clickable|fl, lk, nil)
	pl.listBuf[i] = list
	nRows := v.nRows()
	g.visRows = int(lh / rowPx)

	// scrollbar drag, keyboard reveal and scroll restore, before the range is taken
	_, sy := c.Scroll(list)
	span := float64(nRows)*float64(rowPx) - float64(lh)
	ts := c.PeekSignal(thumbKey(i))
	if ts.Pressed {
		pl.dragFrom[i] = sy
	}
	if (ts.Dragging || ts.Released) && span > 0 {
		trackH := float64(lh)
		thumbH := max(trackH*float64(lh)/(float64(nRows)*float64(rowPx)), float64(24*s))
		if room := trackH - thumbH; room > 0 {
			c.SetScroll(list, 0, min(max(pl.dragFrom[i]+float64(ts.DragDelta[1])*span/room, 0), span))
		}
	}
	if p.scrollRow >= 0 {
		c.SetScroll(list, 0, min((float64(p.scrollRow)+float64(p.scrollOff))*float64(rowPx), max(span, 0)))
		p.scrollRow = -1
	}
	if p.reveal >= 0 && nRows > 0 {
		pl.revealRow(c, list, p.reveal, rowPx, lh)
		p.reveal = -1
	}
	_, sy = c.Scroll(list)
	g.scrollY = sy

	first, last := c.ListRange(list, nRows, rowPx, 2)
	pl.frameRows += last - first
	// what Build keeps for the model: the entry at the top of the view
	top := int(sy / float64(rowPx))
	p.topRow = top
	p.topEntry = -1
	if v.l != nil && top >= 0 && top < nRows {
		p.topEntry = v.entryAt(top)
	}
	p.topFrac = float32(sy/float64(rowPx) - float64(top))

	hot := c.HotKey() == lk
	g.hover = -1
	in := c.Input()
	if hot && in.Inside {
		if r := int((float64(in.MouseY-g.list[1]) + sy) / float64(rowPx)); r >= 0 && r < nRows {
			g.hover = r
		}
	}

	c.Push(list)
	if v.l != nil && nRows > 0 {
		for r := first; r < last; r++ {
			pl.drawRow(c, i, p, L, pal, ft, r, lk, rowPx, lw)
		}
	}
	c.Pop()

	// empty / loading note
	if nRows == 0 {
		var msg []byte
		switch {
		case p.loading():
			msg = loadingTx
		case p.jobBusy:
			msg = nil
		case len(p.q) > 0:
			msg = noMatchTx
		default:
			msg = emptyTx
		}
		if msg != nil {
			kg := rexkit.KeyGen{Base: ui.KeyN(lk, 99999)}
			c.Push(list)
			pl.PutBytes(c, &kg, pl.FMono, msg, 10*s, rexkit.Snap(rowPx*0.5+pl.FM[pl.FMono].H*0.3), rexkit.Col(pal.Dim), 0, 0)
			c.Pop()
		}
	}

	// scrollbar thumb
	if span > 0 {
		trackH := float64(lh)
		content := float64(nRows) * float64(rowPx)
		thumbH := max(trackH*float64(lh)/content, float64(24*s))
		top := (trackH - thumbH) * min(max(sy/span, 0), 1)
		hit := thumbHit * s
		x := L.list[2] - hit
		t := max(c.HotTOf(thumbKey(i)), c.ActiveTOf(thumbKey(i)))
		rexkit.At(c, x, L.list[1]+float32(top), hit, float32(thumbH))
		c.NextCursor(ui.CursorArrow)
		tb := c.BoxKeyed(ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|fl, thumbKey(i), nil)
		c.Push(tb)
		tw := thumbWPt*s + 2*s*t
		rexkit.At(c, hit-tw-3*s, 0, tw, float32(thumbH))
		c.NextBG(rexkit.ColA(0x6E6378, 0.28+0.3*t))
		c.NextRadius(tw / 2)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(thumbKey(i), 1), nil)
		c.Pop()
	}
}

// revealRow scrolls so that row r is in view (a far jump is instant, a near
// one glides).
func (pl *Pilot) revealRow(c *ui.Context, list ui.ID, r int, rowPx, viewH float32) {
	top := float64(r) * float64(rowPx)
	bottom := top + float64(rowPx)
	_, sy := c.Scroll(list)
	view := float64(viewH)
	var target float64
	switch {
	case top < sy:
		target = top
	case bottom > sy+view:
		target = bottom - view
	default:
		return
	}
	d := target - sy
	if d < 0 {
		d = -d
	}
	if d > 4*view {
		c.SetScroll(list, 0, target)
	} else {
		c.ScrollTo(list, 0, target)
	}
}

func (pl *Pilot) drawRow(c *ui.Context, i int, p *pane, L *paneLayout, pal *rexkit.Palette, ft float32, r int, lk ui.Key, rowPx, lw float32) {
	s := pl.Scale
	v := &p.v
	e := v.entryAt(r)
	ent := &v.l.Entries[e]
	selected := v.isSel(e)
	hover := r == pl.geo[i].hover
	isCur := v.curRow == r

	c.NextSize(ui.Pct(1), ui.Px(rowPx))
	rk := ui.KeyN(lk, uint64(r))
	row := c.BoxKeyed(0, rk, nil)
	c.Push(row)
	kg := rexkit.KeyGen{Base: ui.KeyN(rk, 1)}

	if selected || hover {
		var bg uint32
		switch {
		case selected:
			bg = rexkit.ColA(pal.Blue, rexkit.Lerp(0.1, 0.17, ft))
		default:
			bg = rexkit.ColA(0x2A2030, 0.05)
		}
		rexkit.At(c, 3*s, 0, lw-6*s, rowPx)
		c.NextBG(bg)
		c.NextRadius(4 * s)
		flags := ui.DrawBackground | fl
		if isCur && selected && ft > 0.5 {
			c.NextBorder(rexkit.ColA(pal.Blue, 0.6), max(1, pl.K))
			flags |= ui.DrawBorder
		}
		c.BoxKeyed(flags, kg.Next(), nil)
	}

	col := rexkit.Col(pl.kindColor(pal, ent))
	font := pl.FMono
	if ent.Kind == fs.KindDir || ent.Flags&fs.FlagSymlinkDir != 0 {
		font = pl.FMonoBold
	}
	fm := pl.FM[pl.FMono]
	base := rexkit.Snap((rowPx-fm.H)/2) + fm.Off

	pl.IconAt(c, &kg, kindIcon(ent), L.iconCX, rowPx/2, col)
	pl.PutBytes(c, &kg, font, v.l.Name(int(e)), L.nameX, base, col, L.nameW, ui.FadeOverflowX)

	dim := rexkit.Col(pal.Dim)
	if selected {
		dim = rexkit.Col(pal.FG)
	}
	if L.showSize {
		var tx []byte
		if ent.Kind == fs.KindDir {
			tx = dashTx
		} else {
			f := c.Fmt()
			appendSize(f, ent.Size)
			tx = f.Bytes()
		}
		pl.PutBytes(c, &kg, pl.FMono, tx, L.sizeR-float32(len(tx))*pl.Adv, base, dim, 0, 0)
	}
	if L.showDate {
		f := c.Fmt()
		appendDate(f, ent.MTime, pl.loc)
		pl.PutBytes(c, &kg, pl.FMono, f.Bytes(), L.dateX, base, dim, 0, 0)
	}
	c.Pop()
}

func (pl *Pilot) drawStatus(c *ui.Context, p *pane, L *paneLayout, pal *rexkit.Palette) {
	r := L.status
	kg := rexkit.KeyGen{Base: ui.KeyN(paneKey(int(p.id)-1), 960)}
	v := &p.v
	var tx []byte
	col := pal.Dim
	switch {
	case p.msg != "":
		tx = rexkit.StrBytes(p.msg)
		if p.msgErr {
			col = 0xCF3E4B
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
		if v.nSel > 0 {
			f.Str("   ")
			appendCount(f, v.nSel)
			f.Str(" selected")
			if v.selBytes > 0 {
				f.Str(" (")
				appendSize(f, v.selBytes)
				f.Byte(')')
			}
		}
		if !p.hideHidden {
			f.Str("   hidden shown")
		}
		tx = f.Bytes()
	}
	fm := pl.FM[pl.FSub]
	base := rexkit.Snap((r[3]-r[1]-fm.H)/2) + fm.Off
	rexkit.At(c, r[0], r[1], r[2]-r[0], r[3]-r[1])
	box := c.BoxKeyed(fl, kg.Next(), nil)
	c.Push(box)
	pl.PutBytes(c, &kg, pl.FSub, tx, 2*pl.Scale, base, rexkit.Col(col), r[2]-r[0]-4*pl.Scale, ui.FadeOverflowX)
	c.Pop()
}

// ---- titlebar ----

func (pl *Pilot) drawTitlebar(c *ui.Context) {
	k, s := pl.K, pl.Scale
	W := float32(pl.lastW)
	pl.TitlebarDrag(c, W)
	p := pl.panes[pl.active]
	hit := 28 * s
	cy := 22.5 * s
	pl.IconButton(c, keyBack, icBack, 104*s, cy, hit, 0x5F5D62, 0x1E2227, p.hist.canBack())
	pl.IconButton(c, keyFwd, icForward, 134*s, cy, hit, 0x5F5D62, 0x1E2227, p.hist.canForward())
	pl.IconButton(c, keyUp, icUp, 164*s, cy, hit, 0x5F5D62, 0x1E2227, p.canUp())

	kg := rexkit.KeyGen{Base: ui.KeyN(keyTitle, 1)}
	name := p.base
	x := 204 * s
	room := W - x - 24*s
	if room > 8*s {
		pl.PutBytes(c, &kg, pl.FUISemi, rexkit.StrBytes(name), x, 42*k, rexkit.Col(0x3D3B42), min(pl.Width(pl.FUISemi, name)+2, room), ui.FadeOverflowX)
		pl.PutBytes(c, &kg, pl.FSub, rexkit.StrBytes(p.dir), x, 66*k, rexkit.Col(0x87848B), min(pl.Width(pl.FSub, p.dir)+2, room), ui.FadeOverflowX)
	}
}
