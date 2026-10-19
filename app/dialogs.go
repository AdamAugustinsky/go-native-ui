package app

import "gonativeui/platform"

// Native alerts and file panels (phase 6, N2; platform/dialog.go has the
// contract). Until the application object (F3) delivers service results, the
// answer of a dialog is queued here, in the order the sheets ended, and read
// with TakeDialog from a frame, like ui.Context.TakeCommand.
//
// Where the OS has no native dialogs (Windows and Linux so far) the three
// calls return an error that satisfies errors.Is(err, errors.ErrUnsupported),
// and the app shows its drawn dialog (widget.Dialog) instead.

// maxDialogResults bounds the unread dialog results. A window has one dialog
// open at a time, so this is never reached by an app that takes its results;
// the oldest result is dropped past it.
const maxDialogResults = 8

// Alert shows an alert sheet on the window and returns its ticket; the answer
// is read with TakeDialog. Call it from Build or an event handler, not from a
// callback of the OS.
func (a *App) Alert(s platform.AlertSpec) (platform.DialogTicket, error) {
	if a.Win == nil {
		return 0, platform.ErrUnsupported
	}
	return a.Win.Alert(s)
}

// OpenPanel shows an open panel sheet and returns its ticket; the paths come
// back in the result read with TakeDialog.
func (a *App) OpenPanel(s platform.OpenSpec) (platform.DialogTicket, error) {
	if a.Win == nil {
		return 0, platform.ErrUnsupported
	}
	return a.Win.OpenPanel(s)
}

// SavePanel shows a save panel sheet and returns its ticket; the path comes
// back in the result read with TakeDialog.
func (a *App) SavePanel(s platform.SaveSpec) (platform.DialogTicket, error) {
	if a.Win == nil {
		return 0, platform.ErrUnsupported
	}
	return a.Win.SavePanel(s)
}

// CancelDialog withdraws a dialog: its sheet closes and no result arrives.
// It reports whether the ticket was still open.
func (a *App) CancelDialog(t platform.DialogTicket) bool {
	return a.Win != nil && a.Win.CancelDialog(t)
}

// TakeDialog returns the oldest dialog result not read yet, and false when
// there is none. A result carries the ticket its call returned.
func (a *App) TakeDialog() (platform.DialogResult, bool) {
	if len(a.dlg) == 0 {
		return platform.DialogResult{}, false
	}
	r := a.dlg[0]
	a.dlg = a.dlg[:copy(a.dlg, a.dlg[1:])]
	return r, true
}

// onDialog queues the result of an EvDialog and asks for a frame to show it.
func (a *App) onDialog(e *platform.Event) {
	if len(a.dlg) >= maxDialogResults {
		a.dlg = a.dlg[:copy(a.dlg, a.dlg[1:])]
	}
	a.dlg = append(a.dlg, a.Win.DialogResult(*e))
	a.p.invalidate()
}
