package platform

// EditCmd is a text editing command the system's key bindings turned a key
// into (EvEdit, Event.Tag). The set is the standard one every Cocoa text view
// answers (NSStandardKeyBindingResponding); FlagSelect on the event says the
// command extends the selection (the ...AndModifySelection: forms) instead of
// moving the caret. A command that only moves has no effect on the text.
type EditCmd uint8

const (
	// EditNone is no command (the zero value).
	EditNone EditCmd = iota
	// EditMoveLeft and EditMoveRight move by one character cluster (←, →, ⌃B, ⌃F),
	// EditMoveUp and EditMoveDown by one line (↑, ↓, ⌃P, ⌃N).
	EditMoveLeft
	EditMoveRight
	EditMoveUp
	EditMoveDown
	// EditMoveWordLeft and EditMoveWordRight move by word (⌥←, ⌥→).
	EditMoveWordLeft
	EditMoveWordRight
	// EditMoveLineStart and EditMoveLineEnd go to the ends of the visual line (⌘←, ⌘→).
	EditMoveLineStart
	EditMoveLineEnd
	// EditMoveParagraphStart and EditMoveParagraphEnd go to the ends of the
	// paragraph (⌃A, ⌃E); EditMoveParagraphBackward and EditMoveParagraphForward
	// move to the previous or next paragraph boundary (⌥↑, ⌥↓).
	EditMoveParagraphStart
	EditMoveParagraphEnd
	EditMoveParagraphBackward
	EditMoveParagraphForward
	// EditMoveDocumentStart and EditMoveDocumentEnd go to the ends of the text (⌘↑, ⌘↓, Home, End).
	EditMoveDocumentStart
	EditMoveDocumentEnd
	// EditDeleteBackward and EditDeleteForward delete a character cluster
	// (Backspace, Delete, ⌃H, ⌃D); the selection if there is one.
	EditDeleteBackward
	EditDeleteForward
	// EditDeleteWordBackward and EditDeleteWordForward delete a word (⌥Backspace, ⌥Delete).
	EditDeleteWordBackward
	EditDeleteWordForward
	// EditDeleteToLineStart and EditDeleteToLineEnd delete to the ends of the
	// line (⌘Backspace); EditDeleteToParagraphStart and EditDeleteToParagraphEnd to
	// the ends of the paragraph (⌃K).
	EditDeleteToLineStart
	EditDeleteToLineEnd
	EditDeleteToParagraphStart
	EditDeleteToParagraphEnd
	// EditTranspose swaps the characters either side of the caret (⌃T).
	EditTranspose
	numEditCmds
)
