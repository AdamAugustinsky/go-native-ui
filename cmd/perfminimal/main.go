// Throwaway survey fixture: GUIDE.md section 2's app.RunWith, with an empty Builder.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"gonativeui/app"
	"gonativeui/ui"
)

type empty struct{}

func (empty) Build(*ui.Context) {}

func main() {
	exit := flag.Bool("exit-first", false, "exit after first presented frame")
	bench := flag.Bool("bench", false, "600 empty frames")
	flag.Parse()
	cfg := app.Config{Title: "Perf minimal", Width: 900, Height: 600, Clear: [4]float32{1, 1, 1, 1}}
	var result any
	var err error
	if *bench {
		result, err = app.RunBench(cfg, empty{}, app.BenchOptions{Warm: 120, Frames: 600})
	} else {
		var r app.RunResult
		r, err = app.RunWith(cfg, empty{}, app.RunOptions{ExitFirst: *exit})
		result = r.Startup
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(result)
}
