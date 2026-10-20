//go:build darwin

package platform

import (
	"strings"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The key bindings. AppKit turns a key into a selector with the user's
// bindings (the standard ones, ~/Library/KeyBindings/DefaultKeyBinding.dict,
// the Emacs keys of ⌃A, ⌃E, ⌃K and ⌃T) and calls doCommandBySelector:. While
// a text field has focus (SetTextInput) the selectors of the standard set
// below become EvEdit and the key's own EvKey goes; a selector outside it
// (insertNewline:, insertTab:, cancelOperation:, scrollPageUp: …) leaves the
// key as an EvKey, as it is without a field.

// editSelector is one row of the table: a selector and the command it means.
type editSelector struct {
	sel    objc.SEL
	cmd    EditCmd
	extend bool // the ...AndModifySelection: form
}

var editSelectors []editSelector

func init() {
	for _, r := range []struct {
		name string
		cmd  EditCmd
	}{
		{"moveLeft:", EditMoveLeft}, {"moveBackward:", EditMoveLeft},
		{"moveRight:", EditMoveRight}, {"moveForward:", EditMoveRight},
		{"moveUp:", EditMoveUp}, {"moveDown:", EditMoveDown},
		{"moveWordLeft:", EditMoveWordLeft}, {"moveWordBackward:", EditMoveWordLeft},
		{"moveWordRight:", EditMoveWordRight}, {"moveWordForward:", EditMoveWordRight},
		{"moveToBeginningOfLine:", EditMoveLineStart}, {"moveToLeftEndOfLine:", EditMoveLineStart},
		{"moveToEndOfLine:", EditMoveLineEnd}, {"moveToRightEndOfLine:", EditMoveLineEnd},
		{"moveToBeginningOfParagraph:", EditMoveParagraphStart}, {"moveToEndOfParagraph:", EditMoveParagraphEnd},
		{"moveParagraphBackward:", EditMoveParagraphBackward}, {"moveParagraphForward:", EditMoveParagraphForward},
		{"moveToBeginningOfDocument:", EditMoveDocumentStart}, {"moveToEndOfDocument:", EditMoveDocumentEnd},
	} {
		editSelectors = append(editSelectors, editSelector{objc.Sel(r.name), r.cmd, false})
		// moveLeft: → moveLeftAndModifySelection:
		sel := strings.TrimSuffix(r.name, ":") + "AndModifySelection:"
		editSelectors = append(editSelectors, editSelector{objc.Sel(sel), r.cmd, true})
	}
	for _, r := range []struct {
		name string
		cmd  EditCmd
	}{
		{"deleteBackward:", EditDeleteBackward}, {"deleteBackwardByDecomposingPreviousCharacter:", EditDeleteBackward},
		{"deleteForward:", EditDeleteForward},
		{"deleteWordBackward:", EditDeleteWordBackward}, {"deleteWordForward:", EditDeleteWordForward},
		{"deleteToBeginningOfLine:", EditDeleteToLineStart}, {"deleteToEndOfLine:", EditDeleteToLineEnd},
		{"deleteToBeginningOfParagraph:", EditDeleteToParagraphStart}, {"deleteToEndOfParagraph:", EditDeleteToParagraphEnd},
		{"transpose:", EditTranspose},
	} {
		editSelectors = append(editSelectors, editSelector{objc.Sel(r.name), r.cmd, false})
	}
}

// editFor is the command selector sel means in the standard set, and whether
// it extends the selection.
func editFor(sel objc.SEL) (cmd EditCmd, extend, ok bool) {
	for i := range editSelectors {
		if editSelectors[i].sel == sel {
			return editSelectors[i].cmd, editSelectors[i].extend, true
		}
	}
	return EditNone, false, false
}

// viewDoCommand is doCommandBySelector:, which interpretKeyEvents: calls for a
// key the input method left alone and the key bindings gave a selector. With
// a text field focused, a selector of the standard set becomes EvEdit; any
// other is swallowed (it would beep), remembering that the key was handed back
// as a command so that it stays an EvKey.
func viewDoCommand(c *ffi.CB) {
	w := theWindow.Load()
	if w == nil {
		return
	}
	if w.textInput {
		if cmd, extend, ok := editFor(objc.SEL(c.Int(2))); ok {
			e := Event{Kind: EvEdit, Tag: uint64(cmd), Time: Now()}
			if extend {
				e.Flags = FlagSelect
			}
			w.q.push(e)
			w.editTaken = true
			return
		}
	}
	w.cmdCalled = true
}
