package text

import (
	"math/rand"
	"slices"
	"testing"
	"unicode/utf8"

	"github.com/go-text/typesetting/segmenter"
)

// gtBoundaries returns the grapheme boundaries (byte offsets, 0 and len
// included) go-text's segmenter finds in s.
func gtBoundaries(t *testing.T, seg *segmenter.Segmenter, s string) []int {
	t.Helper()
	if err := seg.InitWithString(s); err != nil {
		t.Fatal(err)
	}
	out := []int{0}
	it := seg.GraphemeIterator()
	for it.Next() {
		g := it.Grapheme()
		out = append(out, g.OffsetInBytes+g.LengthInBytes)
	}
	return out
}

func ourBoundaries(s string) []int {
	b := []byte(s)
	out := []int{0}
	for off := NextGrapheme(b, 0); ; off = NextGrapheme(b, off) {
		out = append(out, off)
		if off >= len(b) {
			return out
		}
	}
}

func TestGraphemeASCIIPairs(t *testing.T) {
	var seg segmenter.Segmenter
	for a := 0; a < 128; a++ {
		for b := 0; b < 128; b++ {
			pair := []byte{byte(a), byte(b)}
			want := slices.Contains(gtBoundaries(t, &seg, string(pair)), 1)
			if got := IsGraphemeBoundary(pair, 1); got != want {
				t.Fatalf("ASCII %d,%d: boundary %v, Unicode reference %v", a, b, got, want)
			}
		}
	}
}

// The boundaries must be those of go-text's segmenter (the reference
// implementation of UAX #29 the tables come from) over text built from
// every kind of character the rules distinguish.
func TestGraphemeMatchesGoText(t *testing.T) {
	pool := []rune{
		'a', 'b', ' ', '\r', '\n', '\t', 'é', '日', 0x0301, 0x0308, 0x200D, 0x200C, 0xFE0F, 0x20E3,
		0x1F468, 0x1F469, 0x1F467, 0x1F3FB, 0x1F3FF, 0x2764, 0xA9, 0x1F600, 0x1F1E6, 0x1F1E7, 0x1F1E8, 0x1F1E9,
		0xE0067, 0xE007F, 0x1100, 0x1161, 0x11A8, 0xAC00, 0xAC01, 0x0600, 0x0903, 0x0E33, 0x0E01,
		0x0915, 0x094D, 0x0937, 0x0924, 0x09BE, 0x0D4E, 0x061C, 0xFFFD,
	}
	var seg segmenter.Segmenter
	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 30000; n++ {
		rs := make([]rune, 1+rng.Intn(9))
		for i := range rs {
			rs[i] = pool[rng.Intn(len(pool))]
		}
		s := string(rs)
		want, got := gtBoundaries(t, &seg, s), ourBoundaries(s)
		if !slices.Equal(want, got) && !pictRestart(rs) {
			t.Fatalf("%+q: go-text %v, ours %v", s, want, got)
		}
	}
}

// pictRestart reports whether rs has a pictographic character followed, past
// any Extend characters, by another one. go-text's segmenter (v0.3.5) does
// not start a new GB11 sequence at the second, so it breaks "😀 ZWJ 👨" where
// the rule says (ExtPict Extend* ZWJ x ExtPict) not to; we follow the rule.
func pictRestart(rs []rune) bool {
	seen := false
	for _, r := range rs {
		switch {
		case extPict(r):
			if seen {
				return true
			}
			seen = true
		case gbClass(r)&gbExtend != 0:
		default:
			seen = false
		}
	}
	return false
}

// ZWJ sequences: Extended_Pictographic X joins with another across a ZWJ for
// exactly the characters go-text's table calls pictographic.
func TestGraphemeEmojiTable(t *testing.T) {
	var seg segmenter.Segmenter
	for r := rune(0x80); r < 0x1FFFF; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		s := string([]rune{r, 0x200D, r})
		if want, got := gtBoundaries(t, &seg, s), ourBoundaries(s); !slices.Equal(want, got) {
			t.Fatalf("%U ZWJ %U: go-text %v, ours %v", r, r, want, got)
		}
	}
}

func TestGraphemeNavigation(t *testing.T) {
	// a, e + combining acute, family emoji, CRLF, flag, 日
	s := []byte("ae\u0301👨\u200D👩\u200D👧\r\n🇩🇪日")
	var stops []int
	for off := 0; ; {
		stops = append(stops, off)
		if off >= len(s) {
			break
		}
		off = NextGrapheme(s, off)
	}
	want := []int{0, 1, 4, 4 + 4 + 3 + 4 + 3 + 4, 4 + 4 + 3 + 4 + 3 + 4 + 2, 4 + 4 + 3 + 4 + 3 + 4 + 2 + 8, len(s)}
	// recompute explicitly: "a"=1, "é"=3 (e+2 bytes), family=4+3+4+3+4=18, CRLF=2, flag=8, 日=3
	want = []int{0, 1, 4, 22, 24, 32, 35}
	if !slices.Equal(stops, want) {
		t.Fatalf("stops %v, want %v", stops, want)
	}
	for i := len(want) - 1; i > 0; i-- {
		if p := PrevGrapheme(s, want[i]); p != want[i-1] {
			t.Errorf("PrevGrapheme(%d) = %d, want %d", want[i], p, want[i-1])
		}
	}
	for off := 0; off <= len(s); off++ {
		start := GraphemeStart(s, off)
		if !IsGraphemeBoundary(s, start) || start > off || (off < len(s) && NextGrapheme(s, start) <= off) {
			t.Errorf("GraphemeStart(%d) = %d is not the cluster containing it", off, start)
		}
	}
	if NextGrapheme(s, len(s)) != len(s) || PrevGrapheme(s, 0) != 0 || NextGrapheme(nil, 0) != 0 {
		t.Error("clamping at the ends")
	}
	// Invalid bytes are clusters of their own.
	bad := []byte("a\xffb\xe6\x97")
	if got := NextGrapheme(bad, 1); got != 2 {
		t.Errorf("invalid byte: next = %d, want 2", got)
	}
}

func TestGraphemeAllocs(t *testing.T) {
	s := []byte("Hello é 👨\u200D👩\u200D👧 🇩🇪 क्ष 日本語")
	n := testing.AllocsPerRun(100, func() {
		for off := 0; off < len(s); off = NextGrapheme(s, off) {
		}
		for off := len(s); off > 0; off = PrevGrapheme(s, off) {
		}
	})
	if n != 0 {
		t.Errorf("%v allocs", n)
	}
}

// sanitizedBoundaries returns the grapheme boundaries of b (as byte offsets
// into b) computed on the text the shapers see: every invalid byte one U+FFFD.
func sanitizedBoundaries(t *testing.T, seg *segmenter.Segmenter, b []byte) []int {
	t.Helper()
	var san []byte
	var starts []int // offset in b of each byte of san
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n == 1 {
			san = utf8.AppendRune(san, utf8.RuneError)
			starts = append(starts, i, i, i)
		} else {
			san = append(san, b[i:i+n]...)
			for k := 0; k < n; k++ {
				starts = append(starts, i)
			}
		}
		i += n
	}
	starts = append(starts, len(b))
	gt := gtBoundaries(t, seg, string(san))
	out := make([]int, len(gt))
	for i, o := range gt {
		out[i] = starts[o]
	}
	return out
}

// Malformed UTF-8: every invalid byte is a cluster of its own, whatever its
// value (a stray continuation byte included), and a continuation byte inside a
// valid sequence is never a boundary.
func TestGraphemeMalformedUTF8(t *testing.T) {
	cases := [][]byte{
		{0x80, 0x81, 'a'},                   // stray continuation bytes
		{0x80, 0x80, 0x80, 0x80, 0x80},      // a run of them
		{'a', 0xE2, 0x82, 'b'},              // truncated 3-byte sequence
		{0xE2, 0x82},                        // truncated at the end
		{0xF0, 0x9F, 0x98},                  // truncated 4-byte sequence
		{0xC0, 0x80, 'x'},                   // overlong NUL
		{0xE0, 0x80, 0x80},                  // overlong
		{0xC1, 0xBF},                        // overlong
		{0xED, 0xA0, 0x80},                  // UTF-16 surrogate D800
		{0xED, 0xBF, 0xBF, 'z'},             // surrogate DFFF
		{0xF4, 0x90, 0x80, 0x80},            // above U+10FFFF
		{0xF8, 0x88, 0x80, 0x80, 0x80},      // 5-byte form
		{'e', 0xCC, 0x81, 0x80, 'x'},        // é then a stray continuation byte
		{0xE2, 0x82, 0xAC, 0x82, 0xAC},      // € then stray continuations
		{0xFF, 0xFE, 0xE2, 0x82, 0xAC},      // invalid then valid
		{'a', 0xCC, 0x81, 0xFF, 0xCC, 0x81}, // marks after invalid bytes
	}
	var seg segmenter.Segmenter
	for _, b := range cases {
		want := sanitizedBoundaries(t, &seg, b)
		var got []int
		for off := 0; off <= len(b); off++ {
			if IsGraphemeBoundary(b, off) {
				got = append(got, off)
			}
		}
		if !slices.Equal(want, got) {
			t.Errorf("% x: boundaries %v, want %v", b, got, want)
			continue
		}
		// Navigation agrees with the boundaries, both ways.
		var fwd []int
		for off := 0; ; {
			fwd = append(fwd, off)
			if off >= len(b) {
				break
			}
			off = NextGrapheme(b, off)
		}
		if !slices.Equal(fwd, want) {
			t.Errorf("% x: NextGrapheme walk %v, want %v", b, fwd, want)
		}
		for i := len(want) - 1; i > 0; i-- {
			if p := PrevGrapheme(b, want[i]); p != want[i-1] {
				t.Errorf("% x: PrevGrapheme(%d) = %d, want %d", b, want[i], p, want[i-1])
			}
		}
		for off := 0; off <= len(b); off++ {
			s := GraphemeStart(b, off)
			if !slices.Contains(want, s) || s > off || (off < len(b) && NextGrapheme(b, s) <= off) {
				t.Errorf("% x: GraphemeStart(%d) = %d", b, off, s)
			}
		}
	}
}

// Random byte soup: the same property for arbitrary bytes.
func TestGraphemeRandomBytes(t *testing.T) {
	pool := []byte{0x80, 0x81, 0xBF, 0xC2, 0xCC, 0xE2, 0xED, 0xF0, 0xF4, 0xFF, 'a', 'e', 0x9F, 0x98, 0x82, 0xAC, 0x81, '\r', '\n'}
	var seg segmenter.Segmenter
	rng := rand.New(rand.NewSource(7))
	for n := 0; n < 20000; n++ {
		b := make([]byte, 1+rng.Intn(10))
		for i := range b {
			b[i] = pool[rng.Intn(len(pool))]
		}
		want := sanitizedBoundaries(t, &seg, b)
		var got []int
		for off := 0; off <= len(b); off++ {
			if IsGraphemeBoundary(b, off) {
				got = append(got, off)
			}
		}
		if !slices.Equal(want, got) {
			t.Fatalf("% x: boundaries %v, want %v", b, got, want)
		}
	}
}
