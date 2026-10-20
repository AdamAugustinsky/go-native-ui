package gallery

import (
	"os"
	"runtime"
	"testing"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// TestAuditScenes runs the layout audit on the last frame of every scene of
// Shot and of every appkit page (all of rexui's catalog samples), on the real
// text system and renderer.
func TestAuditScenes(t *testing.T) {
	for _, name := range SceneNames() {
		off, err := runScene(ShotOptions{Scene: name})
		if err != nil {
			if os.Getenv("CI") == "" {
				t.Skipf("offscreen rendering: %v", err)
			}
			t.Fatal(err)
		}
		widgettest.AuditLayout(t, name, off.UI)
		off.Close()
	}
	var allow []widgettest.Allow
	if runtime.GOOS != "darwin" {
		allow = append(allow, widgettest.Allow{Check: ui.AuditOverflow,
			Reason: "a sample's frame is AppKit's, measured on macOS: another system's fonts are wider than San Francisco"})
	}
	for p, name := range AppKitPages {
		off, err := runAppKit(p, theme.ForceLight, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		widgettest.AuditLayout(t, name, off.UI, allow...)
		off.Close()
	}
}
