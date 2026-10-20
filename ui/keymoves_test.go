package ui

import "testing"

// edit queues edit actions for the field and runs a frame.
func (r *fieldRig) edit(a ...EditAction) {
	for _, x := range a {
		r.in.PushEdit(x)
	}
	r.frame()
}

// put sets the text and the selection (anchor, caret in bytes) and runs a frame.
func (r *fieldRig) put(text string, anchor, caret int) {
	r.buf.SetString(text)
	r.buf.SetSelection(anchor, caret)
	r.frame()
}

// TestKeyBindingMoves: the commands of the system's key bindings move the
// caret and extend the selection as the keys bound to them do. state() writes
// the caret as |, a selection as <…> with | on the caret's side.
func TestKeyBindingMoves(t *testing.T) {
	cases := []struct {
		name          string
		text          string
		anchor, caret int
		do            EditAction
		want          string
	}{
		{"left", "abcd", 2, 2, EditMoveLeft, "a|bcd"},
		{"right", "abcd", 2, 2, EditMoveRight, "abc|d"},
		{"left collapses a selection to its start", "abcd", 3, 1, EditMoveLeft, "a|bcd"},
		{"right collapses a selection to its end", "abcd", 1, 3, EditMoveRight, "abc|d"},
		{"left extends", "abcd", 2, 2, EditMoveLeft.Extend(), "a|<b>cd"},
		{"right extends the other way", "abcd", 2, 1, EditMoveRight.Extend(), "ab|cd"},
		{"word left", "one two three", 7, 7, EditMoveWordLeft, "one |two three"},
		{"word right", "one two three", 3, 3, EditMoveWordRight, "one two| three"},
		{"word left extends", "one two", 7, 7, EditMoveWordLeft.Extend(), "one |<two>"},
		{"line start", "one two", 6, 6, EditMoveLineStart, "|one two"},
		{"line end extends", "one two", 4, 4, EditMoveLineEnd.Extend(), "one <two>|"},
		{"paragraph start (⌃A)", "one two", 6, 6, EditMoveParagraphStart, "|one two"},
		{"paragraph end (⌃E)", "one two", 2, 2, EditMoveParagraphEnd, "one two|"},
		{"document start", "one two", 5, 5, EditMoveDocumentStart, "|one two"},
		{"document end", "one two", 1, 1, EditMoveDocumentEnd, "one two|"},
		{"up goes to the start of a one-line field", "one two", 5, 5, EditMoveUp, "|one two"},
		{"down goes to the end", "one two", 1, 1, EditMoveDown, "one two|"},
		{"a cluster is one step", "ae\u0301b", 1, 1, EditMoveRight, "ae\u0301|b"},
	}
	for _, c := range cases {
		r := newRig(t, true)
		r.put(c.text, c.anchor, c.caret)
		r.edit(c.do)
		if got := r.state(); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestKeyBindingDeletes: the deletes remove the selection if there is one and
// otherwise what they are named for; each is one undo step.
func TestKeyBindingDeletes(t *testing.T) {
	cases := []struct {
		name          string
		text          string
		anchor, caret int
		do            EditAction
		want          string
	}{
		{"backward", "abcd", 2, 2, EditDeleteBackward, "a|cd"},
		{"forward", "abcd", 2, 2, EditDeleteForward, "ab|d"},
		{"backward takes the selection", "abcd", 1, 3, EditDeleteBackward, "a|d"},
		{"forward takes the selection", "abcd", 1, 3, EditDeleteForward, "a|d"},
		{"word backward", "one two three", 7, 7, EditDeleteWordBackward, "one | three"},
		{"word forward", "one two three", 3, 3, EditDeleteWordForward, "one| three"},
		{"to line start", "one two", 4, 4, EditDeleteToLineStart, "|two"},
		{"to line end", "one two", 3, 3, EditDeleteToLineEnd, "one|"},
		{"to paragraph end (⌃K)", "one two", 3, 3, EditDeleteToParagraphEnd, "one|"},
		{"to paragraph start", "one two", 4, 4, EditDeleteToParagraphStart, "|two"},
		{"⌃K with a selection deletes it", "one two", 0, 3, EditDeleteToParagraphEnd, "| two"},
		{"backward at the start does nothing", "abc", 0, 0, EditDeleteBackward, "|abc"},
		{"forward at the end does nothing", "abc", 3, 3, EditDeleteForward, "abc|"},
		{"backward takes a whole cluster", "ae\u0301", 4, 4, EditDeleteBackward, "a|"},
	}
	for _, c := range cases {
		r := newRig(t, true)
		r.put(c.text, c.anchor, c.caret)
		r.edit(c.do)
		if got := r.state(); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if r.text() != c.text {
			r.edit(EditUndo)
			if r.text() != c.text {
				t.Errorf("%s: undo gives %q, want %q", c.name, r.text(), c.text)
			}
		}
	}
}

// TestTranspose: ⌃T swaps the characters either side of the caret, the last
// two at the end of the text, and does nothing at the start or with a
// selection.
func TestTranspose(t *testing.T) {
	cases := []struct {
		name          string
		text          string
		anchor, caret int
		want          string
	}{
		{"inside", "abcd", 2, 2, "acb|d"},
		{"at the end", "abc", 3, 3, "acb|"},
		{"at the start", "abc", 0, 0, "|abc"},
		{"one character", "a", 1, 1, "a|"},
		{"a selection", "abc", 0, 2, "<ab>|c"},
		{"clusters", "ae\u0301x", 1, 1, "e\u0301a|x"},
		{"empty", "", 0, 0, "|"},
	}
	for _, c := range cases {
		r := newRig(t, true)
		r.put(c.text, c.anchor, c.caret)
		r.edit(EditTranspose)
		if got := r.state(); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	r := newRig(t, true)
	r.put("abcd", 2, 2)
	r.edit(EditTranspose)
	r.edit(EditUndo)
	if r.text() != "abcd" {
		t.Errorf("undo of a transpose: %q", r.text())
	}
}

// TestKeyBindingsNeedAFocusedField: the commands go to the focused field
// only, and not while an input method composes.
func TestKeyBindingsNeedAFocusedField(t *testing.T) {
	r := newRig(t, true)
	r.put("abcd", 2, 2)
	r.c.SetFocus(0)
	r.edit(EditMoveLeft, EditDeleteBackward)
	if r.state() != "ab|cd" {
		t.Errorf("an unfocused field took a command: %q", r.state())
	}
	r.focus()
	r.in.SetPreedit([]byte("あ"), 0, 3)
	r.edit(EditMoveLeft)
	if r.state() != "ab|cd" {
		t.Errorf("a composing field took a command: %q", r.state())
	}
}

// TestKeyBindingsAllocateNothing: applying commands allocates nothing.
func TestKeyBindingsAllocateNothing(t *testing.T) {
	r := newRig(t, true)
	r.put("one two three", 7, 7)
	n := testing.AllocsPerRun(40, func() {
		r.in.PushEdit(EditMoveWordLeft.Extend())
		r.in.PushEdit(EditTranspose)
		r.in.PushEdit(EditMoveWordRight)
		r.in.PushEdit(EditDeleteToParagraphEnd)
		r.frame()
		r.in.PushEdit(EditUndo)
		r.frame()
	})
	if n != 0 {
		t.Errorf("a frame with commands allocates %.1f times", n)
	}
}
