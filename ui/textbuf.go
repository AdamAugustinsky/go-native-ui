package ui

import (
	"unicode"
	"unicode/utf8"

	"gonativeui/text"
)

// TextBuffer is the state of a single-line text field: the UTF-8 text, the
// caret and the selection anchor (byte offsets, always on grapheme cluster
// boundaries), the horizontal scroll offset and the little interaction state
// the field needs between frames (drag mode, caret blink origin).
//
// The app owns it and passes it to Context.TextField every frame. A
// TextBuffer holds a few slices and no other pointers; its storage grows only
// past its high-water mark, so editing allocates nothing in steady state.
// The zero value is an empty buffer. It must not be copied once in use.
//
// The buffer keeps an undo history of the edits made to it (Undo, Redo,
// SetUndoLimit). Replacing the whole text (SetString, SetBytes, Reset) empties
// the history.
type TextBuffer struct {
	// Max is the largest text size in bytes (0 = unlimited). Input that would
	// exceed it is cut at a character boundary.
	Max int

	b             []byte
	tmp           []byte // sanitized input waiting to be inserted
	undo          undoLog
	now           int64 // Input.Time of the frame being applied (set by TextField), 0 without a clock
	caret, anchor int
	rev           uint32 // changes with every change of the text

	scroll    float32 // horizontal scroll offset of the text, pixels
	scrollAt  float32 // the scale scroll is in (Context.scale when TextField last ran), 0 before the first
	activity  int64   // Input.Time of the last interaction (caret blink origin)
	focused   bool    // the field had keyboard focus in the window's active state last frame
	composing bool    // an input-method composition was shown last frame
	secure    bool    // a secure field edits it (NextSecureField): no history, no words, old storage zeroed
	preHash   uint64  // identifies that composition (text and caret), to notice changes
	drag      dragKind
	dragLo    int // the word a word-drag started on
	dragHi    int
}

type dragKind uint8

const (
	dragNone dragKind = iota
	dragChar
	dragWord
)

// Bytes returns the text. It aliases the buffer: use it before the next edit.
func (t *TextBuffer) Bytes() []byte { return t.b }

// String returns a copy of the text.
func (t *TextBuffer) String() string { return string(t.b) }

// Len is the size of the text in bytes.
func (t *TextBuffer) Len() int { return len(t.b) }

// Rev changes whenever the text changes; compare it to notice an edit.
func (t *TextBuffer) Rev() uint32 { return t.rev }

// Reset empties the text.
func (t *TextBuffer) Reset() {
	t.undo.clear()
	if t.secure {
		clear(t.b)
	}
	t.b = t.b[:0]
	t.caret, t.anchor, t.scroll = 0, 0, 0
	t.rev++
}

// SetString replaces the text, with the caret at its end. Characters a
// single-line field cannot hold are dropped or turned into spaces (see
// Insert).
func (t *TextBuffer) SetString(s string) { t.SetBytes(unsafeBytes(s)) }

// SetBytes is SetString for a byte slice (which it does not keep).
func (t *TextBuffer) SetBytes(p []byte) {
	t.undo.clear()
	room := -1
	if t.Max > 0 {
		room = t.Max
	}
	t.tmp = cleanText(t.tmp[:0], p, room)
	if t.secure && len(t.tmp) > cap(t.b) {
		clear(t.b[:cap(t.b)]) // append moves the text: the old storage holds no copy of it
	}
	t.b = append(t.b[:0], t.tmp...)
	t.caret, t.anchor, t.scroll = len(t.b), len(t.b), 0
	t.rev++
	t.scrub()
}

// Caret is the byte offset of the caret (the moving end of the selection).
func (t *TextBuffer) Caret() int { return t.caret }

// Selection returns the selected byte range [lo, hi); lo == hi when nothing is
// selected.
func (t *TextBuffer) Selection() (lo, hi int) {
	return min(t.caret, t.anchor), max(t.caret, t.anchor)
}

// HasSelection reports whether any text is selected.
func (t *TextBuffer) HasSelection() bool { return t.caret != t.anchor }

// Selected returns the selected text (aliasing the buffer).
func (t *TextBuffer) Selected() []byte {
	lo, hi := t.Selection()
	return t.b[lo:hi]
}

// SetSelection selects from anchor to caret (byte offsets, clamped and moved
// back to character cluster boundaries).
func (t *TextBuffer) SetSelection(anchor, caret int) {
	t.breakRun()
	t.anchor, t.caret = t.snap(anchor), t.snap(caret)
}

// SetCaret puts the caret at off and clears the selection.
func (t *TextBuffer) SetCaret(off int) { t.SetSelection(off, off) }

// SelectAll selects the whole text.
func (t *TextBuffer) SelectAll() {
	t.breakRun()
	t.anchor, t.caret = 0, len(t.b)
}

func (t *TextBuffer) snap(off int) int {
	return text.GraphemeStart(t.b, min(max(off, 0), len(t.b)))
}

// Insert replaces the selection (or inserts at the caret) with p, which it
// does not keep. A single-line field holds no line breaks or other control
// characters: line breaks and tabs become one space each (CRLF is one),
// other controls and invalid UTF-8 are dropped.
func (t *TextBuffer) Insert(p []byte) {
	lo, hi := t.Selection()
	room := -1
	if t.Max > 0 {
		room = max(t.Max-(len(t.b)-(hi-lo)), 0)
	}
	t.tmp = cleanText(t.tmp[:0], p, room)
	if len(t.tmp) == 0 && lo == hi {
		return
	}
	t.replace(lo, hi, t.tmp)
	t.scrub()
}

// insertSealed is Insert as a step of its own (a paste): it neither continues
// the typing run before it nor is continued by the typing after it.
func (t *TextBuffer) insertSealed(p []byte) {
	t.breakRun()
	t.Insert(p)
	t.breakRun()
}

// ReplaceUTF16 replaces the text range that starts start UTF-16 units from the
// caret (negative: before it) and is units long with p, which it does not keep,
// and puts the caret after p. It is what a platform input method asks for in
// UTF-16 terms (press-and-hold accents: -1, 1 replaces the character before the
// caret); an astral character counts two units, a range boundary inside one
// moves over the whole character, and a range beyond the text is cut to it. p
// is cleaned like Insert's; an empty p removes the range. The replacement is
// one undo step. An existing selection is not part of the range.
func (t *TextBuffer) ReplaceUTF16(start, units int, p []byte) {
	lo := t.moveUnits(t.caret, start)
	hi := t.moveUnits(t.caret, start+units)
	if hi < lo {
		lo, hi = hi, lo
	}
	room := -1
	if t.Max > 0 {
		room = max(t.Max-(len(t.b)-(hi-lo)), 0)
	}
	t.tmp = cleanText(t.tmp[:0], p, room)
	if len(t.tmp) == 0 && lo == hi {
		return
	}
	t.breakRun()
	t.replace(lo, hi, t.tmp)
	t.breakRun()
	t.scrub()
}

// moveUnits returns the byte offset that is units UTF-16 units from byte offset
// from (negative: before), clamped to the text; a unit boundary inside an
// astral character moves over the whole character.
func (t *TextBuffer) moveUnits(from, units int) int {
	b, i := t.b, from
	if units > 0 {
		for units > 0 && i < len(b) {
			r, n := utf8.DecodeRune(b[i:])
			i += n
			units--
			if r >= 0x10000 {
				units--
			}
		}
		return i
	}
	for units < 0 && i > 0 {
		r, n := utf8.DecodeLastRune(b[:i])
		i -= n
		units++
		if r >= 0x10000 {
			units++
		}
	}
	return i
}

// InsertString is Insert for a string.
func (t *TextBuffer) InsertString(s string) { t.Insert(unsafeBytes(s)) }

// cleanText appends the text a single-line field accepts from p to dst, at most
// room bytes (room < 0: no limit).
func cleanText(dst, p []byte, room int) []byte {
	start := len(dst)
	for i := 0; i < len(p); {
		r, n := utf8.DecodeRune(p[i:])
		if r == utf8.RuneError && n == 1 { // invalid byte
			i++
			continue
		}
		switch {
		case r == '\r' && i+1 < len(p) && p[i+1] == '\n':
			i++
			continue
		case r == '\r' || r == '\n' || r == '\t' || r == 0x2028 || r == 0x2029:
			r = ' ' // one space for the line break or tab
		case r < 0x20 || (r >= 0x7F && r < 0xA0):
			i += n
			continue
		}
		w := utf8.RuneLen(r)
		if room >= 0 && len(dst)-start+w > room {
			break
		}
		dst = utf8.AppendRune(dst, r)
		i += n
	}
	return dst
}

// replace replaces [lo, hi) of the text with ins and puts the caret after it.
// What stands next to the edit may join with it into one cluster (a letter
// typed in front of a combining mark, a ZWJ between two emoji, a flag letter
// beside another, text removed between a base and its mark), so the caret is
// moved onto a cluster boundary: forward after an insertion (past the
// cluster the typed text became part of), back after a deletion (it does not
// jump over text that was not touched).
func (t *TextBuffer) replace(lo, hi int, ins []byte) {
	t.record(lo, hi, ins)
	t.splice(lo, hi, ins)
	t.noteAfter()
}

// splice is replace without the history.
func (t *TextBuffer) splice(lo, hi int, ins []byte) {
	old := len(t.b)
	n := old - (hi - lo) + len(ins)
	if n > cap(t.b) {
		nb := make([]byte, old, n+n/2+16)
		copy(nb, t.b)
		if t.secure {
			clear(t.b[:cap(t.b)]) // the old storage holds no copy of the text
		}
		t.b = nb
	}
	t.b = t.b[:max(n, old)]
	copy(t.b[lo+len(ins):], t.b[hi:old])
	copy(t.b[lo:], ins)
	t.b = t.b[:n]
	if t.secure && n < old {
		clear(t.b[n:old]) // what the text moved out of
	}
	at := lo + len(ins)
	if len(ins) > 0 {
		at = snapUp(t.b, at)
	} else {
		at = text.GraphemeStart(t.b, at)
	}
	t.caret, t.anchor = at, at
	t.rev++
}

// deleteRange removes [lo, hi) and puts the caret at lo.
func (t *TextBuffer) deleteRange(lo, hi int) {
	if lo >= hi {
		return
	}
	t.replace(lo, hi, nil)
}

// deleteSelection removes the selection, if any.
func (t *TextBuffer) deleteSelection() bool {
	lo, hi := t.Selection()
	if lo == hi {
		return false
	}
	t.deleteRange(lo, hi)
	return true
}

// moveTo puts the caret at off; without extend the selection collapses.
func (t *TextBuffer) moveTo(off int, extend bool) {
	t.breakRun()
	t.caret = off
	if !extend {
		t.anchor = off
	}
}

// ---- words ----

const (
	classSpace = iota
	classWord
	classOther
)

func wordClass(r rune) int {
	switch {
	case unicode.IsSpace(r):
		return classSpace
	case r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r):
		return classWord
	}
	return classOther
}

// wordLeft returns the start of the word before off: spaces and punctuation
// are skipped, then the word.
func wordLeft(b []byte, off int) int {
	i := off
	for i > 0 {
		r, n := utf8.DecodeLastRune(b[:i])
		if wordClass(r) == classWord {
			break
		}
		i -= n
	}
	for i > 0 {
		r, n := utf8.DecodeLastRune(b[:i])
		if wordClass(r) != classWord {
			break
		}
		i -= n
	}
	return text.GraphemeStart(b, i)
}

// wordRight returns the position after the word at or after off (end of the
// next word); with start set, the start of the word after the one at off (the
// Windows convention for Ctrl+Right).
func wordRight(b []byte, off int, start bool) int {
	i := off
	if start {
		for i < len(b) {
			r, n := utf8.DecodeRune(b[i:])
			if wordClass(r) != classWord {
				break
			}
			i += n
		}
		for i < len(b) {
			r, n := utf8.DecodeRune(b[i:])
			if wordClass(r) == classWord {
				break
			}
			i += n
		}
		return snapUp(b, i)
	}
	for i < len(b) {
		r, n := utf8.DecodeRune(b[i:])
		if wordClass(r) == classWord {
			break
		}
		i += n
	}
	for i < len(b) {
		r, n := utf8.DecodeRune(b[i:])
		if wordClass(r) != classWord {
			break
		}
		i += n
	}
	return snapUp(b, i)
}

func snapUp(b []byte, i int) int {
	if text.IsGraphemeBoundary(b, i) {
		return i
	}
	return text.NextGrapheme(b, i)
}

// wordBounds returns the run of same-class characters (word, space, or
// punctuation) around off, for double-click selection.
func wordBounds(b []byte, off int) (lo, hi int) {
	if len(b) == 0 {
		return 0, 0
	}
	var cls int
	if off < len(b) {
		r, _ := utf8.DecodeRune(b[off:])
		cls = wordClass(r)
	} else {
		r, _ := utf8.DecodeLastRune(b)
		cls = wordClass(r)
	}
	lo, hi = off, off
	for lo > 0 {
		r, n := utf8.DecodeLastRune(b[:lo])
		if wordClass(r) != cls {
			break
		}
		lo -= n
	}
	for hi < len(b) {
		r, n := utf8.DecodeRune(b[hi:])
		if wordClass(r) != cls {
			break
		}
		hi += n
	}
	return text.GraphemeStart(b, lo), snapUp(b, hi)
}
