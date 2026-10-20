package ui

import (
	"slices"
	"testing"
)

// cmdLog is a commander that records what it is offered and answers by a
// table; the buffer it can read is the field's.
type cmdLog struct {
	got    []FieldCommand
	seps   []rune
	handle map[FieldCommand]bool
	buf    *TextBuffer
	onSep  func(l *cmdLog, r rune) bool
}

func (l *cmdLog) FieldCommand(cmd FieldCommand, r rune) bool {
	l.got = append(l.got, cmd)
	if cmd == FieldInsertSeparator {
		l.seps = append(l.seps, r)
		if l.onSep != nil {
			return l.onSep(l, r)
		}
	}
	return l.handle[cmd]
}

// The system's key bindings arrive on macOS as edit actions, not keys: Down
// is EditMoveDown and Backspace EditDeleteBackward. A commander is offered
// them, and when it handles one the field does not act on it.
func TestFieldCommanderTakesTheMacEditPath(t *testing.T) {
	r := newRig(t, true)
	l := &cmdLog{handle: map[FieldCommand]bool{FieldMoveDown: true, FieldDeleteBackward: true}}
	r.cmdr = l
	r.buf.SetString("ab")
	r.buf.SetCaret(0)
	r.edit(EditMoveDown) // handled: the caret does not go to the end
	r.edit(EditDeleteBackward)
	if want := []FieldCommand{FieldMoveDown, FieldDeleteBackward}; !slices.Equal(l.got, want) {
		t.Fatalf("offered %v, want %v", l.got, want)
	}
	if r.state() != "|ab" {
		t.Errorf("a handled command moved or deleted: %q", r.state())
	}
	// Not at the start: Backspace is the field's, not offered.
	l.got = nil
	r.buf.SetCaret(1)
	r.edit(EditDeleteBackward)
	if len(l.got) != 0 || r.state() != "|b" {
		t.Errorf("Backspace inside the text: offered %v, state %q", l.got, r.state())
	}
	// A pass is the field's: the same command acts.
	l.handle = nil
	r.buf.SetCaret(0)
	r.edit(EditMoveDown)
	if r.state() != "b|" {
		t.Errorf("a passed EditMoveDown: %q", r.state())
	}
}

// A frame's keys and edit actions are offered in the order they arrived, each
// once, and what the commander handled reaches nothing else.
func TestFieldCommanderSeesABatchInOrder(t *testing.T) {
	r := newRig(t, true)
	l := &cmdLog{handle: map[FieldCommand]bool{FieldMoveDown: true, FieldMoveUp: true, FieldInsertNewline: true, FieldCancel: true}}
	r.cmdr = l
	r.in.PushKey(KeyEvent{Code: KeyDown, Down: true})
	r.in.PushEdit(EditMoveUp)
	r.in.PushKey(KeyEvent{Code: KeyEnter, Down: true})
	r.in.PushEdit(EditMoveDown)
	r.in.PushKey(KeyEvent{Code: KeyEscape, Down: true})
	r.frame()
	want := []FieldCommand{FieldMoveDown, FieldMoveUp, FieldInsertNewline, FieldMoveDown, FieldCancel}
	if !slices.Equal(l.got, want) {
		t.Fatalf("offered %v, want %v", l.got, want)
	}
	if r.res.Submitted || r.res.Cancelled {
		t.Errorf("Enter or Escape leaked to the field: %+v", r.res)
	}
	// A pass: Enter submits as before.
	l.handle = nil
	r.key(KeyEnter, 0)
	if !r.res.Submitted {
		t.Error("a passed Enter did not submit")
	}
}

// While the input method has marked text the commander is offered nothing:
// Escape cancels the composition, Return and Backspace are the input method's.
func TestFieldCommanderIsNotOfferedDuringComposition(t *testing.T) {
	r := newRig(t, true)
	l := &cmdLog{handle: map[FieldCommand]bool{FieldCancel: true, FieldInsertNewline: true, FieldDeleteBackward: true}}
	r.cmdr, r.seps = l, ","
	r.in.SetPreedit([]byte("かな"), 3, 0)
	r.frame()
	r.in.PushKey(KeyEvent{Code: KeyEscape, Down: true})
	r.in.PushKey(KeyEvent{Code: KeyEnter, Down: true})
	r.in.PushEdit(EditDeleteBackward)
	r.frame()
	if len(l.got) != 0 {
		t.Fatalf("offered %v while composing", l.got)
	}
	if !r.c.IMECancel() {
		t.Error("Escape did not cancel the composition")
	}
}

// Tab is offered to a commander: handled, it is claimed and the focus stays;
// passed, it is left for the focus to traverse (an open combo box accepts or
// closes and still lets the focus leave).
func TestFieldCommanderTab(t *testing.T) {
	r := newRig(t, true)
	r.extra = func(c *Context) { c.Box(Clickable|Focusable, "next") }
	l := &cmdLog{handle: map[FieldCommand]bool{FieldInsertTab: true}}
	r.cmdr = l
	r.frame()
	field := r.c.FocusKey()
	r.key(KeyTab, 0)
	if !slices.Equal(l.got, []FieldCommand{FieldInsertTab}) || r.c.FocusKey() != field {
		t.Fatalf("a handled Tab: offered %v, focus moved: %v", l.got, r.c.FocusKey() != field)
	}
	l.handle, l.got = nil, nil
	r.key(KeyTab, 0)
	if !slices.Equal(l.got, []FieldCommand{FieldInsertTab}) || r.c.FocusKey() == field {
		t.Fatalf("a passed Tab: offered %v, focus moved: %v", l.got, r.c.FocusKey() != field)
	}
	r.c.SetFocus(field)
	r.frame()
	l.handle, l.got = map[FieldCommand]bool{FieldInsertBacktab: true}, nil
	r.key(KeyTab, ModShift)
	if !slices.Equal(l.got, []FieldCommand{FieldInsertBacktab}) || r.c.FocusKey() != field {
		t.Errorf("a handled Shift+Tab: offered %v, focus moved: %v", l.got, r.c.FocusKey() != field)
	}
}

// Separators are offered at the place they are inserted, each of a batch in
// turn, with the text around them kept; a separator the commander passes is
// inserted as text.
func TestFieldCommanderSeparators(t *testing.T) {
	r := newRig(t, true)
	var done []string
	l := &cmdLog{buf: &r.buf}
	l.onSep = func(l *cmdLog, sep rune) bool {
		if sep == ';' {
			return false
		}
		done = append(done, l.buf.String())
		l.buf.Remove(0, l.buf.Caret())
		return true
	}
	r.cmdr, r.seps = l, ",;"
	r.typ("a,bc,d;e,f")
	if want := []string{"a", "bc", "d;e"}; !slices.Equal(done, want) {
		t.Errorf("tokens committed %q, want %q", done, want)
	}
	if r.state() != "f|" {
		t.Errorf("the rest of the text: %q", r.state())
	}
	if want := []rune{',', ',', ';', ','}; !slices.Equal(l.seps, want) {
		t.Errorf("separators offered %q, want %q", string(l.seps), string(want))
	}
}
