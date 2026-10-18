package ui

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"gonativeui/gpu"
)

// The edit shaper advances 8 px per character in font 0 and the line height is
// 14 px (ascent 10, descent 3, 1 px gap), so a width of 80 holds ten characters.

func newWrapHarness(t *testing.T, w, h int) *harness {
	sh := newEditShaper()
	hs := &harness{t: t, c: New(sh), sh: sh.fakeShaper, w: w, h: h}
	hs.in.Inside, hs.in.Focused = true, true
	hs.in.MouseX, hs.in.MouseY = -100, -100
	return hs
}

// ink groups the glyph quads of the last frame by line (their y) and returns,
// per line from the top, the x of its first glyph and its number of glyphs.
func ink(list *gpu.DrawList) (lines [][2]float32) {
	rows := map[float32][]float32{}
	for _, q := range list.Quads {
		if q.Mode == gpu.ModeMaskR8 {
			rows[q.Rect[1]] = append(rows[q.Rect[1]], q.Rect[0])
		}
	}
	var ys []float32
	for y := range rows {
		ys = append(ys, y)
	}
	sort.Slice(ys, func(i, j int) bool { return ys[i] < ys[j] })
	for _, y := range ys {
		xs := rows[y]
		sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
		lines = append(lines, [2]float32{xs[0], float32(len(xs))})
	}
	return lines
}

// wrapped builds a wrapped label of the given width and returns its box size.
func wrapped(h *harness, text string, width float32, pad float32) (w, hgt float32) {
	h.frame(func(c *Context) {
		c.NextWidth(Px(width))
		c.NextHeight(TextSize(0))
		c.NextPad(pad)
		id := c.Box(DrawText|WrapText, text)
		r := c.Rect(id)
		w, hgt = r[2]-r[0], r[3]-r[1]
	})
	h.frame(func(c *Context) {
		c.NextWidth(Px(width))
		c.NextHeight(TextSize(0))
		c.NextPad(pad)
		id := c.Box(DrawText|WrapText, text)
		r := c.Rect(id)
		w, hgt = r[2]-r[0], r[3]-r[1]
	})
	return
}

func TestWrapBreaksAtTheWidth(t *testing.T) {
	cases := []struct {
		text  string
		width float32
		lines []float32 // glyphs (non-space characters) per line
	}{
		{"the quick brown fox", 80, []float32{8, 8}},             // "the quick" / "brown fox"
		{"the quick brown fox", 40, []float32{3, 5, 5, 3}},       // "the" / "quick" / "brown" / "fox"
		{"aaaaaaaaaa bbb", 80, []float32{10, 3}},                 // the space after ten characters hangs outside the width
		{"line one\nline two", 800, []float32{7, 7}},             // a newline always ends the line
		{"abcdefghijklmnopqrstuvwxyz", 80, []float32{10, 10, 6}}, // one word wider than the box splits between characters
		{"short", 800, []float32{5}},                             // fits: one line
		{"a  b", 16, []float32{1, 1}},                            // spaces hang, they do not make lines of their own
	}
	for _, c := range cases {
		h := newWrapHarness(t, 900, 600)
		_, hgt := wrapped(h, c.text, c.width, 0)
		got := ink(&h.list)
		var counts []float32
		for _, l := range got {
			counts = append(counts, l[1])
		}
		if fmt.Sprint(counts) != fmt.Sprint(c.lines) {
			t.Errorf("%q in %v px: glyphs per line %v, want %v", c.text, c.width, counts, c.lines)
		}
		if want := 14 * float32(len(c.lines)); hgt != want {
			t.Errorf("%q in %v px: height %v, want %v (14 per line)", c.text, c.width, hgt, want)
		}
	}
}

func TestWrapHeightIncludesPaddingAndGrowsParents(t *testing.T) {
	h := newWrapHarness(t, 900, 600)
	var panel, label [4]float32
	build := func(c *Context) {
		c.NextWidth(Px(100))
		c.NextHeight(Children(1))
		c.NextPad(10)
		p := c.Open(DrawBackground, "panel")
		c.NextWidth(Pct(1)) // 80 px of text width inside the padding
		c.NextHeight(TextSize(0))
		l := c.Box(DrawText|WrapText, "the quick brown fox jumps")
		panel, label = c.Rect(p), c.Rect(l)
		c.Close()
	}
	h.frame(build)
	h.frame(build)
	// 80 px: "the quick" / "brown fox" / "jumps": three lines.
	if label[3]-label[1] != 42 || panel[3]-panel[1] != 62 {
		t.Fatalf("label %v panel %v: want 42 high in a panel of 42 plus 20 padding", label, panel)
	}
}

func TestWrapRewrapsWhenTheWidthOrTextChanges(t *testing.T) {
	h := newWrapHarness(t, 900, 600)
	_, h1 := wrapped(h, "the quick brown fox", 160, 0)
	_, h2 := wrapped(h, "the quick brown fox", 40, 0)
	_, h3 := wrapped(h, "the quick brown fox", 160, 0)
	if h1 != 14 || h2 != 56 || h3 != 14 {
		t.Fatalf("heights %v %v %v, want 14 56 14", h1, h2, h3)
	}
	_, h4 := wrapped(h, "the quick brown fox and the lazy dog", 160, 0)
	if h4 != 28 { // "the quick brown fox" / "and the lazy dog"
		t.Fatalf("new text: height %v, want 28", h4)
	}
}

func TestWrapAlignment(t *testing.T) {
	for _, c := range []struct {
		align Align
		x     [2]float32 // first glyph x of the two lines (ink starts 1 px after the pen)
	}{
		{AlignLeft, [2]float32{1, 1}},
		{AlignCenter, [2]float32{1 + (80-48)/2, 1 + (80-24)/2}}, // "abcdef" is 48 px, "abc" 24 px
		{AlignRight, [2]float32{1 + 80 - 48, 1 + 80 - 24}},
	} {
		h := newWrapHarness(t, 900, 600)
		build := func(cx *Context) {
			cx.NextWidth(Px(80))
			cx.NextHeight(TextSize(0))
			cx.NextAlign(c.align)
			cx.Box(DrawText|WrapText, "abcdef\nabc")
		}
		h.frame(build)
		h.frame(build)
		got := ink(&h.list)
		if len(got) != 2 || got[0][0] != c.x[0] || got[1][0] != c.x[1] {
			t.Errorf("align %d: lines %v, want first glyphs at %v", c.align, got, c.x)
		}
	}
}

func TestWrapAutoWidthFillsTheParentAndZeroWidthDoesNotCrumble(t *testing.T) {
	h := newWrapHarness(t, 900, 600)
	var lab [4]float32
	build := func(c *Context) {
		c.NextWidth(Px(80))
		c.NextHeight(Children(1))
		c.Open(0, "col")
		c.NextHeight(TextSize(0)) // width left on auto: the width of the column
		lab = c.Rect(c.Box(DrawText|WrapText, "the quick brown fox"))
		c.Close()
	}
	h.frame(build)
	h.frame(build)
	if lab[2]-lab[0] != 80 || lab[3]-lab[1] != 28 {
		t.Fatalf("label %v: want 80 wide, two lines", lab)
	}
	// A box with no width to wrap in (its parent sums its children) shows one
	// line per newline, not one character per line.
	h2 := newWrapHarness(t, 900, 600)
	build2 := func(c *Context) {
		c.NextWidth(Children(1))
		c.NextHeight(Children(1))
		c.Open(0, "col")
		c.NextHeight(TextSize(0))
		lab = c.Rect(c.Box(DrawText|WrapText, "the quick\nbrown fox"))
		c.Close()
	}
	h2.frame(build2)
	h2.frame(build2)
	if lab[3]-lab[1] != 28 {
		t.Fatalf("label %v: want two lines", lab)
	}
}

func TestWrapCachesLinesWhileNothingChanges(t *testing.T) {
	h := newWrapHarness(t, 900, 600)
	build := func(c *Context) {
		c.NextWidth(Px(80))
		c.NextHeight(TextSize(0))
		c.Box(DrawText|WrapText, "the quick brown fox jumps over the lazy dog")
		c.NextWidth(Px(120))
		c.NextHeight(TextSize(0))
		c.Box(DrawText|WrapText, "second paragraph")
	}
	h.frame(build)
	n := h.c.wrapRuns
	if n != 2 {
		t.Fatalf("%d computations in the first frame, want 2", n)
	}
	for i := 0; i < 5; i++ {
		h.frame(build)
	}
	if h.c.wrapRuns != n {
		t.Fatalf("lines recomputed %d times although nothing changed", h.c.wrapRuns-n)
	}
	first := ink(&h.list)
	// One paragraph changes: only that one is broken again.
	h.frame(func(c *Context) {
		c.NextWidth(Px(80))
		c.NextHeight(TextSize(0))
		c.Box(DrawText|WrapText, "the quick brown fox jumps over the lazy dog")
		c.NextWidth(Px(120))
		c.NextHeight(TextSize(0))
		c.Box(DrawText|WrapText, "second paragraph, changed")
	})
	if h.c.wrapRuns != n+1 {
		t.Fatalf("%d computations after one change, want 1", h.c.wrapRuns-n)
	}
	// A cached frame draws exactly what a computed one does.
	h2 := newWrapHarness(t, 900, 600)
	h2.frame(build)
	h2.frame(build)
	for i := 0; i < 3; i++ {
		h.frame(build)
	}
	if got, want := fmt.Sprint(ink(&h.list)), fmt.Sprint(first); got != want {
		t.Fatalf("cached lines %v differ from computed %v", got, want)
	}
	if got, want := fmt.Sprint(ink(&h.list)), fmt.Sprint(ink(&h2.list)); got != want {
		t.Fatalf("different drawing: %v / %v", got, want)
	}
}

func TestWrapAllocatesNothingWhenWarm(t *testing.T) {
	h := newWrapHarness(t, 900, 600)
	long := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 6)
	w := float32(200)
	flip := 0
	build := func(c *Context) {
		for i := 0; i < 4; i++ {
			c.NextWidth(Px(w))
			c.NextHeight(TextSize(0))
			c.Box(DrawText|WrapText, c.Fmt().Str(long).Int(i).String())
		}
	}
	frame := func() {
		flip++
		if flip%3 == 0 { // the width changes now and then: the lines are broken again
			w = 200 + float32(flip%2)*100
		}
		h.frame(build)
	}
	for i := 0; i < 40; i++ {
		frame()
	}
	if n := testing.AllocsPerRun(60, frame); n != 0 {
		t.Fatalf("%v allocations per frame with wrapped text", n)
	}
	if len(ink(&h.list)) < 8 {
		t.Fatal("nothing drawn")
	}
}
