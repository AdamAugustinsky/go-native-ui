// Command vlistbench is a messenger timeline of a million messages on
// base.VList, the variable-height virtual list (D43): wrapped text of one to
// eight lines, images whose rows grow when they load, history prepended as
// the view nears the top, messages arriving while it is stuck to the bottom,
// deletions and backfill above the view, a reply jump and a distant move. It
// is the benchmark of the list's budgets: the list's own work under 4 ms in
// every frame (its P99 and its maximum) and 0 allocations while scrolling a
// million mixed-height items, at most 2 ms per frame for edits (a distant move
// on the million included), and no jump of the view on any edit. The whole
// frame's CPU (FrameCPUus: P99 and maximum) is reported, not gated: most of it
// is laying out the messages' wrapped text, which the rich-text paragraph
// cache (C2) is to bound. It builds on every OS.
//
//	vlistbench                  show the window; Esc quits
//	vlistbench -bench           the scripted session (script.go), 120 warm-up +
//	                            600 measured frames, JSON report
//	vlistbench -bench -gate     also exit with status 3 if a budget fails
//	vlistbench -o out.png       render offscreen at 2× (no window), at the bottom
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
	"gonativeui/ui"
)

const (
	winW, winH = 480, 800 // points
	pngW, pngH = 420, 640 // points; the PNG is at 2×
	warmFrames = 120
	measFrames = 600 // one session of the script
	maxWindows = 3

	defaultMaxListUs = 4000 // ARCHITECTURE §11: the list's work in any frame
	defaultMaxEditUs = 2000 // D43: the most a frame spends applying edits
)

var config = app.Config{
	Title: "vlistbench", Width: winW, Height: winH,
	Clear: [4]float32{0x17 / 255.0, 0x18 / 255.0, 0x1B / 255.0, 1},
}

// report is the JSON of -bench.
type report struct {
	OS, Arch string
	Items    int // at the end of the run
	app.BenchReport
	ItemsBuiltPerFrame float64
	ItemsBuiltMax      int
	// ListWorkUs is the distribution over the counted frames of the list's
	// own work per frame: the edits and BeginVList (which applies a
	// rebuild's slice) in main-thread CPU, plus the Item calls and EndVList
	// in wall time. Its P99 and Max are gated; the whole frame is FrameCPUus.
	ListWorkUs app.Stats
	// EditFrameMaxUs is the most main-thread CPU a counted frame spent in the
	// edits and BeginVList, EditFrameMeanUs the mean.
	EditFrameMaxUs, EditFrameMeanUs float64
	Arrivals, HistoryLoads          int
	Rebuilds                        int      // edits rebuilt in slices
	Jumps                           int      // edits above the view that moved it
	Unpinned                        int      // frames stuck to the bottom that did not end at it
	GateFailures                    []string `json:",omitempty"`
}

func main() {
	out := flag.String("o", "", "render offscreen at 2× into this PNG and exit")
	items := flag.Int("items", defItems, "number of messages loaded at the start")
	bench := flag.Bool("bench", false, "scripted run; print allocation, CPU, pacing and the list's checks as JSON")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless the budgets and checks held")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: display asleep)")
	maxList := flag.Float64("max-list-us", defaultMaxListUs, "with -gate: the most the list's own work may take in a frame (its P99 and its maximum), in µs (0: do not gate, e.g. on noisy hosted CI runners)")
	maxEdit := flag.Float64("max-edit-us", defaultMaxEditUs, "with -gate: the most a frame may spend in edits and BeginVList, in µs (0: do not gate)")
	timeout := flag.Duration("timeout", 90*time.Second, "with -bench: give up after this long")
	sites := flag.Bool("allocsites", false, "with -bench: profile every allocation and print the call stacks of those inside the measured frames")
	flag.Parse()

	ch := newChat(max(*items, 1))
	var err error
	switch {
	case *out != "":
		err = renderPNG(ch, *out)
	case *bench:
		err = runBench(ch, *gate, *allowUnpaced, *sites, *maxList, *maxEdit, *timeout)
	default:
		ch.script = func(_ int, in *ui.Input) bool {
			for i := 0; i < in.NKeys; i++ {
				if in.Keys[i].Down && in.Keys[i].Code == ui.KeyEscape && ch.a != nil {
					ch.a.Quit()
				}
			}
			return true
		}
		_, err = app.RunWith(config, ch, app.RunOptions{})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vlistbench:", err)
		os.Exit(1)
	}
}

func renderPNG(ch *chat, path string) error {
	img, err := app.RenderOffscreen(config, pngW*2, pngH*2, 2, ch, 3+loadAfter)
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
	return f.Close()
}

func runBench(ch *chat, gate, allowUnpaced, sites bool, maxList, maxEdit float64, timeout time.Duration) error {
	ch.script = ch.benchScript(warmFrames, measFrames, warmFrames+maxWindows*measFrames+30)
	ch.warmRebuild()
	o := app.BenchOptions{Warm: warmFrames, Frames: measFrames, Windows: maxWindows, Timeout: timeout}
	var as allocSites
	if sites {
		o.WindowStart, o.WindowEnd = as.start, as.end
	}
	br, err := app.RunBench(config, ch, o)
	if err != nil {
		return err
	}
	rep := ch.report(br)
	if gate {
		rep.GateFailures = gateFailures(rep, allowUnpaced, maxList, maxEdit)
	}
	printJSON(rep)
	if len(rep.GateFailures) > 0 {
		fmt.Fprintln(os.Stderr, "vlistbench: gate failed:", strings.Join(rep.GateFailures, "; "))
		os.Exit(3)
	}
	return nil
}

func (ch *chat) report(br app.BenchReport) report {
	rep := report{
		OS: runtime.GOOS, Arch: runtime.GOARCH, Items: ch.st.Len(), BenchReport: br,
		ItemsBuiltMax: ch.builtMax, ListWorkUs: app.Summarize(ch.listUs),
		EditFrameMaxUs: float64(ch.editMax.Nanoseconds()) / 1e3,
		Arrivals:       ch.arrivals, HistoryLoads: ch.histLoads, Rebuilds: ch.rebuilds,
		Jumps: ch.jumps, Unpinned: ch.unpinned,
	}
	if ch.buildCalls > 0 {
		rep.ItemsBuiltPerFrame = float64(ch.built) / float64(ch.buildCalls)
		rep.EditFrameMeanUs = float64(ch.editSum.Nanoseconds()) / 1e3 / float64(ch.buildCalls)
	}
	return rep
}

func gateFailures(r report, allowUnpaced bool, maxList, maxEdit float64) []string {
	var f []string
	if r.Frames < measFrames {
		f = append(f, fmt.Sprintf("%d measured frames, want %d", r.Frames, measFrames))
	}
	if r.Mallocs != 0 || r.TotalAlloc != 0 {
		f = append(f, fmt.Sprintf("%d mallocs / %d bytes allocated over the measured frames", r.Mallocs, r.TotalAlloc))
	}
	if maxList > 0 && r.ListWorkUs.P99 > maxList {
		f = append(f, fmt.Sprintf("the list's work per frame has a P99 of %.0f µs, budget %.0f", r.ListWorkUs.P99, maxList))
	}
	if maxList > 0 && r.ListWorkUs.Max > maxList {
		f = append(f, fmt.Sprintf("a frame spent %.0f µs in the list's work, budget %.0f", r.ListWorkUs.Max, maxList))
	}
	if maxEdit > 0 && r.EditFrameMaxUs > maxEdit {
		f = append(f, fmt.Sprintf("a frame spent %.0f µs in edits and BeginVList, budget %.0f", r.EditFrameMaxUs, maxEdit))
	}
	if !r.Paced && !allowUnpaced {
		f = append(f, fmt.Sprintf("%d frames were not paced (no frame events); -allow-unpaced accepts a sleeping display", r.UnpacedFrames))
	}
	if r.Jumps != 0 {
		f = append(f, fmt.Sprintf("%d edits above the view moved it", r.Jumps))
	}
	if r.Unpinned != 0 {
		f = append(f, fmt.Sprintf("%d frames stuck to the bottom did not end at it", r.Unpinned))
	}
	if r.Rebuilds == 0 || r.HistoryLoads == 0 || r.Arrivals == 0 {
		f = append(f, fmt.Sprintf("the session did not exercise the list (rebuilds %d, history loads %d, arrivals %d)", r.Rebuilds, r.HistoryLoads, r.Arrivals))
	}
	return f
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
