package pilot

import (
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/widget/widgettest"
)

// TestAuditLayout runs the layout audit on the -o scene at 2×, as shoot
// renders it.
func TestAuditLayout(t *testing.T) {
	pl := New(SceneOptions())
	o, err := app.NewOffscreen(Config(), 2400, 1520, 2, pl)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	defer o.Close()
	settle := func() {
		for n := 0; n < 600; n++ {
			o.Frame()
			if !o.UI.Animating() {
				return
			}
		}
		t.Fatal("pilot: the scene did not settle")
	}
	pl.SceneSetup()
	settle()
	pl.ScenePost()
	settle()
	widgettest.AuditLayout(t, "pilot", o.UI)
}
