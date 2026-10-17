package fs

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// Pure-function tests: they run on every OS, including the Mac development
// machine, although only the Windows backend uses the encoding.

func TestWTF8Golden(t *testing.T) {
	for _, c := range []struct {
		w    []uint16
		want string
	}{
		{nil, ""},
		{[]uint16{'a'}, "a"},
		{[]uint16{0xE9}, "\xC3\xA9"},
		{[]uint16{0x65E5}, "\xE6\x97\xA5"},
		{[]uint16{0xD83D, 0xDE00}, "\xF0\x9F\x98\x80"}, // a pair is the 4-byte form
		{[]uint16{0xD800}, "\xED\xA0\x80"},
		{[]uint16{0xDBFF}, "\xED\xAF\xBF"},
		{[]uint16{0xDC00}, "\xED\xB0\x80"},
		{[]uint16{0xDFFF}, "\xED\xBF\xBF"},
		{[]uint16{'a', 0xD800, 'b'}, "a\xED\xA0\x80b"},
		{[]uint16{0xDC00, 0xD800}, "\xED\xB0\x80\xED\xA0\x80"}, // low then high: both unpaired
		{[]uint16{0xD800, 0xD83D, 0xDE00}, "\xED\xA0\x80\xF0\x9F\x98\x80"},
		{[]uint16{0xFFFE, 0xFFFF}, "\xEF\xBF\xBE\xEF\xBF\xBF"},
	} {
		if got := string(appendWTF8([]byte("pre"), c.w)); got != "pre"+c.want {
			t.Errorf("%x: encoded %q, want %q", c.w, got, c.want)
		}
		back, ok := appendUTF16(nil, c.want)
		if !ok || !slices.Equal(back, c.w) && !(len(back) == 0 && len(c.w) == 0) {
			t.Errorf("%q: decoded %x (ok %v), want %x", c.want, back, ok, c.w)
		}
	}
}

// Text without unpaired surrogates is plain UTF-8, equal to what package
// utf16/utf8 produce.
func TestWTF8ValidTextIsUTF8(t *testing.T) {
	for _, s := range []string{"", "a", "plain ascii.txt", "é", "日本語", "😀", "a😀b😀😀c", "\U0010FFFF", "\u0800\uFFFD", strings.Repeat("ü😀", 130)} {
		w, ok := appendUTF16(nil, s)
		if !ok {
			t.Errorf("%q: rejected", s)
			continue
		}
		if want := utf16.Encode([]rune(s)); !slices.Equal(w, want) && !(len(w) == 0 && len(want) == 0) {
			t.Errorf("%q: UTF-16 %x, want %x", s, w, want)
		}
		enc := appendWTF8(nil, w)
		if string(enc) != s || !utf8.Valid(enc) {
			t.Errorf("%q: round trip %q", s, enc)
		}
	}
}

// Every UTF-16 code unit, alone and in context, survives encode -> decode.
func TestWTF8RoundTripsEveryUnit(t *testing.T) {
	var w []uint16
	for u := 1; u <= 0xFFFF; u++ {
		for _, ctx := range [][]uint16{{uint16(u)}, {'x', uint16(u), 'y'}, {0xD800, uint16(u)}, {uint16(u), 0xDC00}} {
			w = append(w[:0], ctx...)
			enc := appendWTF8(nil, w)
			dec, ok := appendUTF16(nil, string(enc))
			if !ok || !slices.Equal(dec, w) {
				t.Fatalf("%x -> %q -> %x (ok %v)", w, enc, dec, ok)
			}
		}
	}
	// All high x low surrogate combinations: pairs become 4 bytes, and
	// decode back to the same pair.
	for hi := uint16(0xD800); hi < 0xDC00; hi += 7 {
		for lo := uint16(0xDC00); lo < 0xE000; lo += 5 {
			enc := appendWTF8(nil, []uint16{hi, lo})
			if len(enc) != 4 || !utf8.Valid(enc) {
				t.Fatalf("pair %x %x: %q", hi, lo, enc)
			}
			if dec, _ := appendUTF16(nil, string(enc)); !slices.Equal(dec, []uint16{hi, lo}) {
				t.Fatalf("pair %x %x: back %x", hi, lo, dec)
			}
		}
	}
}

// Two names that differ only in which unpaired surrogate they hold stay
// different (they were both U+FFFD before).
func TestWTF8KeepsIdentity(t *testing.T) {
	a := appendWTF8(nil, []uint16{'a', 0xD800})
	b := appendWTF8(nil, []uint16{'a', 0xDC00})
	c := appendWTF8(nil, []uint16{'a', 0xFFFD})
	if string(a) == string(b) || string(a) == string(c) || string(b) == string(c) {
		t.Errorf("names collapsed: %q %q %q", a, b, c)
	}
}

func TestWTF8Decode(t *testing.T) {
	if _, ok := appendUTF16(nil, "a\x00b"); ok {
		t.Error("NUL accepted")
	}
	for _, s := range []string{"\xff", "\xED", "\xED\xA0", "\xED\xA0x", "\xED\x9F\xBF", "\xC0\x80", "\xF8\x88\x80\x80\x80", "a\xE6\x97", "\xED\xA0\x80\xED"} {
		w, ok := appendUTF16(nil, s)
		if !ok || len(w) == 0 {
			t.Errorf("%q: %x ok=%v", s, w, ok)
		}
	}
	// "\xED\x9F\xBF" is U+D7FF, the last valid code point before the surrogates.
	if w, _ := appendUTF16(nil, "\xED\x9F\xBF"); !slices.Equal(w, []uint16{0xD7FF}) {
		t.Errorf("U+D7FF: %x", w)
	}
}

func FuzzWTF8(f *testing.F) {
	for _, s := range []string{"", "a", "é日😀", "\xED\xA0\x80", "\xED\xB0\x80\xED\xA0\x80", "\xff\xED\xA0", "\xF0\x9F\x98\x80"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		u1, ok := appendUTF16(nil, s)
		if !ok {
			if !strings.Contains(s, "\x00") {
				t.Fatalf("%q rejected without a NUL", s)
			}
			return
		}
		// Decoding then encoding then decoding is stable, and the encoding
		// is what a second pass would produce.
		e := appendWTF8(nil, u1)
		u2, _ := appendUTF16(nil, string(e))
		if !slices.Equal(u1, u2) {
			t.Fatalf("%q: %x -> %q -> %x", s, u1, e, u2)
		}
		if e2 := appendWTF8(nil, u2); string(e2) != string(e) {
			t.Fatalf("%q: encoding not stable: %q vs %q", s, e, e2)
		}
	})
}

func TestCheckName(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("needs 64-bit int")
	}
	max := int(math.MaxUint32)
	for _, c := range []struct {
		arena, n int
		want     error
	}{
		{0, 0, nil},
		{0, 1, nil},
		{0, 65535, nil},
		{0, 65536, errNameTooLong},
		{1 << 20, 70000, errNameTooLong},
		{max - 10, 10, nil},
		{max - 10, 11, errArenaFull},
		{max, 0, nil},
		{max, 1, errArenaFull},
		{max + 1, 0, errArenaFull},
		{1 << 40, 1, errArenaFull},
		{max - 70000, 65535, nil},
		{max - 65534, 65535, errArenaFull},
		{max - 65535, 65535, nil},
	} {
		if got := checkName(c.arena, c.n); got != c.want {
			t.Errorf("checkName(%d, %d) = %v, want %v", c.arena, c.n, got, c.want)
		}
		if checkName(c.arena, c.n) == nil {
			// What the entry stores must not wrap: NameOff+NameLen in uint32
			// (as Listing.Name computes it) equals the true end.
			off, ln := uint32(c.arena), uint16(c.n)
			if uint64(off+uint32(ln)) != uint64(c.arena)+uint64(c.n) {
				t.Errorf("(%d, %d) accepted but wraps in uint32", c.arena, c.n)
			}
		}
	}
}
