package rexui

import (
	"errors"
	"unicode/utf8"

	"gonativeui/icon"
	"gonativeui/img"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ComposerVariant selects a composer's size.
type ComposerVariant uint8

const (
	// ComposerDefault is the one-line composer that grows to six lines, then
	// scrolls.
	ComposerDefault ComposerVariant = iota
	// ComposerExpanded starts three lines high and grows to eight, for a
	// composer that is the main thing in its window. The drawn field of
	// Windows and Linux is one line either way.
	ComposerExpanded

	// NumComposerVariants is the number of variants.
	NumComposerVariants
)

// ComposerSetup is what NewComposer makes a composer's state from.
type ComposerSetup struct {
	// Variant is the size.
	Variant ComposerVariant
	// Placeholder shows while the text is empty.
	Placeholder string
	// MaxLen is the most bytes of text (0: 4000). The state reserves that
	// much, so typing allocates nothing.
	MaxLen int
	// Enter is the Enter policy: Enter sends (platform.EnterSends, the
	// default), or Enter adds a line and ⌘Enter sends. The drawn field is
	// one line and always sends with Enter.
	Enter platform.EnterPolicy
}

// ComposerAttachment is one attachment of the composer's row: a chip with a
// small thumbnail (or the file icon) and the name, and a button that removes
// it.
type ComposerAttachment struct {
	// Name is the file's name: the chip's text and, with "Remove ", the
	// accessible name of its button.
	Name string
	// Thumb is the thumbnail in the kit's image cache (Kit.SetImages); the
	// zero Handle shows the file icon instead.
	Thumb img.Handle
}

// ComposerState is a composer's state, owned by the app: the text, the
// editor that holds it and what the popup needs between frames. Make it once
// with NewComposer. On macOS the text is a hosted NSTextView
// (base.NativeEditor); everywhere else, and in an offscreen window, it is a
// drawn single-line text field with the same API.
type ComposerState struct {
	ed *base.NativeEditor // the hosted editor; nil: the drawn field
	// buf is the drawn field's text.
	buf      ui.TextBuffer
	variant  ComposerVariant
	enter    platform.EnterPolicy
	maxLen   int
	hint     string                 // the placeholder
	fieldKey ui.Key                 // the drawn field's focus key
	sel      platform.TextSelection // the drawn field's selection of the last frame
	caret    [4]float32             // the completion's anchor, from the last Changed or Selected
	caretOK  bool
	// completing: the last frame showed a completion; list is its panel's
	// rectangle then
	completing bool
	list       [4]float32
}

// NewComposer makes the state of a composer in window w. With an NSTextView
// to host (macOS, a real window) the text is hosted; with no window (an
// offscreen render) or on a system that has none, it is the drawn field. The
// error is for a hosted editor that could not be made (more than the window's
// hosts): the state is then still usable, drawn. Call it in Init or between
// frames, on the main thread.
func NewComposer(w *platform.Window, s ComposerSetup) (*ComposerState, error) {
	rc := &ComposerLook
	st := &ComposerState{variant: s.Variant % NumComposerVariants, enter: s.Enter, maxLen: s.MaxLen, hint: s.Placeholder}
	if st.maxLen <= 0 {
		st.maxLen = 4000
	}
	st.buf.Max = st.maxLen
	st.buf.Reserve(st.maxLen)
	if w == nil {
		return st, nil
	}
	ed, err := base.NewNativeEditor(w, platform.EditorOptions{
		Placeholder: s.Placeholder, MaxLen: st.maxLen, Enter: s.Enter,
		MaxLines: rc.MaxLines[st.variant], InsetX: rc.InsetX, InsetY: rc.InsetY,
	})
	if err != nil {
		if errors.Is(err, errors.ErrUnsupported) {
			return st, nil
		}
		return st, err
	}
	ed.Reserve(st.maxLen)
	st.ed = ed
	return st, nil
}

// Hosted reports whether the text is a hosted native editor (a drawn
// composer is one line).
func (s *ComposerState) Hosted() bool { return s.ed != nil }

// Editor is the hosted native editor, nil for a drawn composer. The app uses
// it for what only the native view has: PastedImage's clipboard, SetAutocorrect.
func (s *ComposerState) Editor() *base.NativeEditor { return s.ed }

// Text is the text, UTF-8: the editor's buffer, valid until the next change.
func (s *ComposerState) Text() []byte {
	if s.ed != nil {
		return s.ed.Text()
	}
	return s.buf.Bytes()
}

// SetText sets the whole text (restoring a draft); nil empties it. It clears
// the undo stack, and fires no Changed. A hosted editor refuses it while an
// input method composes (platform.ErrComposing).
func (s *ComposerState) SetText(b []byte) error {
	if s.ed != nil {
		return s.ed.SetText(b)
	}
	s.buf.SetBytes(b)
	return nil
}

// Selection is the selection in bytes.
func (s *ComposerState) Selection() platform.TextSelection {
	if s.ed != nil {
		return s.ed.Selection()
	}
	return s.drawnSelection()
}

// Replace replaces the bytes r with text and puts the selection at sel (bytes
// of the new text), as one undo step: the completion's accept. The hosted
// editor refuses it while an input method composes (platform.ErrComposing).
func (s *ComposerState) Replace(r platform.TextRange, text []byte, sel platform.TextRange) error {
	if s.ed != nil {
		return s.ed.Replace(r, text, sel)
	}
	b := s.buf.Bytes()
	if r.Start < 0 || r.End < r.Start || r.End > len(b) || !boundary(b, r.Start) || !boundary(b, r.End) || !utf8.Valid(text) {
		return platform.ErrNotBoundary
	}
	if n := len(b) - (r.End - r.Start) + len(text); n > s.maxLen {
		return platform.ErrTooLong
	}
	s.buf.SetSelection(r.Start, r.End)
	s.buf.Insert(text)
	s.buf.SetSelection(sel.Start, sel.End)
	return nil
}

// boundary reports whether byte offset i of b is on a UTF-8 scalar boundary.
func boundary(b []byte, i int) bool { return i == len(b) || utf8.RuneStart(b[i]) }

// SetSpans styles ranges of the text (mentions); the style is display-only.
// A drawn composer shows none.
func (s *ComposerState) SetSpans(spans []platform.TextSpan) error {
	if s.ed != nil {
		return s.ed.SetSpans(spans)
	}
	return nil
}

// Focus gives the composer the keyboard. A hosted editor takes it now; the
// drawn field at the next frame.
func (s *ComposerState) Focus(k *base.Kit) {
	if s.ed != nil {
		s.ed.Focus()
		return
	}
	if s.fieldKey != 0 {
		c := k.Context()
		c.SetFocus(s.fieldKey)
		c.SetFocusVisible(false)
	}
}

// Release frees the hosted editor's view. The state is not used afterwards.
func (s *ComposerState) Release() {
	if s.ed != nil {
		s.ed.Release()
	}
}

// ComposerOpts are a composer's options, passed by value.
type ComposerOpts struct {
	// Attachments are the chips of the attachments row, above the text.
	Attachments []ComposerAttachment
	// Sending: a send is in flight. The send button shows a spinner and
	// does nothing, and Enter sends nothing, until the app clears it. The
	// text stays editable.
	Sending bool
	// Still draws the sending spinner at rest and asks for no frames:
	// screenshots and comparisons.
	Still bool
	// SendIcon is the glyph of the send button, when HasSendIcon; otherwise
	// an up chevron. An app that wants an up arrow adds the symbol
	// (arrow.up) with Kit.AddIcon.
	SendIcon    icon.ID
	HasSendIcon bool

	// Completion is the state of the completion list the app shows, nil
	// while there is none; Items are its rows. While it shows, the composer
	// routes the keys of the list to it (Up, Down, Tab, Enter and Escape: as
	// ui.FieldCommands, whether the text is a drawn field or the hosted
	// editor), builds the list at the caret (outside the composer, never
	// over the hosted view), and reports what it did in ComposerSignal. The
	// app decides when to open it (from Changed and Selected), and applies
	// an accept with Replace.
	Completion *CompletionState
	Items      []CompletionItem
	// CompletionLabel is the list's accessible name ("Suggestions").
	CompletionLabel string
}

// ComposerSignal is what a composer reports in a frame.
type ComposerSignal struct {
	// ID is the field's box, Editor the box of the text (the hosted view's)
	// and Key the key that takes the keyboard focus.
	ID, Editor ui.ID
	Key        ui.Key
	// Changed: the text changed (typing, a paste, an input method, undo,
	// Replace; not SetText). Composing: it still holds marked text, which
	// the app must not rewrite (Changed fires again without it at the
	// commit). Selected: the selection moved.
	Changed, Composing, Selected bool
	// Submitted: the send button was pressed, or Enter sent (by the Enter
	// policy), and there was something to send (text other than white
	// space, or an attachment) and no send in flight. The app sends, clears
	// the text (SetText(nil)) and sets Sending.
	Submitted bool
	// Cancelled: Escape with no completion showing, or the editor ended the
	// app's completion (it lost the keyboard focus, or an input method began
	// composing).
	Cancelled bool
	// Focused: the text has the keyboard focus.
	Focused bool
	// PastedImage: a paste brought an image, which the app takes from the
	// clipboard; nothing was inserted (hosted editor only).
	PastedImage bool
	// HeightChanged: the text grew or shrank a line, and the field with it.
	HeightChanged bool
	// Removed is the attachment whose remove button was pressed, -1 for none.
	Removed int
	// Completion is what the list's keys did this frame (CompletionState's
	// Take): ResultAccept says the highlighted row is chosen, ResultCancel
	// that the list is cancelled. The composer has already stopped
	// diverting keys; the app stops passing Completion.
	Completion CompletionResult
	// Chosen is the list's row clicked this frame, -1 for none; Dismissed a
	// press outside the list. Both ask the app to stop passing Completion.
	Chosen    int
	Dismissed bool
}

// ComposerRecipe is the look of the composer: a rounded text field (FieldLook's
// fill and outline) whose last line holds the text and the send button, with
// the attachments row above. Metrics in points.
type ComposerRecipe struct {
	// Radius is the field's corner radius (a capsule of one line), BorderW
	// its outline, Pad the space between the outline and its content.
	Radius, BorderW, Pad float32
	// Gap is the space between the text and the send button, SendSize the
	// button's side (a circle).
	Gap, SendSize float32
	// InsetX and InsetY are the space between the text view and its text;
	// AppKit adds 5 pt of line fragment padding at the sides. They make a
	// line of the hosted view as high as the send button.
	InsetX, InsetY float32
	// ExpandedMinH is the Expanded variant's least height of text, and
	// MaxLines how many lines each variant grows to before it scrolls.
	ExpandedMinH float32
	MaxLines     [NumComposerVariants]int
	// ChipH is a chip's height, ChipPad its padding at the sides, ChipGap the
	// space between a chip's parts and between chips, ChipMaxW the widest its
	// name makes it, Thumb the thumbnail's side and RemoveSize the remove
	// button's; AttachPad is the attachments row's inset from the field's
	// content, and AttachGap the space below it.
	ChipH, ChipPad, ChipGap, ChipMaxW, Thumb, RemoveSize, AttachPad, AttachGap float32
	// Fill and Border are FieldLook's; Text, Placeholder and TextDisabled
	// colour the drawn field's text.
	Fill, Border, Text, Placeholder, TextDisabled Tint
	// Chip is a chip's fill, ChipText its name, ChipRemove its remove button
	// and ChipRemoveHot the button under the pointer.
	Chip, ChipText, ChipRemove, ChipRemoveHot Tint
}

// ComposerLook is the composer's recipe.
var ComposerLook = ComposerRecipe{
	Radius: 18, BorderW: 1, Pad: 4,
	Gap: 4, SendSize: 28,
	InsetX: 6, InsetY: 6,
	ExpandedMinH: 64,
	MaxLines:     [NumComposerVariants]int{6, 8},
	ChipH:        24, ChipPad: 8, ChipGap: 6, ChipMaxW: 160, Thumb: 16, RemoveSize: 16, AttachPad: 6, AttachGap: 2,
	Fill: Of(theme.InputFill), Border: Of(theme.FieldOutline),
	Text: Of(theme.ControlText), Placeholder: Of(theme.Placeholder), TextDisabled: Of(theme.TextDisabled),
	Chip: Of(theme.Fill2), ChipText: Of(theme.Label), ChipRemove: Of(theme.Label2), ChipRemoveHot: Of(theme.Label),
}

// hasContent reports whether b holds anything but white space.
func hasContent(b []byte) bool {
	for _, c := range b {
		switch c {
		case ' ', '\t', '\n', '\r':
		default:
			return true
		}
	}
	return false
}

// Composer builds the chat composer: a rounded field holding the attachments
// row (when there are attachments), the text and the send button, with the
// completion list at the caret while the app shows one. The text is a hosted
// NSTextView on macOS (spell check, autocorrect, dictation, the emoji palette,
// Writing Tools; it grows from one line to six, then scrolls) and a drawn
// single-line field elsewhere. It fills the parent's width.
//
// The send button is enabled when there is text (other than white space) or an
// attachment, and no send is in flight (Sending); Enter sends by the Enter
// policy. The focus ring shows whenever the text has the keyboard, in a key
// window, as a text field's does.
//
//	sig := rexui.Composer(k, "Message", v.composer, rexui.ComposerOpts{Attachments: v.files, Sending: v.sending})
//	if sig.Submitted { v.send(v.composer.Text()); v.composer.SetText(nil) }
//	if sig.Removed >= 0 { v.files = slices.Delete(v.files, sig.Removed, sig.Removed+1) }
//
// The editor's signals are taken as the frame begins, so what the app does with
// them is drawn in the next frame, which Composer asks for.
func Composer(k *base.Kit, label string, st *ComposerState, o ComposerOpts) ComposerSignal {
	c, r := k.Context(), k.Theme()
	rc := &ComposerLook
	sc := k.Scale()
	display, ident := ui.SplitLabel(label)
	disabled := k.Disabled()
	hosted := st.ed != nil && !disabled // a disabled composer draws its text: the view cannot be made read-only
	look := k.TakeForce()
	sig := ComposerSignal{Removed: -1, Chosen: -1}

	// the completion this frame: a list with rows, and no marked text
	var cmdr ui.FieldCommander
	if o.Completion != nil && len(o.Items) > 0 && !disabled {
		cmdr = o.Completion.commander(len(o.Items))
	}

	key := ui.HashString(c.ParentKey(), ident)
	prev := c.RectOf(key)
	attached := len(o.Attachments) > 0

	var ns base.NativeEditorSignal
	submit := false

	// the field
	pad := pt(sc, rc.Pad)
	c.NextBG(rc.Fill.RGBA(r))
	c.NextBorder(rc.Border.RGBA(r), pt(sc, rc.BorderW))
	c.NextRadius(pt(sc, rc.Radius))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossStart)
	c.NextGap(pt(sc, rc.AttachGap))
	c.NextPadding(pad, pad, pad, pad)
	k.ApplySize(ui.Pct(1), ui.Children(1))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(display)
	flags := ui.DrawBackground | ui.DrawBorder
	if !disabled {
		flags |= ui.Clickable
	}
	field := c.BoxKeyed(flags, key, nil)
	sig.ID = field
	c.Push(field)

	// the attachments row
	if attached {
		sig.Removed = composerAttachments(k, key, o.Attachments, rc)
	}

	if sig.Removed >= 0 {
		st.Focus(k) // the press took the keyboard; the caret stays in the text
	}

	// the line: the text and the send button, bottoms aligned
	k.NextGap(rc.Gap)
	k.NextCross(ui.CrossEnd)
	k.Row("line")

	var drawn base.FieldSignal
	var ti base.TextInput
	var ringVisual base.Visual
	inactive := k.Inactive() || look&base.LookInactive != 0
	switch {
	case hosted:
		if st.variant == ComposerExpanded && st.ed.Height() < rc.ExpandedMinH {
			k.NextHeight(k.Fixed(rc.ExpandedMinH))
		}
		ns = k.NativeEditor(st.ed, label)
		sig.Key = c.KeyOf(ns.ID)
		sig.Editor = ns.ID
		sig.Changed, sig.Composing, sig.Selected = ns.Changed, ns.Composing, ns.Selected
		sig.Cancelled, sig.PastedImage, sig.HeightChanged = ns.Cancelled, ns.PastedImage, ns.HeightChanged
		sig.Focused = ns.Focused
		if st.completing && ns.Cancelled && !ns.Focused && cmdr != nil && !sig.Composing && inside(c.Input().MouseX, c.Input().MouseY, st.list) {
			// A press on the list took the first responder from the text view
			// (any press on the window's own view does), which ended the
			// completion. The press is the list's: give the keyboard back and
			// keep the list for the click.
			st.ed.Focus()
			sig.Cancelled, ns.Cancelled = false, false
			sig.Focused = true
		}
		submit = ns.Submitted
		ringVisual = base.Visual{Inactive: inactive}
		if sig.Focused || look&base.LookFocused != 0 {
			ringVisual.Focus = 1
		}
	case st.ed != nil: // disabled: the text, drawn
		sig.Editor = composerStatic(k, st, rc)
		ringVisual = base.Visual{Inactive: inactive, Disabled: true}
	default:
		sig.Editor, drawn, ti = composerField(k, st, label, look, cmdr)
		sig.Changed, sig.Focused = drawn.Changed, drawn.Focused
		sig.Composing = k.Composing(drawn)
		sig.Cancelled = drawn.Cancelled
		submit = drawn.Submitted
		sel := st.drawnSelection()
		sig.Selected = sel != st.sel
		st.sel = sel
		st.fieldKey, sig.Key = ti.Key, ti.Key
		ringVisual = ti.Visual
	}
	ringVisual = ringVisual.TextEntry()

	// the send button
	text := st.Text()
	canSend := !disabled && !o.Sending && (hasContent(text) || attached)
	k.Force(look & base.LookInactive)
	pressed := composerSend(k, o, rc, canSend)
	k.EndRow()
	if !disabled && c.Signal(field).Clicked && !pressed && !sig.Focused {
		st.Focus(k) // a press on the field's padding puts the caret in the text
	}

	// Enter by the policy, and the keys of a completion, take effect
	if cmdr != nil && hosted {
		for cmd := st.ed.TakeFieldCommand(); cmd != ui.FieldNone; cmd = st.ed.TakeFieldCommand() {
			c.RequestFrame()
			if cmdr.FieldCommand(cmd, 0) {
				continue
			}
			// a key the list does not use: the hosted editor already took it
			switch cmd {
			case ui.FieldInsertNewline: // Enter with no row highlighted is the editor's own
				if st.enter == platform.EnterSends {
					submit = true
				} else {
					st.newline()
				}
			case ui.FieldInsertTab, ui.FieldInsertBacktab: // the list closes; Tab does not leave the text
				cmdr.FieldCommand(ui.FieldCancel, 0)
			}
		}
	}
	if o.Completion != nil {
		sig.Completion = o.Completion.Take()
	}
	if sig.Completion == ResultAccept || sig.Completion == ResultCancel {
		cmdr = nil // the list is over: stop diverting keys at once
	}
	if hosted {
		if cmdr == nil || sig.Composing || !sig.Focused {
			if st.ed.CompletionActive() {
				st.ed.SetCompletionActive(false)
			}
		} else if !st.ed.CompletionActive() {
			st.ed.SetCompletionActive(true)
		}
		if hosted && (sig.Changed || sig.Selected || sig.Cancelled || sig.HeightChanged || sig.PastedImage || submit) {
			c.RequestFrame() // what the app does with these is drawn in the next frame
		}
	}

	// a send needs something to send
	sig.Submitted = (submit || pressed) && canSend
	if pressed && canSend {
		st.Focus(k) // the caret stays in the text after the button (the press took the keyboard)
	}

	// the focus ring: whenever the text has the keyboard, in a key window
	k.FocusRing(field, ringVisual, pt(sc, rc.Radius))
	c.Pop()

	// the completion list, at the caret and outside the field
	completing := cmdr != nil && !sig.Composing && (sig.Focused || look&base.LookFocused != 0)
	if completing {
		var caret [4]float32
		if hosted {
			if !st.completing || sig.Changed || sig.Selected || sig.HeightChanged || st.caretOK == false {
				st.caret, st.caretOK = st.ed.CaretRect()
			}
			caret = st.caret
		} else if ime, ok := c.IMERect(); ok {
			caret = [4]float32{ime[0], ime[1], ime[0] + 1, ime[1] + ime[3]}
			st.caretOK = true
		}
		if prev[2] > prev[0] && prev[3] > prev[1] {
			if !st.caretOK {
				caret = [4]float32{prev[0], prev[1], prev[0] + 1, prev[3]}
			}
			lo := CompletionOpts{Exclude: prev, Above: true} // above, as a composer at the bottom of a chat: below when there is no room
			name := o.CompletionLabel
			if name == "" {
				name = "Suggestions"
			}
			cs := CompletionList(k, name, o.Completion, o.Items, caret, lo)
			sig.Chosen, sig.Dismissed = cs.Chosen, cs.Dismissed
			st.list = c.Rect(cs.ID)
		}
	}
	st.completing = completing
	if !completing {
		st.caretOK = false
	}
	return sig
}

// inside reports whether (x, y) is in rectangle r (x0, y0, x1, y1).
func inside(x, y float32, r [4]float32) bool { return x >= r[0] && x < r[2] && y >= r[1] && y < r[3] }

// drawnSelection is the drawn field's selection, in bytes.
func (s *ComposerState) drawnSelection() platform.TextSelection {
	lo, hi := s.buf.Selection()
	if s.buf.Caret() == lo {
		return platform.TextSelection{Anchor: hi, Focus: lo}
	}
	return platform.TextSelection{Anchor: lo, Focus: hi}
}

// newline inserts a line break at the selection of the hosted editor.
func (s *ComposerState) newline() {
	r := s.ed.Selection().Range()
	_ = s.ed.Replace(r, []byte{'\n'}, platform.TextRange{Start: r.Start + 1, End: r.Start + 1})
}

// composerField builds the drawn single-line text field of the composer, in
// the send button's height.
func composerField(k *base.Kit, st *ComposerState, label string, look base.Look, cmdr ui.FieldCommander) (ui.ID, base.FieldSignal, base.TextInput) {
	c, r := k.Context(), k.Theme()
	rc := &ComposerLook
	sc := k.Scale()
	s := &r.Sizes[theme.SizeRegular]
	k.Force(look)
	h := pt(sc, rc.SendSize)
	ti := k.BeginTextInput(label, h, base.HoverOff)
	font := s.Font.Font
	padL := pt(sc, rc.InsetX+5)
	tc := rc.Text.RGBA(r)
	if ti.Disabled {
		tc = rc.TextDisabled.RGBA(r)
	}
	c.NextBG(0)
	c.NextSize(ui.Pct(1), ui.Px(h))
	c.NextPadding(padL, 0, padL, 0)
	c.NextFont(font)
	c.NextTextColor(tc)
	if cmdr != nil && !ti.Disabled {
		c.NextFieldCommander(cmdr, "") // the list's keys, as the hosted editor's are offered
	}
	res := k.TextInput(&ti, &st.buf, ui.RoleTextField, false)
	if st.buf.Len() == 0 && !k.Composing(res) && st.hint != "" {
		pc := rc.Placeholder.RGBA(r)
		if ti.Disabled {
			pc = rc.TextDisabled.RGBA(r)
		}
		theme.At(c, padL, 0, 0, h)
		c.NextSize(ui.TextSize(base.LabelSlack), ui.Px(h))
		c.NextFont(font)
		c.NextTextColor(pc)
		c.BoxKeyed(ui.DrawText|base.Floating, ui.KeyN(ti.WrapKey, 2), base.StrBytes(st.hint))
	}
	k.EndTextInput(&ti)
	return ti.Wrap, res, ti
}

// composerStatic draws the text of a disabled hosted composer, which is not
// built as a view (it cannot be made read-only): the text wrapped, or the
// placeholder, in the disabled colour, a line high at least.
func composerStatic(k *base.Kit, st *ComposerState, rc *ComposerRecipe) ui.ID {
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	s := &r.Sizes[theme.SizeRegular]
	txt := st.ed.Text()
	if len(txt) == 0 {
		txt = base.StrBytes(st.hint)
	}
	vpad := theme.Snap((pt(sc, rc.SendSize) - s.Font.Line) / 2)
	c.NextPadding(pt(sc, rc.InsetX+5), max(vpad, 0), pt(sc, rc.InsetX+5), max(vpad, 0))
	c.NextSize(ui.Pct(1), ui.TextSize(0))
	c.NextFont(s.Font.Font)
	c.NextTextColor(rc.TextDisabled.RGBA(r))
	c.NextRole(ui.RoleTextField)
	c.NextSemName("Message")
	c.NextSemValueBytes(st.ed.Text())
	return c.BoxKeyed(ui.DrawText|ui.WrapText, ui.HashString(c.ParentKey(), "static"), txt)
}

// composerAttachments builds the attachments row and returns the chip whose
// remove button was pressed, -1 for none.
func composerAttachments(k *base.Kit, fieldKey ui.Key, list []ComposerAttachment, rc *ComposerRecipe) (removed int) {
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	removed = -1
	font := r.Sizes[theme.SizeRegular].Font.Font
	rowKey := ui.KeyN(fieldKey, 0xA7)
	chipH := pt(sc, rc.ChipH)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(pt(sc, rc.ChipGap))
	c.NextPadding(pt(sc, rc.AttachPad), pt(sc, rc.AttachPad/2), pt(sc, rc.AttachPad), 0)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextRole(ui.RoleList)
	c.NextSemName("Attachments")
	row := c.BoxKeyed(ui.ScrollX|ui.Clip, rowKey, nil) // a row longer than the field scrolls sideways
	c.Push(row)
	for i := range list {
		a := &list[i]
		ck := ui.KeyN(rowKey, uint64(1+i))
		nameW := theme.Snap(min(k.TextWidth(font, a.Name)+base.LabelSlack, pt(sc, rc.ChipMaxW)))
		c.NextBG(rc.Chip.RGBA(r))
		c.NextRadius(chipH / 2)
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextGap(pt(sc, rc.ChipGap/2))
		c.NextPadding(pt(sc, rc.ChipPad), 0, pt(sc, 3), 0)
		c.NextSize(ui.Children(1), ui.Px(chipH))
		c.NextRole(ui.RoleListItem)
		c.NextSemName(a.Name)
		c.NextSemPos(i, len(list))
		chip := c.BoxKeyed(ui.DrawBackground, ck, nil)
		c.Push(chip)
		thumb := pt(sc, rc.Thumb)
		if a.Thumb != 0 {
			c.NextSize(ui.Px(thumb), ui.Px(thumb))
			k.Image(uint64(i), a.Thumb, ui.FitFill, pt(sc, 4), "")
		} else {
			k.Icon(icon.File, rc.ChipRemove.RGBA(r))
		}
		c.NextSize(ui.Px(nameW), ui.Px(chipH))
		c.NextFont(font)
		c.NextTextColor(rc.ChipText.RGBA(r))
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(ck, 1), base.StrBytes(a.Name))
		// the remove button
		rk := ui.KeyN(ck, 2)
		v := k.VisualOf(rk, base.HoverOn)
		col := mix(rc.ChipRemove.RGBA(r), rc.ChipRemoveHot.RGBA(r), v.Hot)
		side := pt(sc, rc.RemoveSize)
		c.NextSize(ui.Px(side), ui.Px(side))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextJustify(ui.JustifyCenter)
		c.NextRadius(side / 2)
		c.NextRole(ui.RoleButton)
		c.NextSemNameBytes(c.Fmt().Str("Remove ").Str(a.Name).Bytes())
		rm := c.BoxKeyed(k.InteractFlags(base.HoverOn), rk, nil)
		c.Push(rm)
		if f, disp := k.Icons().Prepare(c, icon.Close, col); f != 0 {
			c.BoxKeyed(f, ui.KeyN(rk, 1), disp)
		}
		k.FocusRing(rm, v, side/2)
		c.Pop()
		c.Pop()
		if !k.Disabled() && c.Signal(rm).Clicked {
			removed = i
		}
	}
	c.Pop()
	return removed
}

// composerSend builds the send button and reports whether it was pressed. It
// is inert unless canSend; a send in flight shows a spinner on it.
func composerSend(k *base.Kit, o ComposerOpts, rc *ComposerRecipe, canSend bool) (pressed bool) {
	c, r := k.Context(), k.Theme()
	sc := k.Scale()
	prc := &ButtonRecipes[ButtonPrimary]
	name := "Send###send"
	if o.Sending { // a send in flight: the neutral button with a spinner, which does nothing
		name = "Sending###send"
		prc = &ButtonRecipes[ButtonDefault]
		canSend = false
	}
	k.BeginDisabled(!canSend && !o.Sending)
	p := k.PressState(name, base.HoverOff)
	fill, fg, _ := prc.Colors(r, p.Visual)
	side := pt(sc, rc.SendSize)
	c.NextBG(fill)
	c.NextRadius(side / 2)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	k.BeginPress(&p, ui.RoleButton, ui.Px(side), ui.Px(side), ui.DrawBackground, nil)
	if o.Sending {
		Spinner(k, "Sending", SpinnerOpts{Size: theme.SizeSmall, Still: o.Still})
	} else {
		ic := icon.ChevronUp
		if o.HasSendIcon {
			ic = o.SendIcon
		}
		if f, disp := k.Icons().Prepare(c, ic, fg); f != 0 {
			c.BoxKeyed(f, ui.KeyN(p.Key, 1), disp)
		}
	}
	k.FocusRing(p.ID, p.Visual, side/2)
	pressed = k.EndPress(&p).Clicked
	k.EndDisabled()
	return pressed
}
