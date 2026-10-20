package rex

import (
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// Every interactive element of the window has an accessible name.
func TestAccessibleNames(t *testing.T) {
	x := NewApp()
	x.Traffic = true
	off, err := app.NewOffscreen(Config(), 2114, 1278, 2, &shotRex{Rex: x})
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	defer off.Close()
	for n := 0; n < 600; n++ {
		off.Frame()
		if !off.UI.Animating() {
			break
		}
	}
	var tree ui.SemTree
	off.UI.Semantics(&tree)
	widgettest.LintNames(t, "rexlook", &tree)
}
