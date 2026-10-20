package app

import (
	"math"
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

func keyEv(k platform.Key, char rune, m platform.Mods) *platform.Event {
	return ev(platform.EvKey, func(e *platform.Event) { e.Key, e.Char, e.Mods, e.Flags = k, char, m, platform.FlagDown })
}

// TestShortcutCodeFollowsTheLayout: with Ctrl or Super a shortcut names the key
// by what it types, so ⌘Z is where an AZERTY or Dvorak user expects it, and the
// physical position stays in KeyEvent.Physical.
func TestShortcutCodeFollowsTheLayout(t *testing.T) {
	const (
		cmd  = platform.ModSuper
		ctrl = platform.ModCtrl
	)
	cases := []struct {
		name     string
		key      platform.Key
		char     rune
		mods     platform.Mods
		code     ui.KeyCode
		physical ui.KeyCode
	}{
		{"US ⌘Z", platform.KeyA + 'Z' - 'A', 'z', cmd, 'Z', 0},
		{"AZERTY ⌘Z (Z is where W is on US)", platform.KeyA + 'W' - 'A', 'z', cmd, 'Z', 'W'},
		{"AZERTY ⌘W (W is where Z is on US)", platform.KeyA + 'Z' - 'A', 'w', cmd, 'W', 'Z'},
		{"Dvorak ⌘, (the comma is where W is on US)", platform.KeyA + 'W' - 'A', ',', cmd, ui.KeyComma, 'W'},
		{"Dvorak ⌘Z (Z is where ; is on US)", platform.KeySemicolon, 'z', cmd, 'Z', ui.KeySemicolon},
		{"Ctrl is a shortcut modifier too", platform.KeyA + 'W' - 'A', 'z', ctrl, 'Z', 'W'},
		{"Shift with it", platform.KeyA + 'W' - 'A', 'z', cmd | platform.ModShift, 'Z', 'W'},
		{"without Ctrl or Super the key is physical", platform.KeyA + 'W' - 'A', 'z', 0, 'W', 0},
		{"Alt alone is not a shortcut", platform.KeyA + 'W' - 'A', 'z', platform.ModAlt, 'W', 0},
		{"a Cyrillic layout falls back to the position", platform.KeyA + 'C' - 'A', 'с', cmd, 'C', 0},
		{"a digit row typing symbols stays on its key (AZERTY ⌘1)", platform.Key0 + 1, '&', cmd, '1', 0},
		{"a digit stays physical even where the digit is typed elsewhere", platform.Key0 + 1, 'z', cmd, 'Z', '1'},
		{"a key that types nothing is physical", platform.KeyLeft, 0, cmd, ui.KeyLeft, 0},
		{"US ⌘=", platform.KeyEqual, '=', cmd, ui.KeyEqual, 0},
		{"AZERTY ⌘; ", platform.KeyA + 'M' - 'A', ';', cmd, ui.KeySemicolon, 'M'},
	}
	for _, c := range cases {
		tr := newTranslator()
		tr.event(keyEv(c.key, c.char, c.mods), nil)
		if tr.in.NKeys != 1 {
			t.Errorf("%s: %d key events", c.name, tr.in.NKeys)
			continue
		}
		if k := tr.in.Keys[0]; k.Code != c.code || k.Physical != c.physical {
			t.Errorf("%s: code %q physical %q, want %q %q", c.name, rune(k.Code), rune(k.Physical), rune(c.code), rune(c.physical))
		}
	}
}

// TestNewKeys: punctuation, the numpad and F1–F20 reach ui, each as its own
// code, and the platform's contiguous ranges stay contiguous in ui's.
func TestNewKeys(t *testing.T) {
	seen := map[ui.KeyCode]platform.Key{}
	add := func(k platform.Key, want ui.KeyCode) {
		t.Helper()
		got := keyCode(k)
		if got != want {
			t.Errorf("platform key %d → ui %#x, want %#x", k, got, want)
		}
		if prev, dup := seen[got]; dup && got != 0 {
			t.Errorf("platform keys %d and %d both map to ui %#x", prev, k, got)
		}
		seen[got] = k
	}
	for k, want := range map[platform.Key]ui.KeyCode{
		platform.KeyComma: ',', platform.KeyEqual: '=', platform.KeyMinus: '-', platform.KeySlash: '/',
		platform.KeySemicolon: ';', platform.KeyQuote: '\'', platform.KeyBackslash: '\\', platform.KeyGrave: '`',
		platform.KeyLeftBracket: '[', platform.KeyRightBracket: ']', platform.KeyPeriod: '.',
		platform.KeyNumDecimal: ui.KeyNumDecimal, platform.KeyNumAdd: ui.KeyNumAdd, platform.KeyNumSubtract: ui.KeyNumSubtract,
		platform.KeyNumMultiply: ui.KeyNumMultiply, platform.KeyNumDivide: ui.KeyNumDivide, platform.KeyNumEqual: ui.KeyNumEqual,
		platform.KeyNumClear: ui.KeyNumClear,
	} {
		add(k, want)
	}
	for i := 0; i < 10; i++ {
		add(platform.KeyNum0+platform.Key(i), ui.KeyNum0+ui.KeyCode(i))
	}
	for i := 0; i < 12; i++ {
		add(platform.KeyF1+platform.Key(i), ui.KeyF1+ui.KeyCode(i))
	}
	for i := 0; i < 8; i++ {
		add(platform.KeyF13+platform.Key(i), ui.KeyF13+ui.KeyCode(i))
	}
	if ui.KeyF20-ui.KeyF1 != 19 || ui.KeyNum9-ui.KeyNum0 != 9 {
		t.Error("ui's function key or keypad range is not contiguous")
	}
	tr := newTranslator()
	if !tr.event(keyEv(platform.KeyF13+2, 0, 0), nil) || tr.in.Keys[0].Code != ui.KeyF1+14 {
		t.Errorf("F15: %+v", tr.in.Keys[0])
	}
}

// TestModifierChanges: a modifier pressed or released on its own updates
// Input.Mods and repaints, once per change.
func TestModifierChanges(t *testing.T) {
	tr := newTranslator()
	mod := func(m platform.Mods) bool {
		return tr.event(ev(platform.EvModifiers, func(e *platform.Event) { e.Mods = m }), nil)
	}
	if !mod(platform.ModSuper) || tr.in.Mods != ui.ModSuper {
		t.Fatalf("⌘ pressed: mods %v", tr.in.Mods)
	}
	if mod(platform.ModSuper) {
		t.Error("an unchanged modifier state repaints")
	}
	if !mod(platform.ModSuper|platform.ModAlt) || tr.in.Mods != ui.ModSuper|ui.ModAlt {
		t.Errorf("⌥ added: mods %v", tr.in.Mods)
	}
	if !mod(0) || tr.in.Mods != 0 {
		t.Errorf("released: mods %v", tr.in.Mods)
	}
	if tr.in.NKeys != 0 {
		t.Error("a modifier change produced a key")
	}
}

// TestEditCommands: the platform's EvEdit becomes ui's edit action, extending
// when the event says; a frame that is full keeps the rest, in order, for the
// next one.
func TestEditCommands(t *testing.T) {
	tr := newTranslator()
	edit := func(c platform.EditCmd, f platform.Flags) {
		tr.event(ev(platform.EvEdit, func(e *platform.Event) { e.Tag, e.Flags = uint64(c), f }), nil)
	}
	edit(platform.EditMoveWordLeft, 0)
	edit(platform.EditMoveWordRight, platform.FlagSelect)
	edit(platform.EditDeleteToParagraphEnd, 0)
	edit(platform.EditTranspose, 0)
	edit(platform.EditMoveParagraphForward, 0)
	edit(platform.EditNone, 0) // no command: nothing
	want := []ui.EditAction{ui.EditMoveWordLeft, ui.EditMoveWordRight.Extend(), ui.EditDeleteToParagraphEnd, ui.EditTranspose, ui.EditMoveParagraphEnd}
	if tr.in.NEdit != len(want) {
		t.Fatalf("%d edit actions %v, want %v", tr.in.NEdit, tr.in.Edit[:tr.in.NEdit], want)
	}
	for i, w := range want {
		if tr.in.Edit[i] != w {
			t.Errorf("action %d = %#x, want %#x", i, tr.in.Edit[i], w)
		}
	}

	tr = newTranslator()
	for i := 0; i < ui.MaxEdit+3; i++ { // a key held down faster than frames
		edit(platform.EditDeleteBackward, 0)
	}
	if tr.in.NEdit != ui.MaxEdit || !tr.pending() {
		t.Fatalf("%d actions in the frame, pending %v", tr.in.NEdit, tr.pending())
	}
	tr.endFrame()
	if tr.in.NEdit != 3 {
		t.Errorf("the next frame has %d of the 3 that waited", tr.in.NEdit)
	}
}

// TestGestures: a pinch, a rotation (degrees to radians, counterclockwise
// positive) and a smart magnify reach Input.Gestures in order with their phases
// and points; a full frame keeps the rest for the next.
func TestGestures(t *testing.T) {
	tr := newTranslator()
	g := func(k platform.EventKind, p platform.ScrollPhase, amount, x, y float32) bool {
		return tr.event(ev(k, func(e *platform.Event) { e.Phase, e.Amount, e.X, e.Y, e.Time = p, amount, x, y, 99 }), nil)
	}
	if !g(platform.EvMagnify, platform.PhaseBegan, 0.1, 10, 20) {
		t.Fatal("a pinch does not repaint")
	}
	g(platform.EvRotate, platform.PhaseBegan, 90, 11, 21)
	g(platform.EvMagnify, platform.PhaseChanged, -0.05, 12, 22)
	g(platform.EvSmartMagnify, platform.PhaseNone, 0, 13, 23)
	g(platform.EvMagnify, platform.PhaseEnded, 0, 14, 24)
	want := []ui.GestureEvent{
		{Kind: ui.GestureMagnify, Phase: ui.PhaseBegan, Amount: 0.1, X: 10, Y: 20, Time: 99},
		{Kind: ui.GestureRotate, Phase: ui.PhaseBegan, Amount: math.Pi / 2, X: 11, Y: 21, Time: 99},
		{Kind: ui.GestureMagnify, Phase: ui.PhaseChanged, Amount: -0.05, X: 12, Y: 22, Time: 99},
		{Kind: ui.GestureSmartMagnify, X: 13, Y: 23, Time: 99},
		{Kind: ui.GestureMagnify, Phase: ui.PhaseEnded, X: 14, Y: 24, Time: 99},
	}
	if tr.in.NGestures != len(want) {
		t.Fatalf("%d gestures, want %d", tr.in.NGestures, len(want))
	}
	for i, w := range want {
		got := tr.in.Gestures[i]
		if math.Abs(float64(got.Amount-w.Amount)) > 1e-6 {
			t.Errorf("gesture %d amount %v, want %v", i, got.Amount, w.Amount)
		}
		got.Amount, w.Amount = 0, 0
		if got != w {
			t.Errorf("gesture %d = %+v, want %+v", i, got, w)
		}
	}
	tr.endFrame()
	for i := 0; i < ui.MaxGestures+2; i++ {
		g(platform.EvMagnify, platform.PhaseChanged, float32(i), 0, 0)
	}
	if tr.in.NGestures != ui.MaxGestures {
		t.Fatalf("%d gestures in a full frame", tr.in.NGestures)
	}
	tr.endFrame()
	if tr.in.NGestures != 2 || tr.in.Gestures[0].Amount != float32(ui.MaxGestures) || tr.in.Gestures[1].Amount != float32(ui.MaxGestures+1) {
		t.Errorf("the next frame has %d gestures %+v, want the 2 that waited in order", tr.in.NGestures, tr.in.Gestures[:tr.in.NGestures])
	}
}

// TestSystemClickCount: a press carrying the system's click count is that
// count, whatever the fixed timing would say; one without counts by timing.
func TestSystemClickCount(t *testing.T) {
	tr := newTranslator()
	p := func(clicks uint8, tm int64) uint8 {
		tr.in.ClearFrame()
		tr.in.Down = [3]bool{}
		tr.event(ev(platform.EvPointerButton, func(e *platform.Event) {
			e.Button, e.Flags, e.X, e.Y, e.Time, e.Clicks = platform.ButtonLeft, platform.FlagDown, 5, 5, tm, clicks
		}), nil)
		return tr.in.Clicks[ui.ButtonLeft]
	}
	if n := p(3, 1e9); n != 3 {
		t.Errorf("system count 3: %d", n)
	}
	if n := p(1, 1e9+1); n != 1 { // the system says a new click, close in time as it is
		t.Errorf("system count 1 right after: %d", n)
	}
	if n := p(0, 2e9); n != 1 {
		t.Errorf("no system count, first click: %d", n)
	}
	if n := p(0, 2e9+100e6); n != 2 {
		t.Errorf("no system count, a second click 100 ms later: %d", n)
	}
}

// TestFocusLossReleasesModifiers: a modifier held when the window loses focus
// is not held for the app afterwards (its release goes to another window).
func TestFocusLossReleasesModifiers(t *testing.T) {
	tr := newTranslator()
	tr.event(ev(platform.EvModifiers, func(e *platform.Event) { e.Mods = platform.ModSuper }), nil)
	if tr.in.Mods != ui.ModSuper {
		t.Fatalf("⌘ not held: %v", tr.in.Mods)
	}
	tr.event(ev(platform.EvFocus, func(*platform.Event) {}), nil)
	if tr.in.Mods != 0 {
		t.Errorf("modifiers after the focus loss: %v, want none", tr.in.Mods)
	}
}

// TestKeysAndEditsKeepTheirOrderThroughTheTranslator: an edit command knows
// how many keys came before it in the frame.
func TestKeysAndEditsKeepTheirOrderThroughTheTranslator(t *testing.T) {
	tr := newTranslator()
	edit := func() *platform.Event {
		return ev(platform.EvEdit, func(e *platform.Event) { e.Tag = uint64(platform.EditMoveLineStart) })
	}
	tr.event(edit(), nil)
	tr.event(keyEv(platform.KeyLeft, 0, 0), nil)
	tr.event(keyEv(platform.KeyRight, 0, 0), nil)
	tr.event(edit(), nil)
	if tr.in.NEdit != 2 || tr.in.NKeys != 2 || tr.in.EditAfter[0] != 0 || tr.in.EditAfter[1] != 2 {
		t.Errorf("edits after %d and %d keys (%d edits, %d keys), want after 0 and 2", tr.in.EditAfter[0], tr.in.EditAfter[1], tr.in.NEdit, tr.in.NKeys)
	}
}
