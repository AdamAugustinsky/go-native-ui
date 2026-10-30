package text

import (
	"math"
	"math/rand"
	"strconv"
	"testing"
)

var benchmarkCaretXResult float32

func checkCaretIndex(t *testing.T, v RunView) {
	t.Helper()
	var c CaretIndex
	c.Build(v)
	indexed := v
	indexed.carets = c.data
	for off := -1; off <= len(v.Text)+1; off++ {
		want, got := v.CaretX(off), c.CaretX(off)
		if got != want && !(math.IsNaN(float64(got)) && math.IsNaN(float64(want))) {
			t.Fatalf("%q clusters %v: indexed caret at %d = %v, raw %v", v.Text, v.Clusters, off, got, want)
		}
		if got := indexed.CaretX(off); got != want && !math.IsNaN(float64(want)) {
			t.Fatalf("indexed view caret at %d = %v, raw %v", off, got, want)
		}
	}
	for x := float32(-15); x < v.Advance+15; x += 1.25 {
		if got, want := indexed.HitByte(x), v.HitByte(x); got != want {
			t.Fatalf("%q clusters %v: indexed hit at %v = %d, raw %d", v.Text, v.Clusters, x, got, want)
		}
	}
	if a := testing.AllocsPerRun(10, func() { c.Build(v); _ = c.CaretX(len(v.Text) / 2) }); a != 0 {
		t.Fatalf("warmed index: %v allocations", a)
	}
}

// Includes nonadjacent groups with the same logical start, marks left of the
// origin, missing glyphs, malformed UTF-8 and offsets inside graphemes.
func TestCaretIndexReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	parts := []string{"a", "ffi", " ", "אב", "لا", "e\u0301", "🇬🇧", "👩\u200d👩", "क्ष", "\r\n", "\xff\x80"}
	for trial := 0; trial < 600; trial++ {
		var txt string
		for n := 1 + rng.Intn(6); n > 0; n-- {
			txt += parts[rng.Intn(len(parts))]
		}
		n := rng.Intn(16)
		cl, xs := make([]uint32, n), make([]float32, n)
		pen := float32(-5)
		for i := range cl {
			cl[i] = uint32(rng.Intn(len(txt) + 1))
			if rng.Intn(2) == 0 {
				cl[i] |= ClusterRTL
			}
			xs[i] = pen - float32(rng.Intn(12))
			pen += 8
		}
		if trial%10 == 0 {
			cl = nil
		}
		checkCaretIndex(t, view(txt, n, cl, xs, pen+5))
	}
}

func BenchmarkCaretIndex(b *testing.B) {
	for _, n := range []int{100, 1000} {
		v := longView(n)
		var c CaretIndex
		c.Build(v)
		b.Run(strconv.Itoa(n)+" glyphs", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkCaretXResult = c.CaretX(n / 2)
			}
		})
	}
}

// Hand-built views check the caret model itself, independent of any shaper:
// the geometry of ligatures, marks and bidi runs.

func view(text string, glyphs int, cl []uint32, x []float32, adv float32) RunView {
	return RunView{Glyphs: make([]uint16, glyphs), X: x, Faces: make([]FaceID, glyphs), Clusters: cl, Text: []byte(text), Advance: adv}
}

func TestCaretLTR(t *testing.T) {
	v := view("abc", 3, []uint32{0, 1, 2}, []float32{0, 10, 20}, 30)
	for off, want := range []float32{0, 10, 20, 30} {
		if got := v.CaretX(off); got != want {
			t.Errorf("CaretX(%d) = %v, want %v", off, got, want)
		}
	}
	for x, want := range map[float32]int{-5: 0, 4: 0, 6: 1, 14: 1, 16: 2, 26: 3, 99: 3} {
		if got := v.HitByte(x); got != want {
			t.Errorf("HitByte(%v) = %d, want %d", x, got, want)
		}
	}
	if v.CaretX(-1) != 0 || v.CaretX(99) != 30 {
		t.Error("offsets are clamped to the text")
	}
}

func TestCaretLigature(t *testing.T) {
	// "affib": the "ffi" ligature is one glyph for three characters.
	v := view("affib", 3, []uint32{0, 1, 4}, []float32{0, 10, 40}, 50)
	// a [0,10], ffi [10,40] in thirds, b [40,50]
	for off, want := range map[int]float32{0: 0, 1: 10, 2: 20, 3: 30, 4: 40, 5: 50} {
		if got := v.CaretX(off); got != want {
			t.Errorf("CaretX(%d) = %v, want %v", off, got, want)
		}
	}
	for x, want := range map[float32]int{12: 1, 18: 2, 24: 2, 27: 3, 36: 4} {
		if got := v.HitByte(x); got != want {
			t.Errorf("HitByte(%v) = %d, want %d", x, got, want)
		}
	}
}

func TestCaretMarks(t *testing.T) {
	// e + U+0301 as CoreText reports them: two glyphs, string indices 0 and
	// 1, the mark placed left of its (zero-width) cell; then "x".
	v := view("e\u0301x", 3, []uint32{0, 1, 3}, []float32{0, -4, 10}, 20)
	if v.CaretX(0) != 0 || v.CaretX(3) != 10 || v.CaretX(4) != 20 {
		t.Errorf("stops at %v %v %v, want 0 10 20", v.CaretX(0), v.CaretX(3), v.CaretX(4))
	}
	if v.CaretX(1) != 0 || v.CaretX(2) != 0 {
		t.Errorf("offsets inside the cluster: %v %v, want 0", v.CaretX(1), v.CaretX(2))
	}
	for x := float32(-3); x < 25; x++ {
		if o := v.HitByte(x); o != 0 && o != 3 && o != 4 {
			t.Fatalf("HitByte(%v) = %d, inside a grapheme", x, o)
		}
	}
}

func TestCaretRTL(t *testing.T) {
	// Three right-to-left characters of 2 bytes: glyphs in visual order carry
	// the clusters 4, 2, 0.
	r := uint32(ClusterRTL)
	v := view("אבג", 3, []uint32{4 | r, 2 | r, 0 | r}, []float32{0, 10, 20}, 30)
	for off, want := range map[int]float32{0: 30, 2: 20, 4: 10, 6: 0} {
		if got := v.CaretX(off); got != want {
			t.Errorf("CaretX(%d) = %v, want %v", off, got, want)
		}
	}
	for x, want := range map[float32]int{-5: 6, 4: 6, 6: 4, 14: 4, 16: 2, 26: 0, 99: 0} {
		if got := v.HitByte(x); got != want {
			t.Errorf("HitByte(%v) = %d, want %d", x, got, want)
		}
	}
}

func TestCaretBidi(t *testing.T) {
	// "a" + two Hebrew letters + "b": visual order a, ג, ב, b.
	r := uint32(ClusterRTL)
	v := view("aבגb", 4, []uint32{0, 3 | r, 1 | r, 5}, []float32{0, 10, 20, 30}, 40)
	// Offset 1 is the logical start of the right-to-left run: its right edge.
	// Offset 5 is drawn where "b" starts: the same x as offset 1 (a bidi
	// boundary has two logical positions at one place).
	want := map[int]float32{0: 0, 1: 30, 3: 20, 5: 30, 6: 40}
	for off, w := range want {
		if got := v.CaretX(off); got != w {
			t.Errorf("CaretX(%d) = %v, want %v", off, got, w)
		}
	}
	// A click in the left half of the second glyph (ג) goes to its left
	// edge, the end of the right-to-left run in logical order.
	if got := v.HitByte(12); got != 5 {
		t.Errorf("HitByte(12) = %d, want 5", got)
	}
	if got := v.HitByte(38); got != 6 {
		t.Errorf("HitByte(38) = %d, want 6", got)
	}
}

func TestCaretNoClusters(t *testing.T) {
	v := view("héllo", 5, nil, []float32{0, 10, 20, 30, 40}, 50)
	if got := v.CaretX(3); got != 20 { // é is two bytes: 'h' 'é' = 3 bytes, two graphemes of five
		t.Errorf("CaretX(3) = %v, want 20", got)
	}
	if got := v.HitByte(21); got != 3 {
		t.Errorf("HitByte(21) = %d, want 3", got)
	}
}

func TestCaretAllocs(t *testing.T) {
	r := uint32(ClusterRTL)
	v := view("aבגb👨\u200D👩", 4, []uint32{0, 3 | r, 1 | r, 5}, []float32{0, 10, 20, 30}, 40)
	n := testing.AllocsPerRun(100, func() {
		for off := 0; off <= len(v.Text); off++ {
			_ = v.CaretX(off)
		}
		for x := float32(-5); x < 45; x += 5 {
			_ = v.HitByte(x)
		}
	})
	if n != 0 {
		t.Errorf("%v allocs", n)
	}
}

// Adjacent logical groups may lie on either side in visual order. Ligatures
// and bidi boundaries may instead need the nonadjacent logical successor.
func TestCaretGroupLogicalEnd(t *testing.T) {
	r := uint32(ClusterRTL)
	for _, v := range []RunView{
		longView(8),
		view("affib", 3, []uint32{0, 1, 4}, []float32{0, 10, 40}, 50),
		view("e\u0301x", 3, []uint32{0, 1, 3}, []float32{0, -4, 10}, 20),
		view("אבג", 3, []uint32{4 | r, 2 | r, 0 | r}, []float32{-10, 0, 10}, 20),
		view("aבגb", 4, []uint32{0, 3 | r, 1 | r, 5}, []float32{0, 10, 20, 30}, 40),
		view("a\r\nb", 4, []uint32{0, 1, 2, 3}, []float32{0, 10, 10, 10}, 20),
	} {
		for j := range v.Glyphs {
			g := v.groupAt(j)
			want := len(v.Text)
			for k := range v.Glyphs {
				if c := v.clusterOf(k); c > g.start && c < want {
					want = c
				}
			}
			if g.end != want {
				t.Fatalf("%q glyph %d: logical end %d, want %d", v.Text, j, g.end, want)
			}
		}
	}
}

// longView is n glyphs of one character each, 10 px apart.
func longView(n int) RunView {
	txt := make([]byte, n)
	cl := make([]uint32, n)
	x := make([]float32, n)
	for i := range txt {
		txt[i], cl[i], x[i] = 'a'+byte(i%26), uint32(i), float32(10*i)
	}
	return view(string(txt), n, cl, x, float32(10*n))
}

func BenchmarkCaretX(b *testing.B) {
	for _, n := range []int{100, 1000} {
		v := longView(n)
		b.Run(strconv.Itoa(n)+" glyphs", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				benchmarkCaretXResult = v.CaretX(n / 2)
			}
		})
	}
}

func BenchmarkHitByte(b *testing.B) {
	for _, n := range []int{100, 1000} {
		v := longView(n)
		b.Run(strconv.Itoa(n)+" glyphs", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = v.HitByte(float32(5 * n))
			}
		})
	}
}
