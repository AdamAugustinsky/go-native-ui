package widget

import (
	"gonativeui/rexui"
)

// Dialog is the Rex modal dialog, with the widget action API.
type Dialog struct{ rexui.Dialog }

// BeginDialog starts a modal dialog while *open is true: an in-window overlay
// with a sheet below the titlebar, the title,
// whatever content the app builds, and its actions:
//
//	d := k.BeginDialog("confirm", "Delete 3 files?", &v.askDelete)
//	if d.Visible {
//		k.Paragraph("They cannot be recovered.")
//		d.BeginActions()
//		if d.Action("Cancel", widget.ButtonDefault) { … }
//		if d.Action("Delete", widget.ButtonPrimary) { v.delete() }
//		d.EndActions()
//	}
//	k.EndDialog(&d)
//
// Everything beneath the dialog is inert: no pointer, scrolling, drags,
// text input or shortcuts reach it (ui.OverlayModal), and Tab stays inside.
// Commands of a menu model are held back too, except the edit commands for a
// text field in the dialog and the ones marked menu.Command.InModal.
// Choosing an action closes the dialog (sets *open to false) and returns true
// from Action once. Escape cancels: it closes the dialog and sets Cancelled.
// Enter chooses the primary action (the first ButtonPrimary) when the focus is
// not on another action or a text field. The primary action has the focus
// when the dialog opens; a disabled one (BeginDisabled) neither takes the
// focus nor Enter. Text that is too long wraps (Paragraph).
//
// With dialogs stacked, only the topmost takes Enter and Escape, whatever
// order they are built in (ui.Context.OverlayTopmost); a dialog that opens
// above another takes them from the next frame on. Escape in a text field
// inside a dialog cancels the dialog too, unless the field is showing an
// input-method composition, which Escape cancels instead.
func (k *Kit) BeginDialog(label, title string, open *bool) Dialog {
	return Dialog{Dialog: rexui.BeginDialog(k.Kit, label, open, rexui.DialogOpts{Title: title})}
}

// Action builds a dialog action and closes the dialog when chosen.
func (d *Dialog) Action(label string, kind ButtonKind) bool {
	return d.Dialog.Action(label, kind.variant())
}

// EndDialog ends the dialog and applies Escape.
func (k *Kit) EndDialog(d *Dialog) { rexui.EndDialog(k.Kit, &d.Dialog) }
