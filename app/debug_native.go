package app

import (
	"slices"

	"gonativeui/platform"
)

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

// DebugDialogEnded delivers the end of a sheet the target showed: r carries
// the ticket the target gave (not the app's), and the app takes the result,
// under its own ticket, with TakeDialog from the next frame. A result for a
// dialog that is not on screen is dropped, as a withdrawn dialog's is. Main
// thread.
func (a *App) DebugDialogEnded(r platform.DialogResult) {
	for i, s := range a.dlgShown {
		if s.plat == r.Ticket {
			r.Ticket = s.app
			a.dlgShown = slices.Delete(a.dlgShown, i, i+1)
			a.pushDialog(r)
			return
		}
	}
}

// DebugFlushDialogs shows the dialogs queued so far, as the loop does after a
// frame; an offscreen app never runs that part of the loop.
func (a *App) DebugFlushDialogs() { a.flushDialogs() }
