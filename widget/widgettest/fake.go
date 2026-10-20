package widgettest

import (
	"errors"
	"slices"

	"gonativeui/app"
	"gonativeui/platform"
)

// Fakes of native surfaces. An app that asks for an alert, a file panel or a
// notification's answer is testable without AppKit by giving it something
// that answers as the user would. FakeDialogs stands in for the window's
// alerts and panels (app.App.Alert, OpenPanel, SavePanel), so the flow of an
// app that asks "Save changes?" or "Choose a folder" is a test of ordinary
// code: the test sees what was asked and answers it. The menu commands, the
// Edit actions and the appearance flips of the system are steps
// (Command, Edit, Appearance), the text of a hosted native view is read and set
// with ViewString and SetViewString (macOS, in a live window), the answers of
// the AppKit sheets themselves are Sheet.Press and Sheet.End (macOS), and the application
// object's events (notification responses, reopen, a quit request) are in
// fake_f3.go behind the gnui_f3 tag until the application object lands.

// DialogCall is one alert or panel the app asked for.
type DialogCall struct {
	// Ticket is the ticket the fake gave it, Kind says which call it was.
	Ticket platform.DialogTicket
	Kind   platform.DialogKind
	// Alert, Open and Save are the spec of the call of that kind.
	Alert platform.AlertSpec
	Open  platform.OpenSpec
	Save  platform.SaveSpec
	// Answered is true once the fake answered it (or it was withdrawn).
	Answered bool
}

// FakeDialogs stands in for a window's native alerts and file panels. Install
// it with Rig.FakeDialogs (an offscreen rig) or app.App.DebugSetDialogs; the
// app's calls are recorded in Calls, shown at the safe point after the frame
// as with a real window, and stay open until the test answers the oldest open
// one with AnswerButton, AnswerPaths or Dismiss. Err, if set, is what the
// next calls fail with (platform.ErrDialogBusy, platform.ErrUnsupported).
type FakeDialogs struct {
	// Calls are the dialogs asked for so far, in order.
	Calls []DialogCall
	// Withdrawn are the tickets the app withdrew (CancelDialog).
	Withdrawn []platform.DialogTicket
	// Err is returned by Alert, OpenPanel and SavePanel while it is set.
	Err error

	a    *app.App
	next platform.DialogTicket
}

var _ app.DialogTarget = (*FakeDialogs)(nil)

// NewFakeDialogs makes a FakeDialogs for a (installing it with a.DebugSetDialogs).
func NewFakeDialogs(a *app.App) *FakeDialogs {
	f := &FakeDialogs{a: a}
	a.DebugSetDialogs(f)
	return f
}

func (f *FakeDialogs) call(c DialogCall) (platform.DialogTicket, error) {
	if f.Err != nil {
		return 0, f.Err
	}
	f.next++
	c.Ticket = f.next
	f.Calls = append(f.Calls, c)
	return c.Ticket, nil
}

// Alert implements app.DialogTarget.
func (f *FakeDialogs) Alert(s platform.AlertSpec) (platform.DialogTicket, error) {
	return f.call(DialogCall{Kind: platform.DialogAlert, Alert: s})
}

// OpenPanel implements app.DialogTarget.
func (f *FakeDialogs) OpenPanel(s platform.OpenSpec) (platform.DialogTicket, error) {
	return f.call(DialogCall{Kind: platform.DialogOpen, Open: s})
}

// SavePanel implements app.DialogTarget.
func (f *FakeDialogs) SavePanel(s platform.SaveSpec) (platform.DialogTicket, error) {
	return f.call(DialogCall{Kind: platform.DialogSave, Save: s})
}

// CancelDialog implements app.DialogTarget.
func (f *FakeDialogs) CancelDialog(t platform.DialogTicket) bool {
	for i := range f.Calls {
		if f.Calls[i].Ticket == t && !f.Calls[i].Answered {
			f.Calls[i].Answered = true
			f.Withdrawn = append(f.Withdrawn, t)
			return true
		}
	}
	return false
}

// Open returns the dialogs shown and not answered yet, oldest first.
func (f *FakeDialogs) Open() []DialogCall {
	var open []DialogCall
	for _, c := range f.Calls {
		if !c.Answered {
			open = append(open, c)
		}
	}
	return open
}

var errNoDialog = errors.New("widgettest: no dialog is open")

// finish answers the oldest open dialog of one of kinds (any, if none given)
// with r, which gets its ticket and kind.
func (f *FakeDialogs) finish(r platform.DialogResult, kinds ...platform.DialogKind) error {
	for i := range f.Calls {
		c := &f.Calls[i]
		if c.Answered || len(kinds) > 0 && !slices.Contains(kinds, c.Kind) {
			continue
		}
		c.Answered = true
		r.Ticket, r.Kind = c.Ticket, c.Kind
		f.a.DebugDialogEnded(r)
		return nil
	}
	return errNoDialog
}

// AnswerButton answers the oldest open alert: the user pressed the button
// with index button of AlertSpec.Buttons (0 is the default button), with the
// suppression checkbox in the state suppressed. It returns an error when no
// alert is open.
func (f *FakeDialogs) AnswerButton(button int, suppressed bool) error {
	return f.finish(platform.DialogResult{Outcome: platform.DialogAnswered, Button: button, Suppressed: suppressed}, platform.DialogAlert)
}

// AnswerPaths answers the oldest open panel: the user chose the paths (one
// for a save panel). It returns an error when no panel is open.
func (f *FakeDialogs) AnswerPaths(paths ...string) error {
	return f.finish(platform.DialogResult{Outcome: platform.DialogAnswered, Button: -1, Paths: slices.Clone(paths)}, platform.DialogOpen, platform.DialogSave)
}

// Dismiss ends the oldest open dialog as the user cancelling it (a panel's
// Cancel, Escape on an alert with a Cancel button): the result is cancelled
// with no button and no paths.
func (f *FakeDialogs) Dismiss() error {
	return f.finish(platform.DialogResult{Outcome: platform.DialogCancelled, Button: -1})
}

// FakeDialogs installs a FakeDialogs on the app of a rig made by Open and
// returns it. The dialogs the app asks for are shown after each frame, as the
// loop does with a window.
func (r *Rig) FakeDialogs() *FakeDialogs {
	if r.o == nil {
		r.t.Fatal("widgettest: FakeDialogs needs a rig made by Open (an app); NewRig has none")
	}
	r.fd = NewFakeDialogs(r.o.App)
	return r.fd
}
