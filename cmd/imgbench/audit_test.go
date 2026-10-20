package main

import (
	"testing"

	"gonativeui/img"
	"gonativeui/widget/widgettest"
)

// TestAuditLayout runs the layout audit on the -o scene: the grid with its
// first thumbnails decoded.
func TestAuditLayout(t *testing.T) {
	g := newGrid(defItems, img.Options{})
	g.blocking = true
	o := newOffscreen(t, g)
	for range 3 {
		o.Frame()
	}
	widgettest.AuditLayout(t, "imgbench", o.UI)
}
