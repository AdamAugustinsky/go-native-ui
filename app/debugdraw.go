package app

import (
	"os"
	"strconv"
	"unicode/utf8"

	"gonativeui/text"
	"gonativeui/ui"
)

// Drawing of the debug overlay. It is made of ui boxes in one inert overlay
// (ui.Context.BeginDebugOverlay) built after the app's Build: outlines are
// bordered floating boxes, and every character of the text is a box of its
// own, so that the numbers that change each frame never make the text system
// shape a new string (the one-character runs are cached after the first
// frame), which keeps a steady-state frame at 0 allocations.

const (
	dbgLineLen  = 96
	dbgMaxLines = 48
	maxRows     = 96 // rows of text in one overlay
)

// Colours, 0xRRGGBBAA.
const (
	colText    = 0xE8E8EEFF
	colDim     = 0x9A9AA8FF
	colWarn    = 0xFFB454FF
	colBad     = 0xFF5C5CFF
	colGood    = 0x7EE08AFF
	colAccent  = 0xE59A5CFF // copper
	colPanel   = 0x101016E8
	colEdge    = 0xFFFFFF30
	colOutline = 0x58A6FFB0
	colInner   = 0xFF8A3DFF
	colInnerBG = 0xFF8A3D26
	colFinding = 0xFF2D2DFF
	colFindBG  = 0xFF2D2D1E
)

var (
	rowNames  [maxRows]string
	findNames [8]string
)

func init() {
	for i := range rowNames {
		rowNames[i] = "dbg-row-" + strconv.Itoa(i)
	}
	for i := range findNames {
		findNames[i] = "dbg-finding-" + strconv.Itoa(i)
	}
}

// dbgLine is one line of overlay text, in fixed storage.
type dbgLine struct {
	b   [dbgLineLen]byte
	n   int
	col uint32
}

func (l *dbgLine) bytes(s []byte) *dbgLine {
	for _, c := range s {
		if l.n >= len(l.b) {
			break
		}
		if c < 0x20 || c == 0x7f {
			c = '.'
		}
		l.b[l.n] = c
		l.n++
	}
	return l
}

func (l *dbgLine) str(s string) *dbgLine {
	for i := 0; i < len(s) && l.n < len(l.b); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			c = '.'
		}
		l.b[l.n] = c
		l.n++
	}
	return l
}

func (l *dbgLine) num(v int64) *dbgLine {
	var t [24]byte
	return l.bytes(strconv.AppendInt(t[:0], v, 10))
}

func (l *dbgLine) unum(v uint64) *dbgLine {
	var t [24]byte
	return l.bytes(strconv.AppendUint(t[:0], v, 10))
}

// hex appends v as n hex digits (its low n digits).
func (l *dbgLine) hex(v uint64, n int) *dbgLine {
	var t [16]byte
	h := strconv.AppendUint(t[:0], v, 16)
	for i := len(h); i < n; i++ {
		l.str("0")
	}
	if len(h) > n {
		h = h[len(h)-n:]
	}
	return l.bytes(h)
}

func (l *dbgLine) f1(v float64) *dbgLine {
	var t [32]byte
	return l.bytes(strconv.AppendFloat(t[:0], v, 'f', 1, 64))
}

// size appends a byte count as B, KiB or MiB.
func (l *dbgLine) size(n int64) *dbgLine {
	switch {
	case n < 0:
		return l.str("?")
	case n < 1<<10:
		return l.num(n).str(" B")
	case n < 1<<20:
		return l.f1(float64(n) / (1 << 10)).str(" KiB")
	}
	return l.f1(float64(n) / (1 << 20)).str(" MiB")
}

// rect appends "x0,y0 x1,y1".
func (l *dbgLine) rect(r [4]float32) *dbgLine {
	return l.num(int64(r[0])).str(",").num(int64(r[1])).str(" ").num(int64(r[2])).str(",").num(int64(r[3]))
}

// us appends nanoseconds as microseconds.
func (l *dbgLine) us(ns int64) *dbgLine { return l.num(ns / 1000) }

// dbgLines is a block of text lines.
type dbgLines struct {
	l [dbgMaxLines]dbgLine
	n int
}

func (ls *dbgLines) reset() { ls.n = 0 }

// next starts a line in colour col; once the block is full it returns a
// scratch line that is not shown.
func (ls *dbgLines) next(col uint32) *dbgLine {
	if ls.n >= len(ls.l) {
		ls.l[len(ls.l)-1] = dbgLine{col: col}
		return &ls.l[len(ls.l)-1]
	}
	l := &ls.l[ls.n]
	ls.n++
	*l = dbgLine{col: col}
	return l
}

func (ls *dbgLines) maxLen() int {
	m := 0
	for i := 0; i < ls.n; i++ {
		m = max(m, ls.l[i].n)
	}
	return m
}

// drawState is what the overlay keeps between frames to draw.
type drawState struct {
	font      text.FontKey
	fontScale float32
	adv, lh   float32 // the font's character advance and line height, pixels

	// cols and lines are how many characters wide and lines tall a panel can
	// be and still lie inside the window (with its margins): at a large zoom
	// the text is as big as the UI's, and a block that would not fit is cut
	// instead of running off the window.
	cols, lines int

	n    uint64 // boxes made this frame, for their keys
	rows int
	hud  dbgLines
	card dbgLines
	side dbgLines
	tmp  dbgLines
}

func (s *drawState) init() {}

func (s *drawState) ensureFont(a *App) {
	es := a.EffectiveScale() // pixels per ui point: the backing scale times the zoom, as the UI is built at
	if s.fontScale == es && s.fontScale != 0 {
		return
	}
	s.font = a.Text.Font(text.FontDesc{Mono: true, SizePt: 10, Weight: 400, Scale: es})
	m := a.Text.Metrics(s.font)
	s.adv, s.lh = m.MonoAdvance, m.LineHeight
	if m.LineHeight <= 0 {
		s.lh = m.Ascent + m.Descent + m.LineGap
	}
	s.fontScale = es
}

// debugBuild builds the overlay's boxes. build calls it between Build and End.
func (a *App) debugBuild() {
	d := a.dbg
	d.mark(PhaseBuild)
	c := a.UI
	ds := &d.draw
	ds.ensureFont(a)
	ds.fit(a)
	a.fillSnapshot()
	s := &d.snap
	if d.host != nil {
		d.host.Inspect(s)
	}
	ds.hud.reset()
	ds.card.reset()
	ds.side.reset()
	hudLines(s, &ds.hud)
	cardLines(s, &ds.card)
	if d.panel && d.host == nil {
		buildInspector(s, &ds.side, &d.sym) // nothing in a release build
	}
	d.logDups(s)

	ds.n, ds.rows = 0, 0
	c.BeginDebugOverlay()
	ds.outlines(a, s)
	es := a.EffectiveScale()
	m := 8 * es
	w, h := float32(a.w), float32(a.h)
	if ds.hud.n > 0 {
		_, ph := ds.size(a, &ds.hud)
		ds.panel(a, m, h-ph-m, "hud", &ds.hud)
	}
	if s.Inside && s.NUnder > 0 && ds.card.n > 0 {
		pw, ph := ds.size(a, &ds.card)
		x, y := s.Pointer[0]+16*es, s.Pointer[1]+16*es
		if x+pw > w-m {
			x = s.Pointer[0] - 16*es - pw
		}
		if y+ph > h-m {
			y = s.Pointer[1] - 16*es - ph
		}
		ds.panel(a, max(x, m), max(y, m), "card", &ds.card)
	}
	if ds.side.n > 0 {
		pw, _ := ds.size(a, &ds.side)
		ds.panel(a, w-pw-m, m, "side", &ds.side)
	}
	c.EndDebugOverlay()
	d.mark(PhaseOverlay)
}

// fit sets how much text a panel can hold inside the window.
func (s *drawState) fit(a *App) {
	es := a.EffectiveScale()
	edge := 2 * (8 + 6) * es // the margin and the padding, both sides
	s.cols = max(int((float32(a.w)-edge)/max(s.adv, 1)), 1)
	s.lines = max(int((float32(a.h)-edge)/max(s.lh, 1)), 1)
}

// size is the size in pixels a block of lines takes in a panel (what panel
// draws: no more than fits the window).
func (s *drawState) size(a *App, ls *dbgLines) (w, h float32) {
	pad := 6 * a.EffectiveScale()
	return float32(min(ls.maxLen(), s.cols))*s.adv + 2*pad, float32(min(ls.n, s.lines))*s.lh + 2*pad
}

// outlines draws the boxes under the pointer and the audit's findings.
func (s *drawState) outlines(a *App, snap *InspectSnapshot) {
	c := a.UI
	bw := max(1, float32(int(a.EffectiveScale()+0.5)))
	for i := 0; i < snap.NUnder; i++ {
		b := &snap.Under[i]
		inner := i == snap.NUnder-1
		if inner {
			s.outline(c, b.Rect, colInner, colInnerBG, 2*bw)
		} else {
			s.outline(c, b.Rect, colOutline, 0, bw)
		}
	}
	for i := 0; i < snap.NFindings; i++ {
		f := &snap.Findings[i]
		s.outline(c, f.Rect, colFinding, colFindBG, 2*bw)
	}
	// the reasons, above the rectangles they belong to
	for i := 0; i < snap.NFindings && i < len(findNames); i++ {
		f := &snap.Findings[i]
		s.tmp.reset()
		s.tmp.next(colBad).str(f.Reason)
		pw, ph := s.size(a, &s.tmp)
		y := f.Rect[1] - ph
		if y < 0 {
			y = f.Rect[3] // no room above: below
		}
		s.panel(a, min(max(f.Rect[0], 0), float32(a.w)-pw), y, findNames[i], &s.tmp)
	}
}

func (s *drawState) outline(c *ui.Context, r [4]float32, col, fill uint32, bw float32) {
	w, h := r[2]-r[0], r[3]-r[1]
	if w <= 0 || h <= 0 {
		return
	}
	c.NextPos(r[0], r[1])
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextBorder(col, bw)
	fl := ui.FloatingX | ui.FloatingY | ui.DrawBorder
	if fill != 0 {
		c.NextBG(fill)
		fl |= ui.DrawBackground
	}
	s.n++
	c.BoxKeyed(fl, ui.KeyN(c.ParentKey(), s.n), nil)
}

// panel draws lines in a box at (x, y).
func (s *drawState) panel(a *App, x, y float32, label string, ls *dbgLines) {
	c := a.UI
	c.NextPos(x, y)
	c.NextBG(colPanel)
	c.NextBorder(colEdge, 1)
	c.NextRadius(6 * a.EffectiveScale())
	c.NextPad(6 * a.EffectiveScale())
	c.NextAxis(ui.AxisY)
	c.Open(ui.FloatingX|ui.FloatingY|ui.DrawBackground|ui.DrawBorder, label)
	for i := 0; i < min(ls.n, s.lines); i++ {
		s.row(c, ls.l[i].b[:min(ls.l[i].n, s.cols)], ls.l[i].col)
	}
	c.Close()
}

var glyphs = func() (g [128]byte) {
	for i := range g {
		g[i] = byte(i)
	}
	return
}()

// row makes a row of one-character boxes.
func (s *drawState) row(c *ui.Context, txt []byte, col uint32) {
	if s.rows >= maxRows {
		return
	}
	c.NextAxis(ui.AxisX)
	c.Open(0, rowNames[s.rows])
	s.rows++
	pk := c.ParentKey()
	for i, n := 0, uint64(0); i < len(txt); n++ {
		c.NextTextColor(col)
		c.NextFont(s.font)
		var g []byte
		if txt[i] < utf8.RuneSelf {
			g = glyphs[txt[i] : txt[i]+1]
			i++
			c.NextSize(ui.Px(s.adv), ui.TextSize(0)) // a column of the panel's width, not the glyph's advance rounded up
		} else {
			_, sz := utf8.DecodeRune(txt[i:])
			g = txt[i : i+sz]
			i += sz
		}
		c.BoxKeyed(ui.DrawText, ui.KeyN(pk, n), g)
	}
	if len(txt) == 0 {
		c.NextFont(s.font)
		c.BoxKeyed(ui.DrawText, ui.KeyN(pk, 0), glyphs[' ':'!'])
	}
	c.Close()
}

// ---- the text of the overlay ----

func appendCauses(l *dbgLine, rs []RepaintCause) {
	for i := range rs {
		if i > 0 {
			l.str(" + ")
		}
		var t [64]byte
		l.bytes(appendCause(t[:0], &rs[i]))
	}
}

// hudLines is the frame HUD.
func hudLines(s *InspectSnapshot, ls *dbgLines) {
	ls.next(colAccent).str("gnui debug  frame ").unum(s.Frame).str("  ").
		num(int64(s.W)).str("x").num(int64(s.H)).str(" @").f1(float64(s.Scale)).str("x")
	if s.HasLast {
		r := &s.Last
		l := ls.next(colText).str("cpu us")
		for ph := 0; ph < numPhases; ph++ {
			l.str("  ").str(phaseNames[ph]).str(" ").us(r.CPUns[ph])
		}
		l.str("  = ").us(r.TotalCPUns())
		col := uint32(colGood)
		if r.Allocs != 0 {
			col = colBad
		}
		ls.next(col).str("allocs ").unum(r.Allocs).str(" last frame   quads ").num(int64(r.Quads)).
			str("  draws ").num(int64(r.DrawCalls))
	} else {
		ls.next(colDim).str("no frame has ended yet")
	}
	ls.next(colText).str("boxes ").num(int64(s.Boxes)).str("   atlas ").num(int64(s.AtlasPages)).str(" pages ").size(int64(s.AtlasBytes))
	l := ls.next(colText).str("textures ").num(int64(s.TexLive)).str(" live +").num(int64(s.TexRetiring)).str(" retiring")
	if s.TexBytes >= 0 {
		l.str("  ").size(s.TexBytes)
	}
	col := uint32(colText)
	for i := 0; i < s.NThis; i++ {
		if s.This[i].Kind == CauseUnknown {
			col = colBad
		}
	}
	l = ls.next(col).str("this frame: ")
	appendCauses(l, s.This[:s.NThis])
	if s.Unexplained > 0 {
		ls.next(colBad).unum(s.Unexplained).str(" frame(s) ran with no cause")
	}
	if s.NDups > 0 || s.DupsMore > 0 {
		ls.next(colWarn).num(int64(s.NDups + s.DupsMore)).str(" duplicate key(s)")
	}
	if s.NFindings > 0 {
		ls.next(colBad).num(int64(s.NFindings)).str(" layout finding(s)")
	}
}

// flagsLine appends the flags of a box.
func flagsLine(l *dbgLine, f ui.Flags) {
	var t [ui.DebugFlagNamesMax]byte // room for every flag at once: no growth, no allocation
	l.bytes(ui.DebugFlagNames(t[:0], f))
}

// cardLines describes the innermost box under the pointer.
func cardLines(s *InspectSnapshot, ls *dbgLines) {
	if !s.Inside || s.NUnder == 0 {
		return
	}
	b := &s.Under[s.NUnder-1]
	l := ls.next(colAccent).str("#").hex(uint64(b.Key), 16)
	if b.Role != ui.RoleNone {
		l.str("  ").str(ui.DebugRoleName(b.Role))
	}
	if b.NameLen > 0 {
		l.str(" \"").bytes(b.Name[:b.NameLen]).str("\"")
	}
	ls.next(colText).str("rect ").rect(b.Rect).str("  (").num(int64(b.Rect[2] - b.Rect[0])).str("x").num(int64(b.Rect[3] - b.Rect[1])).str(")")
	ls.next(colText).str("clip ").rect(b.Clip)
	l = ls.next(colDim).str("flags ")
	flagsLine(l, b.Flags)
	if b.Overlay != 0 {
		ls.next(colDim).str("overlay ").num(int64(b.Overlay - 1))
	}
	ls.next(colDim).str("parent #").hex(uint64(b.Parent), 16)
}

// logDups writes each duplicate key to stderr once, when the overlay logs.
func (d *debugState) logDups(s *InspectSnapshot) {
	if !d.log || d.logged >= s.NDups {
		return
	}
	for ; d.logged < s.NDups; d.logged++ {
		k := s.Dups[d.logged]
		b := append(d.out[:0], "gnui: duplicate key #"...)
		b = strconv.AppendUint(b, uint64(k.Key), 16)
		b = append(b, " (parent #"...)
		b = strconv.AppendUint(b, uint64(k.Parent), 16)
		b = append(b, ')')
		if s.HasDupBox[d.logged] {
			bx := &s.DupBoxes[d.logged]
			b = append(b, " first box "...)
			b = append(b, ui.DebugRoleName(bx.Role)...)
			b = append(b, " \""...)
			b = append(b, bx.Name[:bx.NameLen]...)
			b = append(b, "\" at "...)
			b = strconv.AppendInt(b, int64(bx.Rect[0]), 10)
			b = append(b, ',')
			b = strconv.AppendInt(b, int64(bx.Rect[1]), 10)
		}
		b = append(b, ": two boxes in one frame; the second got a derived key\n"...)
		d.out = b[:0]
		os.Stderr.Write(b)
	}
}
