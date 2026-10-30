package ui

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gonativeui/text"
)

// The widget's TextField delegates paste handling to this real Context path.
// Payload <=64 KiB; the buffer is capped at 16 KiB. Every paste is cleaned
// against a separate reference, stays within UTF-8/grapheme boundaries, and
// is one reversible undo step. Per case: 5 s and 32 MiB+512*input bytes.
func FuzzTextFieldPaste(f *testing.F) {
	for _, s := range []string{"", "a\r\nb\rc\nd\t", "a\x00\xffb\xed\xa0\x80", "e\u0301👩🏽‍💻", "\u2028\u2029", strings.Repeat("x", 64<<10)} {
		f.Add([]byte(s), uint16(100), true)
	}
	f.Fuzz(func(t *testing.T, payload []byte, maxBytes uint16, mac bool) {
		if len(payload) > 64<<10 {
			t.Skip()
		}
		start := time.Now()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		defer func() {
			runtime.ReadMemStats(&after)
			if time.Since(start) > 5*time.Second || after.TotalAlloc-before.TotalAlloc > uint64(32<<20+512*len(payload)) {
				t.Fatal("text paste resource bound exceeded")
			}
		}()
		r := newRig(t, mac)
		r.buf.Max = 1 + int(maxBytes)%(16<<10)
		r.buf.SetString("start")
		r.buf.SelectAll()
		old := r.buf.String()
		r.clip = string(payload)
		mods := ModCtrl
		if mac {
			mods = ModSuper
		}
		r.key('V', mods)
		// An empty clipboard produces no insertion, per Input.SetPaste.
		want := old
		if len(payload) > 0 {
			want = fuzzCleanPaste(payload, r.buf.Max)
		}
		if r.pastes != 1 || r.buf.String() != want {
			t.Fatalf("paste got %q want %q (requests %d)", r.buf.String(), want, r.pastes)
		}
		b := r.buf.Bytes()
		lo, hi := r.buf.Selection()
		if !utf8.Valid(b) || len(b) > r.buf.Max || lo < 0 || hi > len(b) || text.GraphemeStart(b, lo) != lo || text.GraphemeStart(b, hi) != hi {
			t.Fatal("paste bounds/encoding/selection")
		}
		if want != old {
			if !r.buf.Undo() || r.buf.String() != old {
				t.Fatal("paste did not undo as one step")
			}
			if !r.buf.Redo() || r.buf.String() != want {
				t.Fatal("paste redo differs")
			}
		}
	})
}

func fuzzCleanPaste(src []byte, max int) string {
	// Fold original CRLF pairs before dropping invalid bytes, which must not
	// make a CR and LF on opposite sides of a bad byte become a pair.
	s := string(bytes.ToValidUTF8(bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n")), nil))
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\r', '\n', '\t', '\u2028', '\u2029':
			return ' '
		default:
			if r < 32 || r >= 127 && r < 160 {
				return -1
			}
			return r
		}
	}, s)
	if len(s) > max {
		for max > 0 && !utf8.RuneStart(s[max]) {
			max--
		}
		s = s[:max]
	}
	return s
}
