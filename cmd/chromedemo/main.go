// Command chromedemo shows rexui's window chrome (Toolbar, Sidebar, PathBar,
// Tabs) in Finder-, Settings- and Notes-like windows laid out as the macOS
// 27.2 scenes of tools/appkitscenes, and a window of Rex's tabs; the scene
// lives in package demo.
//
//	chromedemo -scene finder         show the window (finder, settings, notes or tabs), in the system's mode
//	                                 (-mode light or dark forces one); Esc quits
//	chromedemo -scene notes -o n.png render it offscreen at 2× and exit; light unless -mode is given
//	chromedemo -o n.png -o-dark d.png
//	                                 render the light and the dark picture
//	chromedemo -o n.png -inactive    render the window as not key; -scrolled with the content under the toolbar
//	chromedemo -measure              print the frames, in points, of the toolbar's items, the title, the
//	                                 sidebar's rows and the path bar's components as JSON (for comparing with
//	                                 the appkitscenes dumps)
//	chromedemo -bench                the scene's scripted session on the real window, measured as whole sessions;
//	                                 print allocation, CPU and pacing as JSON (-gate: exit with status 3 unless a
//	                                 session allocated nothing, main-thread CPU within budget and paced frames;
//	                                 -allow-unpaced accepts a sleeping display)
//	chromedemo -idle 3s              show the window, let it settle, count the frames drawn while nothing
//	                                 happens for 3 s and print them as JSON
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/cmd/chromedemo/demo"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"
)

const (
	warmFrames      = 600
	postGCWarm      = 200
	maxWindows      = 8
	defaultMaxCPUus = 2000
)

func main() {
	sceneName := flag.String("scene", "finder", "the window: "+strings.Join(demo.SceneNames[:], ", "))
	out := flag.String("o", "", "render offscreen into this PNG and exit")
	outDark := flag.String("o-dark", "", "with -o: also render the dark mode into this PNG")
	mode := flag.String("mode", "system", "system (the default), light or dark; with -o and -measure the default is light")
	inactive := flag.Bool("inactive", false, "with -o or -measure: the window is not key")
	scrolled := flag.Bool("scrolled", false, "the content starts scrolled under the toolbar")
	legacy := flag.Bool("legacy", false, "with -o or -measure: legacy scroll bars (a window follows the system's style)")
	plain := flag.Bool("plain", false, "show a plain striped window (the backdrop of glass captures) and nothing else")
	drawn := flag.Bool("drawn", false, "in a window: draw the glass's fallbacks instead of the system's materials")
	measure := flag.Bool("measure", false, "print the chrome's frames in points as JSON and exit")
	bench := flag.Bool("bench", false, "scripted session; print allocation, CPU and pacing as JSON")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless 0 allocations, CPU within budget and paced frames")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (display asleep)")
	maxCPU := flag.Float64("max-cpu-us", defaultMaxCPUus, "with -gate: main-thread CPU budget in µs per frame (0: none)")
	timeout := flag.Duration("timeout", 120*time.Second, "with -bench: give up after this long")
	idle := flag.Duration("idle", 0, "count the frames drawn over this long while nothing happens, print them as JSON and exit")
	flag.Parse()

	sc := demo.SceneOf(*sceneName)
	pref, err := parseMode(*mode)
	if err == nil && sc < 0 {
		err = fmt.Errorf("-scene %q: want one of %s", *sceneName, strings.Join(demo.SceneNames[:], ", "))
	}
	explicit := false
	flag.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "mode" })
	if err == nil {
		s := demo.Scene(sc)
		o := demo.ShotOptions{Scene: s, Preference: pref, Inactive: *inactive, Scrolled: *scrolled, Legacy: *legacy}
		if !explicit {
			o.Preference = theme.ForceLight
		}
		if o.Preference == theme.System {
			a := platform.SystemAppearance()
			o.System = ui.Appearance{Dark: a.Dark, Accent: a.Accent, Known: a.Known}
		}
		switch {
		case *plain:
			err = runPlain()
		case *out != "":
			err = render(*out, *outDark, o)
		case *measure:
			var nodes []demo.Node
			if nodes, err = demo.Measure(o); err == nil {
				printJSON(nodes)
			}
		case *bench:
			err = runBench(s, pref, *gate, *allowUnpaced, *maxCPU, *timeout)
		case *idle > 0:
			err = runIdle(s, pref, *idle)
		default:
			cfg := demo.Config(s)
			cfg.Materials = !*drawn
			_, err = app.RunWith(cfg, &builder{Demo: demo.New(demo.Options{Scene: s, Preference: pref, Scrolled: *scrolled})}, app.RunOptions{})
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "chromedemo:", err)
		os.Exit(1)
	}
}

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

func render(path, dark string, o demo.ShotOptions) error {
	q, err := demo.Shot(path, o)
	if err != nil {
		return err
	}
	fmt.Printf("%d quads -> %s\n", q, path)
	if dark != "" {
		o.Preference = theme.ForceDark
		if q, err = demo.Shot(dark, o); err != nil {
			return err
		}
		fmt.Printf("%d quads -> %s\n", q, dark)
	}
	return nil
}

// builder is the demo plus Esc to quit.
type builder struct {
	*demo.Demo
	a *app.App
}

func (b *builder) Init(a *app.App) error { b.a = a; return b.Demo.Init(a) }

func (b *builder) Build(c *ui.Context) {
	in := c.Input()
	for i := 0; i < in.NKeys; i++ {
		if k := in.Keys[i]; k.Down && k.Code == ui.KeyEscape && !c.TextFieldFocused() {
			b.a.Quit()
		}
	}
	b.Demo.Build(c)
}

// plainWindow is a window of wide red, green and blue stripes: placed
// behind the demo for a capture of the screen region, it shows whether any
// of it shows through the demo's window or its glass.
type plainWindow struct{ a *app.App }

func (p *plainWindow) Init(a *app.App) error { p.a = a; return nil }

func (p *plainWindow) Build(c *ui.Context) {
	in := c.Input()
	for i := 0; i < in.NKeys; i++ {
		if k := in.Keys[i]; k.Down && k.Code == ui.KeyEscape {
			p.a.Quit()
		}
	}
	w, h := c.Size()
	stripe := c.Pt(80)
	cols := [3]uint32{0xE0352BFF, 0x2BB04AFF, 0x2B6CE0FF}
	for i := 0; float32(i)*stripe < float32(w); i++ {
		c.NextFlags(theme.Floating)
		c.NextPos(float32(i)*stripe, 0)
		c.NextSize(ui.Px(stripe), ui.Px(float32(h)))
		c.NextBG(cols[i%3])
		c.BoxKeyed(ui.DrawBackground, ui.KeyN(ui.HashString(0, "stripe"), uint64(i)), nil)
	}
}

func runPlain() error {
	_, err := app.RunWith(app.Config{Title: "Plain", Width: 1400, Height: 900, Clear: [4]float32{1, 1, 1, 1}}, &plainWindow{}, app.RunOptions{})
	return err
}

// idler counts the frames built during the idle window of -idle.
type idler struct {
	*builder
	start, end atomic.Int64
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

func runIdle(s demo.Scene, pref theme.Preference, d time.Duration) error {
	x := &idler{builder: &builder{Demo: demo.New(demo.Options{Scene: s, Preference: pref})}}
	go func() {
		for x.start.Load() == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		x.end.Store(x.start.Load() + int64(d))
		time.Sleep(time.Duration(x.end.Load() - platform.Now()))
		platform.Wake()
	}()
	if _, err := app.RunWith(demo.Config(s), x, app.RunOptions{}); err != nil {
		return err
	}
	printJSON(struct {
		Scene       string
		IdleSeconds float64
		IdleFrames  int
	}{demo.SceneNames[s], d.Seconds(), x.frames})
	return nil
}

type report struct {
	OS, Arch, Scene string
	app.BenchReport
	SessionCycles int
	GateFailures  []string `json:",omitempty"`
}

func runBench(s demo.Scene, pref theme.Preference, gate, allowUnpaced bool, maxCPU float64, timeout time.Duration) error {
	b := demo.NewBench(demo.Options{Scene: s, Preference: pref})
	br, err := app.RunBench(demo.Config(s), b, app.BenchOptions{Warm: warmFrames, PostGCWarm: postGCWarm, Windows: maxWindows, Timeout: timeout})
	if serr := b.Err(); serr != nil {
		return serr
	}
	if err != nil {
		return err
	}
	rep := report{OS: runtime.GOOS, Arch: runtime.GOARCH, Scene: demo.SceneNames[s], BenchReport: br, SessionCycles: b.Cycles()}
	if gate {
		var f []string
		if rep.Frames < 60 {
			f = append(f, fmt.Sprintf("%d measured frames in the session, want at least 60", rep.Frames))
		}
		if rep.Mallocs != 0 || rep.TotalAlloc != 0 {
			f = append(f, fmt.Sprintf("every one of %d whole sessions allocated (fewest: %d mallocs / %d bytes)", len(rep.Windows), rep.Mallocs, rep.TotalAlloc))
		}
		if maxCPU > 0 && rep.MainThreadCPUus > maxCPU {
			f = append(f, fmt.Sprintf("main-thread CPU %.0f µs/frame, budget %.0f", rep.MainThreadCPUus, maxCPU))
		}
		if !rep.Paced && !allowUnpaced {
			f = append(f, fmt.Sprintf("%d frames were not paced; -allow-unpaced accepts a sleeping display", rep.UnpacedFrames))
		}
		rep.GateFailures = f
	}
	printJSON(rep)
	if len(rep.GateFailures) > 0 {
		fmt.Fprintln(os.Stderr, "chromedemo: gate failed:", strings.Join(rep.GateFailures, "; "))
		os.Exit(3)
	}
	return nil
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
