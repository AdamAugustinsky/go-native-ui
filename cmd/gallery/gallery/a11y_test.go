package gallery

import (
	"os"
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The gallery as VoiceOver sees it through the accessibility bridge: the
// window as it opens (the Playground, with the sidebar), and with the dialog
// open after four rows of the Table were selected (the content beneath it is
// left out). See widgettest.DumpAX.
func TestAccessibilitySnapshot(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.Frames(5)
	r.AssertAX("ax-main")
	r.goTo(scTable)
	r.Play(click(rowRole, "").Nth(3), click(rowRole, "").Nth(6).With(modShift))
	r.goTo(scPlayground)
	r.Play(click(roleButton, "Delete…"), waitFor(ui.RoleDialog, ""), settle(), idle(2))
	r.AssertAX("ax-dialog")
}

// Every interactive element of every scene and every appkit page has an
// accessible name, in light and dark.
func TestAccessibleNames(t *testing.T) {
	for _, mode := range checkModes {
		for _, name := range SceneNames() {
			off, err := runScene(ShotOptions{Scene: name, Preference: mode.pref})
			if err != nil {
				if os.Getenv("CI") == "" {
					t.Skipf("offscreen rendering: %v", err)
				}
				t.Fatal(err)
			}
			var tree ui.SemTree
			off.UI.Semantics(&tree)
			widgettest.LintNames(t, name+", "+mode.name, &tree)
			off.Close()
		}
		for p, name := range AppKitPages {
			off, err := runAppKit(p, mode.pref, 2, nil)
			if err != nil {
				t.Fatal(err)
			}
			var tree ui.SemTree
			off.UI.Semantics(&tree)
			widgettest.LintNames(t, name+", "+mode.name, &tree)
			off.Close()
		}
	}
}
