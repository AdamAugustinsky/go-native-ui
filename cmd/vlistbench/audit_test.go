package main

import (
	"testing"

	"gonativeui/widget/widgettest"
)

// TestAuditLayout runs the layout audit on the -o scene: the timeline at the
// bottom once the history has loaded.
func TestAuditLayout(t *testing.T) {
	ch := newChat(defItems)
	o := newOffscreen(t, ch)
	for range 3 + loadAfter {
		o.Frame()
	}
	widgettest.AuditLayout(t, "vlistbench", o.UI)
}
