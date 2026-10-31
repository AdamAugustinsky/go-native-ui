package text

import (
	"testing"
	"time"
	"unicode"
	"unicode/utf8"
)

// These are the existing grapheme, caret and worker test fixtures, including
// GB11 restarts where the vendored segmenter has a known disagreement.
var diffTextSeeds = []string{
	"", "ascii\r\ntext\t ", "office fi fl ffi", "日本語のメモ 文件",
	"abc مرحبا שלום 123", "e\u0301x\u0308", "🇬🇧🇧🇬🇬\u0301🇬🇧",
	"\u0600🇬🇧", "👩\u0301\u200d👩", "😀👨\u200d👩", "क्ष\u094dक्ष",
	"❤️ 1️⃣ 👍🏽", "a\xffb\xe6\x97c", "\xed\xa0\x80\x80\xff\xcc\x81",
}

// warmDiffFonts shapes every seed in each font before a target's watchdog
// runs, so the system's first load of a fallback font (Devanagari in a bold
// face took 10-14 s on a loaded Windows arm64 runner) is not timed as layout.
func warmDiffFonts(s *System, fonts ...FontKey) {
	for _, f := range fonts {
		for _, seed := range diffTextSeeds {
			s.Shape(f, []byte(seed))
		}
	}
}

// A watchdog catches unexpectedly superlinear work without timing individual
// native calls. Size caps below bound oracle work too; these are deliberately
// generous on the shared host, not performance benchmark assertions.
func diffDeadline(t *testing.T) func() {
	t.Helper()
	start := time.Now()
	return func() {
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("bounded input took %s (limit 5s)", d)
		}
	}
}

// At most 512 bytes: O(n²) stateless reference, O(n) iterator, O(1) storage.
func FuzzGraphemeDifferential(f *testing.F) {
	for _, s := range diffTextSeeds {
		f.Add([]byte(s), uint16(0))
	}
	f.Fuzz(func(t *testing.T, b []byte, restart uint16) {
		if len(b) > 512 {
			return
		}
		defer diffDeadline(t)()
		var it GraphemeIterator
		emoji := 0
		only := true
		for off := 0; off < len(b); {
			want := NextGrapheme(b, off) // unchanged pre-perf per-call oracle
			got := it.Next(b)
			if got != want || got <= off || got > len(b) || !IsGraphemeBoundary(b, got) {
				t.Fatalf("%x: after %d iterator=%d stateless=%d", b, off, got, want)
			}
			r, n := utf8.DecodeRune(b[off:got])
			if n == got-off && unicode.IsSpace(r) {
			} else if isEmojiCluster(r, b[off:got]) {
				emoji++
			} else {
				only = false
			}
			off = got
		}
		if it.Next(b) != len(b) || it.Next(b) != len(b) {
			t.Fatal("end is not stable")
		}
		if !only {
			emoji = 0
		}
		if got := EmojiOnly(b); got != emoji {
			t.Fatalf("emoji count=%d want=%d", got, emoji)
		}
		start := GraphemeStart(b, int(restart)%(len(b)+1))
		it = GraphemeIterator{off: start}
		for off := start; off < len(b); {
			want := NextGrapheme(b, off)
			if got := it.Next(b); got != want {
				t.Fatalf("restart %d after %d: %d != %d", start, off, got, want)
			}
			off = want
		}
	})
}
