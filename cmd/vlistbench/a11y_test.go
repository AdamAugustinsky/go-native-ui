package main

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// Every interactive element of the window has an accessible name.
func TestAccessibleNames(t *testing.T) {
	ch := newChat(defItems)
	o := newOffscreen(t, ch)
	for range 3 + loadAfter {
		o.Frame()
	}
	var tree ui.SemTree
	o.UI.Semantics(&tree)
	widgettest.LintNames(t, "vlistbench", &tree)
}
