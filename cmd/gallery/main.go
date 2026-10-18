// Command gallery shows every widget of package widget in every state, in the
// Rex theme, light and dark, next to a table of a million rows.
//
//	gallery                    show the window (-mode light|dark|system; ⇧⌘D toggles with a crossfade)
//	gallery -o out.png         render the scene offscreen at 2560×1600 px and exit
//	gallery -o out.png -mode dark
//	gallery -o out.png -o-dark dark.png
//	                           render the light and the dark picture
//	gallery -o out.png -scene dialog
//	                           render a state: main, menu, select, context, dialog or tooltip
//	gallery -bench             scripted session on the real window: hover and press, typing with copy, cut
//	                           and undo, choices, a Select, scrolling and selecting in the table, a sort,
//	                           the context menu, the dialog, the light/dark crossfade; prints the JSON of
//	                           allocations, CPU per frame and pacing
//	gallery -bench -gate       also exit with status 3 unless there were 0 allocations, main-thread CPU
//	                           within budget and frames were paced (-allow-unpaced: a sleeping display)
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"gonativeui/app"
	"gonativeui/cmd/gallery/gallery"
	"gonativeui/theme"
)

const (
	warmFrames = 1000
	postGCWarm = 400
	maxWindows = 12

	defaultMaxCPUus = 4000 // ARCHITECTURE §11: main-thread CPU per frame at 1M rows
)

func main() {
	out := flag.String("o", "", "render the offscreen scene into this PNG and exit")
	outDark := flag.String("o-dark", "", "with -o: also render the dark mode into this PNG")
	w := flag.Int("w", 2560, "with -o: width in pixels")
	h := flag.Int("h", 1600, "with -o: height in pixels")
	scale := flag.Float64("scale", 2, "with -o: pixels per point")
	scene := flag.String("scene", "main", "with -o: the state to render ("+strings.Join(gallery.SceneNames(), ", ")+")")
	mode := flag.String("mode", "light", "light, dark or system (system is light for now)")
	rows := flag.Int("rows", gallery.DefaultRows, "rows of the table")
	bench := flag.Bool("bench", false, "scripted session; print allocation, CPU, pacing and latency as JSON")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless there were 0 allocations, main-thread CPU within budget and frames were paced")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: display asleep)")
	maxCPU := flag.Float64("max-cpu-us", defaultMaxCPUus, "with -gate: main-thread CPU budget in µs per frame (0: do not gate on CPU)")
	timeout := flag.Duration("timeout", 180*time.Second, "with -bench: give up after this long")
	sites := flag.Bool("allocsites", false, "with -bench: profile every allocation and print the call stacks of those inside the measured frames")
	verbose := flag.Bool("v", false, "in a window or bench: print a state line every second while frames stop coming")
	flag.Parse()

	m, err := parseMode(*mode)
	if err == nil {
		switch {
		case *out != "":
			err = render(*out, *outDark, *w, *h, float32(*scale), *scene, m, *rows)
		case *bench:
			err = runBench(*rows, m, *gate, *allowUnpaced, *sites, *maxCPU, *timeout, *verbose)
		default:
			err = runWindow(*rows, m)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gallery:", err)
		os.Exit(1)
	}
}

// parseMode maps the -mode flag to a theme mode; system follows the light mode
// until the platform reports the appearance.
func parseMode(s string) (theme.Mode, error) {
	switch s {
	case "light", "system":
		return theme.Light, nil
	case "dark":
		return theme.Dark, nil
	}
	return 0, fmt.Errorf("-mode %q: want light, dark or system", s)
}

func render(path, dark string, w, h int, scale float32, scene string, m theme.Mode, rows int) error {
	o := gallery.ShotOptions{Width: w, Height: h, Scale: scale, Scene: scene, Mode: m, Rows: rows}
	quads, err := gallery.Shot(path, o)
	if err != nil {
		return err
	}
	fmt.Printf("%d quads -> %s\n", quads, path)
	if dark != "" {
		o.Mode = theme.Dark
		if quads, err = gallery.Shot(dark, o); err != nil {
			return err
		}
		fmt.Printf("%d quads -> %s\n", quads, dark)
	}
	return nil
}

func runWindow(rows int, m theme.Mode) error {
	g, err := gallery.New(gallery.Options{Rows: rows, Mode: m})
	if err != nil {
		return err
	}
	_, err = app.RunWith(gallery.Config(), g, app.RunOptions{})
	return err
}

// report is the JSON of -bench.
type report struct {
	OS, Arch string
	Rows     int
	app.BenchReport
	WindowPx      [2]int // the window's size at the end
	WindowScale   float32
	SessionCycles int      // times the session ran through (warm-up and measured frames)
	GateFailures  []string `json:",omitempty"`
}

func runBench(rows int, m theme.Mode, gate, allowUnpaced, sites bool, maxCPU float64, timeout time.Duration, verbose bool) error {
	b, err := gallery.NewBench(gallery.Options{Rows: rows, Mode: m})
	if err != nil {
		return err
	}
	rep := report{OS: runtime.GOOS, Arch: runtime.GOARCH, Rows: rows}
	opt := app.BenchOptions{Warm: warmFrames, PostGCWarm: postGCWarm, Windows: maxWindows, Timeout: timeout, Verbose: verbose}
	var as allocSites
	if sites {
		opt.WindowStart, opt.WindowEnd = as.start, as.end
	}
	br, err := app.RunBench(gallery.Config(), b, opt)
	if serr := b.Err(); serr != nil { // a step that failed ends the run: say which
		w, h, sc := b.WindowPx()
		return fmt.Errorf("%w (window %d×%d px at %.1f×)", serr, w, h, sc)
	}
	if err != nil {
		return err
	}
	rep.BenchReport = br
	rep.SessionCycles = b.Cycles()
	w, h, sc := b.WindowPx()
	rep.WindowPx, rep.WindowScale = [2]int{w, h}, sc
	if gate {
		rep.GateFailures = gateFailures(rep, allowUnpaced, maxCPU)
	}
	printJSON(rep)
	if len(rep.GateFailures) > 0 {
		fmt.Fprintln(os.Stderr, "gallery: gate failed:", strings.Join(rep.GateFailures, "; "))
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
