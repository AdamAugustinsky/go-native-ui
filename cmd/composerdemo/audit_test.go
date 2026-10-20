package main

import (
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/widget/widgettest"
)

// TestAuditLayout runs the layout audit on the -o scene, with and without
// the completion list.
func TestAuditLayout(t *testing.T) {
	for _, popup := range []bool{false, true} {
		d := newDemo()
		o, err := app.NewOffscreen(config(), winW*2, winH*2, 2, d)
		if err != nil {
			if os.Getenv("CI") != "" {
				t.Fatalf("offscreen rendering on CI: %v", err)
			}
			t.Skipf("offscreen rendering: %v", err)
		}
		if popup {
			d.popup, d.nMatch, d.matches[1] = true, 2, 1
		}
		for range 4 {
			o.Frame()
		}
		name := "rest"
		if popup {
			name = "completion"
		}
		widgettest.AuditLayout(t, name, o.UI)
		o.Close()
	}
}
