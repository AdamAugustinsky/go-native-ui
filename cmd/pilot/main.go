// Command pilot is the dual-pane file browser on app + ui + fs, built from the
// rexui and the Rex theme in Finder's layout (unified toolbar, glass sidebar,
// path bar); the scene lives in package pilot. It builds on every OS. The window
// follows the system's light or dark mode and accent colour.
//
//	pilot [left [right]]        show the window; both panes start in the home directory by default
//	pilot -exit-first -v        print the startup timeline as JSON (and exec → listings ready on
//	                            stderr with -v) and exit once the first frame is on glass
//	pilot -bench                scripted session on synthetic directories (100k entries by default, -rows N up
//	                            to 1M): scroll, hover, select, filter, switch panes, sort by each column, drag
//	                            the divider, relists, and copy, cut, paste and undo in the filter field through the
//	                            system clipboard (it replaces the clipboard's text): 1000 warm-up frames, a collection, 400 more, then whole sessions measured
//	                            one after another until one allocates nothing (all listed in the JSON)
//	pilot -bench -bench-dir DIR the same session on a real directory through the real file system service
//	pilot -bench -gate          also exit with status 3 unless 0 allocations, main-thread CPU within budget
//	                            and frames were paced
//	pilot -memory 3s -rows N    idle synthetic application view, without benchmark prewarming; report
//	                            post-GC Go heap after 3s, keeping the window open for OS footprint tools
//	pilot -memory 3s -bench     the same idle view with the benchmark fixture's prewarm, labelled separately
//	pilot -o out.png            render the offscreen scene (fixed names, sizes and UTC times; a selection
//	                            and a filter) at 2400×1520 px (-w -h -scale) with traffic lights and corners
//	pilot -o out.png LEFT [RIGHT]
//	                            render the first frame for real directories, without traffic lights (the
//	                            expected complete content for tools/bouncerec)
//	pilot -o out.png -mode dark the picture in the dark mode (-mode light, the default with -o; -mode system
//	                            renders what this machine says); -inactive renders the window as not key,
//	                            -scrolled with the left list scrolled under the toolbar, -legacy with legacy
//	                            scroll bars
//	pilot -mode dark            the window in a forced mode (the default, system, follows the system's)
//	pilot -size 1120x640        the window at another size (the finder scene's, for comparing with its capture)
//	pilot -mono                 set the file lists in Menlo, in 7.5 pt cells, instead of the system font
//	pilot -scrolled             start with the left list scrolled under the toolbar
//
// Startup is content-first: both start directories are listed and sorted on
// goroutines that run before the window system is touched, and the first frame
// shows them when they are done within 150 ms.
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
	"gonativeui/cmd/pilot/pilot"
	"gonativeui/fs"
	"gonativeui/gpu"
	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"
)

const (
	// The session is long and mostly waits for the worker; the warm-up covers
	// the whole of it once so that every buffer has reached its size.
	warmFrames = 1000

	// After the collection that opens the measurement: frames that refill the
	// runtime's pools through the session's paths (a collection empties its
	// central pools), and up to this many whole sessions, one after another,
	// stopping at the first that allocates nothing.
	postGCWarm = 400
	maxWindows = 12

	defaultMaxCPUus = 4000 // ARCHITECTURE §11: main-thread CPU per frame at 1M entries
	defaultRows     = 100_000
)

func main() {
	t0 := platform.Now() // before anything else
	age := sysinfo.ProcessAgeNs()
	gpu.Prewarm() // the renderer's shaders build while the program starts

	out := flag.String("o", "", "render the offscreen scene into this PNG and exit")
	w := flag.Int("w", 2400, "with -o: width in pixels")
	h := flag.Int("h", 1520, "with -o: height in pixels")
	scale := flag.Float64("scale", 2, "with -o: pixels per point")
	exitFirst := flag.Bool("exit-first", false, "print the startup timeline as JSON and exit once the first frame is on glass")
	bench := flag.Bool("bench", false, "scripted session; print allocation, CPU, pacing and latency as JSON")
	memory := flag.Duration("memory", 0, "idle synthetic view: report post-GC Go heap after this delay; add -bench for the prewarmed fixture (no script)")
	rows := flag.Int("rows", defaultRows, "with -bench or -memory: entries of the synthetic directory in the left pane (up to 1M and beyond)")
	benchDir := flag.String("bench-dir", "", "with -bench: run on this real directory (both panes, real service, watch on) instead of synthetic ones")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless there were 0 allocations, main-thread CPU within budget and frames were paced")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: display asleep)")
	maxCPU := flag.Float64("max-cpu-us", defaultMaxCPUus, "with -gate: main-thread CPU budget in µs per frame (0: do not gate on CPU)")
	timeout := flag.Duration("timeout", 180*time.Second, "with -bench: give up after this long")
	sites := flag.Bool("allocsites", false, "with -bench: profile every allocation and print the call stacks of those inside the measured frames")
	resizeTest := flag.Bool("live-resize-test", false, "macOS: drag the window size through the frame handler (as a user resizing does) and print the result as JSON")
	mode := flag.String("mode", "", "light, dark or system (what this machine says); the window defaults to system, -o to light")
	inactive := flag.Bool("inactive", false, "with -o: render the window as not key")
	scrolled := flag.Bool("scrolled", false, "scroll the left list under the toolbar (with -o the scene's lists are longer)")
	legacy := flag.Bool("legacy", false, "with -o: legacy scroll bars")
	size := flag.String("size", "", "the window's size in points, WIDTHxHEIGHT (the default is 1200x760; the finder scene is 1120x640)")
	mono := flag.Bool("mono", false, "set the file lists in Menlo (the theme's monospace font off macOS), in 7.5 pt cells")
	verbose := flag.Bool("v", false, "print the startup timeline and listing times to stderr; in a window or bench also a state line every second while frames stop coming")
	budgetScale := flag.Float64("budget-scale", 1, "with -gate: multiply the time budgets by this (3: the coarse, catastrophic check of a shared CI runner)")
	flag.Parse()
	if *memory < 0 || *memory > 0 && (*gate || *out != "" || *benchDir != "" || *exitFirst || *resizeTest || flag.NArg() != 0) {
		fmt.Fprintln(os.Stderr, "pilot: -memory requires a positive delay and cannot combine with -gate, -o, -bench-dir, -exit-first, -live-resize-test or directory arguments")
		os.Exit(2)
	}
	*maxCPU *= *budgetScale

	m := *mode
	if m == "" {
		m = "light"
		if *out == "" {
			m = "system"
		}
	}
	look, err := parseLook(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pilot:", err)
		os.Exit(2)
	}
	look.Inactive, look.Scrolled, look.Legacy, look.Mono = *inactive, *scrolled, *legacy, *mono
	var winW, winH int
	if *size != "" {
		if _, err := fmt.Sscanf(*size, "%dx%d", &winW, &winH); err != nil || winW < pilot.MinWidthPt || winH < pilot.MinHeightPt {
			fmt.Fprintf(os.Stderr, "pilot: -size %q: want WIDTHxHEIGHT of at least %dx%d\n", *size, pilot.MinWidthPt, pilot.MinHeightPt)
			os.Exit(2)
		}
	}
	switch {
	case *memory > 0:
		err = runMemory(*rows, *bench, *memory)
	case *out != "" && flag.NArg() > 0:
		err = renderDirsPNG(*out, *w, *h, float32(*scale), flag.Args(), look)
	case *out != "":
		err = renderPNG(*out, *w, *h, float32(*scale), look)
	case *resizeTest:
		err = runResizeTest(flag.Args(), look)
	case *bench:
		err = runBench(*rows, *benchDir, *gate, *allowUnpaced, *sites, *maxCPU, *timeout, *verbose)
	default:
		err = runWindow(flag.Args(), *exitFirst, *verbose, t0, age, look, winW, winH)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pilot:", err)
		os.Exit(1)
	}
}

// parseLook maps the -mode flag to how a picture is made: an offscreen window
// has no system, so "system" asks this machine.
func parseLook(mode string) (pilot.Look, error) {
	switch mode {
	case "light":
		return pilot.Look{Preference: theme.ForceLight}, nil
	case "dark":
		return pilot.Look{Preference: theme.ForceDark}, nil
	case "system":
		a := platform.SystemAppearance()
		return pilot.Look{Preference: theme.System, System: ui.Appearance{Dark: a.Dark, Accent: a.Accent, Known: a.Known}}, nil
	}
	return pilot.Look{}, fmt.Errorf("-mode %q: want light, dark or system", mode)
}

func renderPNG(path string, w, h int, scale float32, look pilot.Look) error {
	quads, calls, err := pilot.RenderShotLook(path, w, h, scale, true, look)
	if err != nil {
		return err
	}
	fmt.Printf("%d quads, %d draw calls -> %s\n", quads, calls, path)
	return nil
}

// runResizeTest opens the normal window (real service, the given or home
// directories) and drags its size through AppKit's live-resize path
// (app.RunLiveResizeTest), then prints how many steps drew and what they cost.
func runResizeTest(args []string, look pilot.Look) error {
	home, _ := os.UserHomeDir()
	o := pilot.Options{Home: home, Src: fs.NewService(platform.Wake), Preference: look.Preference, Mono: look.Mono}
	for i := range o.Initial {
		var a string
		if i < len(args) {
			a = args[i]
		}
		o.Initial[i] = listAsync(startDir(a, home))
	}
	pl := pilot.New(o)
	defer pl.Close()
	cfg := pilot.Config()
	var sizes [][2]float64
	for i := 0; i < 150; i++ {
		f := float64(i) / 149
		sizes = append(sizes, [2]float64{float64(cfg.Width) - 300 + 400*math.Abs(math.Sin(f*math.Pi*2)), float64(cfg.Height) - 200 + 250*f})
	}
	rep, err := app.RunLiveResizeTest(cfg, pl, sizes)
	if err != nil {
		return err
	}
	printJSON(rep)
	if rep.HandlerMsMax > app.LiveResizeMaxHandlerMs || rep.DrawFails == rep.HandlerCalls {
		fmt.Fprintf(os.Stderr, "%s: live-resize test failed: slowest step %.0f ms (limit %d), %d of %d handler calls drew nothing\n",
			"pilot", rep.HandlerMsMax, app.LiveResizeMaxHandlerMs, rep.DrawFails, rep.HandlerCalls)
		os.Exit(3)
	}
	if rep.Pacing == app.PacingFailed {
		fmt.Fprintf(os.Stderr, "pilot: live-resize test failed: %d frame events before the drag, %d paced frames after it (%d wanted)\n",
			rep.FrameEventsBefore, rep.PacedFramesAfter, rep.PacedWanted)
		os.Exit(3)
	}
	return nil
}

// renderDirsPNG renders the first frame for real directories (pilot -o out.png
// LEFT [RIGHT]), the expected content for BounceRec.
func renderDirsPNG(path string, w, h int, scale float32, args []string, look pilot.Look) error {
	home, _ := os.UserHomeDir()
	var dirs [2]string
	for i := range dirs {
		var a string
		if i < len(args) {
			a = args[i]
		}
		dirs[i] = startDir(a, home)
	}
	quads, calls, err := pilot.RenderDirsShotLook(path, w, h, scale, home, dirs, look)
	if err != nil {
		return err
	}
	fmt.Printf("%d quads, %d draw calls -> %s\n", quads, calls, path)
	return nil
}

// startDir resolves a command line directory with the rules of a path field:
// "~" expands, relative paths are taken against the working directory, a
// Windows drive or UNC path is kept (pilot.ResolvePath).
func startDir(arg, home string) string {
	if arg == "" {
		return home
	}
	cwd, err := os.Getwd()
	if err != nil {
		cwd = home
	}
	return pilot.ResolvePath(arg, cwd, home)
}

// listAsync lists and sorts dir on a goroutine, which is the first thing the
// process does, before the window system exists; the result goes to the
// returned channel and wakes the loop (platform.Wake is safe before the window exists: Open picks the wake up).
func listAsync(dir string) <-chan pilot.Ready {
	ch := make(chan pilot.Ready, 1)
	go func() {
		l := new(fs.Listing)
		if err := fs.ListDir(dir, l); err == nil {
			fs.Sort(l, fs.SortName, false)
		}
		ch <- pilot.Ready{Dir: dir, L: l, At: platform.Now()}
		platform.Wake()
	}()
	return ch
}

// startup is the JSON of -exit-first: the window's startup timeline plus when
// the listings were ready.
type startup struct {
	app.StartupReport
	ListingsReadyMs       [2]float64 // exec → listing of each pane finished (-1: not landed)
	ListingsInFirstFrame  [2]bool    // the listing was there when the first frame was built
	ListingsFirstFrameCap string
}

func runWindow(args []string, exitFirst, verbose bool, t0, age int64, look pilot.Look, winW, winH int) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		cwd, _ := os.Getwd()
		home = pilot.ResolvePath("/", cwd, "") // the root of the working directory's volume
	}
	var dirs [2]string
	for i := range dirs {
		var a string
		if i < len(args) {
			a = args[i]
		}
		dirs[i] = startDir(a, home)
	}
	o := pilot.Options{Home: home, Src: fs.NewService(platform.Wake), Preference: look.Preference, Mono: look.Mono, ScrollLeft: look.Scrolled}
	for i, d := range dirs {
		o.Initial[i] = listAsync(d)
	}
	pl := pilot.New(o)
	defer pl.Close()

	cfg := pilot.Config()
	if winW > 0 {
		cfg.Width, cfg.Height = float32(winW), float32(winH)
	}
	res, err := app.RunWith(cfg, pl, app.RunOptions{ExitFirst: exitFirst, Verbose: verbose})
	if err != nil {
		return err
	}
	st := startup{StartupReport: res.Startup, ListingsFirstFrameCap: "150ms"}
	for i := range st.ListingsReadyMs {
		st.ListingsReadyMs[i] = -1
		if at := pl.ListedAt(i); at != 0 {
			st.ListingsReadyMs[i] = float64(at-t0+age) / 1e6
		}
		st.ListingsInFirstFrame[i] = pl.ListedAtInit(i)
	}
	if exitFirst {
		printJSON(st)
	}
	if verbose {
		fmt.Fprintf(os.Stderr, "pilot: exec → first frame on glass %.1f ms, exec → listings ready %.1f / %.1f ms, in the first frame: %v\n",
			st.ExecToOnGlassMs, st.ListingsReadyMs[0], st.ListingsReadyMs[1], st.ListingsInFirstFrame)
	}
	return nil
}

// report is the JSON of -bench.
type report struct {
	OS, Arch string
	Rows     int    // entries of the left directory
	Dir      string `json:",omitempty"` // -bench-dir
	app.BenchReport
	RowsBuiltPerFrame float64 // mean over the measured frames, both panes
	RowsBuiltMax      int
	SessionCycles     int      // times the session ran through (warm-up and measured frames)
	GateFailures      []string `json:",omitempty"`
}

func runBench(rows int, dir string, gate, allowUnpaced, sites bool, maxCPU float64, timeout time.Duration, verbose bool) error {
	var b *pilot.Bench
	rep := report{OS: runtime.GOOS, Arch: runtime.GOARCH, Rows: max(rows, 100)}
	if dir != "" {
		var err error
		if b, err = pilot.NewDirBench(dir, timeout); err != nil {
			return err
		}
		rep.Rows, rep.Dir = 0, dir
	} else {
		b = pilot.NewSynthBench(rep.Rows)
	}
	defer b.Close()
	b.UsePlatformClipboard()
	b.StartCounting()
	opt := app.BenchOptions{Warm: warmFrames, PostGCWarm: postGCWarm, Windows: maxWindows, Timeout: timeout, Verbose: verbose}
	opt.PrintAllocSites = sites
	br, err := app.RunBench(pilot.Config(), b, opt)
	if err != nil {
		return err
	}
	if err := b.Err(); err != nil {
		return err
	}
	rep.BenchReport = br
	rep.RowsBuiltPerFrame, rep.RowsBuiltMax = b.RowsBuilt()
	rep.SessionCycles = b.Cycles()
	if dir != "" {
		rep.Rows = b.LeftEntries()
	}
	if gate {
		rep.GateFailures = gateFailures(rep, allowUnpaced, maxCPU)
	}
	printJSON(rep)
	if len(rep.GateFailures) > 0 {
		fmt.Fprintln(os.Stderr, "pilot: gate failed:", strings.Join(rep.GateFailures, "; "))
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
