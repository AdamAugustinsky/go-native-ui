package rex

import (
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// TestAuditLayout runs the layout audit on the -o scene, drawn and native
// (RenderShot's two scenes), at 2×.
func TestAuditLayout(t *testing.T) {
	for _, native := range []bool{false, true} {
		x := NewApp()
		x.Traffic = true
		cfg := Config()
		name := "rexlook -drawn"
		if native {
			x.backdrop, x.shellShot = true, true
			cfg.Clear = [4]float32{}
			name = "rexlook native"
		}
		off, err := app.NewOffscreen(cfg, 2114, 1278, 2, &shotRex{Rex: x})
		if err != nil {
			if os.Getenv("CI") != "" {
				t.Fatal(err)
			}
			t.Skipf("offscreen rendering: %v", err)
		}
		for n := 0; n < 600; n++ {
			off.Frame()
			if !off.UI.Animating() {
				break
			}
		}
		widgettest.AuditLayout(t, name, off.UI, rexlookAllow...)
		off.Close()
	}
}

// rexlookAllow are the findings on rexlook, the reference design, which is
// not changed to satisfy the audit (rexcompare holds it to the reference).
var rexlookAllow = []widgettest.Allow{
	{Check: ui.AuditOverflow, Where: "image {floating}", Other: "{floating background clickable}",
		Reason: "the pane-split, ⌘ and + icons are rasters with a transparent margin, a little larger than their 28-pt hit box (IconButton places the icon on the parent's grid); the ink is inside"},
}
