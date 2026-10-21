// Command gallery shows every widget of package widget in every state, in the
// Rex theme, light and dark, next to a table of a million rows.
//
//	gallery                    show the window, in the system's light or dark mode and accent colour, which it
//	                           follows while it runs (-mode system, the default; -mode light or dark forces one;
//	                           ⇧⌘D cycles System, Light, Dark; a change crossfades)
//	gallery -window 820x620    show the window at that size in points (System Settings' is 820×620)
//	gallery -o out.png         render the scene offscreen at 2200×1440 px and exit
//	gallery -o out.png -mode dark
//	                           -o renders light unless -mode is given; -mode system renders what this machine says
//	gallery -o out.png -o-dark dark.png
//	                           render the light and the dark picture
//	gallery -o out.png -scene dialog
//	                           render a scene: main (the Playground), states, table, appearance, dialogs, the
//	                           catalogue pages, or a state: menu, select, context, dialog or tooltip
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
	"gonativeui/platform"
	"gonativeui/rexui/catalog"
	"gonativeui/theme"
	"gonativeui/ui"
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
	w := flag.Int("w", 2*gallery.WindowWidthPt, "with -o: width in pixels")
	h := flag.Int("h", 2*gallery.WindowHeightPt, "with -o: height in pixels")
	scale := flag.Float64("scale", 2, "with -o: pixels per point")
	scene := flag.String("scene", "main", "with -o: the state to render ("+strings.Join(gallery.SceneNames(), ", ")+"); "+
		strings.Join(gallery.AppKitPages[:], " and ")+" show rexui's AppKitRef samples, in a window or with -o")
	ref := flag.String("ref", "", "with -o and an appkit scene: pin the system colours to an AppKitRef metrics.json (\"auto\": this host's reference set, GNUI_APPKIT_REF overrides)")
	mode := flag.String("mode", "system", "system (follow the system: the default), light or dark; with -o the default is light")
	rows := flag.Int("rows", gallery.DefaultRows, "rows of the table")
	measure := flag.Bool("measure", false, "print the frames in points of the -scene's elements at -w × -h (1640×1240 px is System Settings' 820×620 pt) as JSON, and exit")
	bench := flag.Bool("bench", false, "scripted session; print allocation, CPU, pacing and latency as JSON")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless there were 0 allocations, main-thread CPU within budget and frames were paced")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: display asleep)")
	maxCPU := flag.Float64("max-cpu-us", defaultMaxCPUus, "with -gate: main-thread CPU budget in µs per frame (0: do not gate on CPU)")
	timeout := flag.Duration("timeout", 180*time.Second, "with -bench: give up after this long")
	sites := flag.Bool("allocsites", false, "with -bench: profile every allocation and print the call stacks of those inside the measured frames")
	start := flag.String("start", "", "in a window: the scene to open on, by its title in the sidebar (Table, Lists …)")
	scrolled := flag.Bool("scrolled", false, "the content starts scrolled under the toolbar")
	inactive := flag.Bool("inactive", false, "with -o or -measure: render the window as not key")
	legacy := flag.Bool("legacy", false, "with -o or -measure: render legacy scroll bars (a window follows the system's style)")
	window := flag.String("window", "", "in a window: its size in points as WxH (820x620 is System Settings'), instead of the default")
	verbose := flag.Bool("v", false, "in a window or bench: print a state line every second while frames stop coming")
	flag.Parse()

	m, err := parseMode(*mode)
	explicit := false
	flag.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "mode" })
	if err == nil {
		switch {
		case gallery.AppKitPage(*scene) >= 0 && *out != "":
			if !explicit {
				m = theme.ForceLight
			}
			err = renderAppKit(*out, *outDark, gallery.AppKitPage(*scene), m, float32(*scale), *ref)
		case gallery.AppKitPage(*scene) >= 0:
			s := gallery.NewAppKit(gallery.AppKitPage(*scene), m)
			_, err = app.RunWith(s.Config(), s, app.RunOptions{})
		case *measure:
			if !explicit {
				m = theme.ForceLight
			}
			err = runMeasure(gallery.ShotOptions{Width: *w, Height: *h, Scale: float32(*scale), Scene: *scene, Preference: m, Rows: *rows,
				Scrolled: *scrolled, Inactive: *inactive, Legacy: *legacy})
		case *out != "":
			if !explicit {
				m = theme.ForceLight // a picture is the same on every machine unless it is asked to follow this one
			}
			err = render(*out, *outDark, *w, *h, float32(*scale), *scene, m, *rows, *scrolled, *inactive, *legacy)
		case *bench:
			err = runBench(*rows, m, *gate, *allowUnpaced, *sites, *maxCPU, *timeout, *verbose)
		default:
			err = runWindow(*rows, m, *window, *start, *scrolled)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gallery:", err)
		os.Exit(1)
	}
}

// renderAppKit renders page p of the appkit scenes, light into path and dark
// into dark when it is set.
func renderAppKit(path, dark string, p int, m theme.Preference, scale float32, ref string) error {
	var sys *theme.SystemValues
	if ref != "" {
		s, err := catalog.LoadSystem(ref)
		if err != nil {
			return err
		}
		sys = s
	}
	if err := gallery.AppKitShot(path, p, m, scale, sys); err != nil {
		return err
	}
	fmt.Println("appkit page ->", path)
	if dark != "" {
		if err := gallery.AppKitShot(dark, p, theme.ForceDark, scale, sys); err != nil {
			return err
		}
		fmt.Println("appkit page ->", dark)
	}
	return nil
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

func render(path, dark string, w, h int, scale float32, scene string, m theme.Preference, rows int, scrolled, inactive, legacy bool) error {
	o := gallery.ShotOptions{Width: w, Height: h, Scale: scale, Scene: scene, Preference: m, Rows: rows, Scrolled: scrolled, Inactive: inactive, Legacy: legacy}
	if m == theme.System { // an offscreen window has no system: ask this machine
		a := platform.SystemAppearance()
		o.System = ui.Appearance{Dark: a.Dark, Accent: a.Accent, Known: a.Known}
	}
	quads, err := gallery.Shot(path, o)
	if err != nil {
		return err
	}
	fmt.Printf("%d quads -> %s\n", quads, path)
	if dark != "" {
		o.Preference = theme.ForceDark
		if quads, err = gallery.Shot(dark, o); err != nil {
			return err
		}
		fmt.Printf("%d quads -> %s\n", quads, dark)
	}
	return nil
}

func runWindow(rows int, m theme.Preference, window, start string, scrolled bool) error {
	g, err := gallery.New(gallery.Options{Rows: rows, Preference: m, Start: start, Scrolled: scrolled})
	if err != nil {
		return err
	}
	cfg := gallery.Config()
	if window != "" {
		var w, h int
		if n, err := fmt.Sscanf(window, "%dx%d", &w, &h); n != 2 || err != nil || w < 200 || h < 200 {
			return fmt.Errorf("-window %q: want WxH in points, such as 820x620", window)
		}
		cfg.Width, cfg.Height = float32(w), float32(h)
		cfg.MinWidth, cfg.MinHeight = min(cfg.MinWidth, float32(w)), min(cfg.MinHeight, float32(h))
	}
	_, err = app.RunWith(cfg, g, app.RunOptions{})
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

func runBench(rows int, m theme.Preference, gate, allowUnpaced, sites bool, maxCPU float64, timeout time.Duration, verbose bool) error {
	b, err := gallery.NewBench(gallery.Options{Rows: rows, Preference: m})
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

// runMeasure prints the frames of the scene's elements.
func runMeasure(o gallery.ShotOptions) error {
	nodes, err := gallery.Measure(o)
	if err != nil {
		return err
	}
	printJSON(struct {
		Scene  string
		Mode   theme.Preference
		Legacy bool
		Nodes  []gallery.Node
	}{o.Scene, o.Preference, o.Legacy, nodes})
	return nil
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
