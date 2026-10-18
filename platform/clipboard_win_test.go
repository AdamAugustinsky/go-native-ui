package platform

import (
	"strings"
	"testing"
	"unicode/utf16"
)

func TestUTF16Encode(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"abc", "abc"},
		{"a\nb", "a\r\nb"},
		{"a\r\nb", "a\r\nb"}, // already Windows line ends: not doubled
		{"a\rb", "a\rb"},     // a lone \r stays
		{"\n\n", "\r\n\r\n"}, // each one
		{"\r\r\n", "\r\r\n"}, // only the one before the \n pairs
		{"h\u00e9\u65e5", "h\u00e9\u65e5"},
		{"\U0001F600x", "\U0001F600x"}, // a surrogate pair
		{"a\xff\xfe\xc3b", "a\uFFFDb"}, // one replacement per run of invalid bytes...
		{"a\xffb\xffc", "a\uFFFDb\uFFFDc"},
		{"\xed\xa0\x80", "\uFFFD"}, // ...an encoded surrogate is invalid UTF-8
		{"\uFFFD\xff", "\uFFFD\uFFFD"},
		{"a\xffend\n", "a\uFFFDend\r\n"},
	} {
		n := utf16Encode(nil, []byte(c.in))
		u := make([]uint16, n)
		if got := utf16Encode(u, []byte(c.in)); got != n {
			t.Errorf("%q: counted %d units, wrote %d", c.in, n, got)
		}
		if got := string(utf16.Decode(u)); got != c.want {
			t.Errorf("%q: encoded as %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUTF16Decode(t *testing.T) {
	enc := func(s string) []uint16 { return utf16.Encode([]rune(s)) }
	for _, c := range []struct {
		in   []uint16
		want string
	}{
		{nil, ""},
		{enc("abc"), "abc"},
		{enc("a\r\nb\r\n"), "a\nb\n"},
		{enc("a\rb"), "a\rb"},
		{enc("h\u00e9\u65e5\U0001F600"), "h\u00e9\u65e5\U0001F600"},
		{append(enc("ab"), 0, 'c'), "ab"},                      // NUL ends the text
		{[]uint16{'a', 0xD83D, 'b'}, "a\uFFFDb"},               // high surrogate alone
		{[]uint16{'a', 0xDE00, 'b'}, "a\uFFFDb"},               // low surrogate alone
		{[]uint16{0xD83D}, "\uFFFD"},                           // ... at the end
		{[]uint16{0xD83D, 0xD83D, 0xDE00}, "\uFFFD\U0001F600"}, // a high one before a pair
		{[]uint16{0xDE00, 0xD83D}, "\uFFFD\uFFFD"},
	} {
		got, ok := utf16Decode(nil, c.in, MaxPaste)
		if !ok || string(got) != c.want {
			t.Errorf("%x: decoded %q (ok %v), want %q", c.in, got, ok, c.want)
		}
	}
}

func TestUTF16RoundTrip(t *testing.T) {
	s := "line 1\nh\u00e9llo \u65e5\u672c\u8a9e \U0001F468\u200D\U0001F469\nend"
	u := make([]uint16, utf16Encode(nil, []byte(s)))
	utf16Encode(u, []byte(s))
	if got, ok := utf16Decode(nil, u, MaxPaste); !ok || string(got) != s {
		t.Errorf("round trip: %q (ok %v)", got, ok)
	}
}

func TestUTF16DecodeLimit(t *testing.T) {
	u := utf16.Encode([]rune(strings.Repeat("\u65e5", 10))) // 30 bytes
	for _, c := range []struct {
		max int
		ok  bool
	}{{30, true}, {29, false}, {0, false}, {1 << 20, true}} {
		dst := []byte("keep")
		got, ok := utf16Decode(dst, u, c.max)
		if ok != c.ok {
			t.Errorf("max %d: ok %v, want %v", c.max, ok, c.ok)
		}
		if !ok && string(got) != "keep" {
			t.Errorf("max %d: failed decode left %q, want the destination as it was", c.max, got)
		}
	}
}
