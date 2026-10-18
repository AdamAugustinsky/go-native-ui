package ui

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// typeRun types s one character at a time, ms milliseconds apart, from the
// buffer's clock.
func typeRun(b *TextBuffer, s string, ms int64) {
	for _, r := range s {
		b.now += ms * 1e6
		b.InsertString(string(r))
	}
}

func bs(b *TextBuffer, n int, ms int64) {
	for i := 0; i < n; i++ {
		b.now += ms * 1e6
		b.deleteRange(max(b.caret-1, 0), b.caret)
	}
}

func TestUndoGroupsTypingRuns(t *testing.T) {
	var b TextBuffer
	b.now = 1e9
	typeRun(&b, "hello", 100)
	b.now += 1500e6 // a pause ends the run
	typeRun(&b, " world", 100)
	if !b.Undo() || b.String() != "hello" || b.Caret() != 5 {
		t.Fatalf("undo 1: %q caret %d", b.String(), b.Caret())
	}
	if !b.Undo() || b.String() != "" {
		t.Fatalf("undo 2: %q", b.String())
	}
	if b.Undo() {
		t.Fatal("undo with nothing to undo reported true")
	}
	if !b.Redo() || b.String() != "hello" || b.Caret() != 5 {
		t.Fatalf("redo 1: %q caret %d", b.String(), b.Caret())
	}
	if !b.Redo() || b.String() != "hello world" || b.Caret() != 11 {
		t.Fatalf("redo 2: %q caret %d", b.String(), b.Caret())
	}
	if b.Redo() {
		t.Fatal("redo past the end")
	}
}

func TestUndoRunEnds(t *testing.T) {
	t.Run("caret move", func(t *testing.T) {
		var b TextBuffer
		b.now = 1e9
		typeRun(&b, "ab", 50)
		b.moveTo(1, false)
		typeRun(&b, "X", 50)
		if b.String() != "aXb" {
			t.Fatal(b.String())
		}
		b.Undo()
		if b.String() != "ab" {
			t.Fatalf("after one undo: %q", b.String())
		}
		b.Undo()
		if b.String() != "" {
			t.Fatalf("after two: %q", b.String())
		}
	})
	t.Run("insert to delete", func(t *testing.T) {
		var b TextBuffer
		b.now = 1e9
		typeRun(&b, "abc", 50)
		bs(&b, 2, 50)
		typeRun(&b, "d", 50)
		if b.String() != "ad" {
			t.Fatal(b.String())
		}
		want := []string{"a", "abc", ""}
		for _, w := range want {
			b.Undo()
			if b.String() != w {
				t.Fatalf("undo: %q, want %q", b.String(), w)
			}
		}
	})
	t.Run("paste and cut are steps of their own", func(t *testing.T) {
		var b TextBuffer
		b.now = 1e9
		typeRun(&b, "ab", 50)
		b.insertSealed([]byte("XY"))
		typeRun(&b, "c", 50)
		b.SetSelection(1, 3)
		b.breakRun()
		b.deleteSelection()
		b.breakRun()
		typeRun(&b, "z", 50)
		if b.String() != "azYc" {
			t.Fatalf("setup: %q", b.String())
		}
		for _, w := range []string{"aYc", "abXYc", "abXY", "ab", ""} {
			b.Undo()
			if b.String() != w {
				t.Fatalf("undo: %q, want %q", b.String(), w)
			}
		}
	})
}

func TestUndoDeleteRunsRestoreInOrder(t *testing.T) {
	var b TextBuffer
	b.SetString("hello world")
	b.now = 1e9
	bs(&b, 5, 50) // Backspace run
	if b.String() != "hello " {
		t.Fatal(b.String())
	}
	if !b.Undo() || b.String() != "hello world" || b.Caret() != 11 {
		t.Fatalf("backspace run: %q caret %d", b.String(), b.Caret())
	}
	if !b.Redo() || b.String() != "hello " || b.Caret() != 6 {
		t.Fatalf("redo: %q caret %d", b.String(), b.Caret())
	}
	b.Undo()
	b.SetCaret(0)
	for i := 0; i < 5; i++ { // forward deletes
		b.now += 50e6
		b.deleteRange(b.caret, text1(&b, b.caret))
	}
	if b.String() != " world" {
		t.Fatal(b.String())
	}
	if !b.Undo() || b.String() != "hello world" {
		t.Fatalf("delete run: %q", b.String())
	}
}

// text1 is the end of the character after off.
func text1(b *TextBuffer, off int) int {
	_, n := utf8.DecodeRune(b.b[off:])
	return off + n
}

func TestUndoRestoresSelection(t *testing.T) {
	var b TextBuffer
	b.SetString("one two three")
	b.SetSelection(4, 7) // "two"
	b.now = 1e9
	b.InsertString("2")
	b.InsertString("!")
	if b.String() != "one 2! three" {
		t.Fatal(b.String())
	}
	b.Undo()
	if b.String() != "one two three" {
		t.Fatalf("undo: %q", b.String())
	}
	if lo, hi := b.Selection(); lo != 4 || hi != 7 {
		t.Fatalf("selection after undo: %d..%d", lo, hi)
	}
}

func TestUndoNewEditEndsRedo(t *testing.T) {
	var b TextBuffer
	b.now = 1e9
	typeRun(&b, "abc", 50)
	b.Undo()
	typeRun(&b, "x", 50)
	if b.Redo() {
		t.Fatal("redo survived a new edit")
	}
	b.Undo()
	if b.String() != "" {
		t.Fatalf("undo after the new edit: %q", b.String())
	}
}

func TestUndoReplacingTheTextForgetsHistory(t *testing.T) {
	var b TextBuffer
	typeRun(&b, "abc", 50)
	b.SetString("fresh")
	if b.Undo() {
		t.Fatal("history survived SetString")
	}
	typeRun(&b, "x", 50)
	b.Reset()
	if b.Undo() {
		t.Fatal("history survived Reset")
	}
}

func TestUndoLimits(t *testing.T) {
	t.Run("steps", func(t *testing.T) {
		var b TextBuffer
		b.SetUndoLimit(3, 1<<20)
		b.now = 1e9
		for i := 0; i < 6; i++ {
			b.now += 2e9
			b.InsertString(string(rune('a' + i)))
		}
		n := 0
		for b.Undo() {
			n++
		}
		if n != 3 || b.String() != "abc" {
			t.Fatalf("%d steps undone, text %q", n, b.String())
		}
	})
	t.Run("bytes", func(t *testing.T) {
		var b TextBuffer
		b.SetUndoLimit(100, 10)
		b.now = 1e9
		for i := 0; i < 5; i++ {
			b.now += 2e9
			b.InsertString("abcd")
		}
		n := 0
		for b.Undo() {
			n++
		}
		if n != 2 || b.Len() != 12 { // two 4-byte steps fit in 10 bytes
			t.Fatalf("%d steps undone, %d bytes left", n, b.Len())
		}
		// An edit larger than the limit cannot be undone, nor can the ones before it.
		b.now += 2e9
		b.InsertString("xy")
		b.now += 2e9
		b.InsertString(strings.Repeat("z", 11))
		if b.Undo() {
			t.Fatal("an edit over the byte limit was undoable")
		}
	})
	t.Run("off", func(t *testing.T) {
		var b TextBuffer
		b.SetUndoLimit(0, 0)
		b.InsertString("abc")
		if b.Undo() {
			t.Fatal("undo with the history off")
		}
	})
	t.Run("ring wraps", func(t *testing.T) {
		// Many more steps than the byte bound holds: the ring wraps around and
		// every step still restores exactly its text.
		var b TextBuffer
		b.SetUndoLimit(1000, 64)
		b.now = 1e9
		var want []string
		for i := 0; i < 200; i++ {
			b.now += 2e9
			b.InsertString(strings.Repeat(string(rune('a'+i%26)), 1+i%7))
			want = append(want, b.String())
		}
		undone := 0
		for i := len(want) - 2; b.Undo(); i-- {
			if i < 0 {
				if b.String() != "" {
					t.Fatalf("last undo left %q", b.String())
				}
			} else if b.String() != want[i] {
				t.Fatalf("undo %d: %q, want %q", undone, b.String(), want[i])
			}
			undone++
		}
		if undone < 5 || undone > 64 {
			t.Fatalf("%d steps kept for a 64-byte bound", undone)
		}
	})
}

// A full history reuses its storage: typing, pausing, deleting, cutting and
// undoing allocate nothing.
func TestUndoZeroAllocWhenFull(t *testing.T) {
	var b TextBuffer
	b.SetString("some text to edit")
	b.now = 1e9
	cycle := func() {
		for i := 0; i < 4; i++ {
			b.now += 2e9
			b.InsertString("ab")
			b.InsertString("c")
			b.now += 2e9
			b.deleteRange(b.caret-2, b.caret)
			b.breakRun()
			b.SetSelection(0, 3)
			b.deleteSelection()
			b.breakRun()
			b.insertSealed([]byte("xyz"))
		}
		for i := 0; i < 3; i++ {
			b.Undo()
		}
		for i := 0; i < 3; i++ {
			b.Redo()
		}
	}
	for i := 0; i < 60; i++ { // reach the bounds
		cycle()
	}
	if n := testing.AllocsPerRun(50, cycle); n != 0 {
		t.Fatalf("%v allocs per cycle with a full history", n)
	}
}

// Lowering the limit while steps are undone must not drop the steps that redo
// needs first.
func TestSetUndoLimitKeepsRedoPrerequisites(t *testing.T) {
	var b TextBuffer
	b.now = 1e9
	b.InsertString("a")
	b.SetCaret(1) // ends the run
	b.InsertString("b")
	if !b.Undo() || !b.Undo() || b.String() != "" {
		t.Fatalf("undo twice: %q", b.String())
	}
	b.SetUndoLimit(1, 1024)
	if !b.Redo() || b.String() != "a" {
		t.Fatalf("redo after SetUndoLimit: %q", b.String())
	}
	if b.Redo() {
		t.Fatal("the dependent step was kept")
	}
	if !b.Undo() || b.String() != "" {
		t.Fatalf("undo: %q", b.String())
	}
}

// A model of the history: states to go back to and states to go forward to.
type undoModel struct {
	cur         string
	back, ahead []string // back: oldest first; ahead: the next redo last
	steps       int
}

func (m *undoModel) edit(s string) {
	m.ahead = m.ahead[:0]
	for len(m.back) >= m.steps {
		m.back = m.back[1:]
	}
	m.back = append(m.back, m.cur)
	m.cur = s
}

func (m *undoModel) undo() bool {
	if len(m.back) == 0 {
		return false
	}
	m.ahead = append(m.ahead, m.cur)
	m.cur, m.back = m.back[len(m.back)-1], m.back[:len(m.back)-1]
	return true
}

func (m *undoModel) redo() bool {
	if len(m.ahead) == 0 {
		return false
	}
	m.back = append(m.back, m.cur)
	m.cur, m.ahead = m.ahead[len(m.ahead)-1], m.ahead[:len(m.ahead)-1]
	return true
}

func (m *undoModel) limit(steps int) {
	m.steps = steps
	for len(m.back)+len(m.ahead) > steps {
		if len(m.back) > 0 {
			m.back = m.back[1:]
		} else {
			m.ahead = m.ahead[1:] // the farthest redo
		}
	}
}

func TestUndoRedoLimitAgainstModel(t *testing.T) {
	seed := uint64(88172645463325252)
	rnd := func(n int) int {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		return int(seed % uint64(n))
	}
	for trial := 0; trial < 300; trial++ {
		var b TextBuffer
		b.now = 1e9
		m := &undoModel{steps: defaultUndoSteps}
		for op := 0; op < 60; op++ {
			switch k := rnd(10); {
			case k < 4: // an edit of its own: append a letter
				b.SetCaret(b.Len())
				b.InsertString(string(rune('a' + rnd(26))))
				m.edit(b.String())
			case k < 6:
				if got, want := b.Undo(), m.undo(); got != want {
					t.Fatalf("trial %d op %d: undo = %v, model %v", trial, op, got, want)
				}
			case k < 8:
				if got, want := b.Redo(), m.redo(); got != want {
					t.Fatalf("trial %d op %d: redo = %v, model %v", trial, op, got, want)
				}
			default:
				n := 1 + rnd(6)
				b.SetUndoLimit(n, 1<<20)
				m.limit(n)
			}
			if b.String() != m.cur {
				t.Fatalf("trial %d op %d: text %q, model %q", trial, op, b.String(), m.cur)
			}
		}
		// drain: everything the model can undo and redo, the buffer can
		for m.undo() {
			if !b.Undo() || b.String() != m.cur {
				t.Fatalf("trial %d: draining undo: %q, model %q", trial, b.String(), m.cur)
			}
		}
		for m.redo() {
			if !b.Redo() || b.String() != m.cur {
				t.Fatalf("trial %d: draining redo: %q, model %q", trial, b.String(), m.cur)
			}
		}
		if b.Redo() {
			t.Fatalf("trial %d: redo beyond the model", trial)
		}
	}
}
