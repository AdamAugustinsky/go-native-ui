package term

import (
	"unsafe"

	"gonativeui/internal/ghosttyvt"
)

// Point is a cell of the viewport.
type Point struct {
	X, Y int // the column, and the row from the viewport's top
}

// gridRef resolves a viewport point to a grid reference in t.ref (or ref2).
func (t *Terminal) gridRef(p Point, dst *ghGridRef) bool {
	if t.closed || p.X < 0 || p.Y < 0 || p.X >= t.cols || p.Y >= t.nrows {
		return false
	}
	t.point = ghPoint{tag: pointViewport, x: uint16(p.X), y: uint32(p.Y)}
	*dst = ghGridRef{size: uint64(unsafe.Sizeof(ghGridRef{}))}
	return t.c().Uint(uint64(t.h)).Struct(unsafe.Pointer(&t.point), unsafe.Sizeof(t.point)).Ptr(unsafe.Pointer(dst)).
		Do(fn(ghosttyvt.TerminalGridRef)).Int32() == resSuccess
}

// setSelection makes t.sel the terminal's selection (nil clears it).
func (t *Terminal) setSelection(sel *ghSelection) {
	t.c().Uint(uint64(t.h)).Int32(optSelection).Ptr(unsafe.Pointer(sel)).Do(fn(ghosttyvt.TerminalSet))
}

// BeginSelection starts a selection at p (a press): it anchors there and
// is empty until ExtendSelection. The anchor follows the text as the
// screen scrolls.
func (t *Terminal) BeginSelection(p Point) {
	t.ClearSelection()
	if !t.gridRef(p, &t.ref) {
		return
	}
	t.point = ghPoint{tag: pointViewport, x: uint16(p.X), y: uint32(p.Y)}
	t.handle = 0
	if t.c().Uint(uint64(t.h)).Struct(unsafe.Pointer(&t.point), unsafe.Sizeof(t.point)).Ptr(unsafe.Pointer(&t.handle)).
		Do(fn(ghosttyvt.TerminalGridRefTrack)).Int32() == resSuccess {
		t.anchor = t.handle
	}
}

// ExtendSelection selects from the anchor to p (a drag, or a shift-click);
// rect selects the rectangle between them (Option-drag) instead of the
// text in reading order.
func (t *Terminal) ExtendSelection(p Point, rect bool) {
	if t.anchor == 0 || !t.gridRef(p, &t.ref2) {
		return
	}
	t.coord = ghCoord{}
	if t.c().Uint(uint64(t.anchor)).Int32(pointScreen).Ptr(unsafe.Pointer(&t.coord)).Do(fn(ghosttyvt.TrackedGridRefPoint)).Int32() != resSuccess {
		return
	}
	t.point = ghPoint{tag: pointScreen, x: t.coord.x, y: t.coord.y}
	t.ref = ghGridRef{size: uint64(unsafe.Sizeof(ghGridRef{}))}
	if t.c().Uint(uint64(t.h)).Struct(unsafe.Pointer(&t.point), unsafe.Sizeof(t.point)).Ptr(unsafe.Pointer(&t.ref)).
		Do(fn(ghosttyvt.TerminalGridRef)).Int32() != resSuccess {
		return
	}
	t.sel = ghSelection{size: uint64(unsafe.Sizeof(ghSelection{})), start: t.ref, end: t.ref2, rectangle: rect}
	t.setSelection(&t.sel)
}

// SelectWord selects the word at p (a double click), as Ghostty delimits
// words.
func (t *Terminal) SelectWord(p Point) {
	if !t.gridRef(p, &t.word.ref) {
		return
	}
	t.word.size = uint64(unsafe.Sizeof(ghSelectWordOpts{}))
	t.word.boundaries, t.word.nb = 0, 0
	t.sel = ghSelection{size: uint64(unsafe.Sizeof(ghSelection{}))}
	if t.c().Uint(uint64(t.h)).Ptr(unsafe.Pointer(&t.word)).Ptr(unsafe.Pointer(&t.sel)).Do(fn(ghosttyvt.SelectWord)).Int32() == resSuccess {
		t.setSelection(&t.sel)
	}
}

// SelectLine selects the line at p, soft wraps joined (a triple click).
func (t *Terminal) SelectLine(p Point) {
	if !t.gridRef(p, &t.line.ref) {
		return
	}
	t.line.size = uint64(unsafe.Sizeof(ghSelectLineOpts{}))
	t.line.ws, t.line.nws, t.line.semantic = 0, 0, false
	t.sel = ghSelection{size: uint64(unsafe.Sizeof(ghSelection{}))}
	if t.c().Uint(uint64(t.h)).Ptr(unsafe.Pointer(&t.line)).Ptr(unsafe.Pointer(&t.sel)).Do(fn(ghosttyvt.SelectLine)).Int32() == resSuccess {
		t.setSelection(&t.sel)
	}
}

// SelectAll selects the scrollback and the screen.
func (t *Terminal) SelectAll() {
	if t.closed {
		return
	}
	t.sel = ghSelection{size: uint64(unsafe.Sizeof(ghSelection{}))}
	if t.c().Uint(uint64(t.h)).Ptr(unsafe.Pointer(&t.sel)).Do(fn(ghosttyvt.SelectAll)).Int32() == resSuccess {
		t.setSelection(&t.sel)
	}
}

// ClearSelection removes the selection.
func (t *Terminal) ClearSelection() {
	if t.closed {
		return
	}
	if t.anchor != 0 {
		t.c().Uint(uint64(t.anchor)).Do(fn(ghosttyvt.TrackedGridRefFree))
		t.anchor = 0
	}
	t.setSelection(nil)
}

// HasSelection reports whether some text is selected.
func (t *Terminal) HasSelection() bool {
	if t.closed {
		return false
	}
	t.sel = ghSelection{size: uint64(unsafe.Sizeof(ghSelection{}))}
	return t.get(dataSelection, unsafe.Pointer(&t.sel)) == resSuccess
}

// AppendSelection appends the selected text, as plain text with soft wraps
// joined and trailing blanks trimmed, to dst: what a copy puts on the
// clipboard. It appends nothing when nothing is selected.
func (t *Terminal) AppendSelection(dst []byte) []byte {
	if !t.HasSelection() {
		return dst
	}
	t.selFmt = ghSelFormatOpts{size: uint64(unsafe.Sizeof(ghSelFormatOpts{})), emit: formatPlain, unwrap: true, trim: true,
		selection: uintptr(unsafe.Pointer(&t.sel))}
	return t.formatInto(dst, func(buf unsafe.Pointer, n int) int32 {
		return t.c().Uint(uint64(t.h)).Struct(unsafe.Pointer(&t.selFmt), unsafe.Sizeof(t.selFmt)).Ptr(buf).Uint(uint64(n)).
			Ptr(unsafe.Pointer(&t.u64)).Do(fn(ghosttyvt.SelectionFormatBuf)).Int32()
	})
}

// formatInto runs a formatter into dst's spare capacity, growing it to the
// size the formatter reports when it does not fit.
func (t *Terminal) formatInto(dst []byte, f func(buf unsafe.Pointer, n int) int32) []byte {
	for range 3 {
		if cap(dst)-len(dst) < 256 {
			dst = append(dst, make([]byte, 256)...)[:len(dst)]
		}
		spare := dst[len(dst):cap(dst)]
		t.u64 = 0
		r := f(unsafe.Pointer(&spare[0]), len(spare))
		switch r {
		case resSuccess:
			return dst[:len(dst)+int(t.u64)]
		case resOutOfSpace:
			dst = append(dst, make([]byte, int(t.u64)+1)...)[:len(dst)]
		default:
			return dst
		}
	}
	return dst
}

// Format is what AppendScreen writes.
type Format uint8

// The formats.
const (
	// Plain is the text alone.
	Plain Format = iota
	// VT is the text with the sequences that redraw it: colours, styles,
	// hyperlinks and the cursor.
	VT
	// HTML is the text with inline styles.
	HTML
)

// AppendScreen appends the screen and its scrollback in format f to dst:
// soft-wrapped lines joined, trailing blanks trimmed. The VT format also
// carries the cursor, the styles, the hyperlinks and the modes, so two
// terminals with the same AppendScreen(VT) show the same thing.
func (t *Terminal) AppendScreen(dst []byte, f Format) []byte {
	if t.closed {
		return dst
	}
	o := &t.fmtOpts
	*o = ghFormatterOpts{size: uint64(unsafe.Sizeof(ghFormatterOpts{})), emit: int32(f), unwrap: true, trim: true}
	o.extra.size = uint64(unsafe.Sizeof(o.extra))
	o.extra.screen.size = uint64(unsafe.Sizeof(o.extra.screen))
	if f == VT {
		o.extra.palette, o.extra.modes, o.extra.region, o.extra.tabstops = true, true, true, true
		o.extra.screen.cursor, o.extra.screen.style, o.extra.screen.link, o.extra.screen.charsets = true, true, true, true
	}
	t.handle = 0
	if t.c().Ptr(nil).Ptr(unsafe.Pointer(&t.handle)).Uint(uint64(t.h)).Struct(unsafe.Pointer(o), unsafe.Sizeof(*o)).
		Do(fn(ghosttyvt.FormatterTerminalNew)).Int32() != resSuccess {
		return dst
	}
	h := t.handle
	dst = t.formatInto(dst, func(buf unsafe.Pointer, n int) int32 {
		return t.c().Uint(uint64(h)).Ptr(buf).Uint(uint64(n)).Ptr(unsafe.Pointer(&t.u64)).Do(fn(ghosttyvt.FormatterFormatBuf)).Int32()
	})
	t.c().Uint(uint64(h)).Do(fn(ghosttyvt.FormatterFree))
	return dst
}

// HyperlinkAt appends the URI of the OSC 8 hyperlink at p to dst; ok is
// false when there is none.
func (t *Terminal) HyperlinkAt(dst []byte, p Point) (_ []byte, ok bool) {
	if !t.gridRef(p, &t.ref) {
		return dst, false
	}
	n0 := len(dst)
	dst = t.formatInto(dst, func(buf unsafe.Pointer, n int) int32 {
		return t.c().Ptr(unsafe.Pointer(&t.ref)).Ptr(buf).Uint(uint64(n)).Ptr(unsafe.Pointer(&t.u64)).
			Do(fn(ghosttyvt.GridRefHyperlinkURI)).Int32()
	})
	return dst, len(dst) > n0
}

// Find starts a search for needle (plain text, case-sensitive) through the
// screen and the scrollback and returns the number of matches. An empty
// needle ends the search. FindNext moves between matches;
// AppendViewportMatches gives the ones in view, to highlight.
func (t *Terminal) Find(needle string) int {
	if t.closed {
		return 0
	}
	if needle == "" {
		if t.search != 0 {
			t.c().Uint(uint64(t.search)).Do(fn(ghosttyvt.SearchFree))
			t.search = 0
		}
		return 0
	}
	if t.search == 0 {
		t.handle = 0
		if t.c().Ptr(nil).Ptr(unsafe.Pointer(&t.handle)).Uint(uint64(t.h)).Do(fn(ghosttyvt.SearchNew)).Int32() != resSuccess {
			return 0
		}
		t.search = t.handle
	}
	t.keyText = append(t.keyText[:0], needle...)
	t.str = ghString{ptr: uintptr(unsafe.Pointer(&t.keyText[0])), n: uint64(len(needle))}
	t.c().Uint(uint64(t.search)).Int32(searchOptNeedle).Ptr(unsafe.Pointer(&t.str)).Do(fn(ghosttyvt.SearchSet))
	t.c().Uint(uint64(t.search)).Do(fn(ghosttyvt.SearchRun))
	t.u64 = 0
	t.c().Uint(uint64(t.search)).Int32(searchDataTotal).Ptr(unsafe.Pointer(&t.u64)).Do(fn(ghosttyvt.SearchGet))
	return int(t.u64)
}

// FindNext selects the next match (the previous one when backward) and
// scrolls it into view; it returns its index, or -1 when there is none.
func (t *Terminal) FindNext(backward bool) int {
	if t.closed || t.search == 0 {
		return -1
	}
	opt := int32(searchOptSelectNext)
	if backward {
		opt = searchOptSelectPrev
	}
	if t.c().Uint(uint64(t.search)).Int32(opt).Ptr(nil).Do(fn(ghosttyvt.SearchSet)).Int32() != resSuccess {
		return -1
	}
	t.u64 = 0
	if t.c().Uint(uint64(t.search)).Int32(searchDataSelIndex).Ptr(unsafe.Pointer(&t.u64)).Do(fn(ghosttyvt.SearchGet)).Int32() != resSuccess {
		return -1
	}
	return int(t.u64)
}

// Span is a run of cells in one viewport row.
type Span struct {
	Y, From, To int // the row, and the first and last columns (inclusive)
}

// AppendViewportMatches appends the cells of the matches in view (each
// split into its rows) to dst, for highlighting. Call it after Update.
func (t *Terminal) AppendViewportMatches(dst []Span) []Span {
	if t.closed || t.search == 0 {
		return dst
	}
	t.c().Uint(uint64(t.search)).Do(fn(ghosttyvt.SearchFeed))
	t.selBuf = ghSelBuffer{ptr: uintptr(unsafe.Pointer(&t.matches[0])), cap: uint64(len(t.matches))}
	if t.c().Uint(uint64(t.search)).Int32(searchDataViewMatches).Ptr(unsafe.Pointer(&t.selBuf)).Do(fn(ghosttyvt.SearchGet)).Int32() != resSuccess {
		return dst
	}
	for i := 0; i < int(min(t.selBuf.len, uint64(len(t.matches)))); i++ {
		m := &t.matches[i]
		var a, b ghCoord
		if !t.viewportCoord(&m.start, &a) || !t.viewportCoord(&m.end, &b) {
			continue
		}
		for y := int(a.y); y <= int(b.y) && y < t.nrows; y++ {
			from, to := 0, t.cols-1
			if y == int(a.y) {
				from = int(a.x)
			}
			if y == int(b.y) {
				to = int(b.x)
			}
			dst = append(dst, Span{y, from, to})
		}
	}
	return dst
}

func (t *Terminal) viewportCoord(ref *ghGridRef, out *ghCoord) bool {
	t.ref = *ref
	t.coord = ghCoord{}
	ok := t.c().Uint(uint64(t.h)).Ptr(unsafe.Pointer(&t.ref)).Int32(pointViewport).Ptr(unsafe.Pointer(&t.coord)).
		Do(fn(ghosttyvt.TerminalPointFromGridRef)).Int32() == resSuccess
	*out = t.coord
	return ok
}
