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
			widgettest.AuditLayout(t, "pilot "+s.name, o.UI)
		})
	}
}

// Every control and row an assistive technology reads out has a name: a
// button its label, a row its file, a column its title.
//
// (widgettest.LintNames asks the accessibility bridge for the same; this
// checks the semantic tree itself until that lands on main.)
func TestInteractiveNodesAreNamed(t *testing.T) {
	_, o := scene(t, Look{Preference: theme.ForceLight})
	var tr ui.SemTree
	o.UI.Semantics(&tr)
	var named func(i int) bool
	named = func(i int) bool {
		if len(tr.Text(tr.Nodes[i].Name)) > 0 {
			return true
		}
		for j := int(tr.Nodes[i].FirstChild); j > 0; j = int(tr.Nodes[j].NextSibling) {
			if named(j) {
				return true
			}
		}
		return false
	}
	n := 0
	for i := 1; i < len(tr.Nodes); i++ {
		switch r := tr.Nodes[i].Role; r {
		case ui.RoleButton, ui.RoleTextField, ui.RoleSearchField, ui.RoleListItem, ui.RoleRow, ui.RoleColumnHeader:
			n++
			if !named(i) {
				t.Errorf("a %s at %v has no name", widgettest.RoleName(r), tr.Nodes[i].Bounds)
			}
		}
	}
	if n < 40 {
		t.Errorf("only %d interactive nodes: the scene is not what it should be", n)
	}
}
