package ui

import (
	"bytes"
	"unicode/utf8"
)

// FieldCommand is a command a component can take from the focused text field
// before the field acts on it. The set follows AppKit's command selectors
// (doCommandBySelector: of NSTextView, which a native composer sends the same
// way): move up and down, page up and down, insert newline, cancel, insert tab
// and backtab, delete backward and forward, and the insertion of a separator
// character.
type FieldCommand uint8

const (
	// FieldNone is no command.
	FieldNone FieldCommand = iota
	// FieldMoveUp and FieldMoveDown: moveUp: and moveDown: (Up, Down; not
	// extending the selection).
	FieldMoveUp
	FieldMoveDown
	// FieldMoveLeft and FieldMoveRight: moveLeft: and moveRight: (not
	// extending the selection).
	FieldMoveLeft
	FieldMoveRight
	// FieldPageUp and FieldPageDown: scrollPageUp: and scrollPageDown:.
	FieldPageUp
	FieldPageDown
	// FieldInsertNewline: insertNewline: (Enter).
	FieldInsertNewline
	// FieldCancel: cancelOperation: (Escape).
	FieldCancel
	// FieldInsertTab and FieldInsertBacktab: insertTab: and insertBacktab:
	// (Tab, Shift+Tab). A commander that returns false leaves the key to
	// focus traversal.
	FieldInsertTab
	FieldInsertBacktab
	// FieldDeleteBackward: deleteBackward: (Backspace, or the system binding
	// for it), wherever the caret is and with or without a selection; a
	// commander that only cares about the edge reads the buffer's caret.
	FieldDeleteBackward
	// FieldDeleteForward: deleteForward: (Delete), likewise.
	FieldDeleteForward
	// FieldInsertSeparator: one of the separator characters is being inserted
	// at the caret; the rune is the argument. The text before it, in the same
	// batch of typed text, is already in the buffer.
	FieldInsertSeparator
)

// FieldCommander takes commands from a text field. A component implements it
// on a pointer to its own state (passing it costs no allocation) and registers
// it for one call of TextField with Context.NextFieldCommander.
//
// FieldCommand returns true when it handled the command: the field then does
// nothing with it (a key is claimed, a separator is not inserted). Returning
// false passes the command to the field as if there were no commander. r is
// the separator for FieldInsertSeparator and 0 otherwise.
type FieldCommander interface {
	FieldCommand(cmd FieldCommand, r rune) bool
}

// NextFieldCommander registers cmdr for the next TextField. It is offered the
// focused field's commands inside the field's ordered processing, in the order
// they arrived: the frame's keys and edit actions (Input.EditAfter), each once,
// after composition is handled; typed text, split at the characters of
// separators, is offered a FieldInsertSeparator for each. While the input
// method has marked text nothing is offered (it gets Escape, Return and
// Backspace). Commands are offered only to the field that owns the frame's
// input (see TextField); a nil cmdr, or an empty separators, offers none of
// that kind.
func (c *Context) NextFieldCommander(cmdr FieldCommander, separators string) {
	c.nextCmdr, c.nextSeps = cmdr, separators
}

// keyCommand is the command for key event e, when the key has one: without
// modifiers (Shift+Tab is the backtab).
func keyCommand(e *KeyEvent) FieldCommand {
	if e.Mods&^ModShift != 0 {
		return FieldNone
	}
	if e.Code == KeyTab {
		if e.Mods == ModShift {
			return FieldInsertBacktab
		}
		return FieldInsertTab
	}
	if e.Mods != 0 {
		return FieldNone
	}
	switch e.Code {
	case KeyUp:
		return FieldMoveUp
	case KeyDown:
		return FieldMoveDown
	case KeyLeft:
		return FieldMoveLeft
	case KeyRight:
		return FieldMoveRight
	case KeyPageUp:
		return FieldPageUp
	case KeyPageDown:
		return FieldPageDown
	case KeyEnter:
		return FieldInsertNewline
	case KeyEscape:
		return FieldCancel
	case KeyBackspace:
		return FieldDeleteBackward
	case KeyDelete:
		return FieldDeleteForward
	}
	return FieldNone
}

// editCommand is the command for edit action a (what the system's bindings
// deliver on macOS), when it has one.
func editCommand(a EditAction) FieldCommand {
	switch a {
	case EditMoveUp:
		return FieldMoveUp
	case EditMoveDown:
		return FieldMoveDown
	case EditMoveLeft:
		return FieldMoveLeft
	case EditMoveRight:
		return FieldMoveRight
	case EditDeleteBackward:
		return FieldDeleteBackward
	case EditDeleteForward:
		return FieldDeleteForward
	}
	return FieldNone
}

// insertTyped inserts p at the caret; with a commander and separators it
// offers each separator character as FieldInsertSeparator at the place it is
// inserted, and inserts it only if the commander passes. sealed makes the
// insertion undo steps of their own (pasted text).
func (c *Context) insertTyped(buf *TextBuffer, p []byte, sealed bool, cm FieldCommander, seps string) {
	ins := buf.Insert
	if sealed {
		ins = buf.insertSealed
	}
	if cm == nil || seps == "" {
		ins(p)
		return
	}
	for len(p) > 0 {
		i := bytes.IndexAny(p, seps)
		if i < 0 {
			ins(p)
			return
		}
		if i > 0 {
			ins(p[:i])
		}
		r, n := utf8.DecodeRune(p[i:])
		if !cm.FieldCommand(FieldInsertSeparator, r) {
			ins(p[i : i+n])
		}
		p = p[i+n:]
	}
}

// Remove deletes the bytes [lo, hi) of the text and puts the caret at lo (the
// clamped, rune-aligned range of an edit; an empty range does nothing).
func (t *TextBuffer) Remove(lo, hi int) {
	lo, hi = t.snap(max(lo, 0)), t.snap(min(hi, len(t.b)))
	t.deleteRange(lo, hi)
}
