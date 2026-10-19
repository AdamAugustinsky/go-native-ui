package app

import (
	"slices"

	"gonativeui/platform"
)

// Native alerts and file panels (phase 6, N2; platform/dialog.go has the
// contract). Until the application object (F3) delivers service results, the
// answer of a dialog is queued here, in the order the sheets ended, and read
// with TakeDialog from a frame, like ui.Context.TakeCommand.
//
// A call returns its ticket at once but only queues the request: the sheet is
// shown after the frame in which the call was made has ended (after ui.End and
// the present), at a safe point, never from inside Build. The specs are copied
// at the call, so the caller may reuse its slices. The ticket is the app's own;
// the results carry it. Where the OS has no native dialogs (Windows and Linux
// so far) the calls fail at once with an error that satisfies
// errors.Is(err, errors.ErrUnsupported), and the app shows its drawn dialog
// (widget.Dialog) instead. A dialog that cannot be shown after all (the window
// already has one open) comes back as a cancelled result with Err set.

// maxDialogResults bounds the unread dialog results. A window has one dialog
// open at a time, so this is never reached by an app that takes its results;
// the oldest result is dropped past it.
const maxDialogResults = 8

// maxDialogQueue bounds the dialogs waiting to be shown.
const maxDialogQueue = 8

// dialogTarget is the part of platform.Window that dialogs use.
type dialogTarget interface {
	Alert(platform.AlertSpec) (platform.DialogTicket, error)
	OpenPanel(platform.OpenSpec) (platform.DialogTicket, error)
	SavePanel(platform.SaveSpec) (platform.DialogTicket, error)
	CancelDialog(platform.DialogTicket) bool
	DialogResult(platform.Event) platform.DialogResult
}

// pendingDialog is a dialog queued for the end of the frame, with its own
// copies of the spec.
type pendingDialog struct {
	ticket platform.DialogTicket
	kind   platform.DialogKind
	alert  platform.AlertSpec
	open   platform.OpenSpec
	save   platform.SaveSpec
}

// shownDialog pairs the app's ticket of a dialog on screen with the
// platform's.
type shownDialog struct{ app, plat platform.DialogTicket }

func (a *App) dialogWin() dialogTarget {
	if a.dlgT != nil {
		return a.dlgT
	}
	if a.Win == nil || !platform.NativeDialogs {
		return nil
	}
	return a.Win
}

// enqueueDialog queues d for the end of the frame and returns its ticket.
func (a *App) enqueueDialog(d pendingDialog) (platform.DialogTicket, error) {
	if a.dialogWin() == nil {
		return 0, platform.ErrUnsupported
	}
	if len(a.dlgQ) >= maxDialogQueue {
		return 0, platform.ErrDialogBusy
	}
	a.dlgSeq++
	d.ticket = a.dlgSeq
	a.dlgQ = append(a.dlgQ, d)
	return d.ticket, nil
}

// Alert queues an alert sheet for the window and returns its ticket; the
// answer is read with TakeDialog. The sheet is shown after the current frame.
func (a *App) Alert(s platform.AlertSpec) (platform.DialogTicket, error) {
	if err := s.Validate(); err != nil {
		return 0, err
	}
	s.Buttons, s.Destructive = slices.Clone(s.Buttons), slices.Clone(s.Destructive)
	return a.enqueueDialog(pendingDialog{kind: platform.DialogAlert, alert: s})
}

// OpenPanel queues an open panel sheet and returns its ticket; the paths come
// back in the result read with TakeDialog. The sheet is shown after the
// current frame.
func (a *App) OpenPanel(s platform.OpenSpec) (platform.DialogTicket, error) {
	s.Types = slices.Clone(s.Types)
	return a.enqueueDialog(pendingDialog{kind: platform.DialogOpen, open: s})
}

// SavePanel queues a save panel sheet and returns its ticket; the path comes
// back in the result read with TakeDialog. The sheet is shown after the
// current frame.
func (a *App) SavePanel(s platform.SaveSpec) (platform.DialogTicket, error) {
	s.Types = slices.Clone(s.Types)
	return a.enqueueDialog(pendingDialog{kind: platform.DialogSave, save: s})
}

// CancelDialog withdraws a dialog: if it was not shown yet it never is, and if
// it is on screen its sheet closes; no result arrives either way. It reports
// whether the ticket was still open.
func (a *App) CancelDialog(t platform.DialogTicket) bool {
	for i := range a.dlgQ {
		if a.dlgQ[i].ticket == t {
			a.dlgQ = slices.Delete(a.dlgQ, i, i+1)
			return true
		}
	}
	for i, s := range a.dlgShown {
		if s.app == t {
			a.dlgShown = slices.Delete(a.dlgShown, i, i+1)
			return a.dialogWin() != nil && a.dialogWin().CancelDialog(s.plat)
		}
	}
	return false
}

// flushDialogs shows the queued dialogs. It runs where no frame is being
// built: after a frame has been presented, and at the end of each turn of the
// loop.
func (a *App) flushDialogs() {
	if len(a.dlgQ) == 0 {
		return
	}
	win := a.dialogWin()
	for _, d := range a.dlgQ {
		var plat platform.DialogTicket
		var err error
		switch {
		case win == nil:
			err = platform.ErrUnsupported
		case d.kind == platform.DialogAlert:
			plat, err = win.Alert(d.alert)
		case d.kind == platform.DialogOpen:
			plat, err = win.OpenPanel(d.open)
		default:
			plat, err = win.SavePanel(d.save)
		}
		if err != nil {
			a.pushDialog(platform.DialogResult{Ticket: d.ticket, Kind: d.kind, Outcome: platform.DialogCancelled, Button: -1, Err: err})
			continue
		}
		a.dlgShown = append(a.dlgShown, shownDialog{app: d.ticket, plat: plat})
	}
	a.dlgQ = a.dlgQ[:0]
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

// pushDialog queues a result and asks for a frame to show it.
func (a *App) pushDialog(r platform.DialogResult) {
	if len(a.dlg) >= maxDialogResults {
		a.dlg = a.dlg[:copy(a.dlg, a.dlg[1:])]
	}
	a.dlg = append(a.dlg, r)
	a.p.invalidate()
}

// onDialog turns the EvDialog of a sheet that ended into the result under the
// app's ticket. A dialog the app withdrew has none to deliver.
func (a *App) onDialog(e *platform.Event) {
	r := a.dialogWin().DialogResult(*e)
	for i, s := range a.dlgShown {
		if s.plat == r.Ticket {
			r.Ticket = s.app
			a.dlgShown = slices.Delete(a.dlgShown, i, i+1)
			a.pushDialog(r)
			return
		}
	}
}
