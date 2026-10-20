package gallery

import (
	"os"
	"runtime"
	"strings"
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

// checkModes are the appearances every page is checked in.
var checkModes = []struct {
	name string
	pref theme.Preference
}{{"light", theme.ForceLight}, {"dark", theme.ForceDark}}

// catalogueFontWide are fontWide's samples as the audit names them in a
// catalogue page hosted in the window (the standalone pages are accepted by
// their frames), off macOS.
func catalogueFontWide(scene string) []widgettest.Allow {
	if runtime.GOOS == "darwin" || !strings.HasPrefix(scene, "catalogue") {
		return nil
	}
	const why = ": its frame is AppKit's, measured on macOS, and this system's font is wider than San Francisco"
	return []widgettest.Allow{
		{Check: ui.AuditOverflow, Where: `Link "Learn more"`, Reason: "link" + why},
		{Check: ui.AuditOverflow, Where: `Label "Large Title`, Reason: "label-styles" + why},
		{Check: ui.AuditOverflow, Where: `Group "Name:"`, Reason: "form-grid" + why},
		{Check: ui.AuditOverflow, Where: `Group "Email address:"`, Reason: "form-grid" + why},
	}
}

// TestAuditScenes runs the layout audit on the last frame of every scene of
// Shot, with overlay and with legacy scroll bars, and of every appkit page
// (all of rexui's catalog samples), on the real text system and renderer.
func TestAuditScenes(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		style := "overlay scrollers"
		if legacy {
			style = "legacy scrollers"
		}
		for _, mode := range checkModes {
			for _, name := range SceneNames() {
				off, err := runScene(ShotOptions{Scene: name, Legacy: legacy, Preference: mode.pref})
				if err != nil {
					if os.Getenv("CI") == "" {
						t.Skipf("offscreen rendering: %v", err)
					}
					t.Fatal(err)
				}
				widgettest.AuditLayout(t, name+", "+style+", "+mode.name, off.UI, catalogueFontWide(name)...)
				off.Close()
			}
		}
	}
	// the smallest window: the forms and the matrix still fit
	for _, name := range SceneNames() {
		off, err := runScene(ShotOptions{Scene: name, Width: 2 * MinWidthPt, Height: 2 * MinHeightPt})
		if err != nil {
			t.Fatal(err)
		}
		widgettest.AuditLayout(t, name+", the smallest window", off.UI, catalogueFontWide(name)...)
		off.Close()
	}
	const scale = 2
	for _, mode := range checkModes {
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
			off, err := runAppKit(p, mode.pref, scale, nil)
			if err != nil {
				t.Fatal(err)
			}
			widgettest.AuditLayout(t, name+", "+mode.name, off.UI, allow...)
			off.Close()
		}
	}
}
