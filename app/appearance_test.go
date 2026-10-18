package app

import (
	"testing"

	"gonativeui/ui"
)

type fakeChrome struct{ calls []bool }

func (f *fakeChrome) SetDarkChrome(dark bool) { f.calls = append(f.calls, dark) }

// The OS-drawn chrome is an OS call (NSWindow appearance, a DWM attribute):
// it must follow the mode the UI shows, once per change, and stay untouched
// for an app that never declares one.
func TestChromeFollowsTheDeclaredModeOncePerChange(t *testing.T) {
	a := &App{UI: ui.New(nil)}
	var w fakeChrome
	for i := 0; i < 3; i++ {
		a.syncChrome(&w)
	}
	if len(w.calls) != 0 {
		t.Fatalf("no mode declared, yet the chrome was set: %v", w.calls)
	}
	steps := []bool{true, true, true, false, false, true}
	for _, d := range steps {
		a.UI.SetChromeDark(d)
		a.syncChrome(&w)
	}
	want := []bool{true, false, true}
	if len(w.calls) != len(want) {
		t.Fatalf("calls %v, want %v", w.calls, want)
	}
	for i := range want {
		if w.calls[i] != want[i] {
			t.Fatalf("calls %v, want %v", w.calls, want)
		}
	}
	if n := testing.AllocsPerRun(100, func() { a.UI.SetChromeDark(true); a.syncChrome(&w) }); n != 0 {
		t.Fatalf("%v allocations per frame", n)
	}
}
