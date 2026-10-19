// Command notes is the example application of the guide (docs/GUIDE.md).
//
//	notes                  open the window, in the system's light or dark mode, which it follows
//	notes -o out.png       render the window offscreen at 840×1120 px and exit (-dark: in the dark mode)
//	notes -exit-first -v   print the startup timeline as JSON, and with -v exec → first frame, and exit
//	                       once the first frame is on glass
//	notes -bench           scripted session on the real window: add, select and remove notes, the
//	                       switch and the menu; prints the JSON of allocations, CPU per frame and pacing
//	notes -bench -gate     also exit with status 3 unless there were 0 allocations, main-thread CPU
//	                       within budget and frames were paced (-allow-unpaced: a sleeping display)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"os"
	"runtime"
	"strings"
	"time"

	"gonativeui/app"
	"gonativeui/examples/notes/notes"
	"gonativeui/ui"
)

func main() {
	out := flag.String("o", "", "render the window offscreen into this PNG and exit")
	dark := flag.Bool("dark", false, "with -o: render the dark mode")
	bench := flag.Bool("bench", false, "scripted session; print allocation, CPU and pacing as JSON")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless there were 0 allocations, main-thread CPU within budget and frames were paced")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: display asleep)")
	maxCPU := flag.Float64("max-cpu-us", 4000, "with -gate: main-thread CPU budget in µs per frame (0: do not gate on CPU)")
	timeout := flag.Duration("timeout", 120*time.Second, "with -bench: give up after this long")
	exitFirst := flag.Bool("exit-first", false, "print the startup timeline as JSON and exit once the first frame is on glass")
	verbose := flag.Bool("v", false, "print a state line every second while frames stop coming")
	flag.Parse()

	var err error
	switch {
	case *out != "":
		err = render(*out, *dark)
	case *bench:
		err = runBench(*gate, *allowUnpaced, *maxCPU, *timeout, *verbose)
	default:
		err = runWindow(*exitFirst, *verbose)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "notes:", err)
		os.Exit(1)
	}
}

func runWindow(exitFirst, verbose bool) error {
	n, err := notes.New()
	if err != nil {
		return err
	}
	res, err := app.RunWith(notes.Config(), n, app.RunOptions{ExitFirst: exitFirst, Verbose: verbose})
	if err == nil && exitFirst {
		printJSON(res.Startup)
		if verbose {
			fmt.Fprintf(os.Stderr, "notes: exec → first frame on glass %.1f ms (budget 150, D21)\n", res.Startup.ExecToOnGlassMs)
		}
	}
	return err
}

// render draws the window offscreen, once everything has settled, into a PNG.
func render(path string, dark bool) error {
	n, err := notes.New()
	if err != nil {
		return err
	}
	o, err := app.NewOffscreen(notes.Config(), 840, 1120, 2, n)
	if err != nil {
		return err
	}
	defer o.Close()
	o.UI.SetSystemAppearance(ui.Appearance{Dark: dark, Known: true}) // an offscreen window has no system to ask
	img, _ := o.Frame()
	for i := 0; i < 600 && (i < 3 || o.UI.Animating()); i++ {
		img, _ = o.Frame()
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// report is the JSON of -bench.
type report struct {
	OS, Arch string
	app.BenchReport
	SessionCycles int
	GateFailures  []string `json:",omitempty"`
}

func runBench(gate, allowUnpaced bool, maxCPU float64, timeout time.Duration, verbose bool) error {
	b, err := notes.NewBench()
	if err != nil {
		return err
	}
	br, err := app.RunBench(notes.Config(), b, app.BenchOptions{Warm: 600, PostGCWarm: 300, Windows: 12, Timeout: timeout, Verbose: verbose})
	if serr := b.Err(); serr != nil { // a step that failed ends the run: say which
		return serr
	}
	if err != nil {
		return err
	}
	rep := report{OS: runtime.GOOS, Arch: runtime.GOARCH, BenchReport: br, SessionCycles: b.Cycles()}
	if gate {
		rep.GateFailures = gateFailures(rep, allowUnpaced, maxCPU)
	}
	printJSON(rep)
	if len(rep.GateFailures) > 0 {
		fmt.Fprintln(os.Stderr, "notes: gate failed:", strings.Join(rep.GateFailures, "; "))
		os.Exit(3)
	}
	return nil
}

func gateFailures(r report, allowUnpaced bool, maxCPU float64) []string {
	var f []string
	if r.Frames < 300 {
		f = append(f, fmt.Sprintf("%d measured frames in the session, want at least 300", r.Frames))
	}
	if r.Mallocs != 0 || r.TotalAlloc != 0 {
		f = append(f, fmt.Sprintf("every one of %d whole sessions allocated (fewest: %d mallocs / %d bytes)", len(r.Windows), r.Mallocs, r.TotalAlloc))
	}
	if msg := r.OwnAllocFailure(); msg != "" {
		f = append(f, msg)
	}
	if maxCPU > 0 && r.MainThreadCPUus > maxCPU {
		f = append(f, fmt.Sprintf("main-thread CPU %.0f µs/frame, budget %.0f", r.MainThreadCPUus, maxCPU))
	}
	if !r.Paced && !allowUnpaced {
		f = append(f, fmt.Sprintf("%d frames were not paced (no frame events); -allow-unpaced accepts a sleeping display", r.UnpacedFrames))
	}
	return f
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
