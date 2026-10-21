package ui

import (
	"gonativeui/core"
	"gonativeui/text"
)

// Secure text fields (password and lock-code entry), as NSSecureTextField
// behaves. The text stays in the app's TextBuffer and nowhere else that ui
// owns: the field draws, shapes, hit-tests and exports one bullet per
// character cluster, never the text, so the draw list, the shaper's runs, the
// debug inspector and the semantic tree hold bullets only. Copy and cut do
// nothing (paste works), the buffer keeps no undo history, word moves and
// double clicks treat the text as one word (where the words break would tell
// where its spaces are), and storage the text moves out of is zeroed.

// bullet is what a secure field shows for each character cluster (U+2022).
const bullet = "\u2022"

// NextSecureField makes the next TextField a secure field: it shows a bullet
// for each character cluster and never the text, copies and cuts nothing
// (paste works, select all too), keeps no undo history of buf (what buf had
// is dropped when the field first edits it), treats the whole text as one word,
// shows an input method's composition as bullets too, and reports SemSecure
// with bullets for a value to assistive technologies. While it has the focus
// in a focused window SecureFieldFocused is true: the app turns the system's
// secure keyboard entry on then (platform.Window.SetSecureTextInput). The app
// reads the text from buf as with any field, and wipes it with
// TextBuffer.Wipe when done.
func (c *Context) NextSecureField() { c.nextSecure = true }

// SecureFieldFocused reports, after the frame was built, that a secure field
// (NextSecureField) has the keyboard focus in a window that has it: the app
// asks for secure keyboard entry while it does, and the native Edit menu's
// Copy and Cut are disabled.
func (c *Context) SecureFieldFocused() bool { return c.secureOn }

// Wipe zeroes the text and every copy of it the buffer holds (its storage,
// the input waiting to be inserted, the undo history and its scratch) and
// empties it, with the caret at the start: for a password or a key, once it
// has been used. Copies the app made (String, a slice of Bytes kept) are the
// app's to clear.
func (t *TextBuffer) Wipe() {
	clear(t.b[:cap(t.b)])
	clear(t.tmp[:cap(t.tmp)])
	t.undo.wipe()
	t.b, t.tmp = t.b[:0], t.tmp[:0]
	t.caret, t.anchor, t.scroll = 0, 0, 0
	t.drag = dragNone
	t.rev++
}

// setSecure marks the buffer as edited by a secure field or not; becoming
// secure drops (and zeroes) the undo history it had and whatever earlier
// edits left in its spare storage.
func (t *TextBuffer) setSecure(on bool) {
	if on && !t.secure {
		t.undo.wipe()
		clear(t.b[len(t.b):cap(t.b)])
		clear(t.tmp[:cap(t.tmp)])
	}
	t.secure = on
}

// scrub zeroes a secure field's input once it has been inserted.
func (t *TextBuffer) scrub() {
	if t.secure {
		clear(t.tmp)
	}
}

// wipe zeroes the history's text and empties it.
func (u *undoLog) wipe() {
	clear(u.ring)
	clear(u.scratch[:cap(u.scratch)])
	u.clear()
}

// wordLeft, wordRight and wordBounds are the package's word functions over the
// buffer's text; for a secure field the whole text is one word.
func (t *TextBuffer) wordLeft(off int) int {
	if t.secure {
		return 0
	}
	return wordLeft(t.b, off)
}

func (t *TextBuffer) wordRight(off int, start bool) int {
	if t.secure {
		return len(t.b)
	}
	return wordRight(t.b, off, start)
}

func (t *TextBuffer) wordBounds(off int) (lo, hi int) {
	if t.secure {
		return 0, len(t.b)
	}
	return wordBounds(t.b, off)
}

// appendMasked appends a bullet for each character cluster of b to a.
func appendMasked(a *core.Arena, b []byte) {
	for i := 0; i < len(b); i = text.NextGrapheme(b, i) {
		a.AppendString(bullet)
	}
}

// maskedOff is byte offset off of b in the text that shows (secure: b's
// bullets, see appendMasked; otherwise b itself).
func maskedOff(secure bool, b []byte, off int) int {
	if !secure {
		return off
	}
	n := 0
	for i := 0; i < off && i < len(b); i = text.NextGrapheme(b, i) {
		n++
	}
	return n * len(bullet)
}

// unmaskOff is the byte offset of b that offset m of b's bullets stands for:
// the start of the cluster of bullet m.
func unmaskOff(b []byte, m int) int {
	i := 0
	for n := m / len(bullet); n > 0 && i < len(b); n-- {
		i = text.NextGrapheme(b, i)
	}
	return i
}
