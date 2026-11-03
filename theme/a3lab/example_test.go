package a3lab_test

import (
	"fmt"

	"gonativeui/theme/a3lab"
	"gonativeui/theme/rex"
)

// An A3 Lab app takes Rex and puts the web app's colours in it; its regions are drawn flat
// with rexui.PaneCardOpts.Flat.
func ExampleApply() {
	th := rex.Theme()
	a3lab.Apply(th)
	fmt.Printf("accent #%06X, sidebar #%06X, main column #%06X\n", a3lab.Rust, a3lab.Light.Sidebar, a3lab.Light.Bg)
	// Output: accent #AD5424, sidebar #EEEEEF, main column #F7F7F8
}
