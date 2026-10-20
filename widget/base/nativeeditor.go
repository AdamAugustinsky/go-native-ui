package base

import (
	"gonativeui/platform"
	"gonativeui/ui"
)

// NativeEditor is the composer's editor: a native multi-line text view hosted
// in the window (platform.Editor; on macOS an NSTextView, which brings spell
// check, autocorrect, text replacement, dictation, the emoji palette, Writing
// Tools, Services, undo and VoiceOver). The app owns it: it makes it once
// (NewNativeEditor, in Init), builds its box every frame (Kit.NativeEditor)
// and reads and edits the text through the embedded Editor, in UTF-8 bytes:
//
//	ed, err := base.NewNativeEditor(a.Win, platform.EditorOptions{Placeholder: "Message", MaxLen: 4000})
//	…
//	s := k.NativeEditor(ed, "Message")
//	if s.Submitted { send(ed.Text()); ed.SetText(nil) }
//	if s.Changed && !s.Composing { // open or filter a completion at the caret
//		r, _ := ed.CaretRect()
//		…
//	}
//	for c := ed.TakeCommand(); c != platform.CmdNone; c = ed.TakeCommand() { … } // while SetCompletionActive(true)
//
// The completion handoff: when the app opens a completion popup (from Changed
// and Selection, placed at CaretRect above or below the editor, so that it
// does not hide the host), it calls SetCompletionActive(true); Up, Down, Tab,
// Shift+Tab, Enter and Escape then come as commands and leave the text alone,
// and every other key keeps typing. Accepting calls Replace with the trigger's
// range (one undo step), cancelling SetCompletionActive(false). The editor ends
// the completion itself (Cancelled) when it loses the keyboard focus or an
// input method starts composing; Replace is refused while it composes
// (platform.ErrComposing).
//
// On Windows and Linux there is no native editor yet: NewNativeEditor returns
// an error (errors.Is(err, errors.ErrUnsupported)) and the app uses the drawn
// text field.
type NativeEditor struct {
	// Editor is the native editor (nil for an offscreen window, where
	// Kit.NativeEditor reserves a one-line box with no view).
	*platform.Editor
}

// NewNativeEditor makes the editor in window w (nil: an offscreen render, no
// view). At a safe point (Init, or between frames), main thread.
func NewNativeEditor(w *platform.Window, o platform.EditorOptions) (*NativeEditor, error) {
	if w == nil {
		return &NativeEditor{}, nil
	}
	e, err := w.NewEditor(o)
	if err != nil {
		return nil, err
	}
	return &NativeEditor{Editor: e}, nil
}

// NativeEditorSignal is what the editor reported since the last frame.
type NativeEditorSignal struct {
	// ID is the host's box.
	ID ui.ID
	// Changed: the text changed (not by SetText). Composing: it still holds
	// marked text, which the app must not rewrite (Changed fires again
	// without it at the commit).
	Changed, Composing bool
	// Selected: the selection moved.
	Selected bool
	// Submitted: Enter (by the Enter policy) asked to send.
	Submitted bool
	// Cancelled: Escape with no completion active, or the editor ended the
	// app's completion (focus lost, composition started).
	Cancelled bool
	// HeightChanged: Height changed (the box follows in this frame).
	HeightChanged bool
	// PastedImage: a paste brought an image, which the app takes from the
	// clipboard; nothing was inserted.
	PastedImage bool
	// Focused: the text view holds the keyboard focus.
	Focused bool
}

// offscreenLinePt is the box height of an editor with no view: one line of
// 13 pt text.
const offscreenLinePt = 16

// NativeEditor builds the editor's box in the current parent: as wide as the
// parent unless NextSize says otherwise, Height points high, focusable (Tab
// reaches the text view through it), with the text-field role and label's
// shown part as its accessible name. It takes the editor's signals of the
// frame. It allocates nothing.
func (k *Kit) NativeEditor(ed *NativeEditor, label string) NativeEditorSignal {
	c := k.c
	display, ident := ui.SplitLabel(label)
	var s platform.EditorSignals
	var host uint32
	h := float32(offscreenLinePt)
	if ed.Editor != nil {
		s = ed.TakeSignals()
		host = uint32(ed.Host())
		h = ed.Height()
	}
	k.ApplySize(ui.Pct(1), ui.Px(h*c.Scale()))
	c.NextRole(ui.RoleTextField)
	c.NextSemName(display)
	if ed.Editor != nil {
		c.NextSemValueBytes(ed.Text())
	}
	id := c.Host(host, ui.HashString(c.ParentKey(), ident), ui.HostFocusable)
	sig := NativeEditorSignal{
		ID:            id,
		Changed:       s&platform.EditorChanged != 0,
		Composing:     s&platform.EditorComposing != 0,
		Selected:      s&platform.EditorSelected != 0,
		Submitted:     s&platform.EditorSubmitted != 0,
		Cancelled:     s&platform.EditorCancelled != 0,
		HeightChanged: s&platform.EditorHeightChanged != 0,
		PastedImage:   s&platform.EditorPastedImage != 0,
	}
	if ed.Editor != nil {
		sig.Focused = ed.Focused()
	}
	return sig
}
