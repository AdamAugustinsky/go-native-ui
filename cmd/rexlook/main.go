// Command rexlook is the interactive recreation of the Rex terminal
// multiplexer's look (docs/design/rex-benchmark.md) on the app package: a tab
// capsule in a custom titlebar and panes of terminal-like text that can be
// focused, split, maximised, closed and resized. It builds on every OS; the
// scene lives in package rex.
//
//	rexlook                    show the window; Esc or the close button quits
//	rexlook -exit-first        print the startup timeline as JSON and exit once the first frame is on glass
//	rexlook -bench             scripted pointer run (hover across the tabs, focus switching, both dividers
//	                           dragged back and forth): 1000 warm-up + 900 measured frames; print allocation,
//	                           CPU split, pacing and latency as JSON
//	rexlook -bench -gate       also exit with status 3 unless 0 allocations, main-thread CPU ≤ 1000 µs/frame
//	                           and frames were paced (-allow-unpaced accepts a sleeping display)
//	rexlook -live-resize-test  drag the window size through the frame handler (macOS) and print
//	                           frame counts and allocations as JSON
//	rexlook -probe-titlebar    macOS: print which view AppKit's hit test finds in the titlebar band and
//	                           how the drag map decides there, and exit
//	rexlook -o out.png         render the initial state offscreen at 2114×1278 px (the reference
//	                           size) with our own traffic lights and the window's corners, and write a
//	                           PNG plus out.png.metrics.json for tools/rexcompare
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"time"

	"gonativeui/app"
	"gonativeui/cmd/rexlook/rex"
	"gonativeui/ui"
)

const (
	refW, refH = 2114, 1278 // the reference screenshot, pixels

	// The pointer script repeats every 900 frames; the warm-up covers one
	// whole cycle so that everything it does has happened once, and the
	// measured frames are another whole cycle.
	warmFrames = 1000
	measFrames = 900

	defaultMaxCPUus = 1000 // main-thread CPU per frame (docs/PHASE2-PLAN.md: the rexlook frame)
)

func main() {
	out := flag.String("o", "", "render offscreen at 2114×1278 px into this PNG and exit")
	exitFirst := flag.Bool("exit-first", false, "print the startup timeline as JSON and exit once the first frame is on glass")
	bench := flag.Bool("bench", false, "scripted run; print allocation, CPU, pacing and latency as JSON")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless there were 0 allocations, main-thread CPU within budget and frames were paced")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: display asleep)")
	maxCPU := flag.Float64("max-cpu-us", defaultMaxCPUus, "with -gate: main-thread CPU budget in µs per frame (0: do not gate on CPU)")
	timeout := flag.Duration("timeout", 120*time.Second, "with -bench: give up after this long")
	resizeTest := flag.Bool("live-resize-test", false, "macOS: drag the window size through the frame handler and print the result as JSON")
	probe := flag.Bool("probe-titlebar", false, "macOS: print the titlebar hit-test and drag-map probe as JSON and exit")
	verbose := flag.Bool("v", false, "print the startup timeline to stderr")
	flag.Parse()

	var err error
	switch {
	case *out != "":
		err = renderPNG(*out)
	case *bench:
		err = runBench(*gate, *allowUnpaced, *maxCPU, *timeout)
	case *resizeTest:
		err = runResizeTest()
	case *probe:
		err = runProbe()
	default:
		err = runWindow(*exitFirst, *verbose)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rexlook:", err)
		os.Exit(1)
	}
}

// builder is the scene plus Esc to quit, for the window modes.
type builder struct {
	*rex.Rex
	a *app.App
}

func newBuilder() *builder { return &builder{Rex: rex.NewApp()} }

func (b *builder) Init(a *app.App) error { b.a = a; return b.Rex.Init(a) }

func (b *builder) Build(c *ui.Context) {
	in := c.Input()
	for i := 0; i < in.NKeys; i++ {
		if k := in.Keys[i]; k.Down && k.Code == ui.KeyEscape {
			b.a.Quit()
		}
	}
	b.Rex.Build(c)
}

func runWindow(exitFirst, verbose bool) error {
	res, err := app.RunWith(rex.Config(), newBuilder(), app.RunOptions{ExitFirst: exitFirst})
	if err != nil {
		return err
	}
	if exitFirst {
		printJSON(res.Startup)
	} else if verbose {
		b, _ := json.MarshalIndent(res.Startup, "", "  ")
		fmt.Fprintln(os.Stderr, string(b))
	}
	return nil
}

// renderPNG draws the initial state offscreen at the reference size and
// writes it, with the layout metrics the compare tool checks.
func renderPNG(path string) error {
	quads, calls, err := rex.RenderShot(path, refW, refH, 2, true)
	if err != nil {
		return err
	}
	fmt.Printf("%d quads, %d draw calls -> %s\n", quads, calls, path)
	return nil
}

// report is the JSON of -bench.
type report struct {
	OS, Arch string
	app.BenchReport
	MonoFace      string
	MonoSizePt    float64
	CellAdvancePt float64
	GateFailures  []string `json:",omitempty"`
}

func runBench(gate, allowUnpaced bool, maxCPU float64, timeout time.Duration) error {
	x := rex.NewApp()
	br, err := app.RunBench(rex.Config(), rex.NewBench(x), app.BenchOptions{Warm: warmFrames, Frames: measFrames, Timeout: timeout})
	if err != nil {
		return err
	}
	rep := report{OS: runtime.GOOS, Arch: runtime.GOARCH, BenchReport: br,
		MonoFace: x.MonoFace(), MonoSizePt: x.MonoSizePt(), CellAdvancePt: x.CellAdvancePt()}
	if gate {
		rep.GateFailures = gateFailures(rep, allowUnpaced, maxCPU)
	}
	printJSON(rep)
	if len(rep.GateFailures) > 0 {
		fmt.Fprintln(os.Stderr, "rexlook: gate failed:", strings.Join(rep.GateFailures, "; "))
		os.Exit(3)
	}
	return nil
}

func gateFailures(r report, allowUnpaced bool, maxCPU float64) []string {
	var f []string
	if r.Frames < 600 {
		f = append(f, fmt.Sprintf("%d measured frames, want at least 600", r.Frames))
	}
	if r.Mallocs != 0 || r.TotalAlloc != 0 {
		f = append(f, fmt.Sprintf("%d mallocs / %d bytes allocated over the measured frames", r.Mallocs, r.TotalAlloc))
	}
	if maxCPU > 0 && r.MainThreadCPUus > maxCPU {
		f = append(f, fmt.Sprintf("main-thread CPU %.0f µs/frame, budget %.0f", r.MainThreadCPUus, maxCPU))
	}
	if !r.Paced && !allowUnpaced {
		f = append(f, fmt.Sprintf("%d frames were not paced (no frame events); -allow-unpaced accepts a sleeping display", r.UnpacedFrames))
	}
	return f
}

// runResizeTest drags the window through 150 sizes, like a user dragging an edge.
func runResizeTest() error {
	cfg := rex.Config()
	var sizes [][2]float64
	for i := 0; i < 150; i++ {
		f := float64(i) / 149
		sizes = append(sizes, [2]float64{float64(cfg.Width) - 250 + 450*math.Abs(math.Sin(f*math.Pi*2)), float64(cfg.Height) - 150 + 250*f})
	}
	rep, err := app.RunLiveResizeTest(cfg, newBuilder(), sizes)
	if err != nil {
		return err
	}
	printJSON(rep)
	return nil
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
