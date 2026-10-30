package text

import (
	"bytes"
	"math"
	"testing"
	"unicode/utf8"
)

// shapeFuzzSeeds add what the diff seeds lack: control characters, bidi
// controls and isolates, digits in right-to-left text, text the primary
// font has no glyph for (fallback), and lone surrogates and overlongs.
var shapeFuzzSeeds = []string{
	"\x00\x01\t\x7f\u0085  ", "‮evil‬.exe", "⁧abc⁩ ⁦مرحبا⁩",
	"مرحبا 123 abc 4.5", "1 א 2 ב 3", "العربيةًّ",
	"ᄀᄀᄀ각ᆨᆨ", "กิ๊ำ", "a⃝⃣", "\U0001F3F4\U000E0067\U000E0062\U000E0065\U000E006E\U000E0067\U000E007F",
	"𓀀𓀁 𐌰𐌱 ꙮ", "�￾￿\U0010FFFF", "\xc0\x80\xe0\x80\x80\xf8\x88\x80\x80\x80",
	"\ufeffBOM", "x͏́́́́́́́", "‮x‬؜‎",
}

// checkShapedRun checks what a shaped view promises whatever the input: the
// slices line up, Text is the input, every cluster is a character start of
// the input (an invalid byte is one character), pen positions are finite and
// inside the run (give or take a mark's offset or hanging white space), the
// clusters' graphemes ascend with X in text with no right-to-left content,
// the run starts its logical text at cluster 0, and carets and hits stay in
// range.
func checkShapedRun(t *testing.T, b []byte, v RunView) {
	t.Helper()
	n := len(v.Glyphs)
	if len(v.X) != n || len(v.Faces) != n || v.Clusters != nil && len(v.Clusters) != n {
		t.Fatalf("%q: %d glyphs, %d X, %d faces, %d clusters", b, n, len(v.X), len(v.Faces), len(v.Clusters))
	}
	if !bytes.Equal(v.Text, b) {
		t.Fatalf("%q: view text %q", b, v.Text)
	}
	if !finite(v.Advance) || v.Advance < 0 {
		t.Fatalf("%q: advance %g", b, v.Advance)
	}
	if len(b) == 0 && (n != 0 || v.Advance != 0) {
		t.Fatalf("empty text: %d glyphs, advance %g", n, v.Advance)
	}
	starts := make([]bool, len(b)+1)
	ltr := true // no right-to-left character or bidi control
	for i := 0; i < len(b); {
		starts[i] = true
		r, sz := utf8.DecodeRune(b[i:])
		ltr = ltr && !rtlOrBidiControl(r)
		i += sz
	}
	starts[len(b)] = true
	// grapheme[i] is the start of the grapheme cluster holding byte i: a
	// shaper may order a base's marks as it likes, but not the graphemes.
	grapheme := make([]int, len(b)+1)
	for i := range grapheme {
		if IsGraphemeBoundary(b, i) {
			grapheme[i] = i
		} else {
			grapheme[i] = grapheme[i-1]
		}
	}
	const eps, markSlack = 0.01, 64
	low := len(b) + 1
	for i := range n {
		// A mark's pen position includes its offset from the base, so X
		// can step back a little (Thai, Arabic, enclosing marks): bounded,
		// not ordered.
		if x := v.X[i]; !finite(x) || x < -markSlack || x > v.Advance+markSlack {
			t.Fatalf("%q: glyph %d at x %g, advance %g", b, i, x, v.Advance)
		}
		if v.Clusters == nil {
			continue
		}
		c := int(v.Clusters[i] &^ ClusterRTL)
		if c > len(b) || !starts[c] {
			t.Fatalf("%q: glyph %d's cluster %d is not a character start (clusters %x)", b, i, c, v.Clusters)
		}
		low = min(low, c)
		// Bidi may put runs of one direction next to each other in
		// reverse order (an isolate beside a number in a right-to-left
		// one), so the order is checked on left-to-right text only.
		if !ltr {
			continue
		}
		if v.Clusters[i]&ClusterRTL != 0 {
			t.Fatalf("%q: glyph %d is right to left (clusters %x)", b, i, v.Clusters)
		}
		if i > 0 && grapheme[c] < grapheme[v.Clusters[i-1]] {
			t.Fatalf("%q: graphemes out of order at glyph %d (clusters %x)", b, i, v.Clusters)
		}
	}
	if v.Clusters != nil && n > 0 && low != 0 {
		t.Fatalf("%q: the lowest cluster is %d, want 0 (clusters %x)", b, low, v.Clusters)
	}
	// A right-to-left line's trailing white space hangs left of the origin
	// (caret.go) and marks may sit past the advance, so carets span the
	// glyphs' extent and the advance.
	left, right := float32(0), v.Advance
	for _, x := range v.X {
		left, right = min(left, x), max(right, x)
	}
	for off := -1; off <= len(b)+1; off++ {
		x := v.CaretX(off)
		if !finite(x) || x < left-eps || x > right+eps {
			t.Fatalf("%q: caret %d at x %g, advance %g", b, off, x, v.Advance)
		}
	}
	for i := -2; i <= 2*n+2; i++ {
		x := float32(i) * v.Advance / float32(2*n+1)
		if h := v.HitByte(x); h < 0 || h > len(b) || !IsGraphemeBoundary(b, h) {
			t.Fatalf("%q: hit at %g is byte %d", b, x, h)
		}
	}
}

// rtlOrBidiControl reports a character that can make text right to left or
// reorder it: Bidi_Class R or AL (bidiClass), Arabic digits and the Arabic
// letter mark, and the explicit embeddings, overrides and isolates.
func rtlOrBidiControl(r rune) bool {
	return bidiClass(r) == bidiR || r >= 0x600 && r <= 0x6ff || r == 0x200f ||
		r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069
}

func finite(x float32) bool { return !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0) }

// At most 256 bytes. The host's shaper (CoreText, DirectWrite or go-text with
// fontconfig) with its own fallback cascade; every glyph is rasterized once,
// so the run's faces and glyph ids reach the rasterizer too.
func FuzzShapeRuns(f *testing.F) {
	for _, s := range append(diffTextSeeds, shapeFuzzSeeds...) {
		f.Add([]byte(s), uint8(0))
	}
	s := workerSystem(f)
	fonts := []FontKey{
		s.Font(FontDesc{SizePt: 13, Scale: 2}),
		s.Font(FontDesc{Mono: true, SizePt: 10.5, Scale: 1.5}),
		s.Font(FontDesc{Family: "Helvetica Neue", SizePt: 9, Scale: 1, Weight: 700}),
	}
	f.Fuzz(func(t *testing.T, b []byte, font uint8) {
		if len(b) > 256 {
			return
		}
		defer diffDeadline(t)()
		v := s.Run(s.Shape(fonts[int(font)%len(fonts)], b))
		checkShapedRun(t, b, v)
		for i := range v.Glyphs {
			e := s.Glyph(v.Faces[i], v.Glyphs[i], uint8(i)&3)
			if e.W != 0 && (int(e.X)+int(e.W) > PageSize || int(e.Y)+int(e.H) > PageSize || !finite(e.BearingX) || !finite(e.BearingY)) {
				t.Fatalf("%q: glyph %d's atlas entry %+v", b, i, e)
			}
		}
	})
}
