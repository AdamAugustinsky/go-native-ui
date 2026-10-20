package app

import (
	"testing"

	"gonativeui/ui"
)

type fakeWindow struct {
	client, max bool
	calls       []string
}

func (f *fakeWindow) ClientDecorations() bool { return f.client }
func (f *fakeWindow) Maximized() bool         { return f.max }
func (f *fakeWindow) Minimize()               { f.calls = append(f.calls, "minimise") }
func (f *fakeWindow) ToggleMaximize()         { f.calls = append(f.calls, "maximise") }
func (f *fakeWindow) RequestClose()           { f.calls = append(f.calls, "close") }

type fakeMacWindow struct {
	fakeWindow
	buttons [4]float32
}

func (f *fakeMacWindow) SystemButtons() [4]float32 { return f.buttons }

// The window's state reaches the UI before each frame, and what a titlebar
// asked for in the last one is carried out once.
func TestWindowStateReachesTheUIAndRequestsReachTheWindow(t *testing.T) {
	a := &App{appObject: &appObject{}, UI: ui.New(nil), cfg: Config{Title: "Notes"}, scale: 2}
	w := &fakeWindow{client: true, max: true}
	a.syncWindowFrom(w, nil)
	if d := a.UI.Decorations(); !d.Client || !d.Maximized {
		t.Errorf("decorations %+v, want client-side and maximized", d)
	}
	if got := a.UI.WindowTitle(); got != "Notes" {
		t.Errorf("title %q", got)
	}
	if r := a.UI.SystemButtons(); r != [4]float32{} {
		t.Errorf("a window that reports no buttons has %v", r)
	}

	for _, act := range []ui.WindowAction{ui.ActionClose, ui.ActionMinimise, ui.ActionToggleMaximise} {
		a.UI.RequestWindowAction(act)
		a.syncWindowFrom(w, nil)
		a.syncWindowFrom(w, nil) // the request is not carried out twice
	}
	if want := []string{"close", "minimise", "maximise"}; len(w.calls) != 3 || w.calls[0] != want[0] || w.calls[1] != want[1] || w.calls[2] != want[2] {
		t.Errorf("window calls %v, want %v", w.calls, want)
	}

	w.client = false
	a.syncWindowFrom(w, nil)
	if a.UI.Decorations().Client {
		t.Error("the compositor draws the decorations now, the UI still thinks it must")
	}
}

// The system's window buttons arrive in pixels (the platform reports points),
// and change with the window.
func TestSystemButtonsAreScaledToPixels(t *testing.T) {
	a := &App{appObject: &appObject{}, UI: ui.New(nil), scale: 2}
	w := &fakeMacWindow{buttons: [4]float32{16, 16, 76, 30}}
	a.syncWindowFrom(w, w)
	if got, want := a.UI.SystemButtons(), [4]float32{32, 32, 152, 60}; got != want {
		t.Errorf("buttons %v px, want %v", got, want)
	}
	w.buttons = [4]float32{} // full screen
	a.syncWindowFrom(w, w)
	if got := a.UI.SystemButtons(); got != [4]float32{} {
		t.Errorf("buttons %v px after they left the content", got)
	}
	if n := testing.AllocsPerRun(100, func() { a.syncWindowFrom(w, w) }); n != 0 {
		t.Errorf("%v allocations per frame", n)
	}
}
