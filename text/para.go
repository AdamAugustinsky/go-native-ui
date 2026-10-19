package text

import (
	"encoding/binary"
	"math"
)

// Paragraph layout (D42): a paragraph of styled text broken into lines at a
// width, with every line's glyphs in visual order. On macOS the lines come
// from CoreText's typesetter (CTTypesetter over an attributed string: the line
// breaking, bidirectional ordering, font fallback and emoji clusters of
// AppKit); elsewhere from the basic layout of para_basic.go. Layouts are
// cached pointer-free (paracache.go), keyed by everything in Para.
//
// # Coordinates
//
// Pixels, like everything in this package. A paragraph's origin is the top
// left of its first line. A line's glyph positions (ParaView.X) are relative
// to the line's own origin on the left of its advance; where that origin sits
// depends on the width the paragraph is drawn in (natural alignment, see
// ParaView.LineLeft), so the geometry functions take that width.

// Span styles the bytes [Off, Off+Len) of a paragraph's text with style Style:
// the font Para.Fonts[Style] for the layout, and the caller's own style table
// (colour, decorations) for drawing. Flags and Tag are not read by the layout;
// they are the caller's (ui marks links and mentions in Flags, and Tag is the
// app's id for the span). Bytes outside every span have style 0.
//
// Spans are pointer-free and sorted by Off without overlapping. A span that
// overlaps the one before it, or reaches outside the text, is cut to fit;
// offsets should be on UTF-8 character boundaries.
type Span struct {
	// Off and Len are the span's bytes in the text.
	Off, Len uint32
	// Style indexes Para.Fonts and the caller's style table.
	Style uint16
	// Flags are the caller's bits (ui: SpanLink, SpanMention).
	Flags uint16
	// Tag is the caller's id for the span (a link or mention id).
	Tag uint32
}

// Dir is the base direction of a paragraph.
type Dir uint8

const (
	// DirAuto takes the direction of each paragraph (text between newlines)
	// from its first strong character (UAX #9 rules P2 and P3); left to right
	// when it has none.
	DirAuto Dir = iota
	// DirLTR sets every paragraph left to right.
	DirLTR
	// DirRTL sets every paragraph right to left.
	DirRTL
)

// Para is the input of a paragraph layout. Every field is part of the cache
// key.
type Para struct {
	// Text is the UTF-8 text. Newlines (LF, CR LF, U+2028, U+2029) end lines;
	// a final newline adds no empty line.
	Text []byte
	// Spans style ranges of Text (see Span).
	Spans []Span
	// Fonts is the font of each style; a style beyond its end uses Fonts[0].
	// It must not be empty. All fonts must have the same scale.
	Fonts []FontKey
	// Width is the width lines are broken at, in pixels; 0 or less breaks only
	// at newlines.
	Width float32
	// Dir is the base direction (DirAuto: per paragraph).
	Dir Dir
	// MaxLines is the most lines laid out (0: no limit). Text beyond them is
	// left out and ParaView.Truncated set.
	MaxLines int
	// Ellipsis ends the last line with "…" when text was left out (MaxLines),
	// cutting the line between characters so that it fits Width.
	Ellipsis bool
	// LineHeight, when positive, gives every line that height in pixels, with
	// the baseline where a one-line box of Fonts[0] has it (centred, as ui
	// draws plain text). When 0 each line is as high as the fonts on it make
	// it (ascent + descent + leading, the largest of its runs), as in AppKit.
	LineHeight float32
}

// ParaRef identifies a paragraph layout in a layout cache. The zero ParaRef is
// invalid and has no lines.
type ParaRef uint32

// Line is one line of a ParaView.
type Line struct {
	// Start and End are the line's bytes in the text. End includes the white
	// space and the newline that end the line; for a line cut by an ellipsis
	// it is where the text was cut.
	Start, End uint32
	// G0 and G1 are the line's glyphs in the ParaView, in visual order.
	G0, G1 uint32
	// Top is the top of the line box and Baseline its baseline, from the
	// paragraph's top; Height is the line box's height.
	Top, Baseline, Height float32
	// Ascent and Descent are the largest of the fonts on the line.
	Ascent, Descent float32
	// Width is the advance of the line without the white space at its end
	// (which hangs outside the width it was broken at); Advance includes it.
	Width, Advance float32
	// Flags are LineRTL, LineEllipsis and LineHard.
	Flags uint8
}

// Line flags.
const (
	// LineRTL: the line's paragraph is right to left.
	LineRTL uint8 = 1 << iota
	// LineEllipsis: the line ends in an ellipsis that stands for the text
	// left out.
	LineEllipsis
	// LineHard: the line ends at a newline.
	LineHard
)

// ParaView is a laid-out paragraph. The slices alias cache storage: do not
// modify or append to them; they are valid until the cache evicts (compare
// ParaGeneration).
type ParaView struct {
	// Text is the laid-out text and Spans the spans as given.
	Text  []byte
	Spans []Span
	// Lines are the lines from the top.
	Lines []Line
	// Glyphs, X, Faces, Clusters and SpanOf are per glyph, line by line, each
	// line in visual order. X is the pen x relative to the line's origin.
	// Clusters are byte offsets into Text with ClusterRTL, as in RunView; an
	// ellipsis glyph has the offset where the text was cut. SpanOf is 1 + the
	// index in Spans of the span the glyph's character is in, 0 for none
	// (style 0).
	Glyphs   []uint16
	X        []float32
	Faces    []FaceID
	Clusters []uint32
	SpanOf   []uint16
	// Width is the widest line's Width; Height the bottom of the last line.
	Width, Height float32
	// Truncated reports that text was left out (Para.MaxLines).
	Truncated bool
}

// Style returns the style of glyph g: its span's, or 0.
func (p *ParaView) Style(g int) uint16 {
	if s := p.SpanOf[g]; s != 0 {
		return p.Spans[s-1].Style
	}
	return 0
}

// LineLeft is where line i starts (the origin of its glyph positions) when the
// paragraph is drawn in a box width wide with natural alignment: at the left
// for a left-to-right line, Width from the right for a right-to-left one. The
// white space that ends a line hangs outside the width: right of a
// left-to-right line, and left of a right-to-left one's origin, where its
// glyphs have negative X (CoreText's placement, and AppKit's).
func (p *ParaView) LineLeft(i int, width float32) float32 {
	l := &p.Lines[i]
	if l.Flags&LineRTL != 0 {
		return width - l.Width
	}
	return 0
}

// LineAt returns the line at y (from the paragraph's top), clamped to the
// first and last lines; -1 when there are none.
func (p *ParaView) LineAt(y float32) int {
	n := len(p.Lines)
	if n == 0 {
		return -1
	}
	for i := 0; i < n-1; i++ {
		if y < p.Lines[i].Top+p.Lines[i].Height {
			return i
		}
	}
	return n - 1
}

// LineOf returns the line holding byte offset off: the line with Start <= off
// < End (an offset at a soft line break is at the start of the next line), the
// last line for the end of the text; -1 when there are no lines.
func (p *ParaView) LineOf(off int) int {
	n := len(p.Lines)
	if n == 0 {
		return -1
	}
	lo, hi := 0, n-1
	for lo < hi {
		m := (lo + hi + 1) / 2
		if int(p.Lines[m].Start) <= off {
			lo = m
		} else {
			hi = m - 1
		}
	}
	return lo
}

// LineRun returns line i as a RunView, for RunView.CaretX and HitByte: its
// glyphs, with clusters and Text indexing the paragraph's text (Text ends at
// the line's end).
func (p *ParaView) LineRun(i int) RunView {
	l := &p.Lines[i]
	g0, g1 := l.G0, l.G1
	return RunView{
		Glyphs:   p.Glyphs[g0:g1:g1],
		X:        p.X[g0:g1:g1],
		Faces:    p.Faces[g0:g1:g1],
		Clusters: p.Clusters[g0:g1:g1],
		Text:     p.Text[:l.End:l.End],
		Advance:  l.Advance,
	}
}

// lineEndCaret is the last caret offset of line i: its end, before the
// newline that ends it.
func (p *ParaView) lineEndCaret(i int) int {
	l := &p.Lines[i]
	e := int(l.End)
	if l.Flags&LineHard != 0 {
		e = int(l.Start) + trimNewline(p.Text[l.Start:l.End])
	}
	return e
}

// trimNewline is the length of b without the newline at its end.
func trimNewline(b []byte) int {
	n := len(b)
	switch {
	case n >= 2 && b[n-2] == '\r' && b[n-1] == '\n':
		return n - 2
	case n >= 1 && (b[n-1] == '\n' || b[n-1] == '\r' || b[n-1] == 0x0B || b[n-1] == 0x0C):
		return n - 1
	case n >= 3 && b[n-3] == 0xE2 && b[n-2] == 0x80 && (b[n-1] == 0xA8 || b[n-1] == 0xA9):
		return n - 3
	}
	return n
}

// HitTest returns the byte offset of the caret position nearest to (x, y), a
// point relative to the paragraph's origin, when the paragraph is drawn width
// wide (see LineLeft). The offset is a grapheme boundary on the line at y,
// never after the newline that ends it. 0 for a paragraph without lines.
func (p *ParaView) HitTest(x, y, width float32) int {
	i := p.LineAt(y)
	if i < 0 {
		return 0
	}
	l := &p.Lines[i]
	if l.G1 == l.G0 {
		return int(l.Start)
	}
	off := p.LineRun(i).HitByte(x - p.LineLeft(i, width))
	return min(max(off, int(l.Start)), p.lineEndCaret(i))
}

// GlyphAt returns the glyph whose cell contains (x, y) (relative to the
// paragraph's origin, drawn width wide), -1 when the point is on no glyph: left
// or right of its line's ink, or below the last line. A cell runs from the
// glyph's pen position to the next glyph's on the line.
func (p *ParaView) GlyphAt(x, y, width float32) int {
	if len(p.Lines) == 0 || y < 0 || y >= p.Height {
		return -1
	}
	i := p.LineAt(y)
	l := &p.Lines[i]
	x -= p.LineLeft(i, width)
	if x < 0 {
		return -1
	}
	hit := -1
	var m float32
	for g := l.G0; g < l.G1; g++ {
		m = max(m, p.X[g])
		if m > x {
			break
		}
		hit = int(g)
	}
	if hit < 0 {
		return -1
	}
	end := l.Advance
	if hit+1 < int(l.G1) {
		end = p.X[hit+1]
	}
	if x >= end && hit == int(l.G1)-1 {
		return -1
	}
	return hit
}

// SpanAt returns the index in Spans of the span under (x, y) (see GlyphAt), -1
// for none.
func (p *ParaView) SpanAt(x, y, width float32) int {
	g := p.GlyphAt(x, y, width)
	if g < 0 || p.SpanOf[g] == 0 {
		return -1
	}
	return int(p.SpanOf[g]) - 1
}

// Caret returns the caret of byte offset off when the paragraph is drawn width
// wide: the line it is on and its x relative to the paragraph's origin. The
// caret rectangle is (x, Lines[line].Top) to (x, Top+Height). line is -1 for
// a paragraph without lines.
func (p *ParaView) Caret(off int, width float32) (line int, x float32) {
	line = p.LineOf(off)
	if line < 0 {
		return -1, 0
	}
	off = min(off, p.lineEndCaret(line))
	l := &p.Lines[line]
	if l.G1 == l.G0 {
		return line, p.LineLeft(line, width)
	}
	return line, p.LineLeft(line, width) + p.LineRun(line).CaretX(off)
}

// RangeRects appends to dst the rectangles (x0, y0, x1, y1, relative to the
// paragraph's origin, drawn width wide) that cover the bytes [from, to): per
// line, one rectangle per stretch of glyphs in visual order whose characters
// are in the range (a bidirectional line can need several), as high as the
// line. A range that runs over the end of a line covers its trailing white
// space too.
func (p *ParaView) RangeRects(from, to int, width float32, dst [][4]float32) [][4]float32 {
	if from >= to {
		return dst
	}
	for i := range p.Lines {
		l := &p.Lines[i]
		if int(l.End) <= from || int(l.Start) >= to {
			continue
		}
		left := p.LineLeft(i, width)
		y0, y1 := l.Top, l.Top+l.Height
		open := false
		var x0, x1 float32
		var m float32
		for g := l.G0; g < l.G1; g++ {
			m = max(m, p.X[g])
			cs := m
			ce := l.Advance
			if g+1 < l.G1 {
				ce = max(m, p.X[g+1])
			}
			c := int(p.Clusters[g] &^ ClusterRTL)
			in := c >= from && c < to
			switch {
			case in && !open:
				open, x0, x1 = true, cs, ce
			case in:
				x1 = ce
			case open:
				dst = append(dst, [4]float32{left + x0, y0, left + x1, y1})
				open = false
			}
		}
		if open {
			dst = append(dst, [4]float32{left + x0, y0, left + x1, y1})
		}
	}
	return dst
}

// ---- helpers shared by the layouts ----

// paraFont is the font of style st.
func paraFont(p *Para, st uint16) FontKey {
	if int(st) < len(p.Fonts) {
		return p.Fonts[st]
	}
	return p.Fonts[0]
}

// styleRun is a stretch of text in one style: bytes [start, end), from span
// (1 + its index in Para.Spans, 0 for none).
type styleRun struct {
	start, end int
	style      uint16
	span       uint16
}

// styleRuns appends the style runs of p, covering its whole text, in order,
// with each span cut to the text and to the end of the span before it.
func styleRuns(dst []styleRun, p *Para) []styleRun {
	n := len(p.Text)
	pos := 0
	for i := range p.Spans {
		if i >= math.MaxUint16 {
			break
		}
		sp := &p.Spans[i]
		s := max(int(sp.Off), pos)
		e := min(int(sp.Off)+int(sp.Len), n)
		if int(sp.Off) > n || e <= s {
			continue
		}
		if s > pos {
			dst = append(dst, styleRun{start: pos, end: s})
		}
		dst = append(dst, styleRun{start: s, end: e, style: sp.Style, span: uint16(i + 1)})
		pos = e
	}
	if pos < n {
		dst = append(dst, styleRun{start: pos, end: n})
	}
	return dst
}

// runAt returns the index of the style run holding byte off (binary search).
func runAt(runs []styleRun, off int) int {
	lo, hi := 0, len(runs)-1
	for lo < hi {
		m := (lo + hi + 1) / 2
		if runs[m].start <= off {
			lo = m
		} else {
			hi = m - 1
		}
	}
	return lo
}

// paraDirs returns whether the paragraph that starts at byte start is right
// to left.
func paraRTL(p *Para, start int) bool {
	switch p.Dir {
	case DirLTR:
		return false
	case DirRTL:
		return true
	}
	return paraStrongRTL(p.Text[start:])
}

// hashPara hashes everything that keys a layout.
func hashPara(p *Para) uint64 {
	h := hashBytes(p.Text, 0x9E3779B97F4A7C15)
	for i := range p.Spans {
		s := &p.Spans[i]
		h = mix64(h ^ uint64(s.Off) ^ uint64(s.Len)<<32)
		h = mix64(h ^ uint64(s.Style) ^ uint64(s.Flags)<<16 ^ uint64(s.Tag)<<32)
	}
	for _, f := range p.Fonts {
		h = mix64(h ^ uint64(f) ^ 0xF0F0<<32)
	}
	h = mix64(h ^ uint64(math.Float32bits(p.Width)) ^ uint64(math.Float32bits(p.LineHeight))<<32)
	var e uint64
	if p.Ellipsis {
		e = 1
	}
	return mix64(h ^ uint64(p.Dir) ^ e<<8 ^ uint64(uint32(p.MaxLines))<<16 ^ uint64(len(p.Spans))<<48)
}

func mix64(x uint64) uint64 {
	x ^= x >> 33
	x *= 0xFF51AFD7ED558CCD
	x ^= x >> 33
	x *= 0xC4CEB9FE1A85EC53
	return x ^ x>>33
}

// hashBytes hashes b eight bytes at a time.
func hashBytes(b []byte, seed uint64) uint64 {
	h := seed ^ uint64(len(b))*0xFF51AFD7ED558CCD
	for len(b) >= 8 {
		h = (h ^ binary.LittleEndian.Uint64(b)) * 0x100000001B3
		h ^= h >> 29
		b = b[8:]
	}
	var t uint64
	for i, c := range b {
		t |= uint64(c) << (8 * i)
	}
	return mix64(h ^ t)
}

// sameSpans reports whether a and b are equal.
func sameSpans(a, b []Span) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameFonts(a, b []FontKey) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isNewlineAt reports whether a newline character starts at byte i of b, and
// its length.
func newlineAt(b []byte, i int) int {
	switch c := b[i]; {
	case c == '\r':
		if i+1 < len(b) && b[i+1] == '\n' {
			return 2
		}
		return 1
	case c == '\n', c == 0x0B, c == 0x0C:
		return 1
	case c == 0xE2 && i+2 < len(b) && b[i+1] == 0x80 && (b[i+2] == 0xA8 || b[i+2] == 0xA9):
		return 3
	}
	return 0
}
