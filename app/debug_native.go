package app

import "gonativeui/platform"

// Seams for tests that stand in for the native surfaces (widget/widgettest's
// fakes): they replace what the window answers, and change nothing for an app
// that does not call them.

// DialogTarget is the window's native alerts and panels as the app calls
// them. widgettest.FakeDialogs implements it.
type DialogTarget interface {
	// Alert, OpenPanel and SavePanel show a sheet and return its ticket.
	Alert(platform.AlertSpec) (platform.DialogTicket, error)
	OpenPanel(platform.OpenSpec) (platform.DialogTicket, error)
	SavePanel(platform.SaveSpec) (platform.DialogTicket, error)
	// CancelDialog withdraws the sheet with the ticket.
	CancelDialog(platform.DialogTicket) bool
	// DialogResult decodes the EvDialog of a sheet that ended.
	DialogResult(platform.Event) platform.DialogResult
}

// DebugSetDialogs makes t the receiver of the app's alerts and panels in
// place of its window, which also makes them available where there is no
// native window (an offscreen app); nil restores the window's. Main thread.
func (a *App) DebugSetDialogs(t DialogTarget) {
	if t == nil {
		a.dlgT = nil
		return
	}
	a.dlgT = t
}

// DebugDialogEnded delivers the event a sheet's end posts on the window's
// queue (platform.EvDialog with Tag the ticket the target gave): the app
// takes the result with TakeDialog from the next frame. Main thread.
func (a *App) DebugDialogEnded(e platform.Event) { a.onDialog(&e) }

// DebugFlushDialogs shows the dialogs queued so far, as the loop does after a
// frame; an offscreen app never runs that part of the loop.
func (a *App) DebugFlushDialogs() { a.flushDialogs() }
