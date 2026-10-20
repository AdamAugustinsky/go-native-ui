package gallery

import (
	"os"
	"runtime"
	"testing"

	"gonativeui/rexui/catalog"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// fontWide are the catalog samples whose text lays out wider than their
// AppKit-measured frames with Linux's and Windows' fonts, which are wider than
// San Francisco (CI, October 2026: the Large Title line of label-styles by 36
// to 136 px, link's "Learn more" by 3 to 8 px, and form-grid's two field rows
// by 16 px on Linux, because the aligned label column is as wide as its widest
// label and the 200 pt fields move with it): only their overflow is accepted
// there, so a layout fault anywhere else on the pages still fails.
var fontWide = []string{"label-styles", "link", "form-grid"}

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
	const scale = 2
	for p, name := range AppKitPages {
		cells, _, _ := catalog.Grid(catalog.Page(p))
		var allow []widgettest.Allow
		if runtime.GOOS != "darwin" {
			for _, cl := range cells {
				sm := catalog.Samples[cl.Sample].Name
				for _, w := range fontWide {
					if w == sm {
						allow = append(allow, widgettest.Allow{Check: ui.AuditOverflow,
							Within: [4]float32{cl.X * scale, cl.Y * scale, (cl.X + cl.W) * scale, (cl.Y + cl.H) * scale},
							Reason: sm + ": its frame is AppKit's, measured on macOS, and this system's font is wider than San Francisco"})
					}
				}
			}
		}
		off, err := runAppKit(p, theme.ForceLight, scale, nil)
		if err != nil {
			t.Fatal(err)
		}
		widgettest.AuditLayout(t, name, off.UI, allow...)
		off.Close()
	}
}
