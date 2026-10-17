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
