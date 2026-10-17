// Command listbench is a file-list-like virtual list of a million rows on the
// app package: header, columns, alternating rows, hover, click and keyboard
// selection, a draggable scrollbar thumb, smooth scrolling. It is the
// benchmark of the 1M-row scroll budget (ARCHITECTURE §11: < 4 ms main-thread
// CPU per frame and 0 allocations) and builds on every OS.
//
//	listbench                      show the window; Esc or the close button quits
//	listbench -bench               scripted scroll/hover/click/jump through the rows,
//	                               120 warm-up + 600 measured frames, JSON report
//	listbench -bench -gate         also exit with status 3 if a budget fails
//	listbench -exit-first          print the startup timeline as JSON and exit once the
//	                               first frame is on glass
//	listbench -o out.png           render offscreen at 2× (no window), scrolled to a
//	                               fixed row, so the PNG is deterministic
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
)

const (
	winW, winH = 1000, 700 // points
	pngW, pngH = 800, 500  // points; the PNG is at 2×
	warmFrames = 120
	measFrames = 600

	defaultMaxCPUus = 4000 // ARCHITECTURE §11: main-thread CPU per frame
)

var config = app.Config{
	Title: "listbench", Width: winW, Height: winH,
	Clear: [4]float32{0x1E / 255.0, 0x1F / 255.0, 0x22 / 255.0, 1},
}

// report is the JSON of -bench.
type report struct {
	OS, Arch string
	Rows     int
	app.BenchReport
	RowsBuiltPerFrame float64 // mean over the measured frames
	RowsBuiltMax      int
	GateFailures      []string `json:",omitempty"`
}

func main() {
	out := flag.String("o", "", "render offscreen at 2× into this PNG and exit")
	row := flag.Int("row", -1, "first visible row (default: 0; with -o: 123456, so the PNG shows mid-list rows)")
	rows := flag.Int("rows", defRows, "number of rows")
	bench := flag.Bool("bench", false, "scripted run; print allocation, CPU, pacing and latency as JSON")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless there were 0 allocations, main-thread CPU < 4 ms/frame and frames were paced")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: display asleep)")
	maxCPU := flag.Float64("max-cpu-us", defaultMaxCPUus, "with -gate: main-thread CPU budget in µs per frame (0: do not gate on CPU, e.g. on noisy hosted CI runners)")
	timeout := flag.Duration("timeout", 60*time.Second, "with -bench: give up after this long")
	sites := flag.Bool("allocsites", false, "with -bench: profile every allocation and print the call stacks of those inside the measured frames")
	exitFirst := flag.Bool("exit-first", false, "print the startup timeline as JSON and exit once the first frame is on glass")
	flag.Parse()

	l := newListApp(max(*rows, 1))
	var err error
	switch {
	case *out != "":
		err = renderPNG(l, *out, *row)
	case *bench:
		err = runBench(l, *gate, *allowUnpaced, *sites, *maxCPU, *timeout)
	case *exitFirst:
		var res app.RunResult
		if res, err = app.RunWith(config, l, app.RunOptions{ExitFirst: true}); err == nil {
			printJSON(res.Startup)
		}
	default:
		l.jump = *row
		_, err = app.RunWith(config, l, app.RunOptions{})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "listbench:", err)
		os.Exit(1)
	}
}

// renderPNG draws the list offscreen at 2× scale, scrolled to a fixed row.
func renderPNG(l *listApp, path string, row int) error {
	if row < 0 {
		row = 123456
	}
	l.jump = min(row, l.rows-1)
	l.selected = l.jump + 3 // a selected row in view
	img, err := app.RenderOffscreen(config, pngW*2, pngH*2, 2, l, 3)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("rows %d.. -> %s\n", l.first, path)
	return nil
}

func runBench(l *listApp, gate, allowUnpaced, sites bool, maxCPU float64, timeout time.Duration) error {
	l.script = l.benchScript(warmFrames, warmFrames+measFrames+30)
	o := app.BenchOptions{Warm: warmFrames, Frames: measFrames, Timeout: timeout}
	var as allocSites
	if sites {
		o.WindowStart, o.WindowEnd = as.start, as.end
	}
	br, err := app.RunBench(config, l, o)
	if err != nil {
		return err
	}
	rep := report{OS: runtime.GOOS, Arch: runtime.GOARCH, Rows: l.rows, BenchReport: br, RowsBuiltMax: l.rowsMax}
	if l.buildCalls > 0 {
		rep.RowsBuiltPerFrame = float64(l.rowsBuilt) / float64(l.buildCalls)
	}
	if gate {
		rep.GateFailures = gateFailures(rep, allowUnpaced, maxCPU)
	}
	printJSON(rep)
	if len(rep.GateFailures) > 0 {
		fmt.Fprintln(os.Stderr, "listbench: gate failed:", strings.Join(rep.GateFailures, "; "))
		os.Exit(3)
	}
	return nil
}

func gateFailures(r report, allowUnpaced bool, maxCPU float64) []string {
	var f []string
	if r.Frames < measFrames {
		f = append(f, fmt.Sprintf("%d measured frames, want %d", r.Frames, measFrames))
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

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
