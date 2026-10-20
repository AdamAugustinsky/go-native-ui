package main

import (
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// TestAuditLayout runs the layout audit on the -o scene in both modes, at
// rest, scrolled (rows under the header) and with the popover open over the
// inspector.
func TestAuditLayout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dark    bool
		scroll  float32
		popover bool
	}{
		{"light", false, 0, false},
		{"dark", true, 0, false},
		{"scrolled", true, 90, false},
		{"popover", true, 90, true},
	} {
		d := &demo{popover: tc.popover, scrollTo: tc.scroll}
		for i := range d.names {
			d.names[i] = []byte("Document 00.txt")
		}
		if tc.dark {
			d.force = 2
		}
		offscreenOnMain(func() {
			o, err := app.NewOffscreen(config(), winW*2, winH*2, 2, d)
			if err != nil {
				t.Error(err)
				return
			}
			defer o.Close()
			o.UI.SetSystemAppearance(ui.Appearance{Dark: tc.dark, Known: true})
			for range 4 {
				o.Frame()
			}
			widgettest.AuditLayout(t, tc.name, o.UI)
		})
	}
}
