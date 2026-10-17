package ui

import (
	"strings"
	"testing"

	"gonativeui/gpu"
)

// pairRig drives two text fields "a" and "b", one above the other.
type pairRig struct {
	*harness
	a, b   TextBuffer
	ra, rb TextFieldSignal
	hideA  bool // the first field is not built
	now    int64
}

func newPairRig(t *testing.T) *pairRig {
	t.Helper()
	old := macKeys
	macKeys = true
	t.Cleanup(func() { macKeys = old })
	sh := newEditShaper()
	h := &harness{t: t, c: New(sh), sh: sh.fakeShaper, w: 400, h: 100}
	h.in.Inside, h.in.Focused = true, true
	h.in.MouseX, h.in.MouseY = -100, -100
	r := &pairRig{harness: h, now: 1e9}
	h.c.TextEdit.Caret = 0xFF0000FF
	r.frame()
	r.focusKey("a")
	return r
}

func (r *pairRig) frame() { r.step(16) }

func (r *pairRig) step(dtMs int64) {
	r.now += dtMs * 1e6
	r.in.Time = r.now
	r.harness.frame(func(c *Context) {
		if !r.hideA {
			c.NextSize(Px(200), Px(24))
			r.ra = c.TextField("a", &r.a)
		}
		c.NextSize(Px(200), Px(24))
		r.rb = c.TextField("b", &r.b)
	})
}

func (r *pairRig) focusKey(which string) {
	id := r.ra.ID
	if which == "b" {
		id = r.rb.ID
	}
	r.c.SetFocus(r.c.KeyOf(id))
	r.frame()
}

// A composition belongs to the field it started in. Moving the focus to the
// other field commits it into the first one (what AppKit does when the first
// responder changes) and the second field neither shows nor commits it; the
// app is told to drop the platform's composition.
func TestIMECompositionStaysWithItsField(t *testing.T) {
	r := newPairRig(t)
	r.b.SetString("bb")
	r.b.SelectAll()
	r.in.SetPreedit([]byte("かな"), 6, 0)
	r.frame()
	if !strings.Contains(string(r.c.fr.Cur().Tail(0)), "かな") || r.a.String() != "" {
		t.Fatal("composition not shown in the first field")
	}
	if r.c.IMECancel() {
		t.Fatal("cancel asked for while the owner still has the focus")
	}
	revB := r.b.Rev()

	r.focusKey("b") // the composition is still in the Input
	if r.a.String() != "かな" || !r.ra.Changed {
		t.Errorf("first field after the focus left: %q changed=%v, want the composition committed", r.a.String(), r.ra.Changed)
	}
	if r.b.String() != "bb" || r.b.Rev() != revB || !r.b.HasSelection() {
		t.Errorf("second field took the composition: %q rev %d selection %v", r.b.String(), r.b.Rev(), r.b.HasSelection())
	}
	if !r.c.IMECancel() {
		t.Error("the app is not told to discard the platform's composition")
	}
	if got := string(r.c.fr.Cur().Tail(0)); strings.Count(got, "かな") != 1 {
		t.Errorf("composition drawn %d times: %q", strings.Count(got, "かな"), got)
	}
	if r.quads(0x00FF00FF) != 0 {
		t.Error("composition underline in the second field")
	}

	// The preedit lingers one more frame (the platform has not answered yet):
	// nothing is committed twice and the second field still ignores it.
	r.frame()
	if r.a.String() != "かな" || r.b.String() != "bb" {
		t.Errorf("a lingering preedit changed text: %q %q", r.a.String(), r.b.String())
	}
	if !r.c.IMECancel() {
		t.Error("cancel not repeated while the stale preedit stays")
	}
	// The platform answers; typing then goes to the second field.
	r.in.SetPreedit(nil, 0, 0)
	r.frame()
	if r.c.IMECancel() {
		t.Error("cancel asked for without a composition")
	}
	r.typ("x")
	if r.b.String() != "x" || r.a.String() != "かな" {
		t.Errorf("typing after the switch: %q %q", r.a.String(), r.b.String())
	}
	// A composition can start in the second field afterwards.
	r.in.SetPreedit([]byte("あ"), 3, 0)
	r.frame()
	if !strings.Contains(string(r.c.fr.Cur().Tail(0)), "あ") || r.c.IMECancel() {
		t.Error("a new composition in the second field is not shown")
	}
}

func (r *pairRig) typ(s string) {
	r.in.AppendText(s)
	r.frame()
}

func (r *pairRig) quads(col uint32) int {
	n := 0
	for _, q := range r.list.Quads {
		if q.Mode == gpu.ModeShape && q.Color[0] == col {
			n++
		}
	}
	return n
}

// The owner may be gone altogether (its field is no longer built): the other
// field does not adopt the composition, the app is told to drop it.
func TestIMEOwnerGone(t *testing.T) {
	r := newPairRig(t)
	r.in.SetPreedit([]byte("か"), 3, 0)
	r.frame()
	r.hideA = true
	r.focusKey("b")
	if r.b.String() != "" || !r.c.IMECancel() {
		t.Errorf("second field %q, cancel %v", r.b.String(), r.c.IMECancel())
	}
}

// Focus leaving every field commits into the field that showed the composition.
func TestIMEFocusLeavesAllFields(t *testing.T) {
	r := newPairRig(t)
	r.in.SetPreedit([]byte("か"), 3, 0)
	r.frame()
	r.c.SetFocus(0)
	r.frame()
	if r.a.String() != "か" || !r.c.IMECancel() {
		t.Errorf("first field %q, cancel %v", r.a.String(), r.c.IMECancel())
	}
	if _, ok := r.c.IMERect(); ok {
		t.Error("IME rect without a focused field")
	}
}

// A selection stays in place while it is being composed over; cancelling the
// composition brings it back, committing replaces it.
func TestIMESelectionSurvivesCancel(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("/Users/adam")
	r.buf.SelectAll()
	r.frame()
	r.in.SetPreedit([]byte("x"), 1, 0)
	r.frame()
	if r.text() != "/Users/adam" || !r.buf.HasSelection() || r.res.Changed {
		t.Fatalf("composing changed the buffer: %q selection %v changed %v", r.text(), r.buf.HasSelection(), r.res.Changed)
	}
	if got := string(r.c.fr.Cur().Tail(0)); !strings.Contains(got, "x") || strings.Contains(got, "/Users") {
		t.Fatalf("the composition does not replace the selection on screen: %q", got)
	}
	// Cancel (the platform clears the preedit and sends no text).
	r.in.SetPreedit(nil, 0, 0)
	r.frame()
	if r.text() != "/Users/adam" || !r.buf.HasSelection() || r.res.Changed {
		t.Fatalf("after cancel: %q selection %v", r.text(), r.buf.HasSelection())
	}
	if got := string(r.c.fr.Cur().Tail(0)); !strings.Contains(got, "/Users/adam") {
		t.Errorf("text not shown again: %q", got)
	}
	// Compose again and commit: the selection is replaced.
	r.in.SetPreedit([]byte("x"), 1, 0)
	r.frame()
	r.in.SetPreedit(nil, 0, 0)
	r.in.AppendText("y")
	r.frame()
	if r.text() != "y" || r.buf.HasSelection() || !r.res.Changed {
		t.Fatalf("after commit: %q", r.text())
	}
}

// The window losing the focus while composing must not eat the selection
// either.
func TestIMESelectionSurvivesWindowDeactivation(t *testing.T) {
	r := newRig(t, true)
	r.buf.SetString("abc")
	r.buf.SelectAll()
	r.in.SetPreedit([]byte("x"), 1, 0)
	r.frame()
	r.in.Focused = false
	r.frame()
	r.in.SetPreedit(nil, 0, 0)
	r.in.Focused = true
	r.frame()
	if r.text() != "abc" || !r.buf.HasSelection() {
		t.Fatalf("%q selection %v", r.text(), r.buf.HasSelection())
	}
}

// A composition nobody finishes must not keep the caret blinking for ever: the
// blink times out 10 s after the last change of the composition, the frames in
// between (the blink's own) do not count.
func TestIMEPersistentPreeditLetsBlinkTimeOut(t *testing.T) {
	r := newRig(t, true)
	r.in.SetPreedit([]byte("かな"), 6, 0)
	r.frame()
	if r.c.WakeAfter() == 0 {
		t.Fatal("no blink while the composition starts")
	}
	// Blink-only frames, as the app draws them: one per half-period.
	for i := 0; i < 19; i++ {
		r.step(500)
	}
	r.step(1500) // past the 10 s since the preedit last changed
	if w := r.c.WakeAfter(); w != 0 {
		t.Fatalf("still asking for blink frames 11.5 s into an unchanged composition: %d ns", w)
	}
	// Any change of the composition counts as activity again.
	r.in.SetPreedit([]byte("かなa"), 9, 0)
	r.frame()
	if r.c.WakeAfter() == 0 {
		t.Error("a changed composition did not restart the blink")
	}
	// So does moving its caret.
	r.step(10500)
	if r.c.WakeAfter() != 0 {
		t.Fatal("blink did not time out again")
	}
	r.in.SetPreedit([]byte("かなa"), 3, 0)
	r.frame()
	if r.c.WakeAfter() == 0 {
		t.Error("a moved composition caret did not restart the blink")
	}
}

// On Windows AltGr arrives as Ctrl+Alt and types a character (AltGr+A is ą on
// a Polish layout): that pair is no Ctrl shortcut, so the character is
// typed and neither selected nor moved over.
func TestTextFieldAltGrTypesOnWindows(t *testing.T) {
	r := newRig(t, false)
	old := altGrIsCtrlAlt
	altGrIsCtrlAlt = true
	t.Cleanup(func() { altGrIsCtrlAlt = old })
	altGr := ModCtrl | ModAlt
	// The platform delivers the key (A, with Ctrl+Alt) and its text together.
	r.in.PushKey(KeyEvent{Code: 'A', Mods: altGr, Down: true})
	r.in.AppendText("ą")
	r.frame()
	if r.state() != "ą|" {
		t.Fatalf("after AltGr+A: %q", r.state())
	}
	r.typ("b")
	if r.text() != "ąb" {
		t.Fatalf("the next character replaced the buffer: %q", r.text())
	}
	// The same pair is no word or delete-word modifier either.
	r.typ(" foo bar")
	r.key(KeyLeft, altGr)
	if r.state() != "ąb foo ba|r" {
		t.Errorf("AltGr+Left moved by word: %q", r.state())
	}
	r.key(KeyBackspace, altGr)
	if r.state() != "ąb foo b|r" {
		t.Errorf("AltGr+Backspace deleted a word: %q", r.state())
	}
	// A plain Ctrl still is the primary modifier.
	r.key('A', ModCtrl)
	if r.state() != "<ąb foo br>|" {
		t.Errorf("Ctrl+A: %q", r.state())
	}
	r.key(KeyRight, 0)
	r.key(KeyLeft, ModCtrl)
	if r.state() != "ąb foo |br" {
		t.Errorf("Ctrl+Left: %q", r.state())
	}
	// Elsewhere Ctrl+Alt is Ctrl.
	altGrIsCtrlAlt = false
	r.key(KeyRight, 0)
	r.key('A', altGr)
	if r.state() != "<ąb foo br>|" {
		t.Errorf("Ctrl+Alt+A without the AltGr rule: %q", r.state())
	}
}
