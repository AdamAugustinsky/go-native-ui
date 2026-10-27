package rexui

import (
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// TextAreaSetup is what NewTextArea makes a text area's state from (the
// phase 8 plan's C4): a general multi-line field for settings, a bot's
// instructions or a quick-add box, on the composer's hosted editor but with
// no send policy: Enter adds a line.
type TextAreaSetup struct {
	// Placeholder shows while the text is empty.
	Placeholder string
	// MaxLen is the most bytes of text (0: 16000). The state reserves that
	// much, so typing allocates nothing.
	MaxLen int
	// MinLines and MaxLines are the height in lines: fixed when they are
	// equal, else growing with the text from MinLines to MaxLines and then
	// scrolling (0, 0: 3 to 10).
	MinLines, MaxLines int
	// ReadOnly shows the text selectable but not editable.
	ReadOnly bool
}

// TextAreaState is a text area's state, owned by the app. Make it once with
// NewTextArea. On macOS (once C4 lands; on Windows after W4) the text is a
// hosted editor (base.NativeEditor); until then, and in an offscreen
// window, it is a drawn single-line field with the same API, as the
// composer's fallback is.
type TextAreaState struct {
	buf      ui.TextBuffer
	hint     string
	readOnly bool
	minLines int
	maxLines int
}

// TextAreaOpts are a text area's options, passed by value.
type TextAreaOpts struct {
	// Disabled greys the area out and refuses the focus.
	Disabled bool
}

// TextAreaSignal is what a text area reports each frame.
type TextAreaSignal struct {
	base.Signal
	// Key is the key of the box that takes the keyboard focus (Kit.Focus).
	Key ui.Key
}

// NewTextArea makes the state of a text area in window w (nil: an
// offscreen render). Call it in Init or between frames, on the main
// thread. The error is for a hosted editor that could not be made; the
// state is then still usable, drawn.
func NewTextArea(w *platform.Window, s TextAreaSetup) (*TextAreaState, error) {
	st := &TextAreaState{hint: s.Placeholder, readOnly: s.ReadOnly, minLines: s.MinLines, maxLines: s.MaxLines}
	if st.minLines <= 0 && st.maxLines <= 0 {
		st.minLines, st.maxLines = 3, 10
	}
	if st.maxLines < st.minLines {
		st.maxLines = st.minLines
	}
	max := s.MaxLen
	if max <= 0 {
		max = 16000
	}
	st.buf.Max = max
	st.buf.Reserve(max)
	return st, nil // wave 0: drawn; C4 hosts platform.Editor where NewEditor succeeds
}

// Hosted reports whether the text is a hosted native editor (false: the
// drawn single-line fallback).
func (s *TextAreaState) Hosted() bool { return false }

// Text is the text, UTF-8, valid until the next change.
func (s *TextAreaState) Text() []byte { return s.buf.Bytes() }

// SetText replaces the text (not an undo step).
func (s *TextAreaState) SetText(b []byte) { s.buf.SetBytes(b) }

// SetReadOnly makes the area read-only or editable.
func (s *TextAreaState) SetReadOnly(on bool) { s.readOnly = on }

// Lines are the area's height range in lines (MinLines, MaxLines).
func (s *TextAreaState) Lines() (min, max int) { return s.minLines, s.maxLines }

// Release frees the hosted editor, if any. The state must not be used
// after.
func (s *TextAreaState) Release() {}

// TextArea builds the text area labelled label for state st. A frame
// without input allocates nothing.
func TextArea(k *base.Kit, label string, st *TextAreaState, o TextAreaOpts) TextAreaSignal {
	k.BeginDisabled(o.Disabled || st.readOnly) // wave 0: read-only shows disabled in the drawn fallback
	f := TextField(k, label, &st.buf, st.hint, FieldOpts{})
	k.EndDisabled()
	return TextAreaSignal{Signal: f.Signal, Key: f.Key}
}
