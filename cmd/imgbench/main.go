// Command imgbench scrolls a grid of a few thousand thumbnails through the
// image cache (package img, D44): every item is its own image, decoded on the
// workers at its cell's size from a JPEG photo, uploaded within the
// per-frame budget into atlas pages and evicted when the GPU budget is short.
// It builds on every OS.
//
//	imgbench                     show the window; scroll it; Esc quits
//	imgbench -bench              the scripted run, JSON report:
//	                             a cold pass over the whole grid (every image
//	                             decoded, most evicted again), then measured
//	                             sessions scrolling up and down over images
//	                             already cached, about 200 in view per frame
//	imgbench -bench -gate        also exit with status 3 unless the measured
//	                             sessions allocated nothing, every image in view
//	                             was drawn, and no frame's Poll exceeded
//	                             -max-poll-ms
//	imgbench -idle 2s            show the window, wait for the view to load, then
//	                             count the frames drawn over 2 s of nothing (0)
//	imgbench -o out.png          render offscreen at 2× once the view has loaded
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
	"gonativeui/ui"
)

const (
	winW, winH = 1000, 680 // points: 16 columns, 12 or 13 rows of thumbnails in view (fits a 1024×768 screen)
	defItems   = 3000

	coldSpeedPt   = 40  // points per frame through the whole grid
	returnSpeedPt = 300 // back to the top
	sessionRows   = 36  // the measured sessions scroll this many rows down and back
	sessionPt     = 30  // points per frame in a session

	maxWindows       = 4
	defaultMaxPollMs = 8
)

var config = app.Config{Title: "imgbench", Width: winW, Height: winH, Clear: [4]float32{0.1, 0.1, 0.11, 1}}

// benchOptions are the cache budgets of the run: the grid's images need
// about 40 MB at 1× (150 MB at 2×), so the cold pass evicts; the GPU budget
// is for 1× and grows with the square of the scale (64 MB at 2×), while a
// session's images fit in it.
var benchOptions = img.Options{GPUBytes: 16 << 20, CPUBytes: 16 << 20}

type report struct {
	OS, Arch     string
	Items        int
	VisibleMean  float64 // images in view per measured frame
	VisibleReady float64 // the share of them drawn (not a placeholder)
	app.BenchReport
	Cold struct {
		Frames  int
		Seconds float64
		BuildMs app.Stats // Poll + Build, wall time per frame
		// PollMs is Poll's wall time per frame: taking decodes, staging
		// uploads, evicting, and the wait for the ring slot's previous frame
		// that the first staged upload of a frame does in Encode's place.
		// PollCPUMs is its thread CPU time, which leaves that wait out.
		PollMs, PollCPUMs app.Stats
	}
	Cache struct {
		Decodes                  int
		DecodeMeanMs             float64 // worker time per decode
		Uploads                  int
		UploadedMB               float64
		EvictedGPU               int
		EvictedCPU               int
		OverBudget               int
		GPUMB, CPUMB             float64
		Pages, Entries           int
		GPUBudgetMB, CPUBudgetMB float64
	}
	FootprintMB, GoHeapMB float64
	GateFailures          []string `json:",omitempty"`
}

func main() {
	out := flag.String("o", "", "render offscreen at 2× into this PNG and exit")
	items := flag.Int("items", defItems, "number of thumbnails")
	bench := flag.Bool("bench", false, "scripted run; print the JSON report")
	gate := flag.Bool("gate", false, "with -bench: exit with status 3 unless the budgets held")
	allowUnpaced := flag.Bool("allow-unpaced", false, "with -gate: accept unpaced frames (no frame events: display asleep)")
	maxPoll := flag.Float64("max-poll-ms", defaultMaxPollMs, "with -gate: the most CPU a frame's Poll may take in the cold pass, in ms (a quarter of it on average; 0: do not gate)")
	anim := flag.Bool("anim", false, "with -bench and -idle: show the animated samples too (the window and -o always do)")
	away := flag.Bool("away", false, "with -idle -anim: scroll past the animated samples first: they are out of view and the app must draw 0 frames")
	idle := flag.Duration("idle", 0, "show the window, wait for the view to load, count the frames drawn over this long, print them as JSON")
	timeout := flag.Duration("timeout", 120*time.Second, "with -bench: give up after this long")
	sites := flag.Bool("allocsites", false, "with -bench: print the call stacks of the allocations attributed over the measured windows")
	flag.Parse()

	var err error
	switch {
	case *out != "":
		err = renderPNG(*items, *out)
	case *bench:
		err = runBench(*items, *gate, *allowUnpaced, *sites, *maxPoll, *timeout, *anim)
	case *idle > 0:
		err = runIdle(*items, *idle, *anim, *away)
	default:
		g := newGrid(*items, img.Options{})
		g.anim = true
		_, err = app.RunWith(config, &interactive{g}, app.RunOptions{})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "imgbench:", err)
		os.Exit(1)
	}
}

// interactive quits on Esc.
type interactive struct{ *grid }

func (x *interactive) Build(c *ui.Context) {
	in := c.Input()
	for i := 0; i < in.NKeys; i++ {
		if in.Keys[i].Down && in.Keys[i].Code == ui.KeyEscape {
			x.a.Quit()
		}
	}
	x.grid.Build(c)
}

func renderPNG(items int, path string) error {
	g := newGrid(items, img.Options{})
	g.blocking, g.anim = true, true
	m, err := app.RenderOffscreen(config, 600*2, 640*2, 2, g, 2)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, m); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ---- the bench --------------------------------------------------------------

// Phases of the scripted run.
const (
	phCold    = iota // scroll the whole grid once
	phReturn         // back to the top
	phPrime          // sessions until one draws every image in view
	phSession        // the measured sessions, repeating
)

// session drives the grid in -bench and the test.
type session struct {
	phase      int
	next       int  // the frame of the session the next session frame is
	missed     bool // a frame of this priming session drew a placeholder
	visSum     int
	readySum   int
	sessFrames int
}

func (s *session) period() int { return 2 * sessionRows * pitchPt / sessionPt }

// at is the scroll position of frame f of a session: down sessionRows rows,
// then back up.
func (s *session) at(f int) float32 {
	half := s.period() / 2
	if f < half {
		return float32(f * sessionPt)
	}
	return float32((s.period() - f) * sessionPt)
}

// step is the grid's script.
func (s *session) step(g *grid, c *ui.Context) {
	switch s.phase {
	case phCold:
		if g.coldStart == 0 {
			g.coldStart = platform.Now()
			g.counting = true
		}
		g.coldFrames++
		g.scroll += coldSpeedPt
		if g.scroll >= g.maxScroll(c) {
			s.phase = phReturn
		}
	case phReturn:
		if g.counting {
			g.counting = false
			g.coldEnd = platform.Now()
		}
		g.scroll -= returnSpeedPt
		if g.scroll <= 0 {
			s.phase, s.next = phPrime, 0
		}
	case phPrime:
		g.scroll = s.at(s.next)
		s.next++
	case phSession:
		g.scroll = s.at(s.next)
		if s.next++; s.next == s.period() {
			s.next = 0
		}
	}
}

// count accumulates what a session frame showed (called after Build). The
// sessions are measured once a whole priming session has drawn every image
// in view: a slow decoder (the standard library's, ~30 ms a thumbnail on a
// CI runner) finishes images after they left the view, and they upload when
// they come back.
func (s *session) count(g *grid) {
	if s.phase == phPrime {
		s.missed = s.missed || g.visible == 0 || g.ready != g.visible
		if s.next == s.period() {
			if !s.missed {
				s.phase = phSession
			}
			s.next, s.missed = 0, false
		}
		return
	}
	if s.phase == phSession {
		s.visSum += g.visible
		s.readySum += g.ready
		s.sessFrames++
	}
}

// benchGrid is the grid with the bench's script and cycle alignment.
type benchGrid struct {
	*grid
	s session
}

func newBenchGrid(items int) *benchGrid {
	b := &benchGrid{grid: newGrid(items, benchOptions)}
	b.grid.script = b.s.step
	return b
}

func (b *benchGrid) Build(c *ui.Context) {
	b.grid.Build(c)
	b.s.count(b.grid)
}

// AtCycleStart implements app.CycleAligned: a measured window is whole
// sessions of cached images.
func (b *benchGrid) AtCycleStart() bool { return b.s.phase == phSession && b.s.next == 0 }

func runBench(items int, gate, allowUnpaced, sites bool, maxPoll float64, timeout time.Duration, anim bool) error {
	b := newBenchGrid(items)
	b.anim = anim
	br, err := app.RunBench(config, b, app.BenchOptions{Warm: 1, Windows: maxWindows, Timeout: timeout, PrintAllocSites: sites})
	if err != nil {
		return err
	}
	rep := b.report(br)
	if gate {
		rep.GateFailures = gateFailures(rep, b.s.period(), allowUnpaced, maxPoll)
	}
	b.cache.Close(time.Second)
	printJSON(rep)
	if len(rep.GateFailures) > 0 {
		fmt.Fprintln(os.Stderr, "imgbench: gate failed:", strings.Join(rep.GateFailures, "; "))
		os.Exit(3)
	}
	return nil
}

func (b *benchGrid) report(br app.BenchReport) report {
	r := report{OS: runtime.GOOS, Arch: runtime.GOARCH, Items: b.items, BenchReport: br}
	if b.s.sessFrames > 0 {
		r.VisibleMean = float64(b.s.visSum) / float64(b.s.sessFrames)
		r.VisibleReady = float64(b.s.readySum) / float64(max(b.s.visSum, 1))
	}
	r.Cold.Frames = b.coldFrames
	r.Cold.Seconds = float64(b.coldEnd-b.coldStart) / 1e9
	r.Cold.BuildMs = app.Summarize(b.build)
	r.Cold.PollMs = app.Summarize(b.pollNs)
	r.Cold.PollCPUMs = app.Summarize(b.pollCPU)
	st := b.cache.Stats()
	r.Cache.Decodes, r.Cache.Uploads = st.Decodes, st.Uploads
	if st.Decodes > 0 {
		r.Cache.DecodeMeanMs = float64(st.DecodeNs) / 1e6 / float64(st.Decodes)
	}
	r.Cache.UploadedMB = float64(st.UploadedBytes) / (1 << 20)
	r.Cache.EvictedGPU, r.Cache.EvictedCPU, r.Cache.OverBudget = st.EvictedGPU, st.EvictedCPU, st.OverBudget
	r.Cache.GPUMB, r.Cache.CPUMB = float64(st.GPUBytes)/(1<<20), float64(st.CPUBytes)/(1<<20)
	r.Cache.Pages, r.Cache.Entries = st.Pages, st.Entries
	r.Cache.GPUBudgetMB, r.Cache.CPUBudgetMB = float64(b.opt.GPUBytes)/(1<<20), float64(b.opt.CPUBytes)/(1<<20)
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	r.GoHeapMB = float64(ms.HeapAlloc) / (1 << 20)
	r.FootprintMB = float64(sysinfo.Read().Footprint) / (1 << 20)
	return r
}

func gateFailures(r report, period int, allowUnpaced bool, maxPoll float64) []string {
	var f []string
	if r.Frames < period {
		f = append(f, fmt.Sprintf("%d measured frames, want a session of %d", r.Frames, period))
	}
	if r.Mallocs != 0 || r.TotalAlloc != 0 {
		f = append(f, fmt.Sprintf("%d mallocs / %d bytes allocated over the measured frames", r.Mallocs, r.TotalAlloc))
	}
	if msg := r.OwnAllocFailure(); msg != "" {
		f = append(f, msg)
	}
	if r.VisibleMean < 150 {
		f = append(f, fmt.Sprintf("%.0f images in view per frame, want about 200", r.VisibleMean))
	}
	if r.VisibleReady < 1 {
		f = append(f, fmt.Sprintf("%.2f%% of the images in view were placeholders in the cached sessions", 100*(1-r.VisibleReady)))
	}
	if r.Cache.EvictedGPU == 0 {
		f = append(f, "the cold pass evicted nothing: the GPU budget was not exercised")
	}
	// Windows' thread clock ticks in 15.6 ms steps: there only the mean is
	// meaningful.
	if poll := r.Cold.PollCPUMs.Max; maxPoll > 0 && runtime.GOOS != "windows" && poll > maxPoll {
		f = append(f, fmt.Sprintf("a cold-pass frame spent %.1f ms of CPU in Poll, budget %.0f", poll, maxPoll))
	}
	if poll := r.Cold.PollCPUMs.Mean; maxPoll > 0 && poll > maxPoll/4 {
		f = append(f, fmt.Sprintf("the cold pass spent %.1f ms of CPU in Poll per frame on average, budget %.1f", poll, maxPoll/4))
	}
	if !r.Paced && !allowUnpaced {
		f = append(f, fmt.Sprintf("%d frames were not paced (no frame events); -allow-unpaced accepts a sleeping display", r.UnpacedFrames))
	}
	return f
}

// ---- idle -------------------------------------------------------------------

// idler counts the frames drawn once the view has loaded; a wake after the
// idle window ends the run.
type idler struct {
	*grid
	start, end atomic.Int64
	frames     int
	done       bool
	away       bool // scrolled past the animated samples
}

func (x *idler) Build(c *ui.Context) {
	if x.away {
		x.grid.scroll = 6 * pitchPt
	}
	x.grid.Build(c)
	now := platform.Now()
	switch {
	case x.start.Load() == 0:
		if x.ready == x.visible && x.visible > 0 && !c.Animating() {
			x.start.Store(now + int64(200*time.Millisecond)) // the last upload's frames settle
		}
	case x.end.Load() != 0 && now >= x.end.Load() && !x.done:
		x.done = true
		x.a.Quit()
	case now >= x.start.Load() && !x.done:
		x.frames++
	}
}

func runIdle(items int, d time.Duration, anim, away bool) error {
	x := &idler{grid: newGrid(items, img.Options{}), away: away}
	x.anim = anim
	go func() {
		for x.start.Load() == 0 {
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(time.Duration(x.start.Load() - platform.Now()))
		x.end.Store(platform.Now() + int64(d))
		time.Sleep(d)
		platform.Wake()
	}()
	if _, err := app.RunWith(config, x, app.RunOptions{}); err != nil {
		return err
	}
	printJSON(struct {
		IdleSeconds float64
		IdleFrames  int
		Visible     int
		Animated    bool // the animated samples are in view: their frames are drawn at their deadlines
		Away        bool // ... or scrolled out of it: nothing is drawn
	}{d.Seconds(), x.frames, x.visible, anim && !away, away})
	if x.frames != 0 && (!anim || away) {
		os.Exit(3)
	}
	return nil
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
