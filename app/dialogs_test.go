package app

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/platform"
	"gonativeui/ui"
)

type fakeTips struct {
	calls int
	last  []platform.TooltipRegion
}

func (f *fakeTips) SetTooltipRegions(r []platform.TooltipRegion) {
	f.calls++
	f.last = append(f.last[:0], r...)
}

// The tooltips of a finished frame reach the window as platform regions with
// the same rectangles and keys, only when tooltips are native, and without
// allocating once the buffers have grown.
func TestSyncTooltips(t *testing.T) {
	a := &App{appObject: &appObject{}, UI: ui.New(fakeui.New())}
	win := &fakeTips{}
	build := func(c *ui.Context) {
		c.NextSize(ui.Px(100), ui.Px(40))
		c.NextPos(10, 20)
		id := c.Box(ui.Hoverable|ui.FloatingX|ui.FloatingY, "a")
		c.NativeTooltip(id, "Tip")
	}
	var in ui.Input
	var list gpu.DrawList
	frame := func() {
		a.UI.Begin(&in, 400, 300, 1, 1.0/60)
		build(a.UI)
		a.UI.End(&list)
	}
	frame()
	a.syncTooltips(win)
	if win.calls != 0 {
		t.Error("regions were handed over although tooltips are not native")
	}
	a.UI.SetNativeTooltips(true)
	frame()
	a.syncTooltips(win)
	want := a.UI.TooltipRegions(nil)
	if win.calls != 1 || len(win.last) != 1 || len(want) != 1 || win.last[0].Rect != want[0].Rect || win.last[0].Key != want[0].Key || win.last[0].Rect != [4]float32{10, 20, 110, 60} {
		t.Fatalf("calls %d, handed over %+v, ui regions %+v", win.calls, win.last, want)
	}
	if n := testing.AllocsPerRun(50, func() {
		frame()
		a.syncTooltips(win)
	}); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}

// Dialog results come out of TakeDialog oldest first.
func TestTakeDialog(t *testing.T) {
	a := &App{appObject: &appObject{}}
	if _, ok := a.TakeDialog(); ok {
		t.Fatal("a result from nothing")
	}
	for i := 1; i <= maxDialogResults+2; i++ {
		a.dlg = append(a.dlg, platform.DialogResult{Ticket: platform.DialogTicket(i)})
		if len(a.dlg) > maxDialogResults { // what onDialog does past the bound
			a.dlg = a.dlg[:copy(a.dlg, a.dlg[1:])]
		}
	}
	first, _ := a.TakeDialog()
	second, _ := a.TakeDialog()
	if first.Ticket != 3 || second.Ticket != 4 {
		t.Errorf("tickets %d, %d: the oldest two were dropped, want 3 and 4 first", first.Ticket, second.Ticket)
	}
	if _, err := (&App{appObject: &appObject{}}).Alert(platform.AlertSpec{Buttons: []string{"OK"}}); err == nil {
		t.Error("Alert without a window did not fail")
	}
}

// fakeDialogs records what reaches the window's dialog calls.
type fakeDialogs struct {
	calls   []string
	alert   platform.AlertSpec
	open    platform.OpenSpec
	err     error
	next    platform.DialogTicket
	cancels []platform.DialogTicket
}

func (f *fakeDialogs) Alert(s platform.AlertSpec) (platform.DialogTicket, error) {
	f.calls = append(f.calls, "alert")
	f.alert = s
	return f.ticket()
}
func (f *fakeDialogs) OpenPanel(s platform.OpenSpec) (platform.DialogTicket, error) {
	f.calls = append(f.calls, "open")
	f.open = s
	return f.ticket()
}
func (f *fakeDialogs) SavePanel(platform.SaveSpec) (platform.DialogTicket, error) {
	f.calls = append(f.calls, "save")
	return f.ticket()
}
func (f *fakeDialogs) ticket() (platform.DialogTicket, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.next += 100
	return f.next, nil
}
func (f *fakeDialogs) CancelDialog(t platform.DialogTicket) bool {
	f.cancels = append(f.cancels, t)
	return true
}

// A dialog asked for in Build is only queued: the sheet is shown by the flush
// that follows the frame. The ticket is reserved at once, the specs are
// copied, and the result carries the ticket the call returned.
func TestDialogsPresentAfterTheFrame(t *testing.T) {
	f := &fakeDialogs{}
	a := &App{appObject: &appObject{}, UI: ui.New(fakeui.New()), dlgT: f}
	buttons := []string{"Save", "Cancel"}
	types := []string{"public.png"}
	var tk, tk2 platform.DialogTicket
	var in ui.Input
	var list gpu.DrawList
	a.UI.Begin(&in, 400, 300, 1, 1.0/60)
	var err error
	tk, err = a.Alert(platform.AlertSpec{Message: "m", Buttons: buttons})
	if err != nil || tk == 0 {
		t.Fatalf("Alert in Build: ticket %d, err %v", tk, err)
	}
	tk2, _ = a.OpenPanel(platform.OpenSpec{Types: types})
	buttons[0], types[0] = "CHANGED", "CHANGED"
	if len(f.calls) != 0 {
		t.Fatalf("the window was called from inside Build: %v", f.calls)
	}
	a.UI.End(&list)
	if len(f.calls) != 0 {
		t.Fatalf("the window was called before the frame was over: %v", f.calls)
	}
	a.flushDialogs() // what present does once the frame has been drawn
	if len(f.calls) != 2 || f.calls[0] != "alert" || f.calls[1] != "open" {
		t.Fatalf("calls after the frame: %v", f.calls)
	}
	if f.alert.Buttons[0] != "Save" || f.open.Types[0] != "public.png" {
		t.Errorf("the specs were not copied: %v %v", f.alert.Buttons, f.open.Types)
	}
	if tk == tk2 {
		t.Error("two dialogs got the same ticket")
	}
	// The sheet ends: the result comes back under the app's ticket.
	a.onDialog(&platform.AppEvent{Kind: platform.AppEvService, Source: platform.SourceDialog, ID: 200, A: int64(platform.DialogOpen)}, nil) // the open panel's platform ticket
	r, ok := a.TakeDialog()
	if !ok || r.Ticket != tk2 {
		t.Errorf("result %+v (ok %v), want ticket %d", r, ok, tk2)
	}
	// A dialog withdrawn before it was shown is never shown; one on screen is cancelled.
	tk3, _ := a.Alert(platform.AlertSpec{Message: "n", Buttons: []string{"OK"}})
	if !a.CancelDialog(tk3) {
		t.Error("CancelDialog of a queued dialog")
	}
	a.flushDialogs()
	if len(f.calls) != 2 {
		t.Errorf("a withdrawn dialog was shown: %v", f.calls)
	}
	if !a.CancelDialog(tk) || len(f.cancels) != 1 || f.cancels[0] != 100 {
		t.Errorf("cancelling the one on screen: cancels %v", f.cancels)
	}
	// A dialog the window refuses comes back as a cancelled result with the error.
	f.err = platform.ErrDialogBusy
	tk4, _ := a.Alert(platform.AlertSpec{Message: "o", Buttons: []string{"OK"}})
	a.flushDialogs()
	r, ok = a.TakeDialog()
	if !ok || r.Ticket != tk4 || r.Err != platform.ErrDialogBusy || r.Outcome != platform.DialogCancelled {
		t.Errorf("refused dialog result %+v", r)
	}
	if _, err := a.Alert(platform.AlertSpec{Message: "no buttons"}); err == nil {
		t.Error("an invalid spec was queued")
	}
}

type recordingHandler struct {
	DefaultHandler
	got []Event
	res []platform.DialogResult // each dialog event decoded from its native payload while delivered
}

func (h *recordingHandler) Event(a *App, e *Event) {
	h.got = append(h.got, *e)
	if e.Kind == EvService && e.Source == platform.SourceDialog {
		h.res = append(h.res, platform.DialogResultOf(&platform.AppEvent{Kind: platform.AppEvService, Source: e.Source, ID: e.ID, A: e.A, B: e.B}, e.Data()))
	}
}

// A sheet a test's dialog target ended (DebugDialogEnded) reaches the app as
// a native one does: the result under the app's ticket for TakeDialog, and
// EvService from SourceDialog with that ticket to the Handler, carrying the
// native encoding (kind, outcome, button, suppression; a panel's paths).
func TestDebugDialogEndedReachesTheHandler(t *testing.T) {
	f := &fakeDialogs{}
	h := &recordingHandler{}
	a := &App{appObject: &appObject{handler: h}, UI: ui.New(fakeui.New()), dlgT: f}
	tk, err := a.Alert(platform.AlertSpec{Message: "m", Buttons: []string{"Delete", "Cancel"}})
	if err != nil {
		t.Fatal(err)
	}
	tk2, _ := a.OpenPanel(platform.OpenSpec{})
	a.flushDialogs()
	a.DebugDialogEnded(platform.DialogResult{Ticket: 100, Kind: platform.DialogAlert, Outcome: platform.DialogAnswered, Button: 1, Suppressed: true}) // the fake's tickets
	a.DebugDialogEnded(platform.DialogResult{Ticket: 200, Kind: platform.DialogOpen, Outcome: platform.DialogAnswered, Paths: []string{"/a b", "/c"}})
	if len(h.got) != 2 || h.got[0].Kind != EvService || h.got[0].Source != platform.SourceDialog || h.got[0].ID != uint64(tk) || h.got[1].ID != uint64(tk2) {
		t.Fatalf("the handler got %+v, want EvService from SourceDialog with IDs %d, %d", h.got, tk, tk2)
	}
	if r := h.res[0]; r.Kind != platform.DialogAlert || r.Outcome != platform.DialogAnswered || r.Button != 1 || !r.Suppressed {
		t.Errorf("the alert's event decodes to %+v", r)
	}
	if r := h.res[1]; r.Kind != platform.DialogOpen || r.Outcome != platform.DialogAnswered || len(r.Paths) != 2 || r.Paths[0] != "/a b" || r.Paths[1] != "/c" {
		t.Errorf("the panel's event decodes to %+v", r)
	}
	if r, ok := a.TakeDialog(); !ok || r.Ticket != tk || r.Button != 1 {
		t.Fatalf("TakeDialog: %+v (ok %v)", r, ok)
	}
	a.DebugDialogEnded(platform.DialogResult{Ticket: 100, Kind: platform.DialogAlert}) // ended already: dropped
	if len(h.got) != 2 {
		t.Fatalf("a result for a dialog not on screen was delivered: %+v", h.got)
	}
}

// A dialog the window refuses at its safe point reaches the Handler as a
// native completion does, once: EvService from SourceDialog with its ticket,
// cancelled; TakeDialog has the error. So does a dialog still queued when the
// app ends.
func TestDialogThatCannotShowReachesTheHandler(t *testing.T) {
	f := &fakeDialogs{err: platform.ErrDialogBusy}
	h := &recordingHandler{}
	a := &App{appObject: &appObject{handler: h}, UI: ui.New(fakeui.New()), dlgT: f}
	tk, _ := a.Alert(platform.AlertSpec{Message: "m", Buttons: []string{"OK"}})
	a.flushDialogs()
	a.flushDialogs()
	if len(h.got) != 1 || h.got[0].ID != uint64(tk) || h.res[0].Outcome != platform.DialogCancelled || h.res[0].Kind != platform.DialogAlert {
		t.Fatalf("the handler got %+v (%+v), want one cancelled EvService for ticket %d", h.got, h.res, tk)
	}
	if r, ok := a.TakeDialog(); !ok || r.Ticket != tk || r.Err != platform.ErrDialogBusy {
		t.Fatalf("TakeDialog: %+v (ok %v)", r, ok)
	}
	f.err = nil
	tk2, _ := a.SavePanel(platform.SaveSpec{})
	a.cancelQueuedDialogs() // the app ends before the safe point
	if len(h.got) != 2 || h.got[1].ID != uint64(tk2) || h.res[1].Outcome != platform.DialogCancelled || len(f.calls) != 1 {
		t.Fatalf("the handler got %+v (%+v), calls %v", h.got, h.res, f.calls)
	}
}
