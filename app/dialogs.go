package app

import (
	"slices"

	"gonativeui/platform"
)

// Native alerts and file panels (phase 6, N2; platform/dialog.go has the
// contract). The application object delivers each dialog's answer (platform
// fills in a completion record reserved with the sheet): the Handler gets an
// EvService from platform.SourceDialog with the ticket as ID, and the result
// is queued here, in the order the sheets ended, for TakeDialog, from the
// Handler or from a frame, like ui.Context.TakeCommand. Both work with the
// app's loop suspended (an answer to a pending quit request) and with no
// frame drawn.
//
// A call returns its ticket at once but only queues the request: the sheet is
// shown at the next safe point (after the frame in which the call was made
// has ended, or after the event that made it), never from inside Build. The
// specs are copied at the call, so the caller may reuse its slices. The ticket is the app's own;
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
	platform.Wake() // a safe point follows: also during AppKit's wait for a quit answer, where only the dispatcher runs
	return d.ticket, nil
}

// Alert queues an alert sheet for the window and returns its ticket; the
// answer arrives as an EvService (platform.SourceDialog, ID the ticket) and is
// read with TakeDialog. The sheet is shown at the next safe point.
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
// built: after a frame has been presented, at the end of each turn of the
// loop, and at the application object's safe points.
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
			a.dlgFailed = append(a.dlgFailed, platform.DialogResult{Ticket: d.ticket, Kind: d.kind, Outcome: platform.DialogCancelled, Button: -1, Err: err})
			continue
		}
		a.dlgShown = append(a.dlgShown, shownDialog{app: d.ticket, plat: plat})
	}
	a.dlgQ = a.dlgQ[:0]
	a.deliverFailedDialogs() // after the queue is reset: a Handler may ask for another
}

// cancelQueuedDialogs ends the dialogs asked for and not shown yet as
// cancelled, as the app ends.
func (a *App) cancelQueuedDialogs() {
	for _, d := range a.dlgQ {
		a.dlgFailed = append(a.dlgFailed, platform.DialogResult{Ticket: d.ticket, Kind: d.kind, Outcome: platform.DialogCancelled, Button: -1})
	}
	a.dlgQ = a.dlgQ[:0]
	a.deliverFailedDialogs()
}

// deliverFailedDialogs delivers the results of the dialogs that ended without
// being shown (the window refused them, or the app ended first) as a native
// completion is: for TakeDialog, and to the Handler as an EvService from
// SourceDialog with the dialog's ticket, once each.
func (a *App) deliverFailedDialogs() {
	for len(a.dlgFailed) > 0 {
		r := a.dlgFailed[0]
		a.dlgFailed = a.dlgFailed[:copy(a.dlgFailed, a.dlgFailed[1:])]
		a.pushDialog(r)
		pe, data := platform.DialogEvent(r)
		pe.Window = a.winID
		a.deliver(&pe, data)
	}
}

// TakeDialog returns the oldest dialog result not read yet, and false when
// there is none. A result carries the ticket its call returned. When several
// owners ask for dialogs, each reads its own with TakeDialogFor.
func (a *App) TakeDialog() (platform.DialogResult, bool) {
	if len(a.dlg) == 0 {
		return platform.DialogResult{}, false
	}
	r := a.dlg[0]
	a.dlg = a.dlg[:copy(a.dlg, a.dlg[1:])]
	return r, true
}

// TakeDialogFor returns the result of the dialog with ticket t, and false when
// that dialog has not ended or its result was read. Other results stay queued
// for their owners, so independent callers do not take each other's answers.
func (a *App) TakeDialogFor(t platform.DialogTicket) (platform.DialogResult, bool) {
	for i, r := range a.dlg {
		if r.Ticket == t {
			a.dlg = slices.Delete(a.dlg, i, i+1)
			return r, true
		}
	}
	return platform.DialogResult{}, false
}

// pushDialog queues a result and asks for a frame to show it.
func (a *App) pushDialog(r platform.DialogResult) {
	if len(a.dlg) >= maxDialogResults {
		a.dlg = a.dlg[:copy(a.dlg, a.dlg[1:])]
	}
	a.dlg = append(a.dlg, r)
	a.p.invalidate()
}

// onDialog turns the answer of a sheet that ended (a SourceDialog
// AppEvService) into the result under the app's ticket, for TakeDialog, and
// hands it to the Handler as an EvService with that ticket. A dialog the app
// withdrew has none to deliver.
func (a *App) onDialog(pe *platform.AppEvent, data []byte) {
	a.dialogEnded(platform.DialogResultOf(pe, data), pe, data)
}

// dialogEnded takes r, the result of the dialog with the platform's ticket
// r.Ticket, under the app's ticket for TakeDialog, and tells the Handler with
// pe (EvService from SourceDialog), whose ID becomes the app's ticket. A
// result for a dialog that is not on screen is dropped, as a withdrawn
// dialog's is.
func (a *App) dialogEnded(r platform.DialogResult, pe *platform.AppEvent, data []byte) {
	for i, s := range a.dlgShown {
		if s.plat == r.Ticket {
			r.Ticket = s.app
			a.dlgShown = slices.Delete(a.dlgShown, i, i+1)
			a.pushDialog(r)
			e := *pe
			e.ID, e.Window = uint64(s.app), a.winID
			a.deliver(&e, data)
			a.debugSweep(CauseDialog)
			return
		}
	}
}
