package ui

import (
	"runtime"
	"testing"
)

func TestReplaceUTF16(t *testing.T) {
	cases := []struct {
		name         string
		text         string
		caret        int
		start, units int
		with         string
		want         string
		wantCaret    int
	}{
		{"accent replaces the letter before the caret", "e", 1, -1, 1, "é", "é", 2},
		{"in the middle", "abcd", 3, -1, 1, "é", "abéd", 4},
		{"empty text removes", "abc", 3, -1, 1, "", "ab", 2},
		{"empty text, nothing to remove", "", 0, -1, 1, "", "", 0},
		{"two characters back", "abcd", 4, -2, 2, "xy", "abxy", 4},
		{"range after the caret", "abcd", 1, 1, 2, "X", "abX", 3},
		{"range starting at the caret", "abcd", 1, 0, 2, "X", "aXd", 2},
		{"range around the caret", "abcd", 2, -1, 2, "X", "aXd", 2},
		{"two-byte characters", "éèà", 6, -1, 1, "e", "éèe", 5},
		{"three-byte characters count one unit", "日本語", 9, -2, 2, "x", "日x", 4},
		{"astral character counts two units", "a😀b", 5, -2, 2, "e", "aeb", 2},
		{"astral character, whole range", "a😀", 5, -3, 3, "x", "x", 1},
		{"a range that ends inside a surrogate pair takes the character", "a😀b", 5, -1, 1, "e", "aeb", 2},
		{"a range that starts inside a surrogate pair takes the character", "a😀bc", 5, -1, 2, "e", "aec", 2},
		{"range before the start is cut", "ab", 2, -9, 9, "x", "x", 1},
		{"range past the end is cut", "ab", 0, 0, 9, "x", "x", 1},
		{"replacement is longer", "e", 1, -1, 1, "éé", "éé", 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var b TextBuffer
			b.SetString(c.text)
			b.SetCaret(c.caret)
			b.ReplaceUTF16(c.start, c.units, []byte(c.with))
			if b.String() != c.want || b.Caret() != c.wantCaret || b.HasSelection() {
				t.Fatalf("%q caret %d (selection %v), want %q caret %d", b.String(), b.Caret(), b.HasSelection(), c.want, c.wantCaret)
			}
			if utf8Invalid(b.String()) {
				t.Fatalf("invalid UTF-8 %q", b.String())
			}
			if c.want == c.text {
				return
			}
			b.Undo()
			if b.String() != c.text || b.Caret() != c.caret {
				t.Fatalf("undo: %q caret %d, want %q caret %d", b.String(), b.Caret(), c.text, c.caret)
			}
			b.Redo()
			if b.String() != c.want || b.Caret() != c.wantCaret {
				t.Fatalf("redo: %q caret %d, want %q caret %d", b.String(), b.Caret(), c.want, c.wantCaret)
			}
		})
	}
}

func utf8Invalid(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return true
		}
	}
	return false
}

// A replacement is one undo step of its own: the typing before it is a step
// too, and neither merges with the replacement.
func TestReplaceUTF16IsOneUndoStep(t *testing.T) {
	var b TextBuffer
	b.now = 1e9
	b.InsertString("h")
	b.InsertString("e")
	b.ReplaceUTF16(-1, 1, []byte("é"))
	b.InsertString("y") // typing after it starts a step of its own
	if b.String() != "héy" {
		t.Fatalf("%q", b.String())
	}
	for _, want := range []string{"hé", "he", ""} {
		b.Undo()
		if b.String() != want {
			t.Fatalf("undo: %q, want %q", b.String(), want)
		}
	}
	// Respects Max: the replacement is cut at a character boundary.
	b = TextBuffer{Max: 4}
	b.SetString("abe")
	b.ReplaceUTF16(-1, 1, []byte("éé"))
	if b.String() != "abé" {
		t.Fatalf("Max: %q", b.String())
	}
}

// The field applies a replacement before the text that follows it, and only
// when it has the focus.
func TestTextFieldReplace(t *testing.T) {
	r := newRig(t, true)
	r.typ("e")
	if r.text() != "e" {
		t.Fatal(r.text())
	}
	// Press-and-hold: the accent replaces the letter.
	r.in.ReplaceText(-1, 1, []byte("é"))
	r.frame()
	if r.text() != "é" || r.buf.Caret() != 2 || !r.res.Changed {
		t.Fatalf("%q caret %d changed %v", r.text(), r.buf.Caret(), r.res.Changed)
	}
	if r.in.HasReplace {
		t.Error("the replacement outlives its frame")
	}
	// Text after the replacement in the same frame is typed after it.
	r.in.ReplaceText(-1, 1, []byte("e"))
	r.in.AppendText("xyz")
	r.frame()
	if r.text() != "exyz" {
		t.Fatalf("replacement plus text: %q", r.text())
	}
	// One frame takes one replacement, and one that must follow text is refused.
	r.in.AppendText("q")
	if r.in.CanReplace() || r.in.ReplaceText(-1, 1, []byte("w")) {
		t.Error("a replacement was accepted after typed text")
	}
	r.frame()
	r.in.ReplaceText(-1, 1, nil)
	if r.in.ReplaceText(-1, 1, nil) {
		t.Error("a second replacement in one frame")
	}
	r.frame()
	if r.text() != "exyz" {
		t.Fatalf("empty replacement: %q", r.text())
	}
	// Without the focus nothing is replaced.
	r.c.SetFocus(0)
	r.frame()
	r.in.ReplaceText(-1, 1, []byte("!"))
	r.frame()
	if r.text() != "exyz" {
		t.Fatalf("an unfocused field was edited: %q", r.text())
	}
	// A replacement does not break an input-method composition's text.
	r.focus()
	r.in.SetPreedit([]byte("かな"), 0, 0)
	r.in.ReplaceText(-1, 1, nil)
	r.frame()
	if r.text() != "exy" {
		t.Fatalf("replacement with a composition: %q", r.text())
	}
}

func TestTextFieldReplaceAllocatesNothing(t *testing.T) {
	r := newRig(t, true)
	r.buf.Reserve(64)
	r.typ("e")
	cycle := func() {
		r.in.ReplaceText(-1, 1, []byte("é"))
		r.frame()
		r.in.ReplaceText(-1, 1, []byte("e"))
		r.frame()
		r.in.ReplaceText(-1, 1, nil)
		r.frame()
		r.in.AppendText("e")
		r.frame()
		r.key('Z', ModSuper)
		r.key('Z', ModSuper|ModShift)
	}
	if n := testing.AllocsPerRun(100, cycle); n != 0 {
		t.Fatalf("%v allocs per cycle", n)
	}
}

// After Reserve even the first edits allocate nothing: the history does not
// have to be filled before the buffer is in steady state.
func TestReserveAvoidsWarmUp(t *testing.T) {
	var b TextBuffer
	b.Reserve(256)
	b.now = 1e9
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	for i := 0; i < 150; i++ { // more steps than the history keeps
		b.now += 2e9
		b.InsertString("ab")
		b.InsertString("é")
		b.now += 2e9
		b.deleteRange(b.caret-2, b.caret)
		b.breakRun()
		b.ReplaceUTF16(-1, 1, []byte("x"))
		b.SetSelection(0, 2)
		b.deleteSelection()
		b.breakRun()
		b.insertSealed([]byte("some pasted text, longer than the typing"))
		b.SelectAll() // keeps the text within what was reserved
		b.deleteSelection()
		b.breakRun()
		if i%5 == 0 {
			b.Undo()
			b.Undo()
			b.Redo()
		}
	}
	runtime.ReadMemStats(&m1)
	if n := m1.Mallocs - m0.Mallocs; n != 0 {
		t.Fatalf("%d mallocs in the first edits after Reserve", n)
	}
	// Without it, the same edits do allocate (the test would pass vacuously otherwise).
	var c TextBuffer
	runtime.ReadMemStats(&m0)
	c.InsertString("ab")
	c.breakRun()
	c.InsertString("c")
	runtime.ReadMemStats(&m1)
	if m1.Mallocs == m0.Mallocs {
		t.Error("an unreserved buffer did not allocate for its first edits: the test measures nothing")
	}
}
