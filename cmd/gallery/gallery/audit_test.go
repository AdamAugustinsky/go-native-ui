package gallery

import (
	"os"
	"testing"

	"gonativeui/theme"
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
	for p, name := range AppKitPages {
		off, err := runAppKit(p, theme.ForceLight, 2, nil)
		if err != nil {
			t.Fatal(err)
		}
		widgettest.AuditLayout(t, name, off.UI)
		off.Close()
	}
}
