package platform

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

// The byte/UTF-16 map against utf16.Encode, over texts with surrogate pairs,
// ZWJ sequences, combining marks and Arabic, short and across many
// checkpoints, at every byte offset and every unit.
func TestEditorMapping(t *testing.T) {
	for _, s := range []string{
		"",
		"a",
		"a👍🏽b👨‍👩‍👧c😀",
		"e\u0301te\u0301 n\u0303o",
		"مرحبا بالعالم!",
		strings.Repeat("é😀مa", 100),
		strings.Repeat("x", 63) + "😀" + strings.Repeat("y", 64) + "é",
	} {
		b := []byte(s)
		var m u16map
		m.build(b)
		units := func(off int) int { return len(utf16.Encode([]rune(s[:off]))) }
		if m.n16 != units(len(s)) {
			t.Errorf("%q: n16 %d, want %d", s, m.n16, units(len(s)))
		}
		for off := 0; off <= len(b); off++ {
			u, err := m.toU16(b, off)
			if !boundary(b, off) {
				if !errors.Is(err, ErrNotBoundary) {
					t.Errorf("%q: toU16(%d) inside a scalar: %d, %v", s, off, u, err)
				}
				continue
			}
			if err != nil || u != units(off) {
				t.Errorf("%q: toU16(%d) = %d, %v; want %d", s, off, u, err, units(off))
			}
			if back := m.toByte(b, u); back != off {
				t.Errorf("%q: toByte(toU16(%d)) = %d", s, off, back)
			}
		}
		for u := 0; u <= m.n16+1; u++ {
			off := m.toByte(b, u)
			if !boundary(b, off) || units(off) > u || (u <= m.n16 && units(off) < u-1) {
				t.Errorf("%q: toByte(%d) = %d (%d units)", s, u, off, units(off))
			}
		}
	}
}

// The checkpoint table is built without allocating once reserved.
func TestEditorMapBuildAllocatesNothing(t *testing.T) {
	var e Editor
	e.Reserve(4096)
	text := []byte(strings.Repeat("é😀مa", 200))
	e.text = append(e.text[:0], text...)
	if n := testing.AllocsPerRun(20, func() { e.m.build(e.text) }); n != 0 {
		t.Errorf("build allocates %.0f times", n)
	}
}

// Replace's checks: boundaries of the text and of the result, the maximum
// length, the composition.
func TestEditorReplaceChecks(t *testing.T) {
	e := &Editor{}
	e.text = []byte("a😀b")
	e.m.build(e.text)
	for _, c := range []struct {
		r    TextRange
		text string
		sel  TextRange
		want error
	}{
		{TextRange{0, 1}, "@Paolo ", TextRange{7, 7}, nil},
		{TextRange{1, 5}, "", TextRange{1, 1}, nil},
		{TextRange{2, 5}, "", TextRange{1, 1}, ErrNotBoundary},       // inside 😀
		{TextRange{1, 5}, "é", TextRange{2, 2}, ErrNotBoundary},      // the selection inside é
		{TextRange{1, 5}, "é", TextRange{3, 4}, nil},                 // after é, then b
		{TextRange{0, 0}, "\xff", TextRange{0, 0}, ErrNotBoundary},   // not UTF-8
		{TextRange{5, 7}, "", TextRange{0, 0}, ErrNotBoundary},       // past the end
		{TextRange{3, 1}, "", TextRange{0, 0}, ErrNotBoundary},       // reversed
		{TextRange{6, 6}, "x", TextRange{8, 8}, ErrNotBoundary},      // the selection past the end
		{TextRange{6, 6}, "x", TextRange{7, 7}, nil},                 // at the end
		{TextRange{0, 6}, "", TextRange{0, 0}, nil},                  // everything
		{TextRange{1, 1}, "", TextRange{3, 3}, ErrNotBoundary},       // inside 😀 of the result
		{TextRange{1, 1}, "", TextRange{5, 5}, nil},                  // after 😀 of the result
		{TextRange{0, 0}, "abcd", TextRange{0, 0}, ErrTooLong},       // with MaxLen 9
		{TextRange{0, 1}, "abcd", TextRange{0, 0}, nil},              // 9 bytes
		{TextRange{0, 1}, "abcd", TextRange{0, 0}, ErrComposing},     // while composing
		{TextRange{0, 0}, "", TextRange{-1, 0}, ErrNotBoundary},      // a negative selection
		{TextRange{-1, 0}, "", TextRange{0, 0}, ErrNotBoundary},      // a negative range
		{TextRange{0, 1}, "ab", TextRange{2, 1}, ErrNotBoundary},     // a reversed selection
		{TextRange{0, 1}, "ab", TextRange{0, 2}, nil},                // the replacement selected
		{TextRange{0, 1}, "😀", TextRange{0, 4}, nil},                 // a pair selected
		{TextRange{0, 1}, "😀", TextRange{0, 2}, ErrNotBoundary},      // half of one
		{TextRange{5, 6}, "c", TextRange{5, 6}, nil},                 // the last byte
		{TextRange{1, 5}, "\u0301", TextRange{1, 3}, nil},            // a lone combining mark is a scalar
		{TextRange{1, 5}, "\u0301", TextRange{2, 3}, ErrNotBoundary}, // inside it
	} {
		e.maxLen, e.marked = 0, false
		if c.want == ErrTooLong || c.text == "abcd" {
			e.maxLen = 9
		}
		if c.want == ErrComposing {
			e.marked = true
		}
		if err := e.checkReplace(c.r, []byte(c.text), c.sel); !errors.Is(err, c.want) {
			t.Errorf("checkReplace(%v, %q, %v) = %v, want %v", c.r, c.text, c.sel, err, c.want)
		}
	}
}

// What an Enter would have done goes with its command, in order, through the
// ring's wrap-around.
func TestEditorCommandSends(t *testing.T) {
	e := &Editor{}
	for round := 0; round < 3; round++ {
		for i := 0; i < maxCommands-1; i++ {
			e.pushCommandSends(CmdAccept, i%2 == 0)
		}
		e.pushCommand(CmdDown)
		for i := 0; i < maxCommands-1; i++ {
			if c, sends := e.TakeCommandSends(); c != CmdAccept || sends != (i%2 == 0) {
				t.Fatalf("round %d, command %d: %d, sends %v", round, i, c, sends)
			}
		}
		if c, sends := e.TakeCommandSends(); c != CmdDown || sends {
			t.Fatalf("round %d: %d, sends %v after the Enters", round, c, sends)
		}
	}
}

// A command carries the Rev the editor had at the key, so that a change after
// it can be told from one before it.
func TestEditorCommandRev(t *testing.T) {
	e := &Editor{}
	e.pushCommand(CmdAccept)
	e.rev += 2
	e.pushCommandSends(CmdAccept, true)
	if _, _, r := e.TakeCommandRev(); r != 0 {
		t.Errorf("first command stamped %d, want 0", r)
	}
	if _, sends, r := e.TakeCommandRev(); r != 2 || !sends {
		t.Errorf("second command stamped %d (sends %v), want 2", r, sends)
	}
}

// Completion commands queue in order and drop when the queue is full; ending
// the completion drops them.
func TestEditorCommands(t *testing.T) {
	e := &Editor{}
	e.SetCompletionActive(true)
	for i := 0; i < maxCommands+3; i++ {
		e.pushCommand(EditorCommand(i%6 + 1))
	}
	for i := 0; i < maxCommands; i++ {
		if c := e.TakeCommand(); c != EditorCommand(i%6+1) {
			t.Fatalf("command %d is %d", i, c)
		}
	}
	if c := e.TakeCommand(); c != CmdNone {
		t.Errorf("an empty queue gives %d", c)
	}
	e.pushCommand(CmdDown)
	e.SetCompletionActive(false)
	if c := e.TakeCommand(); c != CmdNone {
		t.Errorf("a command survived the end of the completion: %d", c)
	}
	e.marked = true
	e.SetCompletionActive(true)
	if e.CompletionActive() {
		t.Error("a completion started while composing")
	}
}
