// Package app owns the frame loop (DECISIONS D22). It ties platform, gpu,
// text and ui together so that an application only writes a Builder:
//
//	type myApp struct{ ... }
//	func (m *myApp) Build(c *ui.Context) { ...boxes... }
//	func main() { app.Run(app.Config{Title: "x", Width: 960, Height: 600}, &myApp{}) }
//
// # What Run does
//
//   - Content-first startup: the window opens hidden, the first frame is
//     built and drawn before the first Wait, and the window appears together
//     with it (macOS: gpu.Surface.RevealOnFirstFrame; elsewhere Show is a
//     no-op and the window is already visible). The first frame is built
//     twice so that layout-dependent builders (scrollbars sized from their
//     list) start from a settled layout.
//   - An event-driven loop: platform events become ui.Input (input.go), a
//     frame is drawn only when something is owed (input, resize,
//     visibility, ui.Context.Animating or RequestFrame, a Wake) and a frame
//     slot is free, and an idle app presents nothing (pacer.go has the
//     rules, including the occluded/asleep fallback and the repaint that
//     stays owed when no frame can be started).
//   - The frame handler for OS modal loops (macOS live resize, the Windows
//     size/move loop): it takes the pending events, resizes the surface and
//     draws in step with the window.
//   - Atlas sync, the cursor regions and window drag areas of the finished
//     frame handed to the window (regions.go), input-to-commit latency
//     sampling.
//
// # Threading
//
// Everything runs on the OS main thread (platform and this package lock the
// main goroutine to it in init). Build, Init and Script are called from it.
//
// # Memory
//
// The per-frame path allocates nothing once the builder's own state is warm:
// the Builder is an interface value (no closure per frame), events and input
// are fixed storage. Apps that shape unbounded numbers of new strings should
// call App.Text.ReserveRuns in Init so the run cache never grows.
package app

import (
	"errors"
	"fmt"
	"runtime"

	"gonativeui/gpu"
	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
	"gonativeui/text"
	"gonativeui/ui"
)

func init() { runtime.LockOSThread() }

// Builder builds the UI of one frame. It is called between ui.Context.Begin
// and End, so the Input of the frame is already applied and the builder only
// declares boxes and reads signals.
type Builder interface{ Build(c *ui.Context) }

// Initer is optionally implemented by a Builder. Init runs once after the
// window, surface, renderer, text system and ui context exist, before the
// first frame. In RenderOffscreen only UI, Text and R are set (Win and Surf
// are nil).
type Initer interface{ Init(a *App) error }

// Scripter is optionally implemented by a Builder, for benchmarks and tests.
// Script runs before Build in every frame of RunBench and RenderOffscreen
// (never in Run). It fills in with synthetic input exactly as translated
// platform events would (pointer position and Inside, SetButton, AddScroll,
// PushKey...); the pending per-frame parts are cleared after each frame.
// frame counts the frames drawn so far from 0. Returning false means the
// script is over: that frame is still drawn, without more input, and the
// run ends.
type Scripter interface {
	Script(frame int, in *ui.Input) bool
}

// Config describes the window and the frame.
type Config struct {
	Title         string
	Width, Height float32 // content size in points
	MinWidth      float32 // smallest content size the user can resize to, in points; 0: no limit
	MinHeight     float32
	Titlebar      float32    // macOS custom titlebar height (pt); ignored elsewhere
	TrafficLights [2]float32 // macOS close-button centre (pt); ignored elsewhere
	Clear         [4]float32 // clear colour, with the semantics of gpu.Renderer.Encode's clear
}

// App is the running application: the window, the GPU objects and the ui
// context, for the Builder to use from Init and Build.
type App struct {
	Win  *platform.Window // nil in RenderOffscreen
	Surf *gpu.Surface     // nil in RenderOffscreen
	R    *gpu.Renderer
	Text *text.System
	UI   *ui.Context

	cfg    Config
	b      Builder
	script Scripter // set by RunBench and RenderOffscreen only

	tr   translator
	p    pacer
	list gpu.DrawList
	fr   gpu.Frame

	w, h  int     // surface size in pixels
	scale float32 // pixels per point

	// Cursor regions and the titlebar drag map handed to the window after
	// every frame (regions.go); the buffers are reused.
	curUI   []ui.CursorRegion
	curPlat []platform.CursorRegion
	dragUI  []ui.DragRegion
	dragPl  []platform.DragRegion

	wakeAt      int64 // platform.Now at which the UI wants a frame although nothing animates (ui.Context.WakeAfter); 0 = none
	resized     bool  // a resize since the last build
	settle      bool  // the frame being built is the first after a resize (see owed)
	done        bool
	more        bool // the last frame left a repaint owed (animation, input to settle)
	scriptFrame int
	scriptDone  bool

	frames      int   // frames presented
	frameEvents int   // EvFrame events received (frame-source ticks)
	lastStart   int64 // platform.Now of the last frame's start; 0 before the first
	fm          frameMeasure
	lat         sampler // input-to-commit in Run, ms

	execNs int64 // platform.Now at process start, -1 if unknown

	bench *benchRun
	hook  func() // runs after every iteration of the loop (RunLiveResizeTest)

	req frameRequester // where drawFrame asks for the next frame (the Win)

	// Diagnostics (diag.go).
	owedWhy     owedMask // why the last frame left another owed
	beginFails  int      // draw attempts that found no frame to draw into
	consecFails int      // ... in a row
	lastFrameEv int64    // platform.Now of the last EvFrame, 0 = none
	lastDrawT   int64    // platform.Now of the last frame drawn (of the start before the first)
	lastDumpT   int64
	verbose     bool
	diagBuf     []byte
}

// frameRequester is the part of platform.Window that arms the next frame.
type frameRequester interface{ RequestFrame() }

// Quit ends the run after the current iteration.
func (a *App) Quit() { a.done = true }

// Size is the surface size in pixels and the pixels per point.
func (a *App) Size() (w, h int, scale float32) { return a.w, a.h, a.scale }

// frameMeasure is what drawFrame measured about the frame it just drew.
type frameMeasure struct {
	start, commit    int64 // platform.Now: frame start (drawable acquired) and EndFrame returned
	evT              int64 // platform time of the oldest input event behind the frame, 0 if none
	cpu0, cpu1, cpu2 int64 // thread CPU ns: before BeginFrame, after BeginFrame, after ui.End
	cpu3             int64 // after EndFrame
}

// StartupReport is the startup timeline of Run (RunOptions.ExitFirst prints
// it). Times are milliseconds; -1 is unknown.
type StartupReport struct {
	ExecToMainMs         float64 // process start → Run entered
	OpenMs               float64 // platform.Open
	SurfaceMs            float64 // gpu surface and frame source
	RendererMs           float64
	TextMs               float64 // text.New
	InitMs               float64 // ui.New, atlas sync and Initer.Init
	FirstFrameMs         float64 // Open returned → first EndFrame returned
	ExecToFirstPresentMs float64
	ExecToOnGlassMs      float64 // first frame on the display, from the OS present feedback (macOS)
}

// RunOptions are the optional parts of RunWith.
type RunOptions struct {
	// Verbose prints a one-line state dump (see StateLine) to stderr every
	// second while a repaint is owed and no frame has been drawn for more than
	// a second.
	Verbose bool

	// ExitFirst ends the run once the first frame is on glass (macOS: the
	// presented feedback; elsewhere right after it is committed), for
	// startup measurement.
	ExitFirst bool
}

// RunResult is what RunWith learned.
type RunResult struct {
	Startup StartupReport
	Frames  int
	// InputToCommitMs: from the platform timestamp of the oldest input event
	// behind a frame to the return of that frame's EndFrame (the commit),
	// over the last 8192 input-driven frames.
	InputToCommitMs Stats
}

// Run opens the window and runs the application until it is closed.
func Run(cfg Config, b Builder) error {
	_, err := RunWith(cfg, b, RunOptions{})
	return err
}

// RunWith is Run with options and a result.
func RunWith(cfg Config, b Builder, o RunOptions) (RunResult, error) {
	var res RunResult
	a, err := openWindowed(cfg, b, &res.Startup)
	if err != nil {
		return res, err
	}
	defer a.close()
	a.verbose = o.Verbose
	if !o.ExitFirst {
		a.Win.SetFrameHandler(a.frameHandler)
	}
	if o.ExitFirst {
		a.waitOnGlass(&res.Startup)
	} else if err := a.loop(); err != nil {
		return res, err
	}
	res.Frames = a.frames
	res.InputToCommitMs = Summarize(a.lat.values())
	return res, nil
}

// openWindowed creates everything and draws the first frame.
func openWindowed(cfg Config, b Builder, st *StartupReport) (*App, error) {
	tEntry := platform.Now()
	age := sysinfo.ProcessAgeNs()
	execNs := int64(-1)
	st.ExecToMainMs, st.ExecToOnGlassMs, st.ExecToFirstPresentMs = -1, -1, -1
	if age >= 0 {
		execNs = tEntry - age
		st.ExecToMainMs = ms(age)
	}

	a := &App{cfg: cfg, b: b, tr: newTranslator(), p: newPacer(), lat: newSampler(8192), diagBuf: make([]byte, 0, 2048)}
	a.lastDrawT = tEntry
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, errors.New("app: Config.Width and Height must be positive")
	}
	win, err := platform.Open(platform.Options{
		Title: cfg.Title, Width: int(cfg.Width + 0.5), Height: int(cfg.Height + 0.5), Hidden: true,
		MinWidth: int(cfg.MinWidth + 0.5), MinHeight: int(cfg.MinHeight + 0.5),
		Titlebar: cfg.Titlebar, TrafficLights: cfg.TrafficLights,
	})
	if err != nil {
		return nil, fmt.Errorf("app: open window: %w", err)
	}
	a.Win, a.req = win, win
	t0 := platform.Now()
	st.OpenMs = ms(t0 - tEntry)

	w, h, scale := win.Size()
	a.w, a.h, a.scale = int(w), int(h), scale
	a.tr.scale = scale
	surf, err := gpu.NewSurface(win.Native(), w, h, scale)
	if err != nil {
		win.Close()
		return nil, fmt.Errorf("app: surface: %w", err)
	}
	a.Surf = surf
	win.SetFrameSource(surf.FrameSource())
	surf.RevealOnFirstFrame(win.Show) // content-first: the window appears with its first frame
	t1 := platform.Now()
	st.SurfaceMs = ms(t1 - t0)

	if a.R, err = gpu.NewRenderer(surf.Device(), surf.Queue()); err != nil {
		a.close()
		return nil, fmt.Errorf("app: renderer: %w", err)
	}
	t2 := platform.Now()
	st.RendererMs = ms(t2 - t1)

	if a.Text, err = text.New(); err != nil {
		a.close()
		return nil, fmt.Errorf("app: text: %w", err)
	}
	t3 := platform.Now()
	st.TextMs = ms(t3 - t2)

	if err := a.initUI(); err != nil {
		a.close()
		return nil, err
	}
	t4 := platform.Now()
	st.InitMs = ms(t4 - t3)

	// Draw-first: before the first Wait, which runs AppKit's deferred launch work.
	if !a.present(false, 2) {
		a.close()
		return nil, errors.New("app: no frame available for the first frame")
	}
	t5 := platform.Now()
	st.FirstFrameMs = ms(t5 - t0)
	if execNs >= 0 {
		st.ExecToFirstPresentMs = ms(t5 - execNs)
	}
	a.execNs = execNs
	return a, nil
}

// initUI creates the ui context and runs the Initer.
func (a *App) initUI() error {
	a.UI = ui.New(a.Text)
	a.UI.ClipboardGet, a.UI.ClipboardSet = platform.Clipboard, platform.SetClipboard
	a.UI.AtlasSync(a.R)
	if in, ok := a.b.(Initer); ok {
		if err := in.Init(a); err != nil {
			return fmt.Errorf("app: init: %w", err)
		}
	}
	return nil
}

func ms(ns int64) float64 { return float64(ns) / 1e6 }

// close releases everything in reverse order of creation. It is safe on a
// partially built App.
func (a *App) close() {
	if a.R != nil {
		a.R.Destroy()
	}
	if a.Surf != nil {
		a.Surf.Destroy()
	}
	if a.Win != nil {
		a.Win.Close()
	}
	if a.Text != nil {
		a.Text.Close()
	}
	a.R, a.Surf, a.Win, a.Text = nil, nil, nil, nil
}

// loop is the event loop. See pacer for the frame rules.
func (a *App) loop() error {
	for !a.done {
		now := platform.Now()
		if a.bench != nil && now > a.bench.deadline {
			return a.benchTimeout(now)
		}
		evs := a.Win.Wait(a.timeout(now))
		a.tr.wheelStep = a.UI.WheelStep
		for i := range evs {
			a.onEvent(&evs[i])
		}
		if a.done {
			break
		}
		now = platform.Now()
		if a.wakeAt != 0 && now >= a.wakeAt {
			a.wakeAt = 0
			a.p.invalidate() // a text field's caret blink is due
		}
		if a.verbose {
			a.dumpIfStalled(now)
		}
		draw, request := a.p.poll(now)
		if request {
			a.Win.RequestFrame()
			a.p.requested(now)
		}
		if draw {
			a.present(false, 1)
		}
		if a.hook != nil {
			a.hook()
		}
	}
	return nil
}

// timeout is the Wait timeout; a bench also wakes in time for its deadline.
func (a *App) timeout(now int64) int64 {
	t := a.p.timeout(now)
	if a.wakeAt != 0 { // the UI asked for a frame at a time (WakeAfter)
		if d := max(a.wakeAt-now, 0); t < 0 || d < t {
			t = d
		}
	}
	if a.bench != nil {
		if d := max(a.bench.deadline-now, 0); t < 0 || d < t {
			t = d
		}
	}
	return t
}

// onEvent handles one platform event.
func (a *App) onEvent(e *platform.Event) {
	switch e.Kind {
	case platform.EvClose:
		a.done = true
	case platform.EvResize:
		if e.W > 0 && e.H > 0 {
			a.w, a.h, a.scale = int(e.W), int(e.H), e.Scale
			a.tr.scale = e.Scale
			if a.Surf != nil {
				a.Surf.Resize(e.W, e.H, e.Scale)
			}
			a.resized = true
			a.tr.dirty = true // repaint (and settle the hover), but a resize is not input latency
			a.p.invalidate()
		}
	case platform.EvVisible:
		if e.Flags&platform.FlagDown != 0 {
			a.p.invalidate() // shown again: what was skipped while hidden is owed
		}
	case platform.EvWake:
		a.p.invalidate() // a worker published something
	case platform.EvFrame:
		a.frameEvents++
		a.lastFrameEv = platform.Now()
		a.p.frameEvent()
	default:
		var txt []byte
		if e.Kind == platform.EvText || e.Kind == platform.EvPreedit {
			txt = a.Win.Text(e.Text)
		}
		if a.tr.event(e, txt) {
			a.p.invalidate()
		}
	}
}

// frameHandler runs inside OS modal loops (live resize), where Wait does not
// return: it handles what is pending and draws at once, in step with the
// window.
func (a *App) frameHandler() {
	a.tr.wheelStep = a.UI.WheelStep
	evs := a.Win.Pending()
	for i := range evs {
		a.onEvent(&evs[i])
	}
	if a.p.needDraw && !a.done {
		a.present(true, 1)
	}
}

// present attempts one frame: it reports false (and leaves the repaint owed)
// when no frame could be started. passes > 1 builds that many times and
// draws the last (the first frame).
func (a *App) present(inHandler bool, passes int) bool {
	if bn := a.bench; bn != nil {
		bn.before(a)
	}
	ok := a.drawFrame(inHandler, passes)
	now := platform.Now()
	if ok {
		a.consecFails, a.lastDrawT = 0, now
		a.p.drawn(a.more)
		if bn := a.bench; bn != nil {
			bn.after(a)
		}
		return true
	}
	a.beginFails++
	a.consecFails++
	// Nothing was painted: the repaint is still owed, and so is the frame
	// request that will bring the slot to draw it in.
	a.p.failed(now)
	a.Win.RequestFrame()
	a.p.requested(now)
	return false
}

// waitOnGlass pumps events until the first frame is on the display (macOS) or
// two seconds passed, then fills in the on-glass time.
func (a *App) waitOnGlass(st *StartupReport) {
	start := platform.Now()
	for {
		for _, e := range a.Win.Wait(int64(5e6)) {
			if e.Kind == platform.EvClose {
				return
			}
		}
		if t := a.Surf.Trace.OnGlass.Load(); t != 0 {
			if a.execNs >= 0 {
				st.ExecToOnGlassMs = ms(t - a.execNs)
			}
			return
		}
		if runtime.GOOS != "darwin" || platform.Now()-start > int64(2e9) {
			return // no present feedback on this backend, or it did not come
		}
	}
}
