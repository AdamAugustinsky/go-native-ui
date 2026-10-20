package widgettest_test

import (
	"errors"
	"testing"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// asker is an app that asks questions through the window's native dialogs and
// keeps the answers.
type asker struct {
	a      *app.App
	ask    func(a *app.App)
	asked  bool
	got    []platform.DialogResult
	cmds   []uint16
	edits  int
	dark   bool
	builds int
}

func (s *asker) Init(a *app.App) error { s.a = a; return nil }

func (s *asker) Build(c *ui.Context) {
	s.builds++
	if !s.asked && s.ask != nil {
		s.asked = true
		s.ask(s.a)
	}
	for {
		r, ok := s.a.TakeDialog()
		if !ok {
			break
		}
		s.got = append(s.got, r)
	}
	for {
		id, ok := c.TakeCommand()
		if !ok {
			break
		}
		s.cmds = append(s.cmds, id)
	}
	s.dark = c.SystemAppearance().Dark
	c.NextSize(ui.Px(10), ui.Px(10))
	c.Box(0, "x")
}

func openAsker(t *testing.T, ask func(a *app.App)) (*widgettest.Rig, *asker) {
	s := &asker{ask: ask}
	return widgettest.Open(t, app.Config{Title: "ask", Width: 200, Height: 120}, 200, 120, 1, s), s
}

// An alert is shown after the frame that asked for it, with the spec the app
// gave; the test answers it and the app reads the answer with TakeDialog, in
// the next frame, under the ticket Alert returned.
func TestFakeAlertAnswers(t *testing.T) {
	var tk platform.DialogTicket
	r, s := openAsker(t, func(a *app.App) {
		tk, _ = a.Alert(platform.AlertSpec{Message: "Save changes?", Buttons: []string{"Save", "Cancel", "Don't Save"}, Destructive: []int{2}, Suppression: "Don't ask again"})
	})
	fd := r.FakeDialogs()
	r.Frames(2)
	if len(fd.Calls) != 1 || fd.Calls[0].Kind != platform.DialogAlert || fd.Calls[0].Alert.Message != "Save changes?" || len(fd.Open()) != 1 {
		t.Fatalf("the alert the app asked for: %+v", fd.Calls)
	}
	if len(s.got) != 0 {
		t.Fatalf("an answer before the test gave one: %+v", s.got)
	}
	if err := fd.AnswerButton(2, true); err != nil {
		t.Fatal(err)
	}
	r.Frames(1)
	if len(s.got) != 1 || s.got[0].Ticket != tk || s.got[0].Button != 2 || !s.got[0].Suppressed || s.got[0].Outcome != platform.DialogAnswered {
		t.Fatalf("the answer: %+v (ticket asked %d)", s.got, tk)
	}
	if len(fd.Open()) != 0 || fd.AnswerButton(0, false) == nil {
		t.Error("an answered dialog is still open, or a second answer was taken")
	}
}

// Open and save panels are answered with paths or dismissed; a dialog the
// window refuses comes back as a cancelled result with the error; a dialog
// the app withdraws is recorded and never answered.
func TestFakePanelsAnswerAndRefuse(t *testing.T) {
	var tks [4]platform.DialogTicket
	r, s := openAsker(t, nil)
	fd := r.FakeDialogs()
	s.ask = func(a *app.App) {
		tks[0], _ = a.OpenPanel(platform.OpenSpec{Files: true, Multiple: true, Types: []string{".png"}})
		tks[1], _ = a.SavePanel(platform.SaveSpec{Name: "note.txt"})
		tks[2], _ = a.OpenPanel(platform.OpenSpec{Dirs: true})
		tks[3], _ = a.Alert(platform.AlertSpec{Message: "m", Buttons: []string{"OK"}})
	}
	r.Frames(2)
	if len(fd.Calls) != 4 || fd.Calls[0].Open.Types[0] != ".png" || fd.Calls[1].Save.Name != "note.txt" {
		t.Fatalf("calls %+v", fd.Calls)
	}
	if err := fd.AnswerPaths("/tmp/a.png", "/tmp/b.png"); err != nil { // the oldest panel
		t.Fatal(err)
	}
	if err := fd.Dismiss(); err != nil { // the save panel: Cancel
		t.Fatal(err)
	}
	if !s.a.CancelDialog(tks[2]) || len(fd.Withdrawn) != 1 { // the app changes its mind about the folder
		t.Errorf("withdrawing: %v", fd.Withdrawn)
	}
	if err := fd.AnswerButton(0, false); err != nil {
		t.Fatal(err)
	}
	r.Frames(1)
	if len(s.got) != 3 {
		t.Fatalf("%d results: %+v", len(s.got), s.got)
	}
	if g := s.got[0]; g.Ticket != tks[0] || len(g.Paths) != 2 || g.Paths[1] != "/tmp/b.png" || g.Outcome != platform.DialogAnswered {
		t.Errorf("open panel result %+v", g)
	}
	if g := s.got[1]; g.Ticket != tks[1] || g.Outcome != platform.DialogCancelled || len(g.Paths) != 0 {
		t.Errorf("save panel result %+v", g)
	}
	if g := s.got[2]; g.Ticket != tks[3] || g.Button != 0 {
		t.Errorf("alert result %+v", g)
	}

	// a window that refuses
	fd.Err = platform.ErrDialogBusy
	s.got, s.asked = nil, false
	s.ask = func(a *app.App) { a.Alert(platform.AlertSpec{Message: "again", Buttons: []string{"OK"}}) }
	r.Frames(2)
	if len(s.got) != 1 || !errors.Is(s.got[0].Err, platform.ErrDialogBusy) || s.got[0].Outcome != platform.DialogCancelled {
		t.Errorf("a refused dialog: %+v", s.got)
	}
}

// The menu commands, the Edit actions and the appearance of the system that
// native surfaces deliver are steps.
func TestFakeCommandsAndAppearance(t *testing.T) {
	r, s := openAsker(t, nil)
	r.Play(widgettest.Command(7), widgettest.Idle(1), widgettest.Command(9))
	r.Frames(1)
	if len(s.cmds) != 2 || s.cmds[0] != 7 || s.cmds[1] != 9 {
		t.Errorf("commands %v", s.cmds)
	}
	r.Play(widgettest.Appearance(true))
	r.Frames(1)
	if !s.dark {
		t.Error("the system appearance did not turn dark")
	}
	r.Play(widgettest.Appearance(false))
	r.Frames(1)
	if s.dark {
		t.Error("the system appearance did not turn light")
	}
	n := 0
	r.Play(widgettest.Do(func() { n++ }), widgettest.Do(func() { n += 10 }))
	if n != 11 {
		t.Errorf("Do steps ran as %d", n)
	}
}
