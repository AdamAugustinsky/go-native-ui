package text

import (
	"fmt"
	"strconv"
	"testing"
	"unicode/utf8"
)

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
	l.cache.maxParas = 8
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
}

func TestParaLayoutAllocatesNothingWhenReserved(t *testing.T) {
	sh := new(gridShaper)
	l := NewBasicLayouter(sh)
	l.ReserveParas(4096, 1<<16, 1<<16)
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
