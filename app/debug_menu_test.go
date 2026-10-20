//go:build !gnui_release

package app

import (
	"testing"

	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/ui"
)

func chordMods() platform.Mods {
	m := platform.ModAlt | platform.ModShift
	if ui.PrimaryMod() == ui.ModSuper {
		return m | platform.ModSuper
	}
	return m | platform.ModCtrl
}

// chord is the key-down of the chord for the key at the physical position phys
// that types char on the user's layout.
func chord(phys platform.Key, char rune) platform.Event {
	return platform.Event{Kind: platform.EvKey, Key: phys, Char: char, Mods: chordMods(), Flags: platform.FlagDown}
}

func keyOf(letter byte) platform.Key { return platform.KeyA + platform.Key(letter-'A') }

func TestChordIsRecognisedAtIngressAndNeverReachesTheApp(t *testing.T) {
	o := newOverlayRig(t, &scene{}, 400, 300, 1)
	settleSteps(t, o)
	e := chord(keyOf('D'), 'd')
	o.dispatch(&e)
	if !o.DebugOn() {
		t.Fatal("the chord did not turn the overlay on")
	}
	if o.tr.in.NKeys != 0 {
		t.Error("the chord reached the app's input")
	}
	if !o.p.needDraw {
		t.Error("the toggle owes no frame")
	}
	e = chord(keyOf('I'), 'i')
	o.dispatch(&e)
	if !o.dbg.panel {
		t.Error("the inspector chord did not show the panel")
	}
	// a key held down repeats: acted on once, still not the app's
	e = chord(keyOf('I'), 'i')
	e.Flags |= platform.FlagRepeat
	o.dispatch(&e)
	if !o.dbg.panel || o.tr.in.NKeys != 0 {
		t.Error("a repeat of the chord toggled again or reached the app")
	}
	// other keys, and the chord without Alt, are the app's
	for _, e := range []platform.Event{
		{Kind: platform.EvKey, Key: keyOf('D'), Char: 'd', Flags: platform.FlagDown},
		{Kind: platform.EvKey, Key: keyOf('D'), Char: 'd', Mods: chordMods() &^ platform.ModAlt, Flags: platform.FlagDown},
		chord(keyOf('X'), 'x'),
	} {
		o.dispatch(&e)
		if !o.DebugOn() || o.tr.in.NKeys == 0 {
			t.Errorf("event %+v was taken as the chord", e)
		}
		o.tr.in.NKeys = 0
	}
	e = chord(keyOf('D'), 'd')
	o.dispatch(&e)
	if o.DebugOn() {
		t.Error("the chord did not turn the overlay off")
	}
}

// The chord names a letter by what the key types on the user's layout
// (platform.Event.Char), as every shortcut: on Dvorak, D is at the QWERTY E
// position, and the key at QWERTY's D position types E.
func TestChordFollowsTheLayout(t *testing.T) {
	o := newOverlayRig(t, &scene{}, 100, 100, 1)
	e := chord(keyOf('D'), 'e') // Dvorak: the D position types e
	o.dispatch(&e)
	if o.DebugOn() {
		t.Error("the physical D key of a Dvorak layout toggled the overlay")
	}
	o.tr.in.NKeys = 0
	e = chord(keyOf('E'), 'd') // Dvorak: the E position types d
	o.dispatch(&e)
	if !o.DebugOn() {
		t.Error("the key that types D on a Dvorak layout did not toggle the overlay")
	}
	o.EnableDebug(false)
	e = chord(keyOf('D'), 0) // a layout without a Latin ⌘ table: the physical key
	o.dispatch(&e)
	if !o.DebugOn() {
		t.Error("the physical key is not the fallback when the layout has no character")
	}
}

func debugMenuOf(s platform.MenuSpec) (platform.Menu, int, bool) {
	for k, bi := range s.Bar {
		if s.Menus[bi].Title == "Debug" {
			return s.Menus[bi], k, true
		}
	}
	return platform.Menu{}, 0, false
}

// The native bar has a Debug menu whether the app sets a model or not, with
// the chords as key equivalents; the model's menus are left as they are, and
// the menu comes before Help. The items are enabled and checked by the state
// of the overlay, and choosing one reaches the overlay, not the app.
func TestDebugMenuInTheNativeBar(t *testing.T) {
	model, err := menu.NewModel(
		[]menu.Command{{ID: 1, Title: "Open"}},
		[]menu.Menu{{Title: "File", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 1}}}, {Title: "Help", Role: menu.MenuHelp}},
		[]int{0, 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []*menu.Model{nil, model} {
		o := newOverlayRig(t, &scene{}, 100, 100, 1)
		o.menu = m
		spec := o.nativeSpec()
		dm, at, ok := debugMenuOf(spec)
		if !ok {
			t.Fatalf("model %v: no Debug menu in the bar", m != nil)
		}
		if spec.Menus[spec.Bar[at+1]].Role != platform.MenuRoleHelp {
			t.Error("the Debug menu is not before Help")
		}
		if m != nil && (spec.Menus[0].Title != "File" || len(spec.Menus[0].Items) != 1) {
			t.Errorf("the model's menus changed: %+v", spec.Menus[0])
		}
		want := []struct {
			id  uint16
			key rune
		}{{debugCmdOverlay, 'd'}, {debugCmdInspector, 'i'}, {debugCmdAudit, 'a'}}
		if len(dm.Items) != len(want) {
			t.Fatalf("%d items", len(dm.Items))
		}
		for i, w := range want {
			it := dm.Items[i]
			if it.ID != w.id || it.Key != w.key || it.Mods != platform.ModSuper|platform.ModAlt|platform.ModShift {
				t.Errorf("item %d: %+v", i, it)
			}
		}
		// state
		if en, ch := o.menuState(debugCmdOverlay, platform.MenuNone); !en || ch {
			t.Errorf("Toggle Overlay off: enabled %v checked %v", en, ch)
		}
		if en, _ := o.menuState(debugCmdInspector, platform.MenuNone); en {
			t.Error("Inspector is enabled with the overlay off")
		}
		// choosing it: an EvCommand, which the overlay takes
		e := platform.Event{Kind: platform.EvCommand, Tag: uint64(debugCmdOverlay)}
		o.dispatch(&e)
		if !o.DebugOn() || o.tr.in.NCommands != 0 {
			t.Fatalf("the menu command: overlay %v, %d commands for the app", o.DebugOn(), o.tr.in.NCommands)
		}
		if en, ch := o.menuState(debugCmdOverlay, platform.MenuNone); !en || !ch {
			t.Errorf("Toggle Overlay on: enabled %v checked %v", en, ch)
		}
		e = platform.Event{Kind: platform.EvCommand, Tag: uint64(debugCmdInspector)}
		o.dispatch(&e)
		if en, ch := o.menuState(debugCmdInspector, platform.MenuNone); !en || !ch {
			t.Errorf("Inspector shown: enabled %v checked %v", en, ch)
		}
		e = platform.Event{Kind: platform.EvCommand, Tag: uint64(debugCmdAudit)}
		o.dispatch(&e)
		if !o.dbg.auditOn {
			t.Error("Run Layout Audit did not show the audit")
		}
		// an app's own command passes through
		e = platform.Event{Kind: platform.EvCommand, Tag: 1}
		o.dispatch(&e)
		if o.tr.in.NCommands != 1 {
			t.Errorf("the app's command was taken: %d", o.tr.in.NCommands)
		}
	}
}

// With the native bar installed AppKit has matched the chord (by character, on
// any layout, ahead of a hosted text view): the key is not matched a second
// time.
func TestChordIsLeftToTheNativeBar(t *testing.T) {
	o := newOverlayRig(t, &scene{}, 100, 100, 1)
	o.barOn = true
	e := chord(keyOf('D'), 'd')
	o.dispatch(&e)
	if o.DebugOn() {
		t.Error("the chord was matched twice")
	}
}

// With the overlay off the events pay for the chord check only on a key with
// modifiers: no allocation, and the frame has no scan of its input.
func TestDebugOffCostsNothingPerEvent(t *testing.T) {
	o := newOverlayRig(t, &scene{}, 100, 100, 1)
	e := platform.Event{Kind: platform.EvKey, Key: keyOf('A'), Char: 'a', Mods: platform.ModSuper, Flags: platform.FlagDown}
	if n := testing.AllocsPerRun(100, func() { o.dispatch(&e); o.tr.in.NKeys = 0 }); n != 0 {
		t.Errorf("a key event allocates %.1f times with the overlay off", n)
	}
}

func BenchmarkDispatchKeyOverlayOff(b *testing.B) {
	o, err := NewOffscreen(Config{}, 100, 100, 1, &scene{})
	if err != nil {
		b.Skip(err)
	}
	defer o.Close()
	e := platform.Event{Kind: platform.EvKey, Key: keyOf('A'), Char: 'a', Mods: platform.ModSuper, Flags: platform.FlagDown}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		o.dispatch(&e)
		o.tr.in.NKeys = 0
	}
}

func BenchmarkFrameOverlayOff(b *testing.B) {
	o, err := NewOffscreen(Config{}, 400, 300, 1, &scene{})
	if err != nil {
		b.Skip(err)
	}
	defer o.Close()
	for i := 0; i < 20; i++ {
		o.Frame()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o.build(400, 300, 1.0/60)
	}
}
