//go:build linux

package platform

import (
	"fmt"
	"testing"
	"time"
)

// imEvents pumps for d and returns the text-input events in order:
// "P:text@start+len" for EvPreedit, "T:text" for EvText.
func imEvents(t *testing.T, w *Window, d time.Duration) []string {
	t.Helper()
	var out []string
	pump(t, w, d, func(e Event) bool {
		switch e.Kind {
		case EvPreedit:
			out = append(out, fmt.Sprintf("P:%s@%d+%d", w.Text(e.Text), e.SelStart, e.SelLen))
		case EvText:
			out = append(out, "T:"+string(w.Text(e.Text)))
		}
		return false
	})
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestWaylandTextInputFollowsFocusAndField(t *testing.T) {
	f := startFakeWL(t, "textinput")
	w := openFake(t, f)
	w.SetTextInput(true) // no focus yet: nothing to enable
	w.SetIMERect(100, 50, 2, 20)
	pump(t, w, 100*time.Millisecond, nil)
	if f.seen("REQ zwp_text_input_v3.enable") {
		t.Fatal("enabled before the surface got the text input")
	}
	f.do("ti_enter")
	request(t, w, f, "REQ zwp_text_input_v3.enable")
	if l := request(t, w, f, "REQ zwp_text_input_v3.set_cursor_rectangle"); l != "REQ zwp_text_input_v3.set_cursor_rectangle 100 50 2 20" {
		t.Errorf("rectangle: %q", l)
	}
	request(t, w, f, "REQ zwp_text_input_v3.commit")

	w.SetIMERect(120, 50, 2, 20)
	if l := request(t, w, f, "REQ zwp_text_input_v3.set_cursor_rectangle"); l != "REQ zwp_text_input_v3.set_cursor_rectangle 120 50 2 20" {
		t.Errorf("moved rectangle: %q", l)
	}
	request(t, w, f, "REQ zwp_text_input_v3.commit")
	w.SetIMERect(120, 50, 2, 20) // unchanged: nothing is sent
	pump(t, w, 100*time.Millisecond, nil)
	if f.seen("REQ zwp_text_input_v3.set_cursor_rectangle") {
		t.Error("an unchanged rectangle was sent again")
	}

	w.SetTextInput(false)
	request(t, w, f, "REQ zwp_text_input_v3.disable")
	request(t, w, f, "REQ zwp_text_input_v3.commit")
}

func TestWaylandTextInputComposition(t *testing.T) {
	f := startFakeWL(t, "textinput")
	w := openFake(t, f)
	w.SetTextInput(true)
	f.do("ti_enter")
	request(t, w, f, "REQ zwp_text_input_v3.enable")

	// A composition grows, then is committed: the commit replaces it.
	f.do("preedit ni 1 1")
	f.do("done")
	if got, want := imEvents(t, w, 300*time.Millisecond), []string{"P:ni@1+0"}; !sameStrings(got, want) {
		t.Errorf("preedit: %v, want %v", got, want)
	}
	f.do("preedit nihao 0 2") // a selected part
	f.do("done")
	if got, want := imEvents(t, w, 300*time.Millisecond), []string{"P:nihao@0+2"}; !sameStrings(got, want) {
		t.Errorf("preedit 2: %v, want %v", got, want)
	}
	f.do("commit 你好")
	f.do("done")
	if got, want := imEvents(t, w, 300*time.Millisecond), []string{"P:@0+0", "T:你好"}; !sameStrings(got, want) {
		t.Errorf("commit: %v, want %v", got, want)
	}

	// Text without composition is just text; a done with nothing ends nothing.
	f.do("commit x")
	f.do("done")
	f.do("done")
	if got, want := imEvents(t, w, 300*time.Millisecond), []string{"T:x"}; !sameStrings(got, want) {
		t.Errorf("plain commit: %v, want %v", got, want)
	}

	// A commit and the next preedit in one change.
	f.do("preedit ab 2 2")
	f.do("done")
	imEvents(t, w, 200*time.Millisecond)
	f.do("commit ab")
	f.do("preedit c 1 1")
	f.do("done")
	if got, want := imEvents(t, w, 300*time.Millisecond), []string{"P:@0+0", "T:ab", "P:c@1+0"}; !sameStrings(got, want) {
		t.Errorf("commit and preedit: %v, want %v", got, want)
	}

	// The compositor ends the composition by sending a change without preedit.
	f.do("done")
	if got, want := imEvents(t, w, 300*time.Millisecond), []string{"P:@0+0"}; !sameStrings(got, want) {
		t.Errorf("cleared: %v, want %v", got, want)
	}
}

func TestWaylandTextInputCancelsComposition(t *testing.T) {
	for _, c := range []struct {
		name string
		do   func(f *fakeWL, w *Window)
		req  string
	}{
		{"field loses focus", func(f *fakeWL, w *Window) { w.SetTextInput(false) }, "REQ zwp_text_input_v3.disable"},
		{"surface loses the text input", func(f *fakeWL, w *Window) { f.do("ti_leave") }, ""},
		{"DiscardComposition", func(f *fakeWL, w *Window) { w.DiscardComposition() }, "REQ zwp_text_input_v3.disable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := startFakeWL(t, "textinput")
			w := openFake(t, f)
			w.SetTextInput(true)
			f.do("ti_enter")
			request(t, w, f, "REQ zwp_text_input_v3.enable")
			f.do("preedit ka 2 2")
			f.do("done")
			if got := imEvents(t, w, 200*time.Millisecond); len(got) != 1 {
				t.Fatalf("composition: %v", got)
			}
			c.do(f, w)
			got := imEvents(t, w, 300*time.Millisecond)
			if want := []string{"P:@0+0"}; !sameStrings(got, want) {
				t.Errorf("events %v, want %v (no EvText)", got, want)
			}
			if c.req != "" {
				request(t, w, f, c.req)
			}
		})
	}
}

func TestWaylandTextInputAbsent(t *testing.T) {
	// Without zwp_text_input_manager_v3 the calls do nothing and keys still work.
	f := startFakeWL(t, "")
	w := openFake(t, f)
	w.SetTextInput(true)
	w.SetIMERect(1, 2, 3, 4)
	w.DiscardComposition()
	w.SetTextInput(false)
	pump(t, w, 100*time.Millisecond, nil)
	if f.seen("REQ zwp_text_input") {
		t.Error("text input requests without the protocol")
	}
}

func TestWaylandTextInputNoAlloc(t *testing.T) {
	f := startFakeWL(t, "textinput")
	w := openFake(t, f)
	w.SetTextInput(true)
	f.do("ti_enter")
	request(t, w, f, "REQ zwp_text_input_v3.enable")
	for i := 0; i < 20; i++ { // warm
		w.SetIMERect(float32(i), 5, 2, 20)
	}
	if n := testing.AllocsPerRun(200, func() {
		w.SetIMERect(30, 5, 2, 20)
		w.SetTextInput(true)
	}); n != 0 {
		t.Errorf("SetIMERect/SetTextInput with unchanged state: %v allocs", n)
	}
}
