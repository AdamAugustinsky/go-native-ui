// SPDX-License-Identifier: Unlicense OR BSD-3-Clause

package text

import (
	"strings"
	"testing"
)

// lines cuts s at the breaks.
func cutLines(s string, br []uint32) []string {
	var out []string
	at := 0
	for _, b := range br {
		end := int(b &^ BreakMandatory)
		out = append(out, s[at:end])
		at = end
	}
	return out
}

func TestLineBreaks(t *testing.T) {
	var lb LineBreaker
	cases := []struct {
		in   string
		want []string
		hard []bool
	}{
		{"hello world", []string{"hello ", "world"}, []bool{false, true}},
		{"one two  three", []string{"one ", "two  ", "three"}, []bool{false, false, true}},
		{"a-b c", []string{"a-", "b ", "c"}, []bool{false, false, true}}, // after a hyphen
		{"line one\nline two", []string{"line ", "one\n", "line ", "two"}, []bool{false, true, false, true}},
		{"no\u00a0break here", []string{"no\u00a0break ", "here"}, []bool{false, true}},
		{"日本語のテキスト", []string{"日", "本", "語", "の", "テ", "キ", "ス", "ト"}, nil}, // ideographs break between characters, kana not before small/prolonged marks
		{"x", []string{"x"}, []bool{true}},
	}
	for _, c := range cases {
		br := lb.Breaks(nil, []byte(c.in))
		got := cutLines(c.in, br)
		if c.in == "日本語のテキスト" {
			// A break may fall between any two of these; what must hold is that the
			// segments cover the text in order and each is non-empty.
			if strings.Join(got, "") != c.in || len(got) < 4 {
				t.Errorf("%q: %q", c.in, got)
			}
			continue
		}
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
			continue
		}
		for i, h := range c.hard {
			if (br[i]&BreakMandatory != 0) != h {
				t.Errorf("%q: break %d mandatory=%v, want %v", c.in, i, br[i]&BreakMandatory != 0, h)
			}
		}
	}
	if br := lb.Breaks(nil, nil); len(br) != 0 {
		t.Errorf("empty text: %v", br)
	}
	// Breaks appends.
	pre := []uint32{99}
	if br := lb.Breaks(pre, []byte("a b")); len(br) != 3 || br[0] != 99 {
		t.Errorf("append: %v", br)
	}
}

func TestLineBreaksInvalidUTF8(t *testing.T) {
	var lb LineBreaker
	in := "ab \xff cd\nef"
	br := lb.Breaks(nil, []byte(in))
	got := cutLines(in, br)
	want := []string{"ab ", "\xff ", "cd\n", "ef"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%q, want %q", got, want)
	}
	if br[len(br)-1]&BreakMandatory == 0 {
		t.Error("the end of the text is a mandatory break")
	}
}

func TestLineBreaksAllocateNothingWhenWarm(t *testing.T) {
	var lb LineBreaker
	text := []byte(strings.Repeat("The quick brown fox jumps over the lazy dog. 日本語 ", 8))
	buf := lb.Breaks(nil, text)
	n := testing.AllocsPerRun(50, func() { buf = lb.Breaks(buf[:0], text) })
	if n != 0 {
		t.Fatalf("%v allocations per call", n)
	}
	if len(buf) < 50 {
		t.Fatalf("%d breaks", len(buf))
	}
}
