package pilot

import (
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/theme"
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
	t.Cleanup(func() { closeScene(pl, o) })
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

func TestAuditSizeColumnAtOneScale(t *testing.T) {
	pl := NewScene(Look{Legacy: true})
	o := newOffscreen(t, pl)
	for range 16 {
		o.Frame()
	}
	widgettest.AuditLayout(t, "pilot 1x size column", o.UI)
}
