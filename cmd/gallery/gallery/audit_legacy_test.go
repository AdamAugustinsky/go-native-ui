package gallery

import (
	"runtime"
	"testing"

	"gonativeui/rexui/catalog"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// TestAuditAppKitLegacyScrollers runs the layout audit on the appkit pages
// with scroll bars that take space (theme.ScrollerLegacy): the Files table's
// Size column is then narrower than its numbers, which it truncates as
// AppKit's does, and that is no column fault.
func TestAuditAppKitLegacyScrollers(t *testing.T) {
	const scale = 2
	sys := &theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: theme.ScrollerLegacy}}
	for p, name := range AppKitPages {
		var allow []widgettest.Allow
		cells, _, _ := catalog.Grid(catalog.Page(p))
		for _, cl := range cells {
			sm := catalog.Samples[cl.Sample].Name
			for _, w := range fontWide {
				if w == sm && runtime.GOOS != "darwin" {
					allow = append(allow, widgettest.Allow{Check: ui.AuditOverflow,
						Within: [4]float32{cl.X * scale, cl.Y * scale, (cl.X + cl.W) * scale, (cl.Y + cl.H) * scale},
						Reason: sm + ": its frame is AppKit's, measured on macOS, and this system's font is wider than San Francisco"})
				}
			}
		}
		off, err := runAppKit(p, theme.ForceLight, scale, sys)
		if err != nil {
			t.Fatal(err)
		}
		widgettest.AuditLayout(t, name+", legacy scrollers", off.UI, allow...)
		off.Close()
	}
}
