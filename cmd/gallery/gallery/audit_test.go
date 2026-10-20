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

// legacyFiles accepts the columns of the lists page's Files table beside a
// legacy scroll bar: the catalogue's table sample lays its last column out
// for 12 pt of bar where AppKit's scroller takes 17 (rexui/README.md, the
// lists' samples), so its Size cells run 7 px past their header. It is the
// table recipe's, not the page's: the standalone appkit-lists page shows the
// same finding with a legacy scroller pinned.
var legacyFiles = widgettest.Allow{Check: ui.AuditColumn, Where: `Table "Files"`,
	Reason: "the catalogue's Files sample reserves 12 pt for a legacy scroll bar where AppKit's takes 17 (rexui README, the lists' samples)"}

// TestAuditScenes runs the layout audit on the last frame of every scene of
// Shot, with overlay and with legacy scroll bars, and of every appkit page
// (all of rexui's catalog samples), on the real text system and renderer.
func TestAuditScenes(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		style := "overlay scrollers"
		if legacy {
			style = "legacy scrollers"
		}
		for _, name := range SceneNames() {
			off, err := runScene(ShotOptions{Scene: name, Legacy: legacy})
			if err != nil {
				if os.Getenv("CI") == "" {
					t.Skipf("offscreen rendering: %v", err)
				}
				t.Fatal(err)
			}
			var allow []widgettest.Allow
			if legacy && name == "catalogue-lists" {
				allow = append(allow, legacyFiles)
			}
			widgettest.AuditLayout(t, name+", "+style, off.UI, allow...)
			off.Close()
		}
	}
	// the smallest window: the forms and the matrix still fit
	for _, name := range SceneNames() {
		off, err := runScene(ShotOptions{Scene: name, Width: 2 * MinWidthPt, Height: 2 * MinHeightPt})
		if err != nil {
			t.Fatal(err)
		}
		widgettest.AuditLayout(t, name+", the smallest window", off.UI)
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
