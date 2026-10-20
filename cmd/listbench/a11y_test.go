package main

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// Every interactive element of the window has an accessible name.
func TestAccessibleNames(t *testing.T) {
	l := newListApp(defRows)
	l.jump = 123456
	l.selected = l.jump + 3
	o := newOffscreen(t, l)
	for range 4 {
		o.Frame()
	}
	var tree ui.SemTree
	o.UI.Semantics(&tree)
	widgettest.LintNames(t, "listbench", &tree)
}
