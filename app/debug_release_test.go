//go:build gnui_release

package app

import (
	"testing"

	"gonativeui/platform"
	"gonativeui/ui"
)

// A release build has no chord, no Debug menu and no inspector panel; the
// environment still turns the overlay on.
func TestReleaseBuildHasNoDebugChordMenuOrPanel(t *testing.T) {
	if devBuild {
		t.Fatal("devBuild in a release build")
	}
	o := newOverlayRig(t, &scene{}, 100, 100, 1)
	m := platform.ModAlt | platform.ModShift | platform.ModCtrl
	if ui.PrimaryMod() == ui.ModSuper {
		m = platform.ModAlt | platform.ModShift | platform.ModSuper
	}
	e := platform.Event{Kind: platform.EvKey, Key: platform.KeyA + 'D' - 'A', Char: 'd', Mods: m, Flags: platform.FlagDown}
	o.dispatch(&e)
	if o.DebugOn() || o.tr.in.NKeys == 0 {
		t.Error("the chord is recognised in a release build")
	}
	spec := o.nativeSpec()
	for _, mn := range spec.Menus {
		if mn.Title == "Debug" {
			t.Error("a Debug menu in a release build")
		}
	}
	o.EnableDebug(true)
	o.SetInspectPanel(true)
	o.Step(0)
	if o.dbg.draw.side.n != 0 {
		t.Error("the inspector panel has lines in a release build")
	}
}
