package term

import (
	"time"
	"unsafe"

	"gonativeui/internal/ghosttyvt"
)

// Color is a cell's colour: the default, one of 256 indexed colours, or
// 24-bit RGB. The renderer maps indexed colours through its palette.
type Color uint32

// ColorKind says what a Color is.
type ColorKind uint8

// The colour kinds.
const (
	// ColorDefault is the terminal's default foreground or background.
	ColorDefault ColorKind = iota
	// ColorIndexed is one of the 256 indexed colours (0–15 are the
	// palette's ANSI colours, which the theme supplies).
	ColorIndexed
	// ColorRGB is a 24-bit colour.
	ColorRGB
)

// DefaultColor is the default colour.
const DefaultColor Color = 0

// Indexed is indexed colour i.
func Indexed(i uint8) Color { return Color(1)<<24 | Color(i) }

// RGB is the 24-bit colour r, g, b.
func RGB(r, g, b uint8) Color { return Color(2)<<24 | Color(r)<<16 | Color(g)<<8 | Color(b) }

// Kind is what c is.
func (c Color) Kind() ColorKind { return ColorKind(c >> 24) }

// Index is an indexed colour's index.
func (c Color) Index() uint8 { return uint8(c) }

// RGB is an RGB colour's components.
func (c Color) RGB() (r, g, b uint8) { return uint8(c >> 16), uint8(c >> 8), uint8(c) }

// Attr are a style's attributes.
type Attr uint16

// The attributes (SGR 1–9, 53).
const (
	// Bold, Dim, Italic, Blink, Inverse, Hidden, Strike and Overline are the
	// SGR attributes of those names.
	Bold Attr = 1 << iota
	Dim
	Italic
	Blink
	Inverse
	Hidden
	Strike
	Overline
)

// Underline is a style's underline (SGR 4 and 4:x, 21).
type Underline uint8

// The underline styles.
const (
	// UnderlineNone is no underline; the others are the styles of their names.
	UnderlineNone Underline = iota
	UnderlineSingle
	UnderlineDouble
	UnderlineCurly
	UnderlineDotted
	UnderlineDashed
)

// Style is a cell's look, interned in the terminal's style table
// (Cell.Style indexes it; 0 is the default style).
type Style struct {
	// FG and BG are the colours, UL the underline's (DefaultColor: the
	// foreground's).
	FG, BG, UL Color
	// Attr are the attributes; Underline the underline style.
	Attr      Attr
	Underline Underline
}

// CellFlags describe a cell beyond its rune.
type CellFlags uint16

// The cell flags.
const (
	// Wide is the first column of a wide character; WideTail the second,
	// which draws nothing.
	Wide CellFlags = 1 << iota
	WideTail
	// Grapheme: the character has more than one code point; Rune is the
	// first and Row.Grapheme has them all.
	Grapheme
	// Hyperlink: the cell is part of an OSC 8 hyperlink (Terminal.HyperlinkAt).
	Hyperlink
)

// Cell is one cell of a row: 8 bytes, pointer-free.
type Cell struct {
	// Rune is the character, 0 for a blank cell.
	Rune rune
	// Style indexes the style table (Terminal.Style).
	Style uint16
	// Flags describe the cell.
	Flags CellFlags
}

// Dirty is how much of the screen changed since CleanFrame.
type Dirty uint8

// The dirty states.
const (
	// Clean: nothing to redraw.
	Clean Dirty = iota
	// Partial: the rows whose Row.Dirty is set.
	Partial
	// Full: every row (a resize, a scroll, a screen switch, a new style table).
	Full
)

// CursorShape is the cursor's shape (DECSCUSR).
type CursorShape uint8

// The cursor shapes.
const (
	// CursorBlock, CursorUnderline, CursorBar and CursorHollow (a block's
	// outline, for an unfocused terminal) are the shapes of their names.
	CursorBlock CursorShape = iota
	CursorUnderline
	CursorBar
	CursorHollow
)

// Cursor is where the cursor is in the viewport and how it shows, as of the
// last Update.
type Cursor struct {
	// X and Y are its cell; InView is false when it is outside the viewport
	// (scrolled away).
	X, Y   int
	InView bool
	// Visible is DECTCEM; Blink and Shape are DECSCUSR's. OnWideTail: the
	// cursor is on a wide character's second column.
	Visible, Blink, OnWideTail bool
	Shape                      CursorShape
}

// Row is one row of the viewport as of the last Update. NextRow fills its
// position, dirty flag and selection; DecodeRow its cells.
type Row struct {
	// Y is the row's line in the viewport, from 0.
	Y int
	// ID is the row's identity: it stays with the row's content as the
	// screen scrolls, and changes when the terminal rewrites the row.
	ID RowID
	// Dirty: the row changed since CleanFrame. A row that is not dirty has
	// the cells it had under its ID, possibly at another Y (the screen
	// scrolled): cache a row's work by ID, not by Y.
	Dirty bool
	// Selected: columns SelFrom to SelTo (inclusive) are selected.
	Selected       bool
	SelFrom, SelTo int
	// Wrapped: the line goes on in the next row (a soft wrap). Set by DecodeRow.
	Wrapped bool
	// Cells are the row's cells, one per column, after DecodeRow; the slice
	// is reused by the next DecodeRow into this Row.
	Cells []Cell

	raw    ghCells
	runes  []rune
	graphs []graphRef
}

type graphRef struct {
	x      uint16
	off, n uint16
	_      uint16
}

// Grapheme is the code points of the Grapheme cell at column x (nil for
// another cell), valid until the next DecodeRow into r.
func (r *Row) Grapheme(x int) []rune {
	for _, g := range r.graphs {
		if int(g.x) == x {
			return r.runes[g.off : g.off+g.n]
		}
	}
	return nil
}

// Update captures the terminal's screen for drawing and reports what
// changed. While a program holds the screen (synchronized output, mode
// 2026) it keeps the previous capture, up to a second.
func (t *Terminal) Update() Dirty {
	if t.closed {
		return Clean
	}
	if t.held && time.Since(t.heldAt) < holdLimit {
		return Clean
	}
	full := false
	if len(t.styles) > styleSoftLimit {
		t.resetStyles()
		full = true
	}
	if !full && t.renderClean {
		t.rowY, t.rowsReset = 0, true
		return Clean
	}
	t.c().Uint(uint64(t.rs)).Uint(uint64(t.h)).Do(fn(ghosttyvt.RenderStateUpdate))
	t.i32 = 0
	t.c().Uint(uint64(t.rs)).Int32(rsDirty).Ptr(unsafe.Pointer(&t.i32)).Do(fn(ghosttyvt.RenderStateGet))
	d := Dirty(t.i32)
	if d == Clean {
		t.renderClean = true
	}
	t.handle = t.rows
	t.c().Uint(uint64(t.rs)).Int32(rsRowIterator).Ptr(unsafe.Pointer(&t.handle)).Do(fn(ghosttyvt.RenderStateGet))
	t.cursor = ghCursor{size: uint64(unsafe.Sizeof(ghCursor{}))}
	t.c().Uint(uint64(t.rs)).Int32(rsCursor).Ptr(unsafe.Pointer(&t.cursor)).Do(fn(ghosttyvt.RenderStateGet))
	t.rowY, t.rowsReset = 0, false
	if full {
		return Full
	}
	return d
}

// CleanFrame marks the captured screen drawn: the next Update reports only
// what changes after it.
func (t *Terminal) CleanFrame() {
	if !t.closed {
		t.c().Uint(uint64(t.rs)).Do(fn(ghosttyvt.RenderStateClean))
		t.renderClean = !t.held
	}
}

// Cursor is the cursor as of the last Update.
func (t *Terminal) Cursor() Cursor {
	c := &t.cursor
	shape := CursorBlock
	switch c.style {
	case 0:
		shape = CursorBar
	case 2:
		shape = CursorUnderline
	case 3:
		shape = CursorHollow
	}
	return Cursor{X: int(c.x), Y: int(c.y), InView: c.hasViewport, Visible: c.visible, Blink: c.blink,
		OnWideTail: c.wideTail, Shape: shape}
}

// NextRow advances to the next row of the last Update, from the top, and
// fills r's position, dirty flag and selection; it returns false after the
// last row. Call DecodeRow for the cells.
func (t *Terminal) NextRow(r *Row) bool {
	if !t.closed && t.rowsReset {
		t.handle = t.rows
		t.c().Uint(uint64(t.rs)).Int32(rsRowIterator).Ptr(unsafe.Pointer(&t.handle)).Do(fn(ghosttyvt.RenderStateGet))
		t.rowsReset = false
	}
	if t.closed || !t.c().Uint(uint64(t.rows)).Do(fn(ghosttyvt.RowIteratorNext)).Bool() {
		return false
	}
	t.keys = [5]int32{rowDirty, rowRaw, rowCellsRaw, rowID, rowSelection}
	t.vals = [5]uintptr{uintptr(unsafe.Pointer(&t.rowDirty)), uintptr(unsafe.Pointer(&t.rowRaw)),
		uintptr(unsafe.Pointer(&t.rowCells)), uintptr(unsafe.Pointer(&t.rowID)), uintptr(unsafe.Pointer(&t.rowSel))}
	t.rowSel = ghRowSel{size: uint64(unsafe.Sizeof(ghRowSel{}))}
	t.written = 0
	res := t.c().Uint(uint64(t.rows)).Uint(5).Ptr(unsafe.Pointer(&t.keys)).Ptr(unsafe.Pointer(&t.vals)).
		Ptr(unsafe.Pointer(&t.written)).Do(fn(ghosttyvt.RowGetMulti)).Int32()
	r.Y = t.rowY
	t.rowY++
	r.Dirty = t.rowDirty
	r.ID = t.rowID
	r.raw = t.rowCells
	r.Selected = res == resSuccess
	r.SelFrom, r.SelTo = int(t.rowSel.start), int(t.rowSel.end)
	if !r.Selected {
		r.SelFrom, r.SelTo = 0, -1
	}
	t.u64 = t.rowRaw
	return true
}

// DecodeRow fills r.Cells, r.Wrapped and the row's graphemes for the row
// NextRow just returned (call it before the next NextRow). Styles are
// interned in the terminal's table; DecodeRow allocates nothing once r's
// buffers and the table have grown to fit.
func (t *Terminal) DecodeRow(r *Row) {
	r.Cells, r.runes, r.graphs = r.Cells[:0], r.runes[:0], r.graphs[:0]
	if t.closed || r.raw.ptr == 0 {
		return
	}
	raw := unsafe.Slice((*uint64)(cptr(r.raw.ptr)), r.raw.n)
	l := &lib.cell
	t.nRowIDs = 0
	t.cellsReady = false
	for x, v := range raw {
		c := Cell{}
		switch l.tag.get(v) {
		case contentCodepoint:
			c.Rune = rune(l.cp.get(v))
		case contentGrapheme:
			c.Rune = rune(l.cp.get(v))
			t.readyCells()
			if t.graphemes(r, x) {
				c.Flags |= Grapheme
			}
		case contentBGPalette:
			c.Style = t.intern(Style{BG: Indexed(uint8(l.palette.get(v)))})
		case contentBGRGB:
			c.Style = t.intern(Style{BG: RGB(uint8(l.r.get(v)), uint8(l.g.get(v)), uint8(l.b.get(v)))})
		}
		if id := uint16(l.style.get(v)); id != 0 {
			c.Style = t.rowStyle(id, x)
		}
		switch l.wide.get(v) {
		case wideWide:
			c.Flags |= Wide
		case wideSpacerTail:
			c.Flags |= WideTail
			c.Rune = 0
		case wideSpacerHead:
			c.Rune = 0
		}
		if l.link.get(v) != 0 {
			c.Flags |= Hyperlink
		}
		r.Cells = append(r.Cells, c)
	}
	t.flag = false
	t.c().Uint(t.u64).Int32(rowDataWrap).Ptr(unsafe.Pointer(&t.flag)).Do(fn(ghosttyvt.RowGet))
	r.Wrapped = t.flag
}

// readyCells points the row-cells object at the current row, once per row.
func (t *Terminal) readyCells() {
	if !t.cellsReady {
		t.handle = t.cells
		t.c().Uint(uint64(t.rows)).Int32(rowCells).Ptr(unsafe.Pointer(&t.handle)).Do(fn(ghosttyvt.RenderRowGet))
		t.cellsReady = true
	}
}

// graphemes reads the code points of the grapheme cell at x into r.
func (t *Terminal) graphemes(r *Row, x int) bool {
	t.c().Uint(uint64(t.cells)).Uint16(uint16(x)).Do(fn(ghosttyvt.RowCellsSelect))
	t.u32 = 0
	t.c().Uint(uint64(t.cells)).Int32(cellsGraphemesLen).Ptr(unsafe.Pointer(&t.u32)).Do(fn(ghosttyvt.RowCellsGet))
	n := int(t.u32)
	if n < 2 || n > len(t.cps) {
		return false
	}
	t.c().Uint(uint64(t.cells)).Int32(cellsGraphemesBuf).Ptr(unsafe.Pointer(&t.cps[0])).Do(fn(ghosttyvt.RowCellsGet))
	off := len(r.runes)
	for _, cp := range t.cps[:n] {
		r.runes = append(r.runes, rune(cp))
	}
	r.graphs = append(r.graphs, graphRef{x: uint16(x), off: uint16(off), n: uint16(n)})
	return true
}

// rowStyle is the table index of the row's style id (ids are local to the
// row's page, so the cache is per row).
func (t *Terminal) rowStyle(id uint16, x int) uint16 {
	for i := 0; i < t.nRowIDs; i++ {
		if t.rowIDs[i] == id {
			return t.rowIdx[i]
		}
	}
	t.readyCells()
	t.c().Uint(uint64(t.cells)).Uint16(uint16(x)).Do(fn(ghosttyvt.RowCellsSelect))
	t.style = ghStyle{size: uint64(unsafe.Sizeof(ghStyle{}))}
	t.c().Uint(uint64(t.cells)).Int32(cellsStyle).Ptr(unsafe.Pointer(&t.style)).Do(fn(ghosttyvt.RowCellsGet))
	idx := t.intern(convertStyle(&t.style))
	if t.nRowIDs < len(t.rowIDs) {
		t.rowIDs[t.nRowIDs], t.rowIdx[t.nRowIDs] = id, idx
		t.nRowIDs++
	}
	return idx
}

func styleColor(c *ghStyleColor) Color {
	switch c.tag {
	case 1:
		return Indexed(c.value[0])
	case 2:
		return RGB(c.value[0], c.value[1], c.value[2])
	}
	return DefaultColor
}

func convertStyle(s *ghStyle) Style {
	st := Style{FG: styleColor(&s.fg), BG: styleColor(&s.bg), UL: styleColor(&s.ul)}
	for _, a := range [...]struct {
		on bool
		a  Attr
	}{{s.bold, Bold}, {s.faint, Dim}, {s.italic, Italic}, {s.blink, Blink}, {s.inverse, Inverse},
		{s.invisible, Hidden}, {s.strike, Strike}, {s.overline, Overline}} {
		if a.on {
			st.Attr |= a.a
		}
	}
	if s.underline > 0 && s.underline <= int32(UnderlineDashed) {
		st.Underline = Underline(s.underline)
	}
	return st
}

// intern is s's index in the style table.
func (t *Terminal) intern(s Style) uint16 {
	if i, ok := t.styleIdx[s]; ok {
		return i
	}
	if len(t.styles) >= 0xFFFF {
		return 0
	}
	i := uint16(len(t.styles))
	t.styles = append(t.styles, s)
	t.styleIdx[s] = i
	return i
}

func (t *Terminal) resetStyles() {
	t.styles = t.styles[:1]
	clear(t.styleIdx)
	t.styleIdx[Style{}] = 0
	t.styleGen++
}

// Style is style i of the style table (0 is the default style).
func (t *Terminal) Style(i uint16) Style {
	if int(i) < len(t.styles) {
		return t.styles[i]
	}
	return Style{}
}

// StyleGen changes when Update rebuilds the style table (which it then
// reports as Full): style indices of rows decoded before it are void.
func (t *Terminal) StyleGen() uint32 { return t.styleGen }
