package pilot

import (
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The scenes the audits run on: light and dark, key and inactive, top and
// scrolled, with overlay and legacy scroll bars, and in Menlo.
var auditScenes = []struct {
	name string
	look Look
}{
	{"light", Look{Preference: theme.ForceLight}},
	{"dark", Look{Preference: theme.ForceDark}},
	{"inactive", Look{Preference: theme.ForceLight, Inactive: true}},
	{"scrolled", Look{Preference: theme.ForceLight, Scrolled: true}},
	{"legacy", Look{Preference: theme.ForceLight, Legacy: true}},
	{"legacy scrolled dark", Look{Preference: theme.ForceDark, Legacy: true, Scrolled: true}},
	{"mono", Look{Preference: theme.ForceLight, Mono: true}},
}

// scene renders the offscreen scene at 2×, as shoot does, and returns the
// window at rest.
func scene(t *testing.T, look Look) (*Pilot, *app.Offscreen) {
	t.Helper()
	pl := NewScene(look)
	o, _, err := renderScene(pl, 2400, 1520, 2, look, pl.SceneSetup, pl.ScenePost)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	t.Cleanup(o.Close)
	return pl, o
}

// TestAuditLayout runs the layout audit on the -o scene at 2×, as shoot
// renders it, in each of its looks.
func TestAuditLayout(t *testing.T) {
	for _, s := range auditScenes {
		t.Run(s.name, func(t *testing.T) {
			_, o := scene(t, s.look)
			widgettest.AuditLayout(t, "pilot "+s.name, o.UI, widgettest.Allow{
				Check: ui.AuditOverflow, Where: `Group "Path"`,
				Reason: "rexui.PathBar snaps each crumb's button, its symbol and its name separately: on Windows the symbol's " +
					"fallback glyph and the last name reach 1 to 2 px past the button (component owner's; measured in CI run 39417646800)",
			})
		})
	}
}

// Every control and row an assistive technology reads out has a name: a
// button its label, a row its file, a column its title. The nodes are the
// accessibility bridge's view of the tree (widgettest.LintNames).
func TestInteractiveNodesAreNamed(t *testing.T) {
	for _, s := range auditScenes[:1] { // the tree does not depend on the look
		t.Run(s.name, func(t *testing.T) {
			_, o := scene(t, s.look)
			var tr ui.SemTree
			o.UI.Semantics(&tr)
			widgettest.LintNames(t, "pilot "+s.name, &tr)
		})
	}
}
