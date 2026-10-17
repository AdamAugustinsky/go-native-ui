//go:build darwin

// Command rexlook renders a static recreation of the Rex terminal
// multiplexer's look (docs/design/rex-benchmark.md): three panes with
// ANSI-coloured terminal text, a tab capsule in a transparent titlebar, the
// host block and the ⌘ and + glyphs. Visuals only.
//
//	rexlook                    show the window; Esc or the close button quits
//	rexlook -exit-first        print the startup timeline as JSON and exit
//	rexlook -bench             re-render the whole scene every frame (120 warm-up
//	                           + 600 measured), print allocation, CPU and pacing as JSON
//	rexlook -o out.png         render the same scene offscreen at 2114×1278 px
//	                           (the reference size) with our own traffic lights
//	                           and write a PNG plus out.png.metrics.json for
//	                           tools/rexcompare
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"runtime"
	"slices"
	"time"

	"gonativeui/gpu"
	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
	"gonativeui/text"
)

const (
	winW, winH = 1057, 639  // points
	refW, refH = 2114, 1278 // the reference screenshot, pixels
	warmFrames = 120
	measFrames = 600
)

var clearColor = [4]float32{0.9, 0.9, 0.9, 1} // every pixel is covered by the background mesh

type startupReport struct {
	ExecToMainMs         float64
	MainToOpenMs         float64
	OpenToFirstFrameMs   float64 // setup + scene + text + encode, until EndFrame returns
	SceneMs              float64
	TextMs               float64
	ExecToFirstPresentMs float64
	ExecToOnGlassMs      float64 // -1 if unknown
}

type benchReport struct {
	Frames          int
	Paced           bool // EvFrame callbacks arrived (false: display asleep / occluded, frames ran unpaced)
	Quads           int
	DrawCalls       int
	Mallocs         uint64
	TotalAlloc      uint64
	NumGC           uint32
	ProcessCPUus    float64 // per frame
	MainThreadCPUus float64 // per frame
	FrameIntervalMs stats
	MonoFace        string
	MonoSizePt      float64
	CellAdvancePt   float64
}

type stats struct{ P50, P99, Max, Mean float64 }

func summarize(xs []float64) stats {
	if len(xs) == 0 {
		return stats{}
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	var sum float64
	for _, x := range s {
		sum += x
	}
	return stats{P50: s[len(s)/2], P99: s[len(s)*99/100], Max: s[len(s)-1], Mean: sum / float64(len(s))}
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}

func main() {
	tMain := platform.Now()
	age := sysinfo.ProcessAgeNs()
	out := flag.String("o", "", "render offscreen at 2114×1278 px into this PNG and exit")
	exitFirst := flag.Bool("exit-first", false, "print the startup timeline as JSON and exit once the first frame is on glass")
	bench := flag.Bool("bench", false, "re-render the scene every frame and print allocation/CPU/pacing as JSON")
	verbose := flag.Bool("v", false, "print the startup stage timings to stderr")
	allowUnpaced := flag.Bool("allow-unpaced", false, "bench: accept unpaced frames (display asleep); exit with status 3 if allocations or CPU budgets fail")
	flag.Parse()

	if *out != "" {
		if err := renderPNG(*out); err != nil {
			fmt.Fprintln(os.Stderr, "rexlook:", err)
			os.Exit(1)
		}
		return
	}
	if err := runWindow(tMain, age, *exitFirst, *bench, *allowUnpaced, *verbose); err != nil {
		fmt.Fprintln(os.Stderr, "rexlook:", err)
		os.Exit(1)
	}
}

// renderPNG draws the scene offscreen at the reference size and writes it,
// with the layout metrics the compare tool checks.
func renderPNG(path string) error {
	runtime.LockOSThread() // the renderer pushes autorelease pools, which are per thread
	ts, err := text.New()
	if err != nil {
		return err
	}
	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		return err
	}
	defer r.Destroy()
	sc := newScene(ts, r, refW, refH, 2, true)
	sc.frame()
	pix := make([]byte, refW*refH*4)
	r.RenderToRGBA(refW, refH, clearColor, &sc.list, pix)

	// The scene is opaque; the window's rounded corners become transparency
	// (as in a window capture without its shadow). The corner is a
	// superellipse (|x|³+|y|³ = 1 over 45 px), fitted to the reference's alpha.
	// Colours stay straight.
	img := &image.NRGBA{Pix: pix, Stride: refW * 4, Rect: image.Rect(0, 0, refW, refH)}
	const cornerA = 45
	for y := 0; y < cornerA; y++ {
		for x := 0; x < cornerA; x++ {
			in := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					u := 1 - (float64(x)+(float64(sx)+0.5)/4)/cornerA
					v := 1 - (float64(y)+(float64(sy)+0.5)/4)/cornerA
					if u*u*u+v*v*v <= 1 {
						in++
					}
				}
			}
			a := uint8((in*255 + 8) / 16)
			for _, p := range [][2]int{{x, y}, {refW - 1 - x, y}, {x, refH - 1 - y}, {refW - 1 - x, refH - 1 - y}} {
				img.Pix[img.PixOffset(p[0], p[1])+3] = a
			}
		}
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
	m := map[string]any{
		"cellAdvancePt": float64(sc.adv) / float64(sc.s),
		"rowPitchPt":    float64(sc.rowPitch) / float64(sc.s),
		"monoFace":      sc.monoFace,
		"monoSizePt":    float64(sc.monoSizeP),
		"quads":         len(sc.list.Quads),
		"drawCalls":     r.DrawCalls(),
		"note":          "layout geometry of the cell grid: advance of the monospace font in use, row pitch of the grid",
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(path+".metrics.json", b, 0o644); err != nil {
		return err
	}
	fmt.Printf("%d quads, %d draw calls -> %s\n", len(sc.list.Quads), r.DrawCalls(), path)
	return nil
}

func runWindow(tMain, age int64, exitFirst, bench, allowUnpaced, verbose bool) error {
	ms := func(ns int64) float64 { return float64(ns) / 1e6 }
	execNs := int64(-1)
	rep := startupReport{ExecToMainMs: -1, ExecToOnGlassMs: -1, ExecToFirstPresentMs: -1}
	if age >= 0 {
		execNs = tMain - age
		rep.ExecToMainMs = ms(age)
	}
	sinceExec := func(t int64) float64 {
		if execNs < 0 {
			return -1
		}
		return ms(t - execNs)
	}
	initThreadCPU()

	win, err := platform.Open(platform.Options{
		Title: "rexlook", Width: winW, Height: winH, Hidden: true,
		Titlebar: 46, TrafficLights: [2]float32{21, 22},
	})
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	tOpen := platform.Now()
	rep.MainToOpenMs = ms(tOpen - tMain)

	w, h, scale := win.Size()
	tStage := platform.Now()
	stage := func(name string) {
		if verbose {
			now := platform.Now()
			fmt.Fprintf(os.Stderr, "rexlook: %-10s %6.2f ms\n", name, ms(now-tStage))
			tStage = now
		}
	}
	surf, err := gpu.NewSurface(win.Native(), w, h, scale)
	if err != nil {
		return fmt.Errorf("surface: %w", err)
	}
	win.SetFrameSource(surf.FrameSource())
	surf.RevealOnFirstFrame(win.Show) // content-first: the window appears with its first frame
	stage("surface")
	r, err := gpu.NewRenderer(surf.Device(), surf.Queue())
	if err != nil {
		return fmt.Errorf("renderer: %w", err)
	}
	stage("renderer")
	ts, err := text.New()
	if err != nil {
		return err
	}
	stage("text.New")

	// Scene: build the items (shapes, shaped glyphs, icons), then one warm
	// frame that rasterises every glyph and uploads the atlas pages.
	tScene0 := platform.Now()
	sc := newScene(ts, r, int(w), int(h), scale, false)
	tScene1 := platform.Now()
	sc.frame()
	tWarm := platform.Now()
	rep.TextMs = ms(sc.textNs + (tWarm - tScene1))
	rep.SceneMs = ms((tScene1 - tScene0) - sc.textNs)
	stage("scene+warm")

	var fr gpu.Frame
	draw := func() bool {
		if !surf.BeginFrame(&fr) {
			return false
		}
		r.Encode(fr.CmdBuf, fr.Target, fr.W, fr.H, clearColor, &sc.list)
		surf.EndFrame(&fr)
		return true
	}
	if !draw() { // draw-first startup: before the first Wait runs AppKit's deferred work
		return fmt.Errorf("no drawable for the first frame")
	}
	tFirst := platform.Now()
	stage("1st frame")
	rep.OpenToFirstFrameMs = ms(tFirst - tOpen)
	rep.ExecToFirstPresentMs = sinceExec(tFirst)

	var (
		needDraw    = bench // bench: every frame; else only on resize / visibility
		frameReady  = bench
		paced       = true
		requested   int64
		drawn       int
		lastDraw    int64
		intervals   = make([]float64, 0, measFrames+16)
		mem0, mem1  runtime.MemStats
		cpu0, tcpu0 int64
		done        bool
	)
	if bench {
		win.RequestFrame()
		requested = platform.Now()
		frameReady = false
	}
	startWait := platform.Now()

	for !done {
		timeout := int64(-1)
		switch {
		case needDraw && frameReady:
			timeout = 1
		case exitFirst:
			timeout = int64(5 * time.Millisecond)
		case bench && paced && requested != 0:
			timeout = max(requested+int64(time.Second)-platform.Now(), 0)
		}
		for _, e := range win.Wait(timeout) {
			switch e.Kind {
			case platform.EvClose:
				done = true
			case platform.EvKey:
				if e.Flags&platform.FlagDown != 0 && e.Key == platform.KeyEscape {
					done = true
				}
			case platform.EvResize:
				surf.Resize(e.W, e.H, e.Scale)
				sc.build(int(e.W), int(e.H), e.Scale)
				sc.frame()
				needDraw, frameReady = true, false
				win.RequestFrame()
			case platform.EvVisible:
				if e.Flags&platform.FlagDown != 0 {
					needDraw, frameReady = true, false
					win.RequestFrame()
				}
			case platform.EvFrame:
				frameReady = true
			}
		}

		if exitFirst {
			if surf.Trace.OnGlass.Load() != 0 || platform.Now()-startWait > int64(2*time.Second) {
				if t := surf.Trace.OnGlass.Load(); t != 0 {
					rep.ExecToOnGlassMs = sinceExec(t)
				}
				printJSON(rep)
				done = true
			}
			continue
		}
		if bench && !frameReady && paced && requested != 0 && platform.Now()-requested > int64(time.Second) {
			paced, frameReady = false, true // display link silent: asleep or occluded
		}
		if needDraw && frameReady {
			now := platform.Now()
			if bench {
				if drawn == warmFrames {
					runtime.GC()
					runtime.ReadMemStats(&mem0)
					cpu0, tcpu0 = sysinfo.CPUNs(), threadCPUNs()
				}
				if drawn > warmFrames && lastDraw != 0 {
					intervals = append(intervals, ms(now-lastDraw))
				}
				lastDraw = now
				sc.frame() // the realistic per-frame cost: rebuild the draw list from cached runs
				win.RequestFrame()
				requested = platform.Now()
				frameReady = !paced
			} else {
				needDraw, frameReady = false, false
			}
			if !draw() {
				// No drawable (occluded, or this tick's drawable expired): the
				// repaint is still owed, so draw on the next frame tick.
				needDraw, frameReady = true, false
				win.RequestFrame()
				continue
			}
			drawn++
			if bench && drawn == warmFrames+measFrames {
				cpu1, tcpu1 := sysinfo.CPUNs(), threadCPUNs()
				runtime.ReadMemStats(&mem1)
				br := benchReport{
					Frames: measFrames, Paced: paced, Quads: len(sc.list.Quads), DrawCalls: r.DrawCalls(),
					Mallocs: mem1.Mallocs - mem0.Mallocs, TotalAlloc: mem1.TotalAlloc - mem0.TotalAlloc, NumGC: mem1.NumGC - mem0.NumGC,
					ProcessCPUus:    float64(cpu1-cpu0) / 1e3 / measFrames,
					MainThreadCPUus: float64(tcpu1-tcpu0) / 1e3 / measFrames,
					FrameIntervalMs: summarize(intervals),
					MonoFace:        sc.monoFace, MonoSizePt: float64(sc.monoSizeP),
					CellAdvancePt: float64(sc.adv) / float64(sc.s),
				}
				printJSON(br)
				if !paced && !allowUnpaced {
					fmt.Fprintln(os.Stderr, "rexlook: frames were not paced (no frame callbacks); use -allow-unpaced for a sleeping display")
					defer os.Exit(3)
				}
				if br.Mallocs != 0 || br.TotalAlloc != 0 || br.MainThreadCPUus > 1000 {
					fmt.Fprintf(os.Stderr, "rexlook: budget failed (allocs %d, main-thread CPU %.0f µs/frame)\n", br.Mallocs, br.MainThreadCPUus)
					defer os.Exit(3)
				}
				done = true
			}
		}
	}
	surf.Destroy()
	win.Close()
	return nil
}
