// Throwaway binary-size lower bound: no diagnostic CLI or JSON output.
package main

import (
	"gonativeui/app"
	"gonativeui/ui"
	"os"
)

type empty struct{ remaining int }

func (e *empty) Build(c *ui.Context) {
	if e.remaining > 0 {
		e.remaining--
		c.RequestFrame()
	}
}
func main() {
	e := new(empty)
	if os.Getenv("PERF_ANIMATE") == "1" {
		e.remaining = 600
	}
	if err := app.Run(app.Config{Title: "Perf tiny", Width: 900, Height: 600, Clear: [4]float32{1, 1, 1, 1}}, e); err != nil {
		panic(err)
	}
}
