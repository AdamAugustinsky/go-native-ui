package main

import (
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// Every interactive element of the window has an accessible name, with the
// popover open too.
func TestAccessibleNames(t *testing.T) {
	for _, popover := range []bool{false, true} {
		d := &demo{popover: popover}
		for i := range d.names {
			d.names[i] = []byte("Document 00.txt")
		}
		offscreenOnMain(func() {
			o, err := app.NewOffscreen(config(), winW*2, winH*2, 2, d)
			if err != nil {
				t.Error(err)
				return
			}
			defer o.Close()
			for range 4 {
				o.Frame()
			}
			var tree ui.SemTree
			o.UI.Semantics(&tree)
			widgettest.LintNames(t, "shelldemo", &tree)
		})
	}
}
