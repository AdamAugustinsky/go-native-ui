package platform

import (
	"errors"
	"unicode/utf8"
)

// The composer's native editor (N4; the contract is docs/PHASE6-PLAN.md, "The
// composer"). On macOS an Editor is a hosted NSTextView (editor_darwin.go):
// AppKit edits the text, and the editor keeps a copy of it in a UTF-8 buffer
// the app reads, refreshed on every change (textDidChange:) and on nothing
// else, so frames read it without allocating. The app speaks UTF-8 bytes and
// AppKit UTF-16 units; the editor converts with a checkpoint table built with
// each copy. Elsewhere NewEditor returns ErrNoHosts.
//
// Signals collect between frames and are taken once per frame (TakeSignals);
// each change also queues one EvHostEdit, so an idle app draws a frame for it.
// While the app holds a completion (SetCompletionActive), the keys that move
// in a completion list come to the app as EditorCommands (TakeCommand), and
// the text view does nothing with them. The options NewEditor was given can
// change while the editor runs (SetOptions).

// TextRange is a range of bytes [Start, End) in an editor's UTF-8 text.
type TextRange struct {
	// Start is the first byte and End the byte after the last.
	Start, End int
}

// TextSelection is an editor's selection in bytes: Anchor is where it began
// and Focus where the caret is (Focus may be before Anchor). A caret is a
// selection with Anchor == Focus.
type TextSelection struct {
	// Anchor is where the selection began and Focus where the caret is.
	Anchor, Focus int
}

// Range is the selection as a range, Start <= End.
func (s TextSelection) Range() TextRange {
	if s.Focus < s.Anchor {
		return TextRange{s.Focus, s.Anchor}
	}
	return TextRange{s.Anchor, s.Focus}
}

// EnterPolicy says what Enter does in an editor.
type EnterPolicy uint8

const (
	// EnterSends: Enter submits (EditorSubmitted); Shift+Enter and
	// Option+Enter add a line.
	EnterSends EnterPolicy = iota
	// CmdEnterSends: Enter adds a line and ⌘Enter submits.
	CmdEnterSends
	// EnterNewline: Enter adds a line and nothing submits (a text area,
	// rexui.TextArea).
	EnterNewline
)

// EditorOptions configure NewEditor.
type EditorOptions struct {
	// Placeholder is shown while the text is empty.
	Placeholder string
	// MaxLen is the most bytes the text may hold; an edit that would make it
	// longer is refused (typing, pasting, an input method's commit, Replace).
	// 0: no maximum.
	MaxLen int
	// Enter is the Enter policy.
	Enter EnterPolicy
	// MaxLines is how many lines the editor grows to before it scrolls
	// (Height); 0 means 6.
	MaxLines int
	// MinLines is how many lines high the editor is when it holds fewer
	// (Height); 0 means 1. Equal to MaxLines, the height is fixed.
	MinLines int
	// ReadOnly shows the text selectable but not editable: typing, pasting
	// and dropping change nothing (SetText and Replace still do).
	ReadOnly bool
	// FontSize is the text's size in points; 0 means the system's 13.
	FontSize float32
	// InsetX and InsetY are the space between the view's edges and the text,
	// in points (the text container's inset; AppKit adds a 5 pt line fragment
	// padding to the left and right).
	InsetX, InsetY float32
}

// EditorSignals are what happened in an editor since the signals were last
// taken.
type EditorSignals uint16

const (
	// EditorChanged: the text changed (typing, a paste, an input method,
	// undo, Replace; not SetText).
	EditorChanged EditorSignals = 1 << iota
	// EditorComposing qualifies EditorChanged: the last change left marked
	// text (an input method's composition). A commit, or the end of the
	// composition, fires EditorChanged without it.
	EditorComposing
	// EditorSelected: the selection moved.
	EditorSelected
	// EditorSubmitted: Enter (by the Enter policy) asked to send.
	EditorSubmitted
	// EditorCancelled: Escape went down with no completion active, or the
	// editor ended the app's completion itself (it lost the keyboard focus,
	// or marked text began).
	EditorCancelled
	// EditorHeightChanged: Height changed.
	EditorHeightChanged
	// EditorPastedImage: a paste found an image on the pasteboard and
	// inserted nothing: the app takes the image (RequestPasteKinds with
	// WantImage, which converts it to PNG off the main thread).
	EditorPastedImage
	// EditorEdgeUp and EditorEdgeDown: with SetEdgeArrows on, Up went down
	// with the caret on the text's first line (or the text empty), Down with
	// it on the last line.
	EditorEdgeUp
	EditorEdgeDown
	// EditorPastedFiles: a paste found files on the pasteboard (a copy in the
	// Finder) and inserted nothing, not even their names: the app takes their
	// paths (RequestPasteKinds with WantFiles).
	EditorPastedFiles
)

// EditorCommand is a key the editor handed to the app's completion instead of
// acting on it (TakeCommand).
type EditorCommand uint8

const (
	// CmdNone is no command.
	CmdNone EditorCommand = iota
	// CmdUp and CmdDown move in the completion list (the Up and Down keys).
	CmdUp
	CmdDown
	// CmdTab and CmdBacktab are Tab and Shift+Tab.
	CmdTab
	CmdBacktab
	// CmdAccept is Enter, in any of its Enter-policy variants.
	CmdAccept
	// CmdCancel is Escape.
	CmdCancel
)

// TextSpan styles a range of an editor's text (SetSpans): mentions. The
// style is display-only (AppKit's temporary attributes): it never enters the
// text or the undo stack.
type TextSpan struct {
	// Range is the bytes it covers.
	Range TextRange
	// Color is the text's colour (0xRRGGBBAA; 0 keeps the text colour), and
	// Background the fill behind it (0: none).
	Color, Background uint32
}

// Errors of an Editor's range methods.
var (
	// ErrNotBoundary: a byte offset is not on a UTF-8 scalar boundary of the
	// text, or is outside it.
	ErrNotBoundary = errors.New("platform: editor offset is not on a UTF-8 boundary")
	// ErrComposing: the editor holds marked text (an input method is
	// composing), which programmatic edits must not touch.
	ErrComposing = errors.New("platform: editor is composing")
	// ErrTooLong: the edit would make the text longer than MaxLen, or the
	// text view refused it.
	ErrTooLong = errors.New("platform: editor text over its maximum length")
)

// maxCommands is how many completion commands wait for TakeCommand.
const maxCommands = 16

// Editor is a native multi-line text editor hosted in a window (NewEditor):
// on macOS an NSTextView in an NSScrollView. Its methods run on the main
// thread, at safe points (not inside AppKit's own calls into the editor).
type Editor struct {
	editorState
	editorNative
}

// editorState is the part of an Editor every system has: the copy of the
// text, its UTF-16 map, the selection, the marked text and the signals.
type editorState struct {
	maxLen int
	woken  bool // an EvHostEdit is queued and the signals were not taken since
	text   []byte
	m      u16map
	sel    TextSelection
	marked bool
	mr     TextRange
	pend   EditorSignals
	cmds   [maxCommands]EditorCommand
	csends [maxCommands]bool
	crev   [maxCommands]uint32 // the text's Rev when the command was queued
	rev    uint32
	cHead  int
	cN     int
	compl  bool
	edges  bool // SetEdgeArrows
	opts   EditorOptions
	height float32 // points
}

// Text is the editor's text, UTF-8. It is the editor's buffer: valid until
// the next change; copy what is kept.
func (e *Editor) Text() []byte { return e.text }

// Selection is the selection in bytes, as of the last change of the text or
// the selection.
func (e *Editor) Selection() TextSelection { return e.sel }

// Marked is the marked text (an input method's composition) in bytes, and
// whether there is any. The app must not draw or rewrite it.
func (e *Editor) Marked() (TextRange, bool) { return e.mr, e.marked }

// Height is the editor's height in points for its current width: its text's
// lines, at least one and at most MaxLines (then it scrolls), and the insets.
func (e *Editor) Height() float32 { return e.height }

// CompletionActive reports whether the app holds a completion.
func (e *Editor) CompletionActive() bool { return e.compl }

// SetCompletionActive starts or ends the app's completion. While it is
// active, Up, Down, Tab, Shift+Tab, Enter and Escape in the text view come to
// the app as EditorCommands and do nothing in the text. It cannot start while
// the editor composes (marked text), and it ends by itself, with
// EditorCancelled, when the editor loses the keyboard focus or marked text
// begins.
func (e *Editor) SetCompletionActive(on bool) {
	if on && e.marked {
		return
	}
	e.compl = on
	if !on {
		e.cHead, e.cN = 0, 0
	}
}

// SetEdgeArrows turns the edge arrows on or off (off when made): with them
// on, Up with the caret on the text's first line or in an empty editor, and
// Down with it on the last line, are reported as EditorEdgeUp and
// EditorEdgeDown (an app's "Up edits the last message"). The text view still
// moves the caret as it does (to the start or the end of the text); a
// selection, a completion (SetCompletionActive) or marked text keeps the keys
// to itself, and Shift+Up and Shift+Down extend the selection as ever.
func (e *Editor) SetEdgeArrows(on bool) { e.edges = on }

// Options are the editor's options: NewEditor's, as SetOptions last changed
// them.
func (e *Editor) Options() EditorOptions { return e.opts }

// TakeSignals returns what happened since the last call and clears it. A
// widget calls it once per frame.
func (e *Editor) TakeSignals() EditorSignals {
	s := e.pend
	e.pend = 0
	e.woken = false
	e.sync()
	s |= e.pend // a height the sync found changed
	e.pend = 0
	return s
}

// TakeCommand returns the oldest completion command not taken yet, or
// CmdNone.
func (e *Editor) TakeCommand() EditorCommand {
	c, _ := e.TakeCommandSends()
	return c
}

// TakeCommandSends is TakeCommand with what the key would have done without a
// completion: for CmdAccept, whether the Enter was one that sends by the Enter
// policy (Enter under EnterSends, ⌘Enter under either; not Shift+Enter or
// Option+Enter under EnterSends, nor Enter under CmdEnterSends), so that an app
// whose completion does not take it can do what the editor would have: send or
// add a line. It is false for every other command.
func (e *Editor) TakeCommandSends() (c EditorCommand, sends bool) {
	c, sends, _ = e.TakeCommandRev()
	return c, sends
}

// Rev changes whenever the text or the selection does, for any reason (typing,
// an input method, undo, SetText, Replace, SetSelection). A command is stamped
// with the Rev of the editor at the key, so that an app can tell an accept of
// the text it looked at from one typed before later changes.
func (e *Editor) Rev() uint32 { return e.rev }

// TakeCommandRev is TakeCommandSends with the Rev the editor had when the key
// came: a command whose Rev is behind the editor's was followed by a change of
// the text or the selection.
func (e *Editor) TakeCommandRev() (c EditorCommand, sends bool, rev uint32) {
	if e.cN == 0 {
		return CmdNone, false, e.rev
	}
	c, sends, rev = e.cmds[e.cHead], e.csends[e.cHead], e.crev[e.cHead]
	e.cHead = (e.cHead + 1) % maxCommands
	e.cN--
	return c, sends, rev
}

func (e *Editor) pushCommand(c EditorCommand) { e.pushCommandSends(c, false) }

func (e *Editor) pushCommandSends(c EditorCommand, sends bool) {
	if e.cN == maxCommands {
		return
	}
	i := (e.cHead + e.cN) % maxCommands
	e.cmds[i], e.csends[i], e.crev[i] = c, sends, e.rev
	e.cN++
}

// Reserve makes room for n bytes of text, so that changes up to that size
// allocate nothing.
func (e *Editor) Reserve(n int) {
	if cap(e.text) < n {
		t := make([]byte, len(e.text), n)
		copy(t, e.text)
		e.text = t
	}
	if k := n/u16Block + 2; cap(e.m.cp) < k {
		cp := make([]uint32, len(e.m.cp), k)
		copy(cp, e.m.cp)
		e.m.cp = cp
	}
}

// ToUTF16 converts a byte offset of the text to UTF-16 units (AppKit's).
func (e *Editor) ToUTF16(off int) (int, error) { return e.m.toU16(e.text, off) }

// FromUTF16 converts a UTF-16 offset to bytes; an offset inside a surrogate
// pair rounds down to the pair's start, one past the end to the end.
func (e *Editor) FromUTF16(u int) int { return e.m.toByte(e.text, u) }

// rangeU16 converts byte range r to a UTF-16 location and length.
func (e *Editor) rangeU16(r TextRange) (loc, n int, err error) {
	if r.End < r.Start {
		return 0, 0, ErrNotBoundary
	}
	a, err := e.m.toU16(e.text, r.Start)
	if err != nil {
		return 0, 0, err
	}
	b, err := e.m.toU16(e.text, r.End)
	if err != nil {
		return 0, 0, err
	}
	return a, b - a, nil
}

// checkReplace validates Replace's arguments against the text it would make:
// r on boundaries of the text, text valid UTF-8, sel on boundaries of the
// result; and the maximum length.
func (e *Editor) checkReplace(r TextRange, text []byte, sel TextRange) error {
	if e.marked {
		return ErrComposing
	}
	if r.Start < 0 || r.End < r.Start || r.End > len(e.text) || !boundary(e.text, r.Start) || !boundary(e.text, r.End) || !utf8.Valid(text) {
		return ErrNotBoundary
	}
	n := len(e.text) - (r.End - r.Start) + len(text)
	if e.maxLen > 0 && n > e.maxLen {
		return ErrTooLong
	}
	at := func(o int) bool { // o is a boundary of the result
		switch {
		case o < 0 || o > n:
			return false
		case o <= r.Start:
			return boundary(e.text, o)
		case o <= r.Start+len(text):
			return boundary(text, o-r.Start)
		}
		return boundary(e.text, o-len(text)+(r.End-r.Start))
	}
	if sel.End < sel.Start || !at(sel.Start) || !at(sel.End) {
		return ErrNotBoundary
	}
	return nil
}

// boundary reports whether off is a scalar boundary of b (0 and len(b)
// included).
func boundary(b []byte, off int) bool {
	return off >= 0 && off <= len(b) && (off == len(b) || b[off]&0xC0 != 0x80)
}

// u16Block is the byte distance between the checkpoints of a u16map.
const u16Block = 64

// u16map maps byte offsets of a UTF-8 text to UTF-16 offsets and back. cp[k]
// is the UTF-16 offset of the first scalar boundary at or after byte
// k*u16Block (the table covers the text's end), so a conversion is a lookup
// and a scan of at most u16Block+3 bytes. It holds no pointers into the text.
type u16map struct {
	cp  []uint32
	n16 int // the text's length in UTF-16 units
}

// build makes the table for b, reusing its storage.
func (m *u16map) build(b []byte) {
	cp, u := m.cp[:0], 0
	for i := 0; i < len(b); {
		for len(cp)*u16Block <= i {
			cp = append(cp, uint32(u))
		}
		r, n := utf8.DecodeRune(b[i:])
		u += units(r)
		i += n
	}
	for len(cp)*u16Block <= len(b) {
		cp = append(cp, uint32(u))
	}
	m.cp, m.n16 = cp, u
}

// rebuild reuses checkpoints before byte off, whose prefix did not change.
func (m *u16map) rebuild(b []byte, off int) {
	k := off / u16Block
	p, u := blockStart(b, k), int(m.cp[k])
	cp := m.cp[:k]
	for p < len(b) {
		for len(cp)*u16Block <= p {
			cp = append(cp, uint32(u))
		}
		r, n := utf8.DecodeRune(b[p:])
		u += units(r)
		p += n
	}
	for len(cp)*u16Block <= len(b) {
		cp = append(cp, uint32(u))
	}
	m.cp, m.n16 = cp, u
}

// units is a scalar's length in UTF-16 (an invalid byte is one unit, as
// U+FFFD).
func units(r rune) int {
	if r >= 0x10000 {
		return 2
	}
	return 1
}

// blockStart is the first scalar boundary of b at or after byte k*u16Block.
func blockStart(b []byte, k int) int {
	p := k * u16Block
	for p < len(b) && b[p]&0xC0 == 0x80 {
		p++
	}
	return min(p, len(b))
}

// toU16 converts byte offset off of b to UTF-16 units; off must be a scalar
// boundary of b.
func (m *u16map) toU16(b []byte, off int) (int, error) {
	if !boundary(b, off) {
		return 0, ErrNotBoundary
	}
	k := off / u16Block
	if k >= len(m.cp) {
		return 0, ErrNotBoundary // the table is stale
	}
	u := int(m.cp[k])
	for p := blockStart(b, k); p < off; {
		r, n := utf8.DecodeRune(b[p:])
		u += units(r)
		p += n
	}
	return u, nil
}

// toByte converts UTF-16 offset u to a byte offset of b: inside a surrogate
// pair it rounds down to the pair's start; past the end it is the end.
func (m *u16map) toByte(b []byte, u int) int {
	if u <= 0 || len(m.cp) == 0 {
		return 0
	}
	if u >= m.n16 {
		return len(b)
	}
	lo, hi := 0, len(m.cp) // the last k with cp[k] <= u
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if int(m.cp[mid]) <= u {
			lo = mid
		} else {
			hi = mid
		}
	}
	p, at := blockStart(b, lo), int(m.cp[lo])
	for p < len(b) {
		r, n := utf8.DecodeRune(b[p:])
		w := units(r)
		if at+w > u {
			break
		}
		at += w
		p += n
	}
	return p
}
