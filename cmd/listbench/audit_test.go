package main

import (
	"testing"

	"gonativeui/widget/widgettest"
)

// TestAuditLayout runs the layout audit on the -o scene: the list at 2×,
// scrolled mid-list with a selected row in view.
func TestAuditLayout(t *testing.T) {
	l := newListApp(defRows)
	l.jump = 123456
	l.selected = l.jump + 3
	o := newOffscreen(t, l)
	for range 4 {
		o.Frame()
	}
	widgettest.AuditLayout(t, "listbench", o.UI)
}
