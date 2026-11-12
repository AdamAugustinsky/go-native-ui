package ui

import (
	"fmt"
	"strings"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
)

// drawnText is the text the glyph quads of the frame spell, in drawing order:
// the fake shapers' glyph id is the rune, and its atlas place says which.
func drawnText(qs []gpu.Quad) string {
	var rs []rune
	for _, q := range qs {
		if q.Rect[2]-q.Rect[0] != 6 || q.UV[2]-q.UV[0] != 6 { // a glyph of the fakes: 6×8
			continue
		}
		rs = append(rs, rune(int(q.UV[1])/10*100+int(q.UV[0])/8))
	}
	return string(rs)
}

func newCutHarness(t *testing.T) *harness {
	sh := newEditShaper()
	h := &harness{t: t, c: New(sh), sh: sh.fakeShaper, w: 400, h: 100}
	h.in.Inside, h.in.Focused = true, true
	return h
}

// A line of text in a box narrower than it ends in "…" without any flag, and
// nothing of it is drawn past the box (D105). Before, it drew at full width
// over whatever was beside the box.
func TestTextIsCutWithAnEllipsisByDefault(t *testing.T) {
	h := newCutHarness(t)
	h.frame(func(c *Context) {
		c.NextSize(Px(60), Px(20))
		c.Box(DrawText, "hello world")
	})
	qs := quadsOf(h)
	if got := drawnText(qs); got != "hello…" {
		t.Fatalf("drew %q, want %q", got, "hello…")
	}
	for _, q := range qs {
		if q.Rect[0] >= 60 || q.Clip[2] > 60 {
			t.Fatalf("quad %v (clip %v) past the box's end at 60", q.Rect, q.Clip)
		}
	}
}

// A row 40 pt too narrow for an icon, a label and a badge: the icon and the
// badge keep their size and the label gives the 40 pt, ending in "…". Before,
// nothing in the row could shrink (text is strictness 1), so the badge was
// laid out past the row's end and the label kept its full width.
func TestRowGivesWayWithItsTextFirst(t *testing.T) {
	h := newCutHarness(t)
	var row, icon, label, badge ID
	h.frame(func(c *Context) {
		c.NextSize(Px(100), Px(20))
		c.NextAxis(AxisX)
		row = c.Open(0, "row")
		c.NextSize(Px(20), Px(20))
		icon = c.Box(0, "icon")
		label = c.Box(DrawText, "hello world") // 88 px of text
		c.NextSize(Px(32), Px(20))
		badge = c.Box(0, "badge")
		c.Close()
	})
	r := func(id ID) [4]float32 { return h.c.boxes[id].rect }
	if w := r(icon)[2] - r(icon)[0]; w != 20 {
		t.Errorf("icon %v px wide, want 20", w)
	}
	if w := r(badge)[2] - r(badge)[0]; w != 32 {
		t.Errorf("badge %v px wide, want 32", w)
	}
	if w := r(label)[2] - r(label)[0]; w != 48 {
		t.Errorf("label %v px wide, want 48 (88 less the 40 the row lacks)", w)
	}
	if r(badge)[2] > r(row)[2] {
		t.Errorf("badge ends at %v, past the row's end %v", r(badge)[2], r(row)[2])
	}
	if got := drawnText(quadsOf(h)); got != "hello…" {
		t.Errorf("label drew %q, want %q", got, "hello…")
	}
}

// Across a column, a label wider than the column is cut to it rather than
// running past the column's edge.
func TestLabelWiderThanItsColumnIsCut(t *testing.T) {
	h := newCutHarness(t)
	var col, label ID
	h.frame(func(c *Context) {
		c.NextSize(Px(60), Px(40))
		col = c.Open(0, "col")
		label = c.Label("hello world")
		c.Close()
	})
	if lr, cr := h.c.boxes[label].rect, h.c.boxes[col].rect; lr[2] != cr[2] {
		t.Errorf("label ends at %v, want the column's end %v", lr[2], cr[2])
	}
	if got := drawnText(quadsOf(h)); got != "hello…" {
		t.Errorf("drew %q, want %q", got, "hello…")
	}
}

// Each kind of cut, on 8 px a rune and an 8 px "…".
func TestTextCutKinds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cut   TextCut
		flags Flags
		w     float32
		text  string
		want  string
	}{
		{"tail", CutTail, 0, 60, "hello world", "hello…"},
		{"head drops the space after the cut", CutHead, 0, 60, "hello world", "…world"},
		{"middle", CutMiddle, 0, 96, "quarterly-report.pdf", "quarte…t.pdf"},
		{"middle keeps the extension whole", CutMiddle, 0, 56, "quarterly-report.pdf", "qu….pdf"},
		{"middle without an extension halves", CutMiddle, 0, 72, "abcdefghijklmnop", "abcd…mnop"},
		{"nothing but the ellipsis fits", CutTail, 0, 12, "hello world", "…"},
		{"a mark stays with its letter", CutTail, 0, 30, "ééééé", "éé…"},
		{"none draws it all", CutNone, 0, 60, "hello world", "helloworld"},
		{"clip cuts at the box", CutClip, 0, 60, "hello world", "hellowo"},
		{"fade cuts at the box", CutFade, 0, 60, "hello world", "hellowo"},
		{"the old flag wins over the style", CutHead, EllipsisOverflowX, 60, "hello world", "hello…"},
		{"the old fade flag wins", CutTail, FadeOverflowX, 60, "hello world", "hellowo"},
		{"text that fits is whole", CutTail, 0, 100, "hello world", "helloworld"},
		{"one grapheme is drawn whole (a symbol in a square)", CutTail, 0, 4, "▤", "▤"},
		{"one grapheme with a mark", CutTail, 0, 4, "e\u0301", "e\u0301"},
	} {
		h := newCutHarness(t)
		h.frame(func(c *Context) {
			c.NextSize(Px(tc.w), Px(20))
			c.NextTextCut(tc.cut)
			c.Box(DrawText|tc.flags, tc.text)
		})
		qs := quadsOf(h)
		if got := drawnText(qs); got != tc.want {
			t.Errorf("%s: drew %q, want %q", tc.name, got, tc.want)
		}
		faded := false
		for _, q := range qs {
			faded = faded || q.FadeX != [2]float32{}
		}
		if want := tc.cut == CutFade && tc.flags == 0 || tc.flags == FadeOverflowX; faded != want {
			t.Errorf("%s: faded %v, want %v", tc.name, faded, want)
		}
	}
}

// A cut line keeps its box's alignment: right-aligned text ends at the box's
// end, centred text is centred.
func TestCutTextKeepsItsAlignment(t *testing.T) {
	for _, tc := range []struct {
		align Align
		x0    float32 // where "h" is drawn (its bearing is 1)
	}{{AlignLeft, 1}, {AlignCenter, 1 + 2}, {AlignRight, 1 + 4}} {
		h := newCutHarness(t)
		h.frame(func(c *Context) {
			c.NextSize(Px(52), Px(20)) // "hello…" is 48
			c.NextAlign(tc.align)
			c.Box(DrawText, "hello world")
		})
		if q := quadsOf(h)[0]; q.Rect[0] != tc.x0 {
			t.Errorf("align %d: first glyph at %v, want %v", tc.align, q.Rect[0], tc.x0)
		}
	}
}

// PushTextCut sets the cut for a subtree; CutNone keeps a label at its text's
// width in a row too small for it, as before D105.
func TestPushTextCutAndNoneKeepsTheWidth(t *testing.T) {
	h := newCutHarness(t)
	var label ID
	h.frame(func(c *Context) {
		c.PushTextCut(CutNone)
		c.NextSize(Px(60), Px(20))
		c.NextAxis(AxisX)
		c.Open(0, "row")
		label = c.Label("hello world")
		c.Close()
		c.PopTextCut()
	})
	if r := h.c.boxes[label].rect; r[2]-r[0] != 88 {
		t.Errorf("CutNone label %v px wide, want its text's 88", r[2]-r[0])
	}
	if got := drawnText(quadsOf(h)); got != "helloworld" {
		t.Errorf("drew %q", got)
	}
}

// Truncated says whether the last layout cut the box's text: a line wider than
// its box, or rich text cut short by MaxLines, which now ends in "…" too.
func TestTruncatedReportsTheCut(t *testing.T) {
	h := newCutHarness(t)
	var long, short, rich, richAll ID
	build := func(c *Context) {
		c.NextSize(Px(60), Px(20))
		long = c.Label("hello world")
		c.NextSize(Px(60), Px(20))
		short = c.Label("hello")
		c.NextWidth(Px(40))
		rich = c.RichText(1, []byte("one two three four five six"), nil, nil, RichOpts{MaxLines: 2}).ID
		c.NextWidth(Px(40))
		richAll = c.RichText(2, []byte("one two"), nil, nil, RichOpts{MaxLines: 2}).ID
	}
	h.frame(build)
	if !h.c.Truncated(long) || h.c.Truncated(short) {
		t.Errorf("Truncated: long %v, short %v", h.c.Truncated(long), h.c.Truncated(short))
	}
	if !h.c.Truncated(rich) || h.c.Truncated(richAll) {
		t.Errorf("Truncated: rich cut by MaxLines %v, rich that fits %v", h.c.Truncated(rich), h.c.Truncated(richAll))
	}
	if !strings.HasSuffix(drawnText(quadsOf(h)), "…onetwo") {
		t.Errorf("rich text cut by MaxLines does not end in …: %q", drawnText(quadsOf(h)))
	}
	// read during the next build: the last layout's
	var during bool
	h.frame(func(c *Context) { build(c); during = c.Truncated(long) })
	if !during {
		t.Error("Truncated during a build does not report the last layout")
	}
}

// clusterShaper is the fakeShaper's metrics and glyphs with runs that carry
// clusters and text, kept without eviction (as a reserved text.System keeps a
// working set): what the cut needs, for the allocation gate and the bench.
type clusterShaper struct {
	*fakeShaper
	runs  []clusterRun
	index map[string]int
	buf   []byte
}

type clusterRun struct {
	g     []uint16
	x     []float32
	faces []text.FaceID
	cl    []uint32
	txt   []byte
	adv   float32
}

func newClusterShaper() *clusterShaper {
	return &clusterShaper{fakeShaper: newFake(), runs: make([]clusterRun, 1, 4096), index: make(map[string]int, 4096)}
}

func (s *clusterShaper) Shape(k text.FontKey, b []byte) text.RunRef {
	s.buf = append(append(s.buf[:0], byte(k)), b...)
	if i, ok := s.index[string(s.buf)]; ok {
		return text.RunRef(i)
	}
	var r clusterRun
	r.txt = append([]byte(nil), b...)
	adv := s.adv(k)
	for off, ru := range string(b) {
		r.g = append(r.g, uint16(ru))
		r.x = append(r.x, r.adv)
		r.faces = append(r.faces, 0)
		r.cl = append(r.cl, uint32(off))
		r.adv += adv
	}
	s.runs = append(s.runs, r)
	s.index[string(s.buf)] = len(s.runs) - 1
	return text.RunRef(len(s.runs) - 1)
}

func (s *clusterShaper) Run(ref text.RunRef) text.RunView {
	r := &s.runs[ref]
	return text.RunView{Glyphs: r.g, X: r.x, Faces: r.faces, Clusters: r.cl, Text: r.txt, Advance: r.adv}
}

func (s *clusterShaper) RunGeneration() uint32 { return 0 }

// cutList is a list of n rows of 34 characters (272 px) in boxes w px wide.
func cutList(c *Context, n int, w float32) {
	c.Begin(&Input{Inside: true, Focused: true}, 800, 600, 1, 1.0/60)
	c.NextSize(Px(w), Px(600))
	c.NextAxis(AxisY)
	c.Open(Clip, "list")
	for r := 0; r < n; r++ {
		c.NextSize(Pct(1), Px(1))
		c.BoxKeyed(DrawText, KeyN(c.ParentKey(), uint64(r)), c.Fmt().Str("Row ").Int(1000+r).Str(" - a fairly long title").Bytes())
	}
	c.Close()
}

// Cutting 500 rows allocates nothing once warm, also in frames that cut them
// again at another width.
func TestCutTextAllocatesNothing(t *testing.T) {
	sh := newClusterShaper()
	c := New(sh)
	c.Reserve(1024)
	var list gpu.DrawList
	list.Quads = make([]gpu.Quad, 0, 1<<15)
	widths := [...]float32{120, 132, 96}
	f := 0
	step := func() {
		cutList(c, 500, widths[f%len(widths)])
		c.End(&list)
		f++
	}
	for i := 0; i < 30; i++ {
		step()
	}
	if got := drawnText(list.Quads[:20]); !strings.Contains(got, "…") {
		t.Fatalf("rows are not cut: %q", got)
	}
	if a := testing.AllocsPerRun(60, step); a != 0 {
		t.Errorf("%v allocations a frame cutting 500 rows", a)
	}
}

// 500 rows cut at the box (the cut cached: a frame that changes no text and
// no width), cut again every frame (a live resize: the width alternates), and
// the same rows wide enough for their text.
func BenchmarkCutRows500(b *testing.B) {
	for _, bc := range []struct {
		name   string
		w, alt float32
	}{{"cut", 120, 120}, {"recut", 120, 121}, {"whole", 400, 400}} {
		b.Run(bc.name, func(b *testing.B) {
			c := New(newClusterShaper())
			c.Reserve(1024)
			var list gpu.DrawList
			list.Quads = make([]gpu.Quad, 0, 1<<15)
			for i := 0; i < 10; i++ {
				cutList(c, 500, bc.w)
				c.End(&list)
				cutList(c, 500, bc.alt)
				c.End(&list)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				w := bc.w
				if i%2 == 1 {
					w = bc.alt
				}
				cutList(c, 500, w)
				c.End(&list)
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/500, "ns/row")
		})
	}
}

// NextScrollFade fades the end of a sideways scroller only while its content
// goes on past that edge.
func TestScrollFadeOnlyWhileMoreFollows(t *testing.T) {
	h := newCutHarness(t)
	var sc ID
	build := func(c *Context) {
		c.NextSize(Px(100), Px(20))
		c.NextAxis(AxisX)
		c.NextScrollFade(20)
		sc = c.Open(ScrollX|Clip, "strip")
		for i := 0; i < 3; i++ {
			c.NextSize(Px(100), Px(20))
			c.NextBG(0xFFFFFFFF)
			c.Box(DrawBackground, fmt.Sprint("cell", i))
		}
		c.Close()
	}
	faded := func() bool {
		for _, q := range quadsOf(h) {
			if q.FadeX != [2]float32{} {
				return q.FadeX == [2]float32{80, 100}
			}
		}
		return false
	}
	h.frame(build)
	h.frame(build)
	if !faded() {
		t.Error("no fade at the end while the content goes on past it")
	}
	h.frame(func(c *Context) { build(c); c.SetScroll(sc, 200, 0) })
	h.frame(build)
	if faded() {
		t.Error("a fade at the end with nothing past it")
	}
}
