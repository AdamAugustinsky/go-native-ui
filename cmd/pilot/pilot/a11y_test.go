package pilot

import (
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// Every interactive element of the -o scene has an accessible name.
func TestAccessibleNames(t *testing.T) {
	pl := New(SceneOptions())
	o, err := app.NewOffscreen(Config(), 2400, 1520, 2, pl)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	defer closeScene(pl, o)
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
	var tree ui.SemTree
	o.UI.Semantics(&tree)
	widgettest.LintNames(t, "pilot", &tree)
}
