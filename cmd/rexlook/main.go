// Command rexlook is the interactive recreation of the Rex terminal
// multiplexer's look (docs/design/rex-benchmark.md) on the app package: a tab
// capsule in a custom titlebar and panes of terminal-like text that can be
// focused, split, maximised, closed and resized. It builds on every OS; the
// scene lives in package rex.
//
//	rexlook                    show the window, in the system's light or dark mode and accent colour, which it
//	                           follows while it runs (-mode system, the default; -mode light or dark forces
//	                           one; a change crossfades); Esc or the close button quits
//	rexlook -exit-first        print the startup timeline as JSON and exit once the first frame is on glass
//	                           (with -v, also platform.Open's stages on stderr)
//	rexlook -bench             scripted pointer run (hover across the tabs, focus switching, both dividers
//	                           dragged back and forth): 1000 warm-up + 900 measured frames; print allocation,
//	                           CPU split, pacing and latency as JSON
//	rexlook -bench -gate       also exit with status 3 unless a window of the script had 0 allocations
//	                           (up to 12 tries), main-thread CPU ≤ 1000 µs/frame and frames were paced
//	                           (-allow-unpaced accepts a sleeping display); -allocsites prints the
//	                           allocating call stacks
//	rexlook -live-resize-test  drag the window size through the frame handler (macOS) and print
//	                           frame counts and allocations as JSON
//	rexlook -idle 3s           show the window, let it settle for a second, then count the frames drawn while
//	                           nothing happens for 3 s (an idle window presents nothing) and print them as JSON
//	rexlook -pose press -pose-tab 1
//	                           macOS: once settled, hover (or press and hold) a tab through the window's own
//	                           event queue, for captures of those states
//	rexlook -probe-titlebar    macOS: print which view AppKit's hit test finds in the titlebar band and
//	                           how the drag map decides there, and exit
//	rexlook -o out.png         render the initial state offscreen at 2114×1278 px (the reference
//	                           size) with our own traffic lights and the window's corners, and write a
//	                           PNG plus out.png.metrics.json for tools/rexcompare; light unless -mode is
//	                           given (-mode system renders what this machine says)
//	rexlook -o out.png -o-dark dark.png
//	                           render the light and the dark picture
//	rexlook -o out.png -native render what the scene draws in the native shell (no field, no ⌘ and +)
//
// On macOS the window modes use the native shell (rex.NativeConfig,
// experimental): a Liquid Glass backdrop where the field was and native ⌘ and
// + buttons (borderless; -glass-buttons: Liquid Glass circles), with the
// scene's own tabs in the titlebar band; -drawn uses the drawn field and
// buttons instead. Other systems have no native shell and always draw them.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/cmd/rexlook/rex"
	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"
)

const (
	refW, refH = 2114, 1278 // the reference screenshot, pixels

	// The pointer script repeats every 900 frames; the warm-up covers one
	// whole cycle so that everything it does has happened once, and the
	// measured frames are another whole cycle.
	warmFrames = 1000
	measFrames = 900

	// The allocation gate (DECISIONS D27): up to maxWindows measured windows
	// of measFrames, after a collection and postGCWarm frames; it passes on the
	// first window with no allocation at all. Each window is one whole cycle
	// of the script, so an allocation of the session recurs in every one; a
	// one-off of the process (a new OS thread, the scavenger's timer heap, a
	// type-assertion cache the runtime fills on a random call) misses most.
	postGCWarm = 300
	maxWindows = 12

	defaultMaxCPUus = 1000 // main-thread CPU per frame (docs/PHASE2-PLAN.md: the rexlook frame)
)

func main() {
	gpu.Prewarm() // the renderer's shaders build while the program starts
	out := flag.String("o", "", "render offscreen at 2114×1278 px into this PNG and exit")
	outDark := flag.String("o-dark", "", "with -o: also render the dark mode into this PNG")
	mode := flag.String("mode", "system", "system (follow the system's light or dark mode and accent: the default), light or dark; with -o the default is light")
	exitFirst := flag.Bool("exit-first", false, "print the startup timeline as JSON and exit once the first frame is on glass")
	bench := flag.Bool("bench", false, "scripted run; print allocation, CPU, pacing and latency as JSON")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless there were 0 allocations, main-thread CPU within budget and frames were paced")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: display asleep)")
	maxCPU := flag.Float64("max-cpu-us", defaultMaxCPUus, "with -gate: main-thread CPU budget in µs per frame (0: do not gate on CPU)")
	timeout := flag.Duration("timeout", 120*time.Second, "with -bench: give up after this long")
	resizeTest := flag.Bool("live-resize-test", false, "macOS: drag the window size through the frame handler and print the result as JSON")
	probe := flag.Bool("probe-titlebar", false, "macOS: print the titlebar hit-test and drag-map probe as JSON and exit")
	sites := flag.Bool("allocsites", false, "with -bench: print the call stacks of the allocations attributed over the measured windows")
	testAlloc := flag.Int("test-alloc-every", 0, "with -bench: a test of the allocation gate: the bench allocates every this many frames, and the gate must fail")
	drawn := flag.Bool("drawn", false, "macOS: draw the field and the ⌘ and + buttons instead of the native shell (backdrop and native buttons)")
	glassButtons := flag.Bool("glass-buttons", false, "macOS: native ⌘ and + as Liquid Glass circles instead of borderless symbols")
	native := flag.Bool("native", false, "with -o: render the native shell's content (no field, no ⌘ and +)")
	pose := flag.String("pose", "", "macOS: hover or press: once settled, hover or press and hold tab -pose-tab through the window's own event queue; fullscreen: enter full screen, leave it 6 s later, quit 6 s after (for captures)")
	poseTab := flag.Int("pose-tab", 1, "with -pose: the tab, from 0")
	idle := flag.Duration("idle", 0, "show the window, wait a second, count the frames drawn over this long while nothing happens, print them as JSON and exit")
	verbose := flag.Bool("v", false, "print the startup timeline to stderr; with -bench or in a window, also a state line every second while frames stop coming")
	budgetScale := flag.Float64("budget-scale", 1, "with -gate: multiply the time budgets by this (3: the coarse, catastrophic check of a shared CI runner)")
	flag.Parse()
	*maxCPU *= *budgetScale

	if *drawn {
		config = rex.Config
	} else if *glassButtons && runtime.GOOS == "darwin" {
		config = func() app.Config { return rex.NativeConfig(true) }
	}
	pref, err := parseMode(*mode)
	explicit := false
	flag.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "mode" })
	if err == nil {
		switch {
		case *out != "":
			if !explicit {
				pref = theme.ForceLight // a picture is the same on every machine unless it is asked to follow this one
			}
			err = renderPNG(*out, *outDark, pref, *native)
		case *bench:
			err = runBench(pref, *gate, *allowUnpaced, *sites, *maxCPU, *timeout, *verbose, *testAlloc)
		case *resizeTest:
			err = runResizeTest(pref)
		case *probe:
			err = runProbe()
		case *pose != "":
			err = runPose(pref, *pose, *poseTab)
		case *idle > 0:
			err = runIdle(pref, *idle)
		default:
			err = runWindow(pref, *exitFirst, *verbose)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "rexlook:", err)
		os.Exit(1)
	}
}

// parseMode maps the -mode flag to a preference.
func parseMode(s string) (theme.Preference, error) {
	switch s {
	case "system":
		return theme.System, nil
	case "light":
		return theme.ForceLight, nil
	case "dark":
		return theme.ForceDark, nil
	}
	return 0, fmt.Errorf("-mode %q: want system, light or dark", s)
}

// newScene is the scene showing the mode pref asks for.
func newScene(pref theme.Preference) *rex.Rex {
	x := rex.NewApp()
	x.SetPreference(pref)
	return x
}

// builder is the scene plus Esc to quit, for the window modes.
type builder struct {
	*rex.Rex
	a *app.App
}

func newBuilder(pref theme.Preference) *builder { return &builder{Rex: newScene(pref)} }

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

// config is the window of the window modes: the native shell on macOS
// (-drawn: the drawn one), the drawn one elsewhere.
var config = rex.Config

func init() {
	if runtime.GOOS == "darwin" {
		config = func() app.Config { return rex.NativeConfig(false) }
	}
}

func runWindow(pref theme.Preference, exitFirst, verbose bool) error {
	res, err := app.RunWith(config(), newBuilder(pref), app.RunOptions{ExitFirst: exitFirst, Verbose: verbose})
	if err != nil {
		return err
	}
	if exitFirst {
		printJSON(res.Startup)
	} else if verbose {
		b, _ := json.MarshalIndent(res.Startup, "", "  ")
		fmt.Fprintln(os.Stderr, string(b))
	}
	if verbose { // platform.Open's stages, as ms since its start and since the stage before
		st := platform.OpenTimeline()
		for i, s := range st {
			prev := st[max(i-1, 0)].T
			fmt.Fprintf(os.Stderr, "open %-20s %7.2f ms  +%.2f\n", s.Name, float64(s.T-st[0].T)/1e6, float64(s.T-prev)/1e6)
		}
	}
	return nil
}

// idler counts the frames built during the idle window of -idle; a wake
// after it ends the run.
type idler struct {
	*builder
	start, end atomic.Int64 // platform.Now bounds of the idle window, set by the first frame
	frames     int
	done       bool
}

func (x *idler) Build(c *ui.Context) {
	now := platform.Now()
	switch {
	case x.start.Load() == 0:
		x.start.Store(now + int64(time.Second))
	case x.end.Load() != 0 && now >= x.end.Load() && !x.done:
		x.done = true
		x.a.Quit()
	case now >= x.start.Load() && x.end.Load() == 0:
	case now >= x.start.Load() && !x.done:
		x.frames++
	}
	x.builder.Build(c)
}

func runIdle(pref theme.Preference, d time.Duration) error {
	x := &idler{builder: newBuilder(pref)}
	go func() {
		for x.start.Load() == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		x.end.Store(x.start.Load() + int64(d))
		time.Sleep(time.Duration(x.end.Load() - platform.Now()))
		platform.Wake()
	}()
	if _, err := app.RunWith(config(), x, app.RunOptions{}); err != nil {
		return err
	}
	printJSON(struct {
		IdleSeconds float64
		IdleFrames  int
	}{d.Seconds(), x.frames})
	return nil
}

// renderPNG draws the initial state offscreen at the reference size and
// writes it, with the layout metrics the compare tool checks; with dark, also
// the dark mode into that file.
func renderPNG(path, dark string, pref theme.Preference, native bool) error {
	o := rex.ShotOptions{Width: refW, Height: refH, Scale: 2, Corners: true, Preference: pref, Native: native}
	if pref == theme.System { // an offscreen window has no system: ask this machine
		a := platform.SystemAppearance()
		o.System = ui.Appearance{Dark: a.Dark, Accent: a.Accent, Known: a.Known}
	}
	quads, calls, err := rex.RenderShot(path, o)
	if err != nil {
		return err
	}
	fmt.Printf("%d quads, %d draw calls -> %s\n", quads, calls, path)
	if dark != "" {
		o.Preference = theme.ForceDark
		if quads, calls, err = rex.RenderShot(dark, o); err != nil {
			return err
		}
		fmt.Printf("%d quads, %d draw calls -> %s\n", quads, calls, dark)
	}
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

func runBench(pref theme.Preference, gate, allowUnpaced, sites bool, maxCPU float64, timeout time.Duration, verbose bool, testAlloc int) error {
	x := newScene(pref)
	opt := app.BenchOptions{Warm: warmFrames, Frames: measFrames, Timeout: timeout, Verbose: verbose,
		PostGCWarm: postGCWarm, Windows: maxWindows}
	opt.PrintAllocSites, opt.TestAllocEvery = sites, testAlloc
	br, err := app.RunBench(config(), rex.NewBench(x), opt)
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
		f = append(f, fmt.Sprintf("every one of %d measured windows allocated (fewest: %d mallocs / %d bytes)", len(r.Windows), r.Mallocs, r.TotalAlloc))
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

// runResizeTest drags the window through 150 sizes, like a user dragging an edge.
func runResizeTest(pref theme.Preference) error {
	cfg := config()
	var sizes [][2]float64
	for i := 0; i < 150; i++ {
		f := float64(i) / 149
		sizes = append(sizes, [2]float64{float64(cfg.Width) - 250 + 450*math.Abs(math.Sin(f*math.Pi*2)), float64(cfg.Height) - 150 + 250*f})
	}
	rep, err := app.RunLiveResizeTest(cfg, newBuilder(pref), sizes)
	if err != nil {
		return err
	}
	printJSON(rep)
	if rep.HandlerMsMax > app.LiveResizeMaxHandlerMs || rep.DrawFails == rep.HandlerCalls {
		fmt.Fprintf(os.Stderr, "%s: live-resize test failed: slowest step %.0f ms (limit %d), %d of %d handler calls drew nothing\n",
			"rexlook", rep.HandlerMsMax, app.LiveResizeMaxHandlerMs, rep.DrawFails, rep.HandlerCalls)
		os.Exit(3)
	}
	switch rep.Pacing {
	case app.PacingFailed:
		fmt.Fprintf(os.Stderr, "rexlook: live-resize test failed: the frame source delivered %d frame events before the drag but only %d paced frames (%d wanted) after it\n",
			rep.FrameEventsBefore, rep.PacedFramesAfter, rep.PacedWanted)
		os.Exit(3)
	case app.PacingInconclusive:
		fmt.Fprintln(os.Stderr, "rexlook: pacing after the drag is inconclusive: no frame events arrived even before it (asleep display or occluded window); the frames drawn were the unpaced fallback")
	}
	return nil
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
