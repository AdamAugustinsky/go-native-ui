package main

import (
	"testing"

	"gonativeui/img"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// Every interactive element of the window has an accessible name.
func TestAccessibleNames(t *testing.T) {
	g := newGrid(defItems, img.Options{})
	g.blocking = true
	o := newOffscreen(t, g)
	for range 3 {
		o.Frame()
	}
	var tree ui.SemTree
	o.UI.Semantics(&tree)
	widgettest.LintNames(t, "imgbench", &tree)
}
