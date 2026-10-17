package ui

import (
	"testing"

	"gonativeui/text"
)

// Edits that join neighbouring characters into one cluster leave the caret on
// a cluster boundary, so a Backspace removes what the user sees as one
// character.
func TestTextBufferCaretStaysOnBoundaries(t *testing.T) {
	const (
		acute = "\u0301"
		zwj   = "\u200D"
		manA  = "👨"
		wom   = "👩"
		deI   = "🇩"
		deE   = "🇪"
		frF   = "🇫"
	)
	cases := []struct {
		name  string
		start string
		caret int    // caret before the edit
		ins   string // inserted (selection replaced), "" with del for a deletion
		del   [2]int // deleted range when ins == ""
		want  string // text after the edit
		wcar  int    // caret after
	}{
		{"letter before an initial accent", acute + "x", 0, "a", [2]int{}, "a" + acute + "x", 3},
		{"accent after base", "ae", 2, acute, [2]int{}, "ae" + acute, 4},
		{"zwj joins two emoji", manA + wom, 4, zwj, [2]int{}, manA + zwj + wom, 4 + 3 + 4},
		{"lone flag letter pairs with the next", deI + deE + frF, 8, "🇦", [2]int{}, deI + deE + "🇦" + frF, 16},
		{"flag pair shifts parity", deI + deE + frF, 0, deI, [2]int{}, deI + deI + deE + frF, 8},
		{"delete between base and accent", "ex" + acute, 0, "", [2]int{1, 2}, "e" + acute, 0},
		{"delete joins zwj sequence", manA + "x" + zwj + wom, 0, "", [2]int{4, 5}, manA + zwj + wom, 0},
		{"delete joins flags", deI + "x" + deE, 0, "", [2]int{4, 5}, deI + deE, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b TextBuffer
			b.SetString(c.start)
			b.SetCaret(c.caret)
			if c.ins != "" {
				b.InsertString(c.ins)
			} else if c.del != [2]int{} {
				b.deleteRange(c.del[0], c.del[1])
			}
			if got := b.String(); got != c.want {
				t.Fatalf("text %q, want %q", got, c.want)
			}
			if !text.IsGraphemeBoundary(b.b, b.caret) || b.anchor != b.caret {
				t.Fatalf("caret %d anchor %d is not a boundary of %q", b.caret, b.anchor, b.b)
			}
			if b.caret != c.wcar {
				t.Errorf("caret %d, want %d", b.caret, c.wcar)
			}
		})
	}
}

// Backspace after typing a letter in front of an accent removes the whole
// accented character.
func TestTextFieldBackspaceAfterJoin(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("\u0301x")
	r.buf.SetCaret(0)
	r.typ("a")
	if r.text() != "a\u0301x" || r.buf.Caret() != 3 {
		t.Fatalf("state %q caret %d", r.text(), r.buf.Caret())
	}
	r.key(KeyBackspace, 0)
	if r.text() != "x" {
		t.Errorf("Backspace left %q, want x", r.text())
	}
}
