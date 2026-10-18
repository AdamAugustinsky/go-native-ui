// Command pilot is the dual-pane file browser on app + ui + fs, in the Rex look
// (cmd/internal/rexkit); the scene lives in package pilot. It builds on every OS.
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
//	pilot -o out.png            render the offscreen scene (fixed names, sizes and UTC times; a selection
//	                            and a filter) at 2400×1520 px (-w -h -scale) with traffic lights and corners
//	pilot -o out.png LEFT [RIGHT]
//	                            render the first frame for real directories, without traffic lights (the
//	                            expected complete content for tools/bouncerec)
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
	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
)

const (
	// The session is long and mostly waits for the worker; the warm-up covers
	// the whole of it once so that every buffer has reached its size.
	warmFrames = 1000

	// After the collection that opens the measurement: frames that refill the
	// runtime's pools through the session's paths (a collection empties the
	// central sudog cache, so the file system workers' first waits afterwards
	// may allocate), and up to this many whole sessions, one after another,
	// stopping at the first that allocates nothing.
	postGCWarm = 400
	maxWindows = 12

	defaultMaxCPUus = 4000 // ARCHITECTURE §11: main-thread CPU per frame at 1M entries
	defaultRows     = 100_000
)

func main() {
	t0 := platform.Now() // before anything else
	age := sysinfo.ProcessAgeNs()

	out := flag.String("o", "", "render the offscreen scene into this PNG and exit")
	w := flag.Int("w", 2400, "with -o: width in pixels")
	h := flag.Int("h", 1520, "with -o: height in pixels")
	scale := flag.Float64("scale", 2, "with -o: pixels per point")
	exitFirst := flag.Bool("exit-first", false, "print the startup timeline as JSON and exit once the first frame is on glass")
	bench := flag.Bool("bench", false, "scripted session; print allocation, CPU, pacing and latency as JSON")
	rows := flag.Int("rows", defaultRows, "with -bench: entries of the synthetic directory in the left pane (up to 1M and beyond)")
	benchDir := flag.String("bench-dir", "", "with -bench: run on this real directory (both panes, real service, watch on) instead of synthetic ones")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless there were 0 allocations, main-thread CPU within budget and frames were paced")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: display asleep)")
	maxCPU := flag.Float64("max-cpu-us", defaultMaxCPUus, "with -gate: main-thread CPU budget in µs per frame (0: do not gate on CPU)")
	timeout := flag.Duration("timeout", 180*time.Second, "with -bench: give up after this long")
	sites := flag.Bool("allocsites", false, "with -bench: profile every allocation and print the call stacks of those inside the measured frames")
	resizeTest := flag.Bool("live-resize-test", false, "macOS: drag the window size through the frame handler (as a user resizing does) and print the result as JSON")
	verbose := flag.Bool("v", false, "print the startup timeline and listing times to stderr; in a window or bench also a state line every second while frames stop coming")
	flag.Parse()

	var err error
	switch {
	case *out != "" && flag.NArg() > 0:
		err = renderDirsPNG(*out, *w, *h, float32(*scale), flag.Args())
	case *out != "":
		err = renderPNG(*out, *w, *h, float32(*scale))
	case *resizeTest:
		err = runResizeTest(flag.Args())
	case *bench:
		err = runBench(*rows, *benchDir, *gate, *allowUnpaced, *sites, *maxCPU, *timeout, *verbose)
	default:
		err = runWindow(flag.Args(), *exitFirst, *verbose, t0, age)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pilot:", err)
		os.Exit(1)
	}
}

func renderPNG(path string, w, h int, scale float32) error {
	quads, calls, err := pilot.RenderShot(path, w, h, scale, true)
	if err != nil {
		return err
	}
	fmt.Printf("%d quads, %d draw calls -> %s\n", quads, calls, path)
	return nil
}

// runResizeTest opens the normal window (real service, the given or home
// directories) and drags its size through AppKit's live-resize path
// (app.RunLiveResizeTest), then prints how many steps drew and what they cost.
func runResizeTest(args []string) error {
	home, _ := os.UserHomeDir()
	o := pilot.Options{Home: home, Src: fs.NewService(platform.Wake)}
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
func renderDirsPNG(path string, w, h int, scale float32, args []string) error {
	home, _ := os.UserHomeDir()
	var dirs [2]string
	for i := range dirs {
		var a string
		if i < len(args) {
			a = args[i]
		}
		dirs[i] = startDir(a, home)
	}
	quads, calls, err := pilot.RenderDirsShot(path, w, h, scale, home, dirs)
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

func runWindow(args []string, exitFirst, verbose bool, t0, age int64) error {
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
	o := pilot.Options{Home: home, Src: fs.NewService(platform.Wake)}
	for i, d := range dirs {
		o.Initial[i] = listAsync(d)
	}
	pl := pilot.New(o)
	defer pl.Close()

	res, err := app.RunWith(pilot.Config(), pl, app.RunOptions{ExitFirst: exitFirst, Verbose: verbose})
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
	var as allocSites
	if sites {
		opt.WindowStart, opt.WindowEnd = as.start, as.end
	}
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
