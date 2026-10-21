//go:build gnui_release

// Command notes is the example application of the guide (docs/GUIDE.md).
package main

import (
	"fmt"
	"os"

	"gonativeui/app"
	"gonativeui/examples/notes/notes"
)

func main() {
	n, err := notes.New()
	if err == nil {
		err = app.Run(notes.Config(), n)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "notes:", err)
		os.Exit(1)
	}
}
