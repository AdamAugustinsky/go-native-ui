package text

import (
	"bytes"
	"testing"
	"unicode/utf8"
)

// FirstStrongRTL by its rules, on arbitrary bytes: only the first paragraph
// counts; a neutral prefix and a closed isolate change nothing; a strong
// prefix decides; an unclosed isolate hides the rest of the paragraph; and the
// answer is the one of the first strong character, found by a plain scan over
// a rune slice.
func FuzzFirstStrongRTL(f *testing.F) {
	for _, s := range append(diffTextSeeds, shapeFuzzSeeds...) {
		f.Add([]byte(s), []byte("x"))
	}
	f.Add([]byte("\u2067abc\u2069 שלום"), []byte("\u2066"))
	f.Add([]byte("123 (\u05d0)"), []byte("\r\nabc"))
	f.Fuzz(func(t *testing.T, a, x []byte) {
		if len(a) > 512 || len(x) > 64 {
			return
		}
		got := FirstStrongRTL(a)
		if want := firstStrongRef(a); got != want {
			t.Fatalf("FirstStrongRTL(%q) = %v, the scan says %v", a, got, want)
		}
		end := paraEnd(a, 0)
		if FirstStrongRTL(a[:end]) != got {
			t.Fatalf("%q: the first paragraph %q resolves differently", a, a[:end])
		}
		if FirstStrongRTL(append([]byte("12 ,.-("), a...)) != got {
			t.Fatalf("%q: a neutral prefix changed it", a)
		}
		if !FirstStrongRTL(append([]byte("\u05d0"), a...)) || FirstStrongRTL(append([]byte("a"), a...)) {
			t.Fatalf("%q: a strong prefix did not decide", a)
		}
		// x without newlines or isolate controls, closed by a PDI, is
		// skipped; left open, it hides the paragraph after it unless the
		// paragraph closes it.
		// x is valid UTF-8, so its end cannot join a's start into a rune.
		if !utf8.Valid(x) || bytes.ContainsFunc(x, func(r rune) bool { return r >= 0x2066 && r <= 0x2069 }) || paraEnd(x, 0) != len(x) {
			return
		}
		for _, open := range []string{"\u2066", "\u2067", "\u2068"} {
			closed := append(append(append([]byte(open), x...), "\u2069"...), a...)
			if FirstStrongRTL(closed) != got {
				t.Fatalf("%q: the isolate %q before it changed it", a, closed[:len(closed)-len(a)])
			}
			if !bytes.Contains(a[:end], []byte("\u2069")) && FirstStrongRTL(append(append([]byte(open), x...), a[:end]...)) {
				t.Fatalf("%q: an unclosed isolate did not hide the paragraph", a)
			}
		}
	})
}

// firstStrongRef is rules P2 and P3 over the decoded runes of the first
// paragraph (an invalid byte is U+FFFD, a neutral).
func firstStrongRef(b []byte) bool {
	var rs []rune
	for i := 0; i < paraEnd(b, 0); {
		r, n := utf8.DecodeRune(b[i:])
		rs = append(rs, r)
		i += n
	}
	for i := 0; i < len(rs); i++ {
		switch bidiClass(rs[i]) {
		case bidiL:
			return false
		case bidiR:
			return true
		case bidiIsolate: // skip to the matching PDI
			for d := 1; d > 0 && i+1 < len(rs); {
				i++
				switch bidiClass(rs[i]) {
				case bidiIsolate:
					d++
				case bidiPDI:
					d--
				}
			}
		}
	}
	return false
}
