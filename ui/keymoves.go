package ui

import "gonativeui/text"

// The commands of the system's key bindings (EditMoveLeft … EditTranspose),
// applied to a text field's buffer exactly as the key that is bound to the
// same thing is (editKeys), plus the ones no key of ours was: the Emacs
// bindings ⌃A ⌃E ⌃K ⌃T and the rest of the standard set. A field is one line,
// so a line, a paragraph and the document are the same text.

// applyBinding does one command of the system's key bindings; a is
// EditMove… or EditDelete…, with EditExtend for a move that extends the
// selection. The commands that are not one of those do nothing.
func (c *Context) applyBinding(buf *TextBuffer, a EditAction) {
	base, extend := a.Base()
	b := buf.b
	to := -1
	switch base {
	case EditMoveLeft, EditMoveRight:
		fwd := base == EditMoveRight
		switch {
		case buf.HasSelection() && !extend:
			lo, hi := buf.Selection()
			to = lo
			if fwd {
				to = hi
			}
		case fwd:
			to = text.NextGrapheme(b, buf.caret)
		default:
			to = text.PrevGrapheme(b, buf.caret)
		}
	case EditMoveWordLeft:
		to = wordLeft(b, buf.caret)
	case EditMoveWordRight:
		to = wordRight(b, buf.caret, !macKeys)
	case EditMoveLineStart, EditMoveParagraphStart, EditMoveDocumentStart, EditMoveUp:
		to = 0
	case EditMoveLineEnd, EditMoveParagraphEnd, EditMoveDocumentEnd, EditMoveDown:
		to = len(b)
	}
	if to >= 0 {
		buf.moveTo(to, extend)
		return
	}
	switch base {
	case EditDeleteBackward, EditDeleteForward, EditDeleteWordBackward, EditDeleteWordForward,
		EditDeleteToLineStart, EditDeleteToLineEnd, EditDeleteToParagraphStart, EditDeleteToParagraphEnd:
		if buf.deleteSelection() {
			return
		}
		lo, hi := buf.caret, buf.caret
		switch base {
		case EditDeleteBackward:
			lo = text.PrevGrapheme(b, buf.caret)
		case EditDeleteForward:
			hi = text.NextGrapheme(b, buf.caret)
		case EditDeleteWordBackward:
			lo = wordLeft(b, buf.caret)
		case EditDeleteWordForward:
			hi = wordRight(b, buf.caret, false)
		case EditDeleteToLineStart, EditDeleteToParagraphStart:
			lo = 0
		default:
			hi = len(b)
		}
		buf.deleteRange(lo, hi)
	case EditTranspose:
		c.transpose(buf)
	}
}

// transpose swaps the characters either side of the caret and puts the caret
// after both; at the end of the text it swaps the last two, and at the start,
// or with a selection, it does nothing (NSTextView's transpose:).
func (c *Context) transpose(buf *TextBuffer) {
	b := buf.b
	if buf.HasSelection() || buf.caret == 0 {
		return
	}
	at := buf.caret
	if at == len(b) {
		at = text.PrevGrapheme(b, at)
		if at == 0 {
			return
		}
	}
	lo, hi := text.PrevGrapheme(b, at), text.NextGrapheme(b, at)
	var tmp [32]byte
	swapped := append(tmp[:0], b[at:hi]...)
	swapped = append(swapped, b[lo:at]...)
	buf.breakRun()
	buf.replace(lo, hi, swapped)
	buf.breakRun()
}
