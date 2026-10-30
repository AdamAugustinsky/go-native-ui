package text

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"
)

func indexedPara(v ParaView) ParaView {
	if len(v.SpanOf) != len(v.Glyphs) {
		v.SpanOf = make([]uint16, len(v.Glyphs))
	}
	b := paraBuild{lines: v.Lines, glyphs: v.Glyphs, xs: v.X, faces: v.Faces, clus: v.Clusters,
		spanOf: v.SpanOf, width: v.Width, height: v.Height}
	var s paraSlabs
	var scratch CaretIndex
	e := s.add(&Para{Text: v.Text, Spans: v.Spans}, 0, &b, &scratch)
	return s.view(&e)
}

// The raw view retains the original per-query implementation as a reference.
// Compare every byte's caret, and random ranges including partial UTF-8 and
// graphemes, against it before relying on the cached geometry.
func checkParaGeometryIndex(t *testing.T, v ParaView, rng *rand.Rand) {
	t.Helper()
	raw := v
	raw.carets, raw.groups, raw.stops = nil, nil, nil
	for i := range v.Lines {
		a, b := v.LineRun(i), raw.LineRun(i)
		if !slices.Equal(a.Text, b.Text) {
			t.Fatalf("line %d: indexed text %q, raw %q", i, a.Text, b.Text)
		}
		for off := -1; off <= len(v.Text)+1; off++ {
			if x, want := a.CaretX(off), b.CaretX(off); x != want {
				t.Fatalf("%q line %d: caret %d at %g, raw %g", v.Text, i, off, x, want)
			}
		}
		for x := float32(-20); x < b.Advance+20; x += 7.25 {
			if got, want := a.HitByte(x), b.HitByte(x); got != want {
				t.Fatalf("%q line %d: hit %g at %d, raw %d", v.Text, i, x, got, want)
			}
		}
	}
	for n := 0; n < 50; n++ {
		from, to := rng.Intn(len(v.Text)+4)-2, rng.Intn(len(v.Text)+4)-2
		from, to = min(from, to), max(from, to)
		if got, want := v.RangeRects(from, to, 300, nil), raw.RangeRects(from, to, 300, nil); !slices.Equal(got, want) {
			t.Fatalf("%q [%d,%d): indexed range %v, raw %v", v.Text, from, to, got, want)
		}
	}
}

func TestParaGeometryIndexReference(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f := s.Font(FontDesc{Family: "Helvetica Neue", SizePt: 13, Scale: 2})
	l := NewBasicLayouter(s)
	rng := rand.New(rand.NewSource(42))
	parts := []string{"office ffi ", "שלום ", "لا مرحبا ", "e\u0301 ", "🇬🇧🇧🇬 ", "👩\u200d👩", "क्ष ", "日本語 ", "\r\n", "\xff\x80", "  "}
	for n := 0; n < 100; n++ {
		p := Para{Fonts: []FontKey{f}, Width: float32(60 + rng.Intn(240)), Dir: Dir(n % 3)}
		for j := 0; j < 5; j++ {
			p.Text = append(p.Text, parts[rng.Intn(len(parts))]...)
		}
		if n%3 == 0 {
			p.MaxLines, p.Ellipsis = 1+n%2, n%2 == 0
		}
		checkParaGeometryIndex(t, s.Paragraph(s.Layout(&p)), rng)
		checkParaGeometryIndex(t, l.Paragraph(l.Layout(&p)), rng)
	}
	for _, p := range []ParaView{
		oneLine("office fi", []float32{0, 10, 40, 50, 60, 70}, []uint32{0, 1, 4, 5, 6, 7}, 90, 90, 0),
		oneLine("لا", []float32{0}, []uint32{ClusterRTL}, 20, 20, LineRTL),
		oneLine("aבגb", []float32{-10, 0, 10, 20}, []uint32{0, 3 | ClusterRTL, 1 | ClusterRTL, 5}, 30, 40, LineRTL),
	} {
		checkParaGeometryIndex(t, indexedPara(p), rng)
	}
}

func TestParaGeometryAtClusterBreak(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	rng := rand.New(rand.NewSource(45))
	for _, txt := range []string{"a \u0301b", "a\u0600日b", "a \u200d👩b", "a🇬🇧🇧🇬b", "a क्ष\u094dक्षb"} {
		for w := float32(1); w <= 80; w++ {
			p := Para{Text: []byte(txt), Fonts: []FontKey{0}, Width: w}
			checkParaGeometryIndex(t, l.Paragraph(l.Layout(&p)), rng)
		}
	}
}

func TestParaLineAtReference(t *testing.T) {
	v := ParaView{}
	if v.LineAt(0) != -1 {
		t.Fatal("empty paragraph has a line")
	}
	for i := 0; i < 100; i++ {
		v.Lines = append(v.Lines, Line{Top: float32(i * 20), Height: float32(10 + i%11)})
	}
	ys := []float32{-100, 1e6, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))}
	for _, l := range v.Lines {
		ys = append(ys, l.Top, l.Top+l.Height-0.001, l.Top+l.Height, l.Top+l.Height+0.001)
	}
	for _, y := range ys {
		want := len(v.Lines) - 1
		for i, l := range v.Lines[:len(v.Lines)-1] {
			if y < l.Top+l.Height {
				want = i
				break
			}
		}
		if got := v.LineAt(y); got != want {
			t.Fatalf("line at %g = %d, raw %d", y, got, want)
		}
	}
}

// gridShaper is a deterministic BasicShaper without allocation: one glyph per
// character (its rune), font 0 advancing 10 px, font 1 12 px; right-to-left
// letters (Hebrew) are reversed within a run, as a shaper's bidi does, with
// their clusters descending: each stretch from a Hebrew letter to the last one
// before a Latin letter or the end, spaces included. Runs live in a
// direct-mapped table of fixed slots.
type gridShaper struct {
	slots  [512]gridRun
	nslots int // when positive, only the first nslots slots are used
	gen    uint32
	shapes int
}

type gridRun struct {
	used  bool
	font  FontKey
	tn    int
	txt   [256]byte
	n     int
	g     [256]uint16
	x     [256]float32
	f     [256]FaceID
	cl    [256]uint32
	adv   float32
	order [256]int
}

func (s *gridShaper) Metrics(f FontKey) Metrics {
	if f == 1 {
		return Metrics{Ascent: 12, Descent: 4, LineGap: 2, LineHeight: 18}
	}
	return Metrics{Ascent: 10, Descent: 3, LineGap: 1, LineHeight: 14}
}

func (s *gridShaper) RunGeneration() uint32 { return s.gen }

func (s *gridShaper) Shape(f FontKey, b []byte) RunRef {
	h := hashBytes(b, uint64(f))
	n := len(s.slots)
	if s.nslots > 0 {
		n = s.nslots
	}
	i := int(h % uint64(n))
	r := &s.slots[i]
	if r.used && r.font == f && string(r.txt[:r.tn]) == string(b) {
		return RunRef(i + 1)
	}
	if r.used {
		s.gen++
	}
	s.shapes++
	r.used, r.font, r.n = true, f, 0
	r.tn = copy(r.txt[:], b)
	adv := float32(10)
	if f == 1 {
		adv = 12
	}
	// Characters in logical order, then each right-to-left stretch reversed
	// in place.
	heb := func(c uint16) bool { return c >= 0x0590 && c <= 0x05FF }
	for off := 0; off < r.tn && r.n < len(r.g); {
		c, sz := utf8.DecodeRune(r.txt[off:r.tn])
		r.g[r.n], r.cl[r.n] = uint16(c), uint32(off)
		r.n++
		off += sz
	}
	for i := 0; i < r.n; {
		if !heb(r.g[i]) {
			i++
			continue
		}
		j, last := i, i
		for j < r.n && (heb(r.g[j]) || r.g[j] == ' ') {
			if heb(r.g[j]) {
				last = j
			}
			j++
		}
		for k := i; k <= last; k++ {
			r.cl[k] |= ClusterRTL
		}
		reverse(r.g[i : last+1])
		reverse(r.cl[i : last+1])
		i = last + 1
	}
	for k := 0; k < r.n; k++ {
		r.x[k], r.f[k] = float32(k)*adv, FaceID(f)
	}
	r.adv = float32(r.n) * adv
	return RunRef(i + 1)
}

func (s *gridShaper) Run(ref RunRef) RunView {
	r := &s.slots[ref-1]
	return RunView{Glyphs: r.g[:r.n], X: r.x[:r.n], Faces: r.f[:r.n], Clusters: r.cl[:r.n], Text: r.txt[:r.tn], Advance: r.adv}
}

func lines(v ParaView) []string {
	var out []string
	for _, l := range v.Lines {
		out = append(out, string(v.Text[l.Start:l.End]))
	}
	return out
}

func TestBasicLayoutBreaksLikeWrapText(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	for _, c := range []struct {
		text  string
		width float32
		want  string
	}{
		{"the quick brown fox", 100, `["the quick " "brown fox"]`},
		{"the quick brown fox", 50, `["the " "quick " "brown " "fox"]`},
		{"aaaaaaaaaa bbb", 100, `["aaaaaaaaaa " "bbb"]`},
		{"line one\nline two", 1000, `["line one\n" "line two"]`},
		{"abcdefghijklmnopqrstuvwxyz", 100, `["abcdefghij" "klmnopqrst" "uvwxyz"]`},
		{"a  b", 20, `["a  " "b"]`},
		{"no width\nonly newlines", 0, `["no width\n" "only newlines"]`},
	} {
		v := l.Paragraph(l.Layout(&Para{Text: []byte(c.text), Fonts: []FontKey{0}, Width: c.width}))
		if got := fmt.Sprintf("%q", lines(v)); got != c.want {
			t.Errorf("%q at %v: %s, want %s", c.text, c.width, got, c.want)
		}
		for i, ln := range v.Lines {
			if ln.Height != 14 || ln.Top != float32(i)*14 || ln.Baseline != ln.Top+10 {
				t.Errorf("%q line %d: top %v height %v baseline %v", c.text, i, ln.Top, ln.Height, ln.Baseline)
			}
			if c.width > 0 && ln.Width > c.width {
				t.Errorf("%q line %d is %v wide, over %v", c.text, i, ln.Width, c.width)
			}
		}
	}
}

func TestBasicLayoutStylesFontsAndLineHeights(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	txt := "plain BOLD plain"
	p := Para{Text: []byte(txt), Spans: []Span{{Off: 6, Len: 4, Style: 1, Tag: 7}}, Fonts: []FontKey{0, 1}, Width: 1000}
	v := l.Paragraph(l.Layout(&p))
	if len(v.Lines) != 1 {
		t.Fatalf("lines %q", lines(v))
	}
	ln := v.Lines[0]
	// "plain " 60 + "BOLD" 48 + " plain" 60, font 1's metrics for the line.
	if ln.Width != 168 || ln.Height != 18 || ln.Baseline != 12 || ln.Ascent != 12 || ln.Descent != 4 {
		t.Fatalf("line %+v", ln)
	}
	for g := range v.Glyphs {
		c := int(v.Clusters[g] &^ ClusterRTL)
		inSpan := c >= 6 && c < 10
		if (v.SpanOf[g] == 1) != inSpan || (v.Style(g) == 1) != inSpan || (v.Faces[g] == 1) != inSpan {
			t.Errorf("glyph %d (%q): span %d style %d face %d", g, txt[c], v.SpanOf[g], v.Style(g), v.Faces[g])
		}
	}
	// A fixed line height: every line that high, baseline centred like a
	// label of Fonts[0].
	p.LineHeight = 20
	v = l.Paragraph(l.Layout(&p))
	if ln := v.Lines[0]; ln.Height != 20 || ln.Baseline != (20-13)/2.0+10 {
		t.Fatalf("fixed line height: %+v", ln)
	}
}

func TestSpansAreCutToFit(t *testing.T) {
	p := Para{Text: []byte("0123456789"), Fonts: []FontKey{0}, Spans: []Span{
		{Off: 2, Len: 3, Style: 1}, {Off: 4, Len: 2, Style: 2}, {Off: 8, Len: 9, Style: 3}, {Off: 20, Len: 1, Style: 4},
	}}
	got := styleRuns(nil, &p)
	want := []styleRun{{0, 2, 0, 0}, {2, 5, 1, 1}, {5, 6, 2, 2}, {6, 8, 0, 0}, {8, 10, 3, 3}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("runs %v, want %v", got, want)
	}
}

func TestBasicLayoutRightToLeftParagraphs(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	p := Para{Text: []byte("שלום\nhello\n123 שלום"), Fonts: []FontKey{0}, Width: 1000}
	v := l.Paragraph(l.Layout(&p))
	if len(v.Lines) != 3 {
		t.Fatalf("lines %q", lines(v))
	}
	rtl := []bool{true, false, true} // "123 שלום": digits are weak, the first strong letter is Hebrew
	for i, want := range rtl {
		if got := v.Lines[i].Flags&LineRTL != 0; got != want {
			t.Errorf("line %d %q: rtl %v", i, lines(v)[i], got)
		}
	}
	// Natural alignment: a right-to-left line ends at the right edge.
	if x := v.LineLeft(0, 200); x != 160 {
		t.Errorf("rtl line left %v, want 160", x)
	}
	if x := v.LineLeft(1, 200); x != 0 {
		t.Errorf("ltr line left %v", x)
	}
	p.Dir = DirLTR
	v = l.Paragraph(l.Layout(&p))
	if v.Lines[0].Flags&LineRTL != 0 {
		t.Error("DirLTR left a right-to-left line")
	}
	p.Dir = DirRTL
	v = l.Paragraph(l.Layout(&p))
	if v.Lines[1].Flags&LineRTL == 0 {
		t.Error("DirRTL left a left-to-right line")
	}
}

func TestBasicLayoutMaxLinesAndEllipsis(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	txt := "one two three four five six"
	p := Para{Text: []byte(txt), Fonts: []FontKey{0}, Width: 80, MaxLines: 2}
	v := l.Paragraph(l.Layout(&p))
	if got := fmt.Sprintf("%q", lines(v)); got != `["one two " "three "]` || !v.Truncated {
		t.Fatalf("cut: %s truncated %v", got, v.Truncated)
	}
	p.Ellipsis = true
	v = l.Paragraph(l.Layout(&p))
	last := v.Lines[1]
	// "three fo…": as much as fits 80 px with the ellipsis (10 px).
	if got := string(v.Text[last.Start:last.End]); got != "three f" || last.Flags&LineEllipsis == 0 || last.Width > 80 {
		t.Fatalf("ellipsis line %q flags %b width %v", got, last.Flags, last.Width)
	}
	if g := v.Glyphs[last.G1-1]; g != '…' || v.Clusters[last.G1-1] != last.End {
		t.Fatalf("last glyph %q cluster %d", rune(g), v.Clusters[last.G1-1])
	}
	// No limit reached: no ellipsis.
	p.MaxLines = 9
	if v = l.Paragraph(l.Layout(&p)); v.Truncated || v.Lines[len(v.Lines)-1].Flags&LineEllipsis != 0 {
		t.Fatal("an ellipsis without truncation")
	}
	// A right-to-left line takes the ellipsis on its left.
	p = Para{Text: []byte("שלום שלום שלום שלום"), Fonts: []FontKey{0}, Width: 80, MaxLines: 1, Ellipsis: true}
	v = l.Paragraph(l.Layout(&p))
	ln := v.Lines[0]
	if v.Glyphs[ln.G0] != '…' || v.X[ln.G0] != 0 || ln.Width > 80 {
		t.Fatalf("rtl ellipsis: first glyph %q at %v, width %v", rune(v.Glyphs[ln.G0]), v.X[ln.G0], ln.Width)
	}
}

// The ellipsis cut measures with copies of the shaped runs: shaping the
// line's pieces and the ellipsis evicts the runs it shaped first (a run cache
// of one slot), which must not let it keep text beyond the width.
func TestBasicLayoutEllipsisAfterRunCacheMisses(t *testing.T) {
	sh := &gridShaper{nslots: 1}
	l := NewBasicLayouter(sh)
	for _, c := range []struct {
		maxLines int
		want     string
	}{
		{1, `["one two "]`},
		{2, `["one two " "three f"]`},
	} {
		p := Para{Text: []byte("one two three four five six"), Fonts: []FontKey{0}, Width: 80, MaxLines: c.maxLines, Ellipsis: true}
		g := sh.gen
		v := l.Paragraph(l.Layout(&p))
		if sh.gen == g {
			t.Fatal("no run cache eviction")
		}
		if got := fmt.Sprintf("%q", lines(v)); got != c.want {
			t.Errorf("%d lines: %s, want %s", c.maxLines, got, c.want)
		}
		for _, ln := range v.Lines {
			if ln.Width > 80 {
				t.Errorf("%d lines: line %q is %v wide, over 80", c.maxLines, v.Text[ln.Start:ln.End], ln.Width)
			}
		}
	}
}

// The copies of the shaped input are reserved for the whole paragraph, not
// for the glyph budget of the output: a truncated paragraph shapes all of its
// text and outputs a few glyphs.
func TestParaReservedTruncatedInputOverTheGlyphBudget(t *testing.T) {
	txt := []byte(strings.Repeat("abcd ", 20)) // 100 characters
	for _, ell := range []bool{false, true} {
		p := Para{Text: txt, Fonts: []FontKey{0}, Width: 60, MaxLines: 1, Ellipsis: ell}
		allocFree(t, "a truncated paragraph", func() func() {
			l := NewBasicLayouter(new(gridShaper))
			l.ReserveParas(ParaBudget{Paras: 4, Glyphs: 16, Text: 256})
			return func() {
				if v := l.Paragraph(l.Layout(&p)); len(v.Lines) != 1 || !v.Truncated || len(v.Glyphs) > 16 {
					t.Fatalf("%d lines, truncated %v, %d glyphs", len(v.Lines), v.Truncated, len(v.Glyphs))
				}
			}
		})
	}
}

// The ellipsis takes the font of the last character kept, and the cut leaves
// room for that font's ellipsis, not for the one of the text left out.
func TestBasicLayoutEllipsisFontFromTheCut(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	for _, c := range []struct {
		text  string
		span  Span
		width float32
		want  string
		face  FaceID
		w     float32
	}{
		// Font 1 (12 px) up to the cut, font 0 (10 px) after it: the ellipsis
		// is 12 px, so "ABCDE" (60) and not "ABCDEF" (72), which would make
		// the line 84 px.
		{"ABCDEFG more words", Span{Off: 0, Len: 7, Style: 1}, 82, "ABCDE", 1, 72},
		// Font 1 only in the text left out: the ellipsis is 10 px, and the cut
		// keeps as much as that leaves room for.
		{"abcdefgh ABCD", Span{Off: 9, Len: 4, Style: 1}, 80, "abcdefg", 0, 80},
	} {
		p := Para{Text: []byte(c.text), Spans: []Span{c.span}, Fonts: []FontKey{0, 1}, Width: c.width, MaxLines: 1, Ellipsis: true}
		v := l.Paragraph(l.Layout(&p))
		ln := v.Lines[0]
		last := ln.G1 - 1
		if got := string(v.Text[ln.Start:ln.End]); got != c.want || v.Glyphs[last] != '…' || v.Faces[last] != c.face ||
			ln.Width != c.w || ln.Width > c.width {
			t.Errorf("%q at %v: %q width %v, ellipsis %q in face %d; want %q width %v face %d",
				c.text, c.width, got, ln.Width, rune(v.Glyphs[last]), v.Faces[last], c.want, c.w, c.face)
		}
		if want := uint16(0); c.face == 1 && v.SpanOf[last] != 1 || c.face == 0 && v.SpanOf[last] != want {
			t.Errorf("%q: the ellipsis is in span %d", c.text, v.SpanOf[last])
		}
	}
}

func TestParaGeometry(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	txt := "hello world\nsecond"
	v := l.Paragraph(l.Layout(&Para{Text: []byte(txt), Fonts: []FontKey{0}, Spans: []Span{{Off: 6, Len: 5, Style: 0, Flags: 1, Tag: 3}}, Width: 1000}))
	const w = 300
	for _, c := range []struct {
		x, y float32
		off  int
	}{
		{0, 5, 0}, {14, 5, 1}, {16, 5, 2}, {1000, 5, 11}, // the end of a hard-broken line is before its newline
		{-5, 20, 12}, {25, 20, 14}, {1000, 20, 18}, {1000, 99, 18},
	} {
		if off := v.HitTest(c.x, c.y, w); off != c.off {
			t.Errorf("hit (%v, %v): %d, want %d", c.x, c.y, off, c.off)
		}
	}
	for _, c := range []struct {
		off, line int
		x         float32
	}{{0, 0, 0}, {5, 0, 50}, {11, 0, 110}, {12, 1, 0}, {18, 1, 60}} {
		if line, x := v.Caret(c.off, w); line != c.line || x != c.x {
			t.Errorf("caret of %d: line %d x %v, want %d %v", c.off, line, x, c.line, c.x)
		}
	}
	if s := v.SpanAt(65, 5, w); s != 0 {
		t.Errorf("span at 'world': %d", s)
	}
	if s := v.SpanAt(25, 5, w); s != -1 {
		t.Errorf("span at 'hello': %d", s)
	}
	if s := v.SpanAt(65, 50, w); s != -1 {
		t.Errorf("span below the text: %d", s)
	}
	if got := fmt.Sprint(v.RangeRects(3, 15, w, nil)); got != "[[30 0 110 14] [0 14 30 28]]" {
		t.Errorf("range rects %s", got)
	}
	if i := v.LineOf(12); i != 1 {
		t.Errorf("line of 12: %d", i)
	}
	// A right-to-left run in a left-to-right line: the range of its first
	// two letters is at the run's right end, and a range over the bidi
	// boundary is two rectangles.
	v = l.Paragraph(l.Layout(&Para{Text: []byte("ab אבג cd"), Fonts: []FontKey{0}, Width: 1000}))
	// Visual: a b ' ' ג ב א ' ' c d; א (byte 3) is at x 50, ב (5) at 40.
	if got := fmt.Sprint(v.RangeRects(3, 7, w, nil)); got != "[[40 0 60 14]]" {
		t.Errorf("rtl range %s", got)
	}
	if got := fmt.Sprint(v.RangeRects(0, 5, w, nil)); got != "[[0 0 30 14] [50 0 60 14]]" {
		t.Errorf("bidi range %s", got)
	}
}

// oneLine is a hand-made paragraph of one line: glyph i at x[i] with cluster
// c[i] (ClusterRTL included), the line Width and Advance wide.
func oneLine(txt string, x []float32, c []uint32, width, adv float32, flags uint8) ParaView {
	n := len(x)
	return ParaView{
		Text:     []byte(txt),
		Lines:    []Line{{End: uint32(len(txt)), G1: uint32(n), Height: 14, Baseline: 10, Width: width, Advance: adv, Flags: flags}},
		Glyphs:   make([]uint16, n),
		X:        x,
		Faces:    make([]FaceID, n),
		Clusters: c,
		SpanOf:   make([]uint16, n),
		Width:    width,
		Height:   14,
	}
}

// A ligature stands for several graphemes: a range of part of them covers
// that part of the glyph, split at its caret stops as Caret places them.
func TestParaRangeRectsSplitLigatures(t *testing.T) {
	const r = ClusterRTL
	// "office fi" with an "ffi" and a "fi" ligature (as Hoefler Text sets it).
	v := oneLine("office fi", []float32{0, 10, 40, 50, 60, 70}, []uint32{0, 1, 4, 5, 6, 7}, 90, 90, 0)
	// "لا" (lam alef): one right-to-left glyph for two graphemes.
	la := oneLine("لا", []float32{0}, []uint32{0 | r}, 20, 20, LineRTL)
	for _, c := range []struct {
		v        *ParaView
		from, to int
		want     string
	}{
		{&v, 1, 2, "[[10 0 20 14]]"}, {&v, 2, 3, "[[20 0 30 14]]"}, {&v, 3, 4, "[[30 0 40 14]]"},
		{&v, 2, 5, "[[20 0 50 14]]"}, {&v, 7, 8, "[[70 0 80 14]]"}, {&v, 8, 9, "[[80 0 90 14]]"},
		{&v, 0, 9, "[[0 0 90 14]]"},
		{&la, 0, 2, "[[10 0 20 14]]"}, {&la, 2, 4, "[[0 0 10 14]]"}, {&la, 0, 4, "[[0 0 20 14]]"},
	} {
		w := c.v.Width
		if got := fmt.Sprint(c.v.RangeRects(c.from, c.to, w, nil)); got != c.want {
			t.Errorf("%q [%d, %d): %s, want %s", c.v.Text, c.from, c.to, got, c.want)
		}
		// The edges are the carets of the range's ends.
		_, a := c.v.Caret(c.from, w)
		_, b := c.v.Caret(c.to, w)
		if want := fmt.Sprint([][4]float32{{min(a, b), 0, max(a, b), 14}}); want != c.want {
			t.Errorf("%q [%d, %d): carets at %v and %v", c.v.Text, c.from, c.to, a, b)
		}
	}
}

// A right-to-left line broken after spaces has them hanging left of its
// origin, at negative X: they are hit, covered and caret-placed there, and the
// rightmost cell ends at the line's Width, not at its Advance.
// A combining mark placed left of its pen position (CoreText's e + U+0301:
// the mark at -4) is not hanging white space: the line still runs from 0 to
// its Advance, so the last caret, ranges and hit testing reach its end.
func TestParaMarkLeftOfTheOriginKeepsTheExtent(t *testing.T) {
	v := oneLine("e\u0301x", []float32{0, -4, 10}, []uint32{0, 1, 3}, 20, 20, 0)
	const w = 100
	if x0, x1 := v.LineExtent(0); x0 != 0 || x1 != 20 {
		t.Errorf("extent %v..%v, want 0..20", x0, x1)
	}
	if _, x := v.Caret(4, w); x != 20 {
		t.Errorf("the caret at the end at %v, want 20", x)
	}
	if got := fmt.Sprint(v.RangeRects(0, 4, w, nil)); got != "[[0 0 20 14]]" {
		t.Errorf("the whole line covered by %s", got)
	}
	if g := v.GlyphAt(17, 5, w); g != 2 {
		t.Errorf("glyph at 17: %d, want 2", g)
	}
	if off := v.HitTest(19, 5, w); off != 4 {
		t.Errorf("hit at 19: %d, want 4", off)
	}
}

func TestParaHangingWhiteSpaceLeftOfTheOrigin(t *testing.T) {
	const r = ClusterRTL
	// "אב גד  ": two spaces hang left; visually "  דג בא".
	v := oneLine("אב גד  ", []float32{-20, -10, 0, 10, 20, 30, 40}, []uint32{10 | r, 9 | r, 7 | r, 5 | r, 4 | r, 2 | r, 0 | r}, 50, 70, LineRTL)
	const w = 100 // the line's origin is at 50
	if x0, x1 := v.LineExtent(0); x0 != -20 || x1 != 50 {
		t.Errorf("extent %v..%v, want -20..50", x0, x1)
	}
	for _, c := range []struct {
		x    float32
		want int
	}{{25, -1}, {35, 0}, {45, 1}, {55, 2}, {95, 6}, {99.5, 6}, {100, -1}, {115, -1}} {
		if g := v.GlyphAt(c.x, 5, w); g != c.want {
			t.Errorf("glyph at %v: %d, want %d", c.x, g, c.want)
		}
	}
	for _, c := range []struct {
		from, to int
		want     string
	}{
		{9, 11, "[[30 0 50 14]]"}, {10, 11, "[[30 0 40 14]]"}, {0, 2, "[[90 0 100 14]]"}, {0, 11, "[[30 0 100 14]]"},
	} {
		if got := fmt.Sprint(v.RangeRects(c.from, c.to, w, nil)); got != c.want {
			t.Errorf("[%d, %d): %s, want %s", c.from, c.to, got, c.want)
		}
	}
	// Every caret stop hit-tests back to itself; the end of the line is the
	// hanging spaces' left edge.
	for off := 0; off <= len(v.Text); off = NextGrapheme(v.Text, off) {
		_, x := v.Caret(off, w)
		if got := v.HitTest(x, 5, w); got != off {
			t.Errorf("caret of %d at %v hit-tests to %d", off, x, got)
		}
		if off == len(v.Text) {
			if x != 30 {
				t.Errorf("caret at the end at %v, want 30", x)
			}
			break
		}
	}
}

func TestParaCacheHitsAndInvalidation(t *testing.T) {
	sh := new(gridShaper)
	l := NewBasicLayouter(sh)
	base := func() Para {
		return Para{Text: []byte("a paragraph to cache"), Fonts: []FontKey{0, 1}, Spans: []Span{{Off: 2, Len: 9, Style: 1}}, Width: 100}
	}
	p := base()
	r0 := l.Layout(&p)
	if r := l.Layout(&p); r != r0 || l.ParaStats().Hits != 1 {
		t.Fatalf("the same paragraph laid out again: %v %v, %+v", r, r0, l.ParaStats())
	}
	for name, change := range map[string]func(*Para){
		"text":        func(p *Para) { p.Text = []byte("a paragraph to cachE") },
		"span":        func(p *Para) { p.Spans = []Span{{Off: 2, Len: 8, Style: 1}} },
		"span tag":    func(p *Para) { p.Spans = []Span{{Off: 2, Len: 9, Style: 1, Tag: 1}} },
		"font":        func(p *Para) { p.Fonts = []FontKey{0, 0} },
		"width":       func(p *Para) { p.Width = 101 },
		"dir":         func(p *Para) { p.Dir = DirRTL },
		"max lines":   func(p *Para) { p.MaxLines = 1 },
		"ellipsis":    func(p *Para) { p.Ellipsis = true },
		"line height": func(p *Para) { p.LineHeight = 20 },
	} {
		q := base()
		change(&q)
		m := l.ParaStats().Misses
		if r := l.Layout(&q); r == r0 || l.ParaStats().Misses != m+1 {
			t.Errorf("%s changed: not laid out again", name)
		}
	}
}

func TestParaCacheEvictionKeepsWhatIsInUse(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	l.cache.lim.Paras = 8
	para := func(i int) Para {
		return Para{Text: []byte(fmt.Sprintf("paragraph number %d wraps", i)), Fonts: []FontKey{0}, Width: 120}
	}
	keep := para(0)
	gen := l.ParaGeneration()
	for i := 1; i < 40; i++ {
		l.Layout(&keep) // in use every frame
		p := para(i)
		l.Layout(&p)
	}
	if l.ParaGeneration() == gen {
		t.Fatal("no eviction")
	}
	m := l.ParaStats().Misses
	r := l.Layout(&keep)
	if l.ParaStats().Misses != m {
		t.Fatal("the paragraph in use was evicted")
	}
	if got := fmt.Sprintf("%q", lines(l.Paragraph(r))); got != `["paragraph " "number 0 " "wraps"]` {
		t.Fatalf("after compaction: %s", got)
	}
	checkParaGeometryIndex(t, l.Paragraph(r), rand.New(rand.NewSource(43)))
}

func TestParaLayoutAllocatesNothingWhenReserved(t *testing.T) {
	sh := new(gridShaper)
	l := NewBasicLayouter(sh)
	l.ReserveParas(ParaBudget{Paras: 4096, Glyphs: 1 << 16, Text: 1 << 16})
	txt := make([]byte, 0, 64)
	p := &Para{Fonts: []FontKey{0}, Width: 90}
	n := 0
	allocFree(t, "laying out new paragraphs", func() func() {
		return func() {
			for i := 0; i < 200; i++ {
				n++
				p.Text = append(strconv.AppendInt(append(txt[:0], "message "...), int64(n%300), 10), " with some words"...)
				l.Layout(p)
			}
		}
	})
}

// The cache never holds more than its budget, of any kind: evicting what it
// must, dropping everything when the survivors leave no room, and keeping a
// layout bigger than the whole budget outside it.
func TestParaCacheStaysWithinItsBudget(t *testing.T) {
	sh := new(gridShaper)
	l := NewBasicLayouter(sh)
	l.ReserveParas(ParaBudget{Paras: 8, Glyphs: 150, Text: 200, Lines: 20, Spans: 10, Fonts: 16})
	c := l.cache
	check := func(what string) {
		t.Helper()
		s := &c.slabs
		if len(c.ents) > 8 || len(s.text) > 200 || len(s.glyphs) > 150 || len(s.lines) > 20 || len(s.spans) > 10 || len(s.fonts) > 16 {
			t.Fatalf("%s: the cache holds %d layouts, %d bytes, %d glyphs, %d lines, %d spans, %d fonts",
				what, len(c.ents), len(s.text), len(s.glyphs), len(s.lines), len(s.spans), len(s.fonts))
		}
		for _, sl := range []int{cap(s.text), cap(s.glyphs), cap(s.xs), cap(s.lines), cap(s.spans), cap(s.fonts), cap(c.ents)} {
			if sl > 200 {
				t.Fatalf("%s: a slab grew to %d", what, sl)
			}
		}
		if len(s.groups) > len(s.glyphs) || len(s.stops) > len(s.text)+len(s.lines) || len(s.carets) != len(s.lines) {
			t.Fatalf("%s: geometry slabs exceeded the text/glyph/line budgets", what)
		}
	}
	text := func(n int) []byte {
		b := make([]byte, n)
		for i := range b {
			b[i] = 'a' + byte(i%7)
			if i%9 == 8 {
				b[i] = ' '
			}
		}
		return b
	}
	fonts := make([]FontKey, 12)
	var spans []Span
	for i := 0; i < 6; i++ {
		spans = append(spans, Span{Off: uint32(i * 3), Len: 2, Style: uint16(i % 2)})
	}
	big := Para{Text: text(150), Fonts: []FontKey{0}, Width: 1000}
	// Layouts more than half the budget each, all in use: every insert
	// must make room.
	for i := 0; i < 30; i++ {
		p := Para{Text: text(60 + i%5), Fonts: fonts[:1+i%12], Spans: spans[:i%7], Width: 100}
		l.Layout(&p)
		l.Layout(&big)
		check(fmt.Sprintf("round %d", i))
	}
	// Bigger than the whole budget, by each measure: laid out right, kept
	// outside the cache, found again there.
	many := make([]Span, 11)
	for i := range many {
		many[i] = Span{Off: uint32(i), Len: 1}
	}
	for name, p := range map[string]Para{
		"text":   {Text: text(300), Fonts: []FontKey{0}, Width: 1000},
		"glyphs": {Text: text(160), Fonts: []FontKey{0}, Width: 1000},
		"lines":  {Text: text(100), Fonts: []FontKey{0}, Width: 30},
		"spans":  {Text: text(20), Fonts: []FontKey{0}, Spans: many},
		"fonts":  {Text: text(20), Fonts: make([]FontKey, 17)},
	} {
		gen := l.ParaGeneration()
		r := l.Layout(&p)
		v := l.Paragraph(r)
		check(name)
		if r != overRef || l.ParaGeneration() == gen || len(v.Lines) == 0 || string(v.Text) != string(p.Text) {
			t.Fatalf("%s: ref %v, %d lines, generation %d -> %d", name, r, len(v.Lines), gen, l.ParaGeneration())
		}
		checkParaGeometryIndex(t, v, rand.New(rand.NewSource(44)))
		m := l.ParaStats().Misses
		if r2 := l.Layout(&p); r2 != r || l.ParaStats().Misses != m || l.ParaGeneration() == gen {
			t.Fatalf("%s: laid out again", name)
		}
		if name == "lines" && len(v.Lines) <= 20 {
			t.Fatalf("lines: %d lines", len(v.Lines))
		}
	}
	// What fits is cached as before.
	p := Para{Text: text(30), Fonts: []FontKey{0}, Width: 100}
	r := l.Layout(&p)
	if r == overRef || r == 0 || l.Layout(&p) != r {
		t.Fatalf("a small layout: ref %v", r)
	}
}

// With ReserveParas a fresh layouter lays out new paragraphs, evictions
// included, without allocating, for any paragraph within the budget: many
// lines, many spans and fonts, ellipses, words split between characters.
func TestParaLayoutReservedMissesAllocateNothing(t *testing.T) {
	budget := ParaBudget{Paras: 64, Glyphs: 1 << 15, Text: 1 << 15, Lines: 1 << 13, Spans: 1 << 12, Fonts: 1 << 10}
	var paras []Para
	nl := make([]byte, 0, 6000)
	for i := 0; i < 2500; i++ {
		nl = append(nl, "a\n\n"[:1+i%3]...)
		if i%2 == 0 {
			nl = append(nl, '\n')
		}
	}
	paras = append(paras, Para{Text: nl, Fonts: []FontKey{0}, Width: 200})
	fonts := make([]FontKey, 32)
	for i := range fonts {
		fonts[i] = FontKey(i % 2)
	}
	var styled []byte
	var spans []Span
	for i := 0; i < 300; i++ {
		spans = append(spans, Span{Off: uint32(len(styled)), Len: 4, Style: uint16(i % 32), Tag: uint32(i)})
		styled = append(styled, "word "...)
	}
	paras = append(paras, Para{Text: styled, Spans: spans, Fonts: fonts, Width: 120})
	paras = append(paras, Para{Text: styled, Spans: spans, Fonts: fonts, Width: 120, MaxLines: 3, Ellipsis: true})
	paras = append(paras, Para{Text: []byte("averyveryverylongwordthatmustbesplitbetweencharacters and שלום עולם"), Fonts: []FontKey{0, 1}, Width: 50})
	for i := 0; i < 200; i++ { // enough distinct layouts to evict, again and again
		paras = append(paras, Para{Text: styled[:40+i], Spans: spans[:i%9], Fonts: fonts[:2+i%30], Width: float32(60 + i)})
	}
	allocFree(t, "fresh reserved layouts", func() func() {
		l := NewBasicLayouter(new(gridShaper))
		l.ReserveParas(budget)
		return func() {
			for i := range paras {
				l.Layout(&paras[i])
			}
		}
	})
}

// reservedBytes is the memory a paragraph cache has reserved: the capacity
// of its entries, table and slabs.
func (c *paraCache) reservedBytes() int {
	s := &c.slabs
	return cap(c.ents)*int(unsafe.Sizeof(paraEnt{})) + cap(c.table)*4 + cap(s.text) + cap(s.spans)*int(unsafe.Sizeof(Span{})) +
		cap(s.fonts)*int(unsafe.Sizeof(FontKey(0))) + cap(s.lines)*int(unsafe.Sizeof(Line{})) +
		cap(s.glyphs)*2 + cap(s.xs)*4 + cap(s.faces)*int(unsafe.Sizeof(FaceID(0))) + cap(s.clus)*4 + cap(s.spanOf)*2 +
		cap(s.carets)*int(unsafe.Sizeof(lineCarets{})) + cap(s.groups)*int(unsafe.Sizeof(caretGroup{})) + cap(s.stops)*int(unsafe.Sizeof(caretStop{}))
}

// Without a declared budget the cache keeps to the default one, small enough
// for an empty app; a declared budget replaces it, and the default
// reservation (ReserveRuns) does not shrink a declared one.
func TestParaCacheDefaultBudget(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	if got, want := l.ParaStats().Budget, DefaultParaBudget().clamp(); got != want {
		t.Fatalf("budget %+v, want the default %+v", got, want)
	}
	l.reserveDefault()
	t.Logf("default cache reservation: %d bytes", l.cache.reservedBytes())
	if n := l.cache.reservedBytes(); n > 4<<20 {
		t.Errorf("the default reservation is %.1f MiB", float64(n)/(1<<20))
	}
	big := ParaBudget{Paras: 8192, Glyphs: 1 << 19, Text: 1 << 19}
	l.ReserveParas(big)
	l.reserveDefault()
	if got := l.ParaStats().Budget; got != big.clamp() {
		t.Errorf("after a declared budget and the default: %+v", got)
	}
	// Filling past the default budget evicts rather than grows.
	l = NewBasicLayouter(new(gridShaper))
	l.reserveDefault()
	n := l.cache.reservedBytes()
	txt := make([]byte, 0, 64)
	for i := 0; i < 5000; i++ {
		p := Para{Text: strconv.AppendInt(append(txt[:0], "a message number "...), int64(i), 10), Fonts: []FontKey{0}, Width: 80}
		l.Layout(&p)
	}
	if m := l.cache.reservedBytes(); m != n || l.ParaStats().Paras > DefaultParaBudget().Paras {
		t.Errorf("the cache grew from %d to %d bytes, %d layouts", n, m, l.ParaStats().Paras)
	}
}

func BenchmarkParaLineAt(b *testing.B) {
	for _, n := range []int{256, 4096} {
		v := ParaView{Lines: make([]Line, n)}
		for i := range v.Lines {
			v.Lines[i] = Line{Top: float32(i * 20), Height: 20}
		}
		for _, indexed := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/indexed=%t", n, indexed), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					y := float32((i%n)*20 + 10)
					if indexed {
						_ = v.LineAt(y)
					} else {
						for j := 0; j < n-1; j++ {
							if y < v.Lines[j].Top+v.Lines[j].Height {
								break
							}
						}
					}
				}
			})
		}
	}
}

// The storage of a layout bigger than the budget is released once it leaves
// use, and does not keep the size of a bigger one before it.
func TestParaCacheReleasesOversizeStorage(t *testing.T) {
	l := NewBasicLayouter(new(gridShaper))
	l.ReserveParas(ParaBudget{Paras: 8, Glyphs: 200, Text: 200})
	c := l.cache
	text := func(n int) []byte { return []byte(strings.Repeat("abcdefg h", n/9+1)[:n]) }
	huge := Para{Text: text(5000), Fonts: []FontKey{0}, Width: 1000}
	if l.Layout(&huge) != overRef || cap(c.over.text) < 5000 {
		t.Fatal("not laid out outside the cache")
	}
	// A smaller oversize layout replaces it in storage of its own size.
	big := Para{Text: text(300), Fonts: []FontKey{0}, Width: 1000}
	if l.Layout(&big) != overRef || cap(c.over.text) > 2*300 {
		t.Fatalf("the replacement keeps %d bytes of text storage", cap(c.over.text))
	}
	small := func(i int) Para {
		return Para{Text: []byte(fmt.Sprintf("small %d", i)), Fonts: []FontKey{0}, Width: 1000}
	}
	// In use (looked up between the others): kept, through evictions.
	for i := 0; i < 40; i++ {
		p := small(i)
		l.Layout(&p)
		l.Layout(&big)
	}
	if !c.overOK || c.over.text == nil {
		t.Fatal("released while in use")
	}
	// Out of use: released, with a new generation.
	gen := l.ParaGeneration()
	for i := 100; i < 120; i++ {
		p := small(i)
		l.Layout(&p)
	}
	if c.overOK || c.over.text != nil || l.ParaGeneration() == gen {
		t.Fatalf("not released: ok %v, %d bytes of text storage", c.overOK, cap(c.over.text))
	}
	if v := l.Paragraph(overRef); len(v.Lines) != 0 {
		t.Fatal("the released layout is still viewable")
	}
	// Laid out again on its next use.
	if r := l.Layout(&big); r != overRef || len(l.Paragraph(r).Lines) == 0 {
		t.Fatal("not laid out again")
	}
}

func TestEmojiOnly(t *testing.T) {
	for _, c := range []struct {
		s string
		n int
	}{
		{"👍", 1}, {" 👍 🎉\n", 2}, {"👨‍👩‍👧‍👦", 1}, {"🇧🇷🇯🇵", 2}, {"❤️", 1}, {"1️⃣", 1}, {"👍🏽", 1},
		{"❤", 0}, {"1", 0}, {"ok 👍", 0}, {"", 0}, {"  ", 0}, {"👍!", 0}, {"©", 0},
	} {
		if n := EmojiOnly([]byte(c.s)); n != c.n {
			t.Errorf("EmojiOnly(%q) = %d, want %d", c.s, n, c.n)
		}
	}
}

func TestFirstStrongRTL(t *testing.T) {
	for _, c := range []struct {
		s   string
		rtl bool
	}{
		{"hello", false}, {"שלום", true}, {"مرحبا", true}, {"123 שלום", true}, {"(hi) שלום", false},
		{"  ", false}, {"\u200Fabc", true}, {"\u200Eשלום", false}, {"abc\nשלום", false}, {"日本", false},
		// Bidi classes, not general categories: Devanagari digits are L,
		// Arabic-Indic digits AN (weak), Syriac, Thaana and N'Ko letters AL or
		// R, an unassigned code point of the Hebrew block R, combining marks
		// NSM.
		{"१२३ שלום", false}, {"٣٤ שלום", true}, {"ܫܠܡܐ", true}, {"ދިވެހި", true}, {"ߒߞߏ", true},
		{"\u05FF abc", true}, {"\u0301abc", false}, {"\u05B0\u05D0", true}, {"\u061Cabc", true},
		{"© ® ™ abc", false}, {"𞤀𞤣𞤤𞤢𞤥", true}, {"€5 שלום", true},
		// Rule P2 skips isolates up to their matching PDI, or the end of the
		// paragraph; an unmatched PDI is ignored.
		{"\u2067abc\u2069 שלום", true}, {"\u2066שלום\u2069 abc", false}, {"\u2068שלום\u2069", false},
		{"\u2067abc", false}, {"\u2067\u2066abc\u2069 def\u2069 שלום", true}, {"\u2069שלום", true},
		{"\u2067abc\nשלום", false},
		// Embeddings and overrides are not isolates: their content counts.
		{"\u202Babc\u202C שלום", false}, {"\u202Eשלום\u202C abc", true},
	} {
		if got := FirstStrongRTL([]byte(c.s)); got != c.rtl {
			t.Errorf("FirstStrongRTL(%q) = %v", c.s, got)
		}
	}
}

func BenchmarkRangeRectsLigatures(b *testing.B) {
	for _, groups := range []int{64, 256, 1024} {
		b.Run(fmt.Sprint(groups), func(b *testing.B) {
			txt := []byte(strings.Repeat("ffi ", groups))
			gs := make([]uint16, groups*2)
			xs := make([]float32, len(gs))
			cs := make([]uint32, len(gs))
			for i := 0; i < groups; i++ {
				gs[2*i] = 1
				gs[2*i+1] = 2
				xs[2*i] = float32(i * 40)
				xs[2*i+1] = float32(i*40 + 30)
				cs[2*i] = uint32(i * 4)
				cs[2*i+1] = uint32(i*4 + 3)
			}
			p := ParaView{Text: txt, Glyphs: gs, X: xs, Faces: make([]FaceID, len(gs)), Clusters: cs, Lines: []Line{{End: uint32(len(txt)), G1: uint32(len(gs)), Width: float32(groups * 40), Advance: float32(groups * 40), Height: 20}}}
			p = indexedPara(p)
			dst := make([][4]float32, 0, 1)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst = p.RangeRects(0, len(txt), float32(groups*40), dst[:0])
			}
		})
	}
}

func BenchmarkNativeRangeRects(b *testing.B) {
	for _, n := range []int{64, 256, 1024} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			s, err := New()
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			f := s.Font(FontDesc{Family: "Helvetica Neue", SizePt: 13, Scale: 2})
			p := Para{Text: []byte(strings.Repeat("ffi ", n)), Fonts: []FontKey{f}, Width: 1e7}
			v := s.Paragraph(s.Layout(&p))
			dst := make([][4]float32, 0, 1)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dst = v.RangeRects(0, len(p.Text), p.Width, dst[:0])
			}
		})
	}
}
