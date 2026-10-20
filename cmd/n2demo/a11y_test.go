package main

import (
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// Every interactive element of the window has an accessible name.
func TestAccessibleNames(t *testing.T) {
	d := &demo{}
	o, err := app.NewOffscreen(app.Config{Title: "Native dialogs", Width: 1000, Height: 600}, 1000, 600, 1, d)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal(err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	defer o.Close()
	for range 3 {
		o.Frame()
	}
	var tree ui.SemTree
	o.UI.Semantics(&tree)
	widgettest.LintNames(t, "n2demo", &tree)
}
