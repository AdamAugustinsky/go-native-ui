package gallery

import (
	"os"
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The gallery as VoiceOver sees it through the accessibility bridge: the
// window as it opens, and with the dialog open over four selected rows (the
// content beneath it is left out). See widgettest.DumpAX.
func TestAccessibilitySnapshot(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(5)
	r.AssertAX("ax-main")
	r.Play(click(rowRole, "").Nth(3), click(rowRole, "").Nth(6).With(modShift),
		click(roleButton, "Delete…"), waitFor(ui.RoleDialog, ""), settle(), idle(2))
	r.AssertAX("ax-dialog")
}

// Every interactive element of every scene has an accessible name.
func TestAccessibleNames(t *testing.T) {
	for _, name := range SceneNames() {
		off, err := runScene(ShotOptions{Scene: name})
		if err != nil {
			if os.Getenv("CI") == "" {
				t.Skipf("offscreen rendering: %v", err)
			}
			t.Fatal(err)
		}
		var tree ui.SemTree
		off.UI.Semantics(&tree)
		widgettest.LintNames(t, name, &tree)
		off.Close()
	}
	for p, name := range AppKitPages {
		off, err := runAppKit(p, theme.ForceLight, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		var tree ui.SemTree
		off.UI.Semantics(&tree)
		widgettest.LintNames(t, name, &tree, appkitNames...)
		off.Close()
	}
}

var appkitNames = []widgettest.NameAllow{
	{Role: ui.RoleSwitch, Reason: "the appkit page reproduces AppKitRef's NSSwitch samples, which have no title (a title would be drawn beside the track and break the comparison); each sample is captioned by the label after it"},
}
