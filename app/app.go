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
//     with it (gpu.Surface.RevealOnFirstFrame: macOS orders the window in,
//     Windows uncloaks it, X11 maps it with the frame; a Wayland window
//     appears with its first buffer). The first frame is built
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
	"gonativeui/menu"
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
// run ends. It ends for good: RunBench finishes its window and the Offscreen
// never asks the Scripter again, so a test that plays one script after another
// returns true between them and false only when it is done, as
// widget/widgettest.Rig does for the window it drives.
type Scripter interface {
	Script(frame int, in *ui.Input) bool
}

// Config describes the window and the frame.
type Config struct {
	// Title is the window title.
	Title         string
	Width, Height float32 // content size in points
	MinWidth      float32 // smallest content size the user can resize to, in points; 0: no limit
	MinHeight     float32
	Titlebar      float32    // macOS custom titlebar height (pt); ignored elsewhere
	TrafficLights [2]float32 // macOS close-button centre (pt); ignored elsewhere
	Clear         [4]float32 // clear colour, with the semantics of gpu.Renderer.Encode's clear

	// Backdrop and Buttons are the native shell of platform.Options (macOS;
	// experimental). When the window gets the backdrop
	// (platform.Window.Backdrop), frames clear to transparent instead of
	// Clear, so the material shows wherever the builder paints nothing.
	// Button clicks arrive as ui commands (ui.Context.TakeCommand) with the
	// buttons' ids; App.Win.SetButtonEnabled enables and disables them.
	Backdrop platform.Backdrop
	Buttons  []platform.NativeButton
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
	curUI    []ui.CursorRegion
	curPlat  []platform.CursorRegion
	dragUI   []ui.DragRegion
	dragPl   []platform.DragRegion
	tipUI    []ui.TooltipRegion // native tooltip regions (regions.go), reused
	tipPl    []platform.TooltipRegion
	dlg      []platform.DialogResult // dialog results not taken yet (dialogs.go)
	dlgQ     []pendingDialog         // dialogs queued for the end of the frame
	dlgShown []shownDialog           // dialogs on screen
	dlgSeq   platform.DialogTicket   // the last ticket given
	dlgT     dialogTarget            // the window's dialogs; tests set a fake

	wakeAt      int64 // platform.Now at which the UI wants a frame although nothing animates (ui.Context.WakeAfter); 0 = none
	chromeDark  bool  // the mode last given to Win.SetDarkChrome
	chromeSet   bool  // SetDarkChrome was called
	resized     bool  // a resize since the last build
	settle      bool  // the frame being built is the first after a resize (see owed)
	done        bool
	more        bool // the last frame left a repaint owed (animation, input to settle)
	scriptFrame int
	scriptDone  bool

	frames      int   // frames presented
	inFrame     bool  // between BeginFrame and EndFrame (drawFrame)
	reentries   int   // frame handler calls refused because a frame was in progress
	frameEvents int   // EvFrame events received (frame-source ticks)
	lastStart   int64 // platform.Now of the last frame's start; 0 before the first
	fm          frameMeasure
	lat         sampler // input-to-commit in Run, ms

	execNs int64 // platform.Now at process start, -1 if unknown

	bench *benchRun
	hook  func() // runs after every iteration of the loop (RunLiveResizeTest)

	req frameRequester // where drawFrame asks for the next frame (the Win)
	// buttons is the Win's buttonsSource, nil where it has none: resolved
	// once in initUI, because the first type assertion of a frame would fill
	// the runtime's assertion cache (a 48 B allocation on a random frame).
	buttons buttonsSource

	// Native menus (menus.go).
	menu        *menu.Model
	menuDirty   bool // the bar is to be (re)built at the next safe point
	menuSpec    platform.MenuSpec
	menuStateFn platform.MenuState
	fieldKeysFn platform.FieldKeys
	listSpec    platform.MenuSpec // the options of a select, reused
	listItems   []platform.MenuItem
	demo        *menuDemo // GNUI_MENU_DEMO (menudemo.go)

	appObject // the application object: Main's handler, app events, the quit policy (lifecycle.go)

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

// Quit ends the run after the current iteration. It wakes the loop, which
// may be waiting with no window open.
func (a *App) Quit() {
	a.done = true
	platform.Wake()
}

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
	// Startup is the start-up timeline.
	Startup StartupReport
	// Frames is the number of frames drawn.
	Frames int
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
	a, err := openWindowed(cfg, b, &res.Startup, true)
	if err != nil {
		return res, err
	}
	defer a.close()
	a.verbose = o.Verbose
	if !o.ExitFirst {
		a.Win.SetFrameHandler(a.frameHandler)
		a.demo = newMenuDemo()
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

// openWindowed creates everything and draws the first frame. nativeMenus
// lets the widgets use the native menus where the OS has them (Run); scripted
// runs keep the drawn ones, which their scripts drive. Every windowed run gets
// the native menu bar.
func openWindowed(cfg Config, b Builder, st *StartupReport, nativeMenus bool) (*App, error) {
	tEntry, age := platform.Now(), sysinfo.ProcessAgeNs()
	a := newApp()
	a.handler = DefaultHandler{}
	if err := a.bindApp(false); err != nil {
		return nil, err
	}
	if err := a.openWin(cfg, b, st, nativeMenus, tEntry, age); err != nil {
		a.unbindApp()
		return nil, err
	}
	a.started = true
	a.flushStartup()
	return a, nil
}

// openWin opens the window with builder b into a, and draws its first frame;
// tEntry is when the opening started (the startup report's origin) and age
// the process's age then (-1 unknown).
func (a *App) openWin(cfg Config, b Builder, st *StartupReport, nativeMenus bool, tEntry, age int64) error {
	execNs := int64(-1)
	st.ExecToMainMs, st.ExecToOnGlassMs, st.ExecToFirstPresentMs = -1, -1, -1
	if age >= 0 {
		execNs = tEntry - age
		st.ExecToMainMs = ms(age)
	}

	a.cfg, a.b = cfg, b
	if a.started { // a window again, after the last one closed (KeepRunning)
		a.tr, a.p, a.frames, a.menuDirty = newTranslator(), newPacer(), 0, true
	}
	a.lastDrawT = tEntry
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return errors.New("app: Config.Width and Height must be positive")
	}
	win, err := platform.Open(platform.Options{
		Title: cfg.Title, Width: int(cfg.Width + 0.5), Height: int(cfg.Height + 0.5), Hidden: true,
		MinWidth: int(cfg.MinWidth + 0.5), MinHeight: int(cfg.MinHeight + 0.5),
		Titlebar: cfg.Titlebar, TrafficLights: cfg.TrafficLights,
		Backdrop: cfg.Backdrop, Buttons: cfg.Buttons,
	})
	if err != nil {
		return fmt.Errorf("app: open window: %w", err)
	}
	if win.Backdrop() != platform.BackdropNone {
		a.cfg.Clear = [4]float32{} // premultiplied transparent: the backdrop shows
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
		a.Win, a.req = nil, nil
		return fmt.Errorf("app: surface: %w", err)
	}
	a.Surf = surf
	win.SetFrameSource(surf.FrameSource())
	if !a.startHidden || a.started {
		surf.RevealOnFirstFrame(win.Show) // content-first: the window appears with its first frame
	}
	t1 := platform.Now()
	st.SurfaceMs = ms(t1 - t0)

	if a.R, err = gpu.NewRenderer(surf.Device(), surf.Queue()); err != nil {
		a.closeWin()
		return fmt.Errorf("app: renderer: %w", err)
	}
	t2 := platform.Now()
	st.RendererMs = ms(t2 - t1)

	if a.Text, err = text.New(); err != nil {
		a.closeWin()
		return fmt.Errorf("app: text: %w", err)
	}
	t3 := platform.Now()
	st.TextMs = ms(t3 - t2)

	if err := a.initUI(); err != nil {
		a.closeWin()
		return err
	}
	a.UI.SetNativeMenus(nativeMenus && platform.HasNativeMenus())
	t4 := platform.Now()
	st.InitMs = ms(t4 - t3)

	// Draw-first: before the first Wait, which runs AppKit's deferred launch work.
	if !a.present(false, 2) {
		a.closeWin()
		return errors.New("app: no frame available for the first frame")
	}
	t5 := platform.Now()
	st.FirstFrameMs = ms(t5 - t0)
	if execNs >= 0 {
		st.ExecToFirstPresentMs = ms(t5 - execNs)
	}
	a.execNs = execNs
	return nil
}

// initUI creates the ui context and runs the Initer.
func (a *App) initUI() error {
	a.UI = ui.New(a.Text)
	a.UI.ClipboardSet = platform.SetClipboardText
	a.UI.AtlasSync(a.R)
	if a.Win != nil { // a window shows tooltips for the regions it is given; offscreen renders draw them
		a.UI.SetNativeTooltips(platform.NativeTooltips)
		a.Win.SetTooltipText(a.UI.TooltipText)
		a.buttons, _ = windowSource(a.Win).(buttonsSource)
	}
	if in, ok := a.b.(Initer); ok {
		if err := in.Init(a); err != nil {
			return fmt.Errorf("app: init: %w", err)
		}
	}
	return nil
}

func ms(ns int64) float64 { return float64(ns) / 1e6 }

// close releases everything in reverse order of creation and, for a Run, the
// application object. It is safe on a partially built App and when called
// again.
func (a *App) close() {
	a.closeWin()
	if !a.mainApp {
		a.unbindApp()
	}
}

// closeWin releases the window and its GPU and text objects.
func (a *App) closeWin() {
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
	a.buttons = nil
}

// loop is the event loop. See pacer for the frame rules. With no window open
// it is the application object's pump (idleTurn).
func (a *App) loop() error {
	a.launch()
	for !a.done {
		if a.Win == nil {
			a.idleTurn()
			continue
		}
		a.syncMenuBar()
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
		a.popUpMenus()
		if a.demo != nil {
			a.demo.run(a)
		}
		a.flushDialogs() // dialogs asked for outside a frame (an event handler, a hook)
		if a.hook != nil {
			a.hook()
		}
		a.appTurn()
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
		a.closeRequested()
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
		if e.Kind == platform.EvText || e.Kind == platform.EvPreedit || e.Kind == platform.EvPaste {
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
//
// It does nothing while a frame is being drawn. AppKit calls it
// synchronously from inside calls the app makes during a frame: anything
// that animates the window as a live resize (toggleFullScreen:, an animated
// zoom:), directly or in a nested run loop (a modal session, menu
// tracking). A frame drawn there would run Build inside Build and draw into
// the frame in progress (once a crash: the outer EndFrame found the frame
// cleared by the inner one). What it would have handled stays queued, and
// the next Wait delivers it.
func (a *App) frameHandler() {
	if a.inFrame {
		a.reentries++
		return
	}
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
	a.flushDialogs() // dialogs asked for in Build: shown now that the frame is over
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
