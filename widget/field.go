package widget

import (
	"gonativeui/rexui"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// FieldSignal is what a text field reports (base.FieldSignal).
type FieldSignal = base.FieldSignal

// FieldKey is the key of the box that takes the keyboard focus in the field
// that TextField or SearchField builds with label inside the box whose key is
// parent. An app moves the focus there with ui.Context.SetFocus before the
// field has been built.
func FieldKey(parent ui.Key, label string) ui.Key { return base.FieldKey(parent, label) }

// TextField builds a single-line text field editing buf (rexui.TextField),
// with the placeholder shown while it is empty. It is ui.Context.TextField
// (the caret, selection, mouse, copy, cut, paste, undo and input method are
// documented there) in macOS's bezel: the field fill and outline, the focus
// ring while it has the focus, the placeholder colour, and the disabled
// colours. NextMono sets it in the monospaced style. The width fills the
// parent unless NextWidth says otherwise.
func (k *Kit) TextField(label string, buf *ui.TextBuffer, placeholder string) FieldSignal {
	return rexui.TextField(k.Kit, label, buf, placeholder, rexui.FieldOpts{})
}

// SearchField is a TextField with a magnifying glass at its start and a clear
// button at its end while it has text; Escape clears it too
// (rexui.SearchField).
func (k *Kit) SearchField(label string, buf *ui.TextBuffer, placeholder string) FieldSignal {
	return rexui.SearchField(k.Kit, label, buf, placeholder, rexui.FieldOpts{})
}
