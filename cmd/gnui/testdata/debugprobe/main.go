// Command debugprobe is a program that links package app and never runs it:
// the test builds it as gnui package and as gnui run do and looks for the
// debug overlay's chord, menu and inspector in the binaries.
package main

import (
	"os"

	"gonativeui/app"
	"gonativeui/ui"
)

type page struct{}

func (page) Build(c *ui.Context) { c.Label("probe") }

func main() {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		app.Run(app.Config{Title: "probe", Width: 200, Height: 100}, page{})
	}
}
