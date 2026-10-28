package main

import (
	"testing"

	"gonativeui/theme"
	"gonativeui/widget/widgettest"
)

var _ = widgettest.DefineUpdateFlag()

// The demo's chrome (the Rex toolbar over the browser pane), offscreen where
// there is no view, in light and dark, with a danger notice under the pane:
// the layout audit is clean, and the render matches testdata (1×).
func TestAuditLayout(t *testing.T) {
	for _, dark := range []bool{false, true} {
		d := &demo{notice: "Blocked a link to gnuitest:hello", danger: true}
		cfg := config()
		o := widgettest.NewOffscreen(t, cfg, int(cfg.Width), int(cfg.Height), 1, d)
		if dark {
			d.k.SetMode(theme.Dark)
		} else {
			d.k.SetMode(theme.Light)
		}
		for range 4 {
			o.Frame()
		}
		name := map[bool]string{false: "light", true: "dark"}[dark]
		widgettest.AuditLayout(t, "webdemo, "+name, o.UI)
		img, _ := o.Frame()
		widgettest.AssertImage(t, "webdemo-"+name, img, widgettest.Tolerance{})
		o.Close()
	}
}
