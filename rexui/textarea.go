package rexui

import (
	"errors"

	"gonativeui/platform"
	"gonativeui/theme"
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

// TextAreaRecipe is the look of text areas: a text field's fill and
// outline (FieldLook) around a taller box, with a selection's corner. The
// outline is drawn inside the frame.
type TextAreaRecipe struct {
	// Fill is the body, Border its outline, BorderW wide in points.
	Fill, Border Tint
	BorderW      float32
	// Radius is the corner radius in points.
	Radius float32
	// InsetX and InsetY are the space between the frame and the text, in
	// points (the text field's 6 to the text, of which the text view's line
	// padding is 5).
	InsetX, InsetY float32
	// Placeholder colours the placeholder where the area draws it (the hosted
	// text view draws its own, in placeholderTextColor), TextDisabled the
	// text of a disabled area.
	Placeholder, TextDisabled Tint
}

// TextAreaLook is the text areas' recipe.
var TextAreaLook = TextAreaRecipe{
	Fill: Of(theme.InputFill), Border: Of(theme.FieldOutline), BorderW: 1,
	Radius: 8, InsetX: 6, InsetY: 5,
	Placeholder: Of(theme.Placeholder), TextDisabled: Of(theme.TextDisabled),
}

// textAreaLinePt is a line's height in points where no text view measures
// it (the drawn look of an area with no view): the system font's 13 pt line.
const textAreaLinePt = 16

// TextAreaState is a text area's state, owned by the app. Make it once with
// NewTextArea. On macOS the text is a hosted editor (base.NativeEditor, an
// NSTextView, with the editor's EnterNewline policy); on Windows (until W4),
// on Linux and in an offscreen window it is a drawn single-line field with
// the same API, as the composer's fallback is.
type TextAreaState struct {
	ed       *base.NativeEditor // the hosted editor; nil: the drawn field
	buf      ui.TextBuffer      // the drawn field's text
	hint     string
	readOnly bool
	minLines int
	maxLines int
}

// NewTextArea makes the state of a text area in window w (nil: an
// offscreen render). Call it in Init or between frames, on the main
// thread. The error is for a hosted editor that could not be made (more
// than the window's hosts); the state is then still usable, drawn.
func NewTextArea(w *platform.Window, s TextAreaSetup) (*TextAreaState, error) {
	st := &TextAreaState{hint: s.Placeholder, readOnly: s.ReadOnly, minLines: s.MinLines, maxLines: s.MaxLines}
	if st.minLines <= 0 && st.maxLines <= 0 {
		st.minLines, st.maxLines = 3, 10
	}
	st.minLines = max(st.minLines, 1)
	if st.maxLines < st.minLines {
		st.maxLines = st.minLines
	}
	max := s.MaxLen
	if max <= 0 {
		max = 16000
	}
	st.buf.Max = max
	if w == nil {
		st.buf.Reserve(max)
		return st, nil
	}
	rc := &TextAreaLook
	ed, err := base.NewNativeEditor(w, platform.EditorOptions{
		Placeholder: s.Placeholder, MaxLen: max, Enter: platform.EnterNewline,
		MinLines: st.minLines, MaxLines: st.maxLines, ReadOnly: s.ReadOnly,
		InsetX: rc.InsetX - textViewPad, InsetY: rc.InsetY,
	})
	if err != nil {
		st.buf.Reserve(max)
		if errors.Is(err, errors.ErrUnsupported) {
			return st, nil
		}
		return st, err
	}
	ed.Reserve(max)
	st.ed = ed
	return st, nil
}

// textViewPad is the text view's line fragment padding, in points: AppKit
// adds it to the text container's inset on the left and right.
const textViewPad = 5

// Hosted reports whether the text is a hosted native editor (false: the
// drawn single-line fallback).
func (s *TextAreaState) Hosted() bool { return s.ed != nil && s.ed.Editor != nil }

// Editor is the hosted editor, nil for a drawn area: for what only the
// native view has (Focus, Selection, Replace, CaretRect).
func (s *TextAreaState) Editor() *base.NativeEditor { return s.ed }

// Text is the text, UTF-8, valid until the next change.
func (s *TextAreaState) Text() []byte {
	if s.Hosted() {
		return s.ed.Text()
	}
	return s.buf.Bytes()
}

// SetText replaces the text (not an undo step). A hosted area refuses text
// over its maximum or not valid UTF-8 (platform.ErrTooLong,
// platform.ErrNotBoundary) and keeps what it had; the drawn one cuts it.
func (s *TextAreaState) SetText(b []byte) error {
	if s.Hosted() {
		return s.ed.SetText(b)
	}
	s.buf.SetBytes(b)
	return nil
}

// SetReadOnly makes the area read-only or editable.
func (s *TextAreaState) SetReadOnly(on bool) {
	if on == s.readOnly {
		return
	}
	s.readOnly = on
	if s.Hosted() {
		o := s.ed.Options()
		o.ReadOnly = on
		s.ed.SetOptions(o)
	}
}

// ReadOnly reports whether the area is read-only.
func (s *TextAreaState) ReadOnly() bool { return s.readOnly }

// Lines are the area's height range in lines (MinLines, MaxLines).
func (s *TextAreaState) Lines() (min, max int) { return s.minLines, s.maxLines }

// Release frees the hosted editor, if any. The state must not be used
// after.
func (s *TextAreaState) Release() {
	if s.Hosted() {
		s.ed.Release()
	}
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
	// Editor is the hosted editor's box (0 for a drawn area).
	Editor ui.ID
	// HeightChanged: the hosted text grew or shrank by a line this frame.
	HeightChanged bool
	// Composing: an input method holds marked text in the area.
	Composing bool
}

// TextArea builds the text area labelled label for state st: as wide as the
// parent unless the kit's NextWidth says otherwise, as high as its lines.
// Hosted, the text view grows from MinLines to MaxLines with the text and
// then scrolls; read-only it can be selected and copied; disabled its text
// is drawn greyed and nothing takes the focus. A frame without input
// allocates nothing.
func TextArea(k *base.Kit, label string, st *TextAreaState, o TextAreaOpts) TextAreaSignal {
	if !st.Hosted() && st.ed == nil {
		return textAreaDrawn(k, label, st, o)
	}
	c, r := k.Context(), k.Theme()
	rc := &TextAreaLook
	sc := k.Scale()
	display, ident := ui.SplitLabel(label)
	disabled := o.Disabled || k.Disabled()
	key := ui.HashString(c.ParentKey(), ident)
	radius := pt(sc, rc.Radius)
	bw := pt(sc, rc.BorderW)

	c.NextBG(rc.Fill.RGBA(r))
	c.NextBorder(rc.Border.RGBA(r), bw)
	c.NextRadius(radius)
	c.NextAxis(ui.AxisY)
	k.ApplySize(ui.Pct(1), ui.Children(1))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(display)
	field := c.BoxKeyed(ui.DrawBackground|ui.DrawBorder, key, nil)
	sig := TextAreaSignal{Signal: base.Signal{ID: field}}
	c.Push(field)
	switch {
	case !st.Hosted(): // no view (an offscreen look of a hosted area): its box and placeholder
		h := pt(sc, float32(st.minLines)*textAreaLinePt+2*rc.InsetY)
		c.NextSize(ui.Pct(1), ui.Px(h))
		id := c.Host(0, ui.HashString(key, ident), ui.HostFocusable|ui.HostTextEntry)
		sig.Editor, sig.Key = id, c.KeyOf(id)
		if len(st.buf.Bytes()) == 0 && st.hint != "" {
			c.Push(id)
			textAreaPlaceholder(k, st.hint, rc.Placeholder, disabled)
			c.Pop()
		}
	case disabled: // the view is not built: the text, drawn greyed
		st.settleDisabled()
		h := st.ed.Height() * sc
		c.NextSize(ui.Pct(1), ui.Px(h))
		id := c.BoxKeyed(ui.Clip, ui.HashString(key, ident), nil)
		sig.Editor = id
		c.Push(id)
		if t := st.ed.Text(); len(t) > 0 {
			s := &r.Styles[theme.StyleBody]
			c.NextPadding(pt(sc, rc.InsetX), pt(sc, rc.InsetY), pt(sc, rc.InsetX), 0)
			c.NextSize(ui.Pct(1), ui.TextSize(0))
			c.NextFont(s.Font)
			c.NextTextColor(rc.TextDisabled.RGBA(r))
			c.BoxKeyed(ui.DrawText|ui.WrapText, ui.KeyN(c.KeyOf(id), 1), t)
		} else if st.hint != "" {
			textAreaPlaceholder(k, st.hint, rc.TextDisabled, true)
		}
		c.Pop()
	default:
		ns := k.NativeEditor(st.ed, label)
		sig.Editor, sig.Key = ns.ID, c.KeyOf(ns.ID)
		sig.Focused, sig.Changed = ns.Focused, ns.Changed
		sig.HeightChanged = ns.HeightChanged
		_, marked := st.ed.Marked()
		sig.Composing = ns.Composing || marked
	}
	c.Pop()

	v := base.Visual{Inactive: k.Inactive(), Disabled: disabled}
	if sig.Focused && !disabled {
		v.Focus, v.Ring = 1, 1
	}
	k.FocusRing(field, v, radius)
	return sig
}

// textAreaPlaceholder draws the placeholder at the text's first line, in
// the current parent.
func textAreaPlaceholder(k *base.Kit, hint string, col Tint, disabled bool) {
	c, r := k.Context(), k.Theme()
	rc := &TextAreaLook
	sc := k.Scale()
	s := &r.Styles[theme.StyleBody]
	pc := col.RGBA(r)
	if disabled {
		pc = rc.TextDisabled.RGBA(r)
	}
	c.NextPos(pt(sc, rc.InsetX), pt(sc, rc.InsetY))
	c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(s.Line))
	c.NextFont(s.Font)
	c.NextTextColor(pc)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.KeyN(c.ParentKey(), 2), base.StrBytes(hint))
}

// settleDisabled drops what the hosted editor queued while the area is
// disabled and its view not built, and commits a composition.
func (s *TextAreaState) settleDisabled() {
	_ = s.ed.TakeSignals()
	for s.ed.TakeCommand() != platform.CmdNone {
	}
	if _, marked := s.ed.Marked(); marked {
		s.ed.CommitComposition()
		_ = s.ed.TakeSignals()
	}
}

// textAreaDrawn is the drawn fallback: the single-line text field, greyed
// while read-only or disabled.
func textAreaDrawn(k *base.Kit, label string, st *TextAreaState, o TextAreaOpts) TextAreaSignal {
	k.BeginDisabled(o.Disabled || st.readOnly)
	f := TextField(k, label, &st.buf, st.hint, FieldOpts{})
	k.EndDisabled()
	return TextAreaSignal{Signal: f.Signal, Key: f.Key}
}
