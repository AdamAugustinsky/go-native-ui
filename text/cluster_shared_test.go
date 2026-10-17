package text

import (
	"math"
	"slices"
	"testing"
	"unicode/utf8"
)

// The cluster and caret checks every backend runs on its own shaper: the
// same expectations for CoreText, DirectWrite and go-text (Linux, or any OS
// with -tags textengine). They are stated as properties of CaretX/HitByte
// over shaped text, so they hold whatever fonts the machine has.

type clusterEnv struct {
	shape func(s string) RunView
	// rtl: the shaper has a font for Arabic, so right-to-left runs can be tested.
	rtl bool
	// ligShape, if set, shapes with a font known to ligate "fi" (the UI font
	// of many systems does not).
	ligShape func(s string) RunView
}

func approx(a, b float32) bool { return math.Abs(float64(a-b)) < 0.02 }

// boundaries returns the grapheme boundaries of s.
func boundaries(s string) []int {
	b := []byte(s)
	out := []int{0}
	for off := 0; off < len(b); {
		off = NextGrapheme(b, off)
		out = append(out, off)
	}
	return out
}

func runClusterChecks(t *testing.T, env clusterEnv) {
	t.Helper()
	cases := []struct {
		name, s string
		ltr     bool // pure left-to-right: carets increase with the offset
	}{
		{"ascii", "Hello, world", true},
		{"spaces", "  a  b  ", true},
		{"combining", "e\u0301x\u0308\u0323y", true},
		{"hangul jamo", "\u1100\u1161\u11A8x", true},
		{"emoji zwj", "a👨\u200D👩\u200D👧b", true},
		{"flags", "x🇩🇪🇫🇷y", true},
		{"skin tone", "👍🏽!", true},
		{"cjk", "日本語のテキスト", true},
		{"ligature", "office fi fl ffi", true},
		{"devanagari conjunct", "कक्षा", true},
		{"invalid utf-8", "a\xffb\xe6\x97c", true},
		{"stray continuation bytes", "a\x80\x81\x80b", true},
		{"continuation run", "\x80\x80\x80\x80", true},
		{"truncated sequences", "x\xe2\x82y\xf0\x9f\x98z\xe2\x82", true},
		{"overlong", "a\xc0\x80\xe0\x80\x80b", true},
		{"encoded surrogates", "a\xed\xa0\x80\xed\xbf\xbfb", true},
		{"invalid beside a mark", "e\u0301\x80e\u0301", true},
		{"arabic", "مرحبا", false},
		{"hebrew", "שלום", false},
		{"mixed bidi", "abc مرحبا def", false},
		{"digits in rtl", "مرحبا 123 مرحبا", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !c.ltr && !env.rtl {
				t.Skip("no right-to-left font")
			}
			v := env.shape(c.s)
			checkCluster(t, v, c.s, c.ltr)
		})
	}

	t.Run("ascii clusters", func(t *testing.T) {
		v := env.shape("Hello")
		if len(v.Glyphs) != 5 {
			t.Skipf("Hello shaped to %d glyphs", len(v.Glyphs))
		}
		for i, c := range v.Clusters {
			if c != uint32(i) {
				t.Fatalf("Clusters = %v, want 0..4", v.Clusters)
			}
		}
	})
	t.Run("cjk clusters", func(t *testing.T) {
		v := env.shape("日本語")
		if len(v.Glyphs) != 3 {
			t.Skipf("shaped to %d glyphs", len(v.Glyphs))
		}
		for i, c := range v.Clusters {
			if c != uint32(3*i) {
				t.Fatalf("Clusters = %v, want 0 3 6", v.Clusters)
			}
		}
	})
	t.Run("combining marks are not caret stops", func(t *testing.T) {
		s := "ae\u0301b" // e + combining acute = one grapheme at bytes 1..4
		v := env.shape(s)
		if x := v.CaretX(2); !approx(x, v.CaretX(1)) {
			t.Errorf("CaretX inside a grapheme: %v, start of it %v", x, v.CaretX(1))
		}
		if v.CaretX(4) <= v.CaretX(1) {
			t.Errorf("é has no width: CaretX(1)=%v CaretX(4)=%v", v.CaretX(1), v.CaretX(4))
		}
		for x := float32(-2); x < v.Advance+2; x += 0.5 {
			if o := v.HitByte(x); o == 2 || o == 3 {
				t.Fatalf("HitByte(%v) = %d, inside a grapheme", x, o)
			}
		}
	})
	t.Run("emoji sequence is one stop", func(t *testing.T) {
		s := "a👨\u200D👩\u200D👧b"
		v := env.shape(s)
		end := 1 + 4 + 3 + 4 + 3 + 4
		got := map[int]bool{}
		for x := float32(-2); x < v.Advance+2; x += 0.25 {
			got[v.HitByte(x)] = true
		}
		for o := range got {
			if o != 0 && o != 1 && o != end && o != end+1 {
				t.Errorf("HitByte returned %d inside the sequence", o)
			}
		}
		if !got[0] || !got[1] || !got[end] || !got[end+1] {
			t.Errorf("stops reached: %v", got)
		}
	})
	t.Run("ligature splits evenly", func(t *testing.T) {
		shape := env.shape
		if env.ligShape != nil {
			shape = env.ligShape
		}
		v := shape("fi")
		if len(v.Glyphs) != 1 {
			t.Skipf("no fi ligature in this font (%d glyphs)", len(v.Glyphs))
		}
		x0, x1, x2 := v.CaretX(0), v.CaretX(1), v.CaretX(2)
		if !approx(x1-x0, x2-x1) || x1 <= x0 {
			t.Errorf("fi ligature: stops at %v %v %v", x0, x1, x2)
		}
	})
	t.Run("rtl direction", func(t *testing.T) {
		if !env.rtl {
			t.Skip("no right-to-left font")
		}
		s := "مرحبا"
		v := env.shape(s)
		for i, c := range v.Clusters {
			if c&ClusterRTL == 0 {
				t.Fatalf("glyph %d is not marked right-to-left: %v", i, v.Clusters)
			}
		}
		if !approx(v.CaretX(0), v.Advance) || !approx(v.CaretX(len(s)), 0) {
			t.Errorf("start of text at %v (advance %v), end at %v", v.CaretX(0), v.Advance, v.CaretX(len(s)))
		}
		prev := v.Advance + 1
		for _, o := range boundaries(s) {
			x := v.CaretX(o)
			if x > prev+0.02 {
				t.Errorf("caret moves right at offset %d: %v after %v", o, x, prev)
			}
			prev = x
		}
		if o := v.HitByte(v.Advance + 5); o != 0 {
			t.Errorf("click right of right-to-left text: offset %d, want 0", o)
		}
		if o := v.HitByte(-5); o != len(s) {
			t.Errorf("click left of right-to-left text: offset %d, want %d", o, len(s))
		}
	})
	t.Run("empty", func(t *testing.T) {
		v := env.shape("")
		if v.CaretX(0) != 0 || v.CaretX(5) != 0 || v.HitByte(10) != 0 {
			t.Error("empty text")
		}
	})
}

// checkCluster verifies the invariants of one shaped string.
func checkCluster(t *testing.T, v RunView, s string, ltr bool) {
	t.Helper()
	if string(v.Text) != s {
		t.Fatalf("Text = %q, want %q", v.Text, s)
	}
	if len(v.Glyphs) == 0 || len(v.Clusters) != len(v.Glyphs) || len(v.X) != len(v.Glyphs) {
		t.Fatalf("glyphs %d, clusters %d, x %d", len(v.Glyphs), len(v.Clusters), len(v.X))
	}
	for i, c := range v.Clusters {
		if o := int(c &^ ClusterRTL); o >= len(s) {
			t.Fatalf("glyph %d: cluster offset %d past the text (%d bytes)", i, o, len(s))
		}
		if v.X[i] < -v.Advance || v.X[i] > v.Advance+1 {
			t.Fatalf("glyph %d: x %v outside the run (advance %v)", i, v.X[i], v.Advance)
		}
	}
	stops := boundaries(s)
	// Every invalid byte is a stop of its own (the shapers draw one U+FFFD
	// for it), so a caret can sit before it and the click can reach it.
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 && !slices.Contains(stops, i) {
			t.Fatalf("offset %d (invalid byte %#x) is not a caret stop: %v", i, s[i], stops)
		}
		i += n
	}
	prev := float32(-1)
	for _, o := range stops {
		x := v.CaretX(o)
		if x < -0.02 || x > v.Advance+0.02 {
			t.Fatalf("CaretX(%d) = %v outside [0, %v]", o, x, v.Advance)
		}
		if ltr {
			if x < prev-0.02 {
				t.Fatalf("CaretX(%d) = %v after %v: not monotonic", o, x, prev)
			}
			prev = x
		}
		// An offset inside a grapheme sits where the grapheme starts.
		for in := o + 1; in < NextGrapheme(v.Text, o); in++ {
			if xi := v.CaretX(in); !approx(xi, x) {
				t.Fatalf("CaretX(%d) = %v inside the grapheme at %d (%v)", in, xi, o, x)
			}
		}
		// HitByte inverts CaretX: it lands on a boundary at the same x.
		h := v.HitByte(x)
		if !IsGraphemeBoundary(v.Text, h) {
			t.Fatalf("HitByte(CaretX(%d)) = %d is not a grapheme boundary", o, h)
		}
		if xh := v.CaretX(h); !approx(xh, x) {
			t.Fatalf("HitByte(CaretX(%d)) = %d at %v, want x %v", o, h, xh, x)
		}
	}
	if ltr {
		if !approx(v.CaretX(0), 0) || !approx(v.CaretX(len(s)), v.Advance) {
			t.Fatalf("ends: CaretX(0)=%v CaretX(len)=%v, advance %v", v.CaretX(0), v.CaretX(len(s)), v.Advance)
		}
		if h := v.HitByte(-10); h != 0 {
			t.Errorf("HitByte left of the text = %d", h)
		}
		if h := v.HitByte(v.Advance + 10); h != len(s) {
			t.Errorf("HitByte right of the text = %d, want %d", h, len(s))
		}
	}
	// Every point maps to a boundary.
	for x := float32(-3); x < v.Advance+3; x += 0.7 {
		if h := v.HitByte(x); !IsGraphemeBoundary(v.Text, h) {
			t.Fatalf("HitByte(%v) = %d is not a grapheme boundary", x, h)
		}
	}
}
