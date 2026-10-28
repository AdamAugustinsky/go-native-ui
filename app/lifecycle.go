package app

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"gonativeui/gpu"
	"gonativeui/internal/sysinfo"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/text"
	"gonativeui/ui"
)

// The application object (DECISIONS D37): Main, the Handler, the quit policy
// and the app's windows. Run is one window over the same object.
//
//	type notes struct{ app.DefaultHandler }
//	func (n *notes) Start(a *app.App) error { _, err := a.OpenWindow(cfg, view); return err }
//	func (n *notes) Event(a *app.App, e *app.Event) { ...; a.DefaultEvent(e) }
//	err := app.Main(app.AppConfig{ID: "dev.example.notes", KeepRunning: true}, &notes{})
//
// # The quit policy
//
// Every route arrives as EvQuitRequested (QuitMenu, QuitDock, QuitLogout,
// QuitSignal, QuitApp …) and is answered with App.ReplyQuit, by the request's
// ID (a late answer to an older request settles nothing). An answer given
// during the event, or from a call nested in it, is latched and returned to
// the system directly. Without one the answer is deferred: the system waits
// (on macOS, AppKit runs its own loop, during which app events, safe-point
// work, sheets and frames go on) until ReplyQuit, however long that takes,
// unless AppConfig.QuitDeadline is set. Save before allowing: an allowed quit
// ends with EvWillTerminate, after which on macOS the process exits and Main
// does not return.
//
// App.Quit is not a quit request: it ends the loop at once, without asking,
// and Main (or Run) returns. The last window closing does the same unless
// AppConfig.KeepRunning.

// Handler is the application: Start opens its windows, Event receives the
// app events.
type Handler interface {
	// Start runs once, before the first window, with the application object
	// ready. Open windows here (content-first applies: each draws its first
	// frame before it appears). Events that arrive before it returns (a
	// launch by URL) are delivered right after, in order.
	Start(a *App) error
	// Event receives one app event, on the main thread, between frames, in
	// order. A window or sheet asked for here opens at the next safe point.
	Event(a *App, e *Event)
}

// DefaultHandler is the default behaviour, to embed in a Handler: no window
// at Start, and DefaultEvent for every event.
type DefaultHandler struct{}

// Start does nothing.
func (DefaultHandler) Start(a *App) error { return nil }

// Event is App.DefaultEvent.
func (DefaultHandler) Event(a *App, e *Event) { a.DefaultEvent(e) }

// CloseRequester is optionally implemented by a window's Builder: the window
// asks it before it closes, when the user closes it and when the app is asked
// to quit (DefaultEvent). False refuses.
type CloseRequester interface {
	CloseRequested(a *App) bool
}

// DefaultEvent is the default handling of e: a quit request is allowed when
// every window agrees to close (CloseRequester) and refused otherwise; a
// reopen with no visible window shows the most recently opened of the
// windows that are hidden or in the Dock. With no window open (KeepRunning)
// a reopen does nothing here: which window to open is the app's to decide,
// and its Handler opens one (OpenWindow) on EvReopen. Other events need
// nothing.
func (a *App) DefaultEvent(e *Event) {
	switch e.Kind {
	case EvQuitRequested:
		a.ReplyQuit(e.ID, a.windowsAgreeToClose())
	case EvReopen:
		if e.A != 0 {
			return
		}
		if w := a.reopenWindow(); w != nil {
			w.Win.Activate()
		}
	}
}

// reopenWindow is the window a reopen shows: the most recently opened of the
// open windows that are hidden or in the Dock; nil when none is.
func (a *App) reopenWindow() *App {
	for i := len(a.wins) - 1; i >= 0; i-- {
		if w := a.wins[i]; w.Win != nil && !w.closing && (!w.Win.Visible() || w.Win.Miniaturized()) {
			return w
		}
	}
	return nil
}

// windowsAgreeToClose asks the builder of every open window, in the order
// they opened, whether it may close; the first refusal stops there.
func (a *App) windowsAgreeToClose() bool {
	for _, w := range a.wins {
		if cr, ok := w.b.(CloseRequester); ok && !cr.CloseRequested(w) {
			return false
		}
	}
	return true
}

// AppConfig configures the application object (Main).
type AppConfig struct {
	// ID is the app's reverse-DNS identifier ("dev.example.notes"). Empty:
	// the bundle's CFBundleIdentifier. Services key their files and system
	// registrations by it.
	ID string
	// KeepRunning keeps the app running when its last window closes (macOS;
	// elsewhere the app ends with its last window).
	KeepRunning bool
	// StartHidden launches without showing a window (a login item): windows
	// opened during Start stay hidden until EvReopen or Win.Show. The
	// --start-hidden argument sets it too.
	StartHidden bool
	// QuitDeadline, if not 0, bounds how long a quit request may stay
	// unanswered; past it QuitDeadlineAction is applied (QuitCancel by
	// default) and EvQuitDeadline arrives. Zero means no deadline: the app's
	// answer decides, however long it takes.
	QuitDeadline       time.Duration
	QuitDeadlineAction QuitAction
	// Policy is the activation policy: PolicyRegular (a Dock icon) or
	// PolicyAccessory (none).
	Policy Policy
	// WindowTabbing lets macOS group the app's windows into tabs (its
	// automatic window tabbing and the View menu's tab items). Off by
	// default: the app's windows stay windows.
	WindowTabbing bool
}

// Main runs the application: it creates the application object, calls
// h.Start, and runs the app-wide loop, which keeps running with no window
// open. It owns the main thread until the app ends: it returns when App.Quit
// is called or the last window closes (unless KeepRunning). After an allowed
// quit request macOS ends the process instead, after EvWillTerminate.
// SIGINT and SIGTERM become quit requests (QuitSignal); a second one while
// the first is unanswered ends the process.
func Main(cfg AppConfig, h Handler) error {
	if h == nil {
		return errors.New("app: Main needs a Handler")
	}
	a := newApp()
	a.appCfg, a.handler, a.mainApp = cfg, h, true
	a.startHidden = cfg.StartHidden
	for _, arg := range os.Args[1:] {
		if arg == "--start-hidden" {
			a.startHidden = true
		}
	}
	if !a.startHidden {
		gpu.Prewarm() // the renderer's shaders build while the app starts and Start opens its window
	}
	if err := a.bindApp(true); err != nil {
		return err
	}
	defer a.unbindApp()
	if err := h.Start(a); err != nil {
		a.closeAll() // the windows Start opened
		return err
	}
	a.started = true
	a.flushStartup()
	err := a.loop()
	a.finish()
	return err
}

// newApp makes the application object and its first App (the root: the
// Handler's, which holds the first window), with no window yet. Post's
// storage is made here, so that no Post allocates or waits for it.
func newApp() *App {
	platform.PreparePosts()
	a := newWindowApp(&appObject{})
	a.root = a
	return a
}

// newWindowApp makes an App for a window of the application object s.
func newWindowApp(s *appObject) *App {
	return &App{appObject: s, tr: newTranslator(), p: newPacer(), lat: newSampler(8192), diagBuf: make([]byte, 0, 2048), menuDirty: true}
}

// bindApp makes a the receiver of the application object's events, then
// starts the platform's application object (once per process). The receiver
// comes first: launching delivers events (a launch by URL or document arrives
// during finishLaunching), which wait in the startup list until Start has
// returned. If the start fails, a is no receiver any more.
func (a *App) bindApp(signals bool) error {
	a.appStateFn = a.appMenuState
	platform.SetAppSink(a)
	err := platform.StartApp(platform.AppOptions{
		Policy:       a.appCfg.Policy,
		QuitDeadline: a.appCfg.QuitDeadline, QuitDeadlineAction: a.appCfg.QuitDeadlineAction,
		Signals: signals, WindowTabbing: a.appCfg.WindowTabbing,
	})
	if err != nil {
		platform.SetAppSink(nil)
		return fmt.Errorf("app: application object: %w", err)
	}
	return nil
}

func (a *App) unbindApp() { platform.SetAppSink(nil) }

// finish delivers EvWillTerminate (if the system did not) and closes the
// window. Before that last event, as before the system's: the quit state is
// settled (a suspended quit Apple event is answered, so a sender such as a
// logout is not left waiting), every dialog ends as cancelled with its result
// delivered, and what was accepted is delivered (platform.EndApp).
func (a *App) finish() {
	if !a.terminated {
		platform.EndApp()
		for _, w := range a.wins {
			w.cancelQueuedDialogs()
		}
		a.terminated = true
		a.deliver(&platform.AppEvent{Kind: platform.AppEvWillTerminate}, nil)
	}
	a.closeAll()
}

// closeAll closes every window, the last one opened first, and then, for a
// Run, the application object.
func (a *App) closeAll() {
	for len(a.wins) > 0 {
		a.wins[len(a.wins)-1].closeWin()
	}
	a.close()
}

// ID is the app's identifier: AppConfig.ID, or the bundle's.
func (a *App) ID() string {
	if a.appCfg.ID != "" {
		return a.appCfg.ID
	}
	return platform.BundleID()
}

// StartHidden reports whether the app was launched to start hidden
// (AppConfig.StartHidden or --start-hidden).
func (a *App) StartHidden() bool { return a.startHidden }

// ReplyQuit answers quit request id (the EvQuitRequested's ID): true lets the
// app quit, false cancels the quit. Called during EvQuitRequested (or a call
// nested in it), the answer is latched and given to the system when the
// event returns; called later, it is sent at once. An answer for a request
// that is not waiting (answered already, or older than the one waiting now:
// a worker's late reply) is ignored, and ReplyQuit reports false. Main
// thread.
func (a *App) ReplyQuit(id uint64, allow bool) bool { return platform.ReplyQuit(id, allow) }

// QuitPending reports whether a quit request waits for the app's answer.
func (a *App) QuitPending() bool { return platform.QuitPending() }

// RequestQuit asks the app to quit through the quit policy (EvQuitRequested
// with QuitApp), at the loop's next turn. Unlike Quit it can be refused.
// Any goroutine.
func (a *App) RequestQuit() { platform.RequestQuit(platform.QuitApp) }

// WindowConfig describes a window (OpenWindow).
type WindowConfig = Config

// ErrOneWindow is OpenWindow's error for a second window where the OS has
// one window per app in this phase (Windows and Linux; macOS has any number,
// DECISIONS D30). It satisfies errors.Is(err, errors.ErrUnsupported).
var ErrOneWindow = fmt.Errorf("app: a second window: %w", errors.ErrUnsupported)

// manyWindows is whether the OS has more than one window per app.
const manyWindows = runtime.GOOS == "darwin"

// OpenWindow opens a window with builder b and draws its first frame
// (content-first: it appears with that frame, unless the app starts hidden).
// The window has its own App (Win.App), surface, ui context and frame pacing;
// the renderer, the text system and the glyph atlas are the app's. From
// Start or a safe point it opens at once; asked for during an event, a frame
// (Build) or another window's opening, it opens at the next safe point, and a
// failure then is reported by Win.Err. On Windows and Linux a second window
// is refused (ErrOneWindow).
func (a *App) OpenWindow(cfg WindowConfig, b Builder) (*Win, error) {
	if !manyWindows && (len(a.wins) > 0 || a.queuedOpens > 0) {
		return nil, ErrOneWindow
	}
	w := &Win{}
	if a.evDepth > 0 || a.framing > 0 || a.opening > 0 {
		a.queuedOpens++
		a.AtSafePoint(func() {
			a.queuedOpens--
			w.err = a.openWindowNow(cfg, b, w)
		})
		return w, nil
	}
	if err := a.openWindowNow(cfg, b, w); err != nil {
		return nil, err
	}
	return w, nil
}

// openWindowNow opens the window of handle w: into the root App while it
// holds no window (so that the Handler's App is the first window's, as a
// one-window app expects), else into an App of its own.
func (a *App) openWindowNow(cfg WindowConfig, b Builder, w *Win) error {
	if !manyWindows && len(a.wins) > 0 {
		return ErrOneWindow
	}
	t := a.root
	if t == nil || t.Win != nil || t.opening > 0 {
		t = newWindowApp(a.appObject)
	} else if t.held {
		t.renew() // the root's window closed: its place goes to this one, with nothing of the closed one
	}
	// The window's identity comes first: Init and the first Build that ask
	// for it (App.Window) get the handle OpenWindow returns.
	a.nextWinID++
	t.winID, t.handle = a.nextWinID, w
	w.a, w.id = t, a.nextWinID
	var st StartupReport
	a.opening++
	err := t.openWin(cfg, b, &st, true, platform.Now(), sysinfo.ProcessAgeNs())
	a.opening--
	if err != nil {
		t.winID, t.handle, w.a = 0, nil, nil
		w.pw.Store(nil)
		return err
	}
	t.Win.SetFrameHandler(t.frameHandler)
	// Its menus before anything else in this turn of the loop: a window
	// opened at a safe point of the pump may be key already, and the keys
	// the pump takes next are its (menus).
	t.syncMenuBar()
	return nil
}

// renew readies the root App, whose window closed, for the next window it
// holds: everything of the closed window starts afresh, as in a new window's
// App (its input, pacing and frame counts, dialogs and their results, timed
// wakes, regions, menus, debug state, id and commands), so that nothing meant
// for the closed window (a late dialog answer, a WakeIn that comes due, a
// command of its stale Win) reaches the new one; the closed window's Win
// handle stays closed (its id is not the new one's). What the run set up
// stays: a bench and its script, a test's hook and dialog and drag targets,
// the verbose flag and the menu demo, the process's start time.
func (a *App) renew() {
	old := *a
	*a = *newWindowApp(old.appObject)
	a.bench, a.script, a.hook, a.verbose, a.demo, a.execNs = old.bench, old.script, old.hook, old.verbose, old.demo, old.execNs
	a.dlgT, a.dragT = old.dlgT, old.dragT
}

// SetKeepRunning changes AppConfig.KeepRunning while the app runs: from now on
// the app does (true) or does not (false) keep running when its last window
// closes. It does not end an app that has no window open at the time. Call it
// on the main thread, from the Handler or a window's Build or event method.
func (a *App) SetKeepRunning(keep bool) { a.appCfg.KeepRunning = keep }

// closeRequested is the window's close button (EvClose): its CloseRequester
// may refuse; the window closes at the next safe point, and the app ends
// with its last window unless it keeps running.
func (a *App) closeRequested() {
	if cr, ok := a.b.(CloseRequester); ok && !cr.CloseRequested(a) {
		return
	}
	if a.lastOpen() && !(a.mainApp && a.appCfg.KeepRunning && manyWindows) {
		a.done = true
		return
	}
	a.closing = true // at the turn's safe point
	platform.WakeLoop()
}

// lastOpen reports whether a is the only open window not closing already.
func (a *App) lastOpen() bool {
	for _, w := range a.wins {
		if w != a && !w.closing {
			return false
		}
	}
	return true
}

// appObject is the application object: what an app's windows share. Each
// window's App points at it.
type appObject struct {
	handler     Handler
	appCfg      AppConfig
	mainApp     bool // Main runs the app (not Run or a bench)
	started     bool // Start returned (Run: the window is open)
	launched    bool // the late setup ran (AppLaunched)
	terminated  bool // EvWillTerminate was delivered
	startHidden bool
	pumping     bool // the loop is inside the pump or a popped-up menu: no window closes under it, except in AppKit's own wait for a quit answer (closeWin)
	inSafe      bool
	done        bool // the loop ends after this turn (Quit, the last window closing)
	nextWinID   uint32

	evs        [8]Event // the events being delivered, by nesting depth
	evDepth    int
	startup    []startupEvent
	safe       []func()
	safeSpare  []func()
	inspDrain  func() // the agent bridge's work for the main thread (bridge_dev.go); nil unless GNUI_INSPECT is set
	appStateFn platform.MenuState
	dockSpec   platform.MenuSpec

	// The windows (D30).
	root        *App        // the first App (Main's or Run's): the Handler's, and the first window's
	wins        []*App      // the open windows, in the order they opened
	framing     int         // windows between BeginFrame and EndFrame: no frame handler draws and no safe point runs
	opening     int         // OpenWindow is opening a window (its Init runs)
	queuedOpens int         // OpenWindow calls waiting for a safe point
	barModel    *menu.Model // the model of the last window bar given to the platform (menus.go)
	stdBar      *byte       // the identity of the standard bar, the one of every window without a model (menus.go)

	dlgSeq    platform.DialogTicket // the last dialog ticket given (dialogs.go): one numbering for the app
	closedDlg []closedDialog        // the dialogs of closed windows whose answer has not arrived (closedDialog)

	// What the windows draw with, made with the first window and released
	// with the last (window.go).
	retired []retiredWin // windows closed for the app whose platform resources wait for the pump to return (closeWin)

	dev    *gpu.Device // the GPU device and queue, where the OS shares one (macOS)
	noDev  bool        // the OS has no shared device: a surface makes its own
	r      *gpu.Renderer
	text   *text.System
	atlas  *ui.SharedAtlas
	scales []float32 // the scales whose fonts are alive, latest first (zoom.go)
}

// closeWindow closes the window at a safe point; the app ends with its last
// one unless it keeps running.
func (a *App) closeWindow() {
	a.closing = false
	if a.Win == nil {
		return
	}
	a.cancelQueuedDialogs() // its dialogs not shown yet end as cancelled
	a.keepClosedDialogs()
	id := a.winID
	a.closeWin()
	a.UI = nil
	a.menuDirty = true // the app's bar, without the window's state
	a.markMenus()
	if a.started && a.told != id {
		a.told = id
		a.deliver(&platform.AppEvent{Kind: platform.AppEvWindowClosed, Window: id}, nil)
	}
	if len(a.wins) == 0 && a.queuedOpens == 0 && (!a.appCfg.KeepRunning || !manyWindows) {
		a.done = true
	}
}

// launch finishes the application object's setup (the system's sleep, wake
// and session notifications) once Start has returned and before the loop
// first waits: the first frame is on screen by then, and an app with no
// window and nothing to do still gets them.
func (a *App) launch() {
	if !a.launched {
		a.launched = true
		platform.AppLaunched()
	}
}

// appTurn is the app object's part of every turn of the loop: dispatch what
// the source did not (other OSes), the safe point (with a window to close), a
// quit request from a goroutine, an approved quit.
func (a *App) appTurn() {
	platform.DispatchApp()
	a.safePoint()
	if r := platform.TakeQuitRequest(); r != 0 {
		platform.Terminate(r)
	}
	if platform.QuitApproved() {
		a.done = true
	}
}

// safePoint is the work that waits for a safe point, from the loop's turn and
// from the application object's dispatch alike: the queued functions, the
// dialogs asked for, the windows' commands (window.go), the windows to close.
// Not inside any window's frame or an event. A window is not closed under the
// loop's own call into the windows (the pump, a popped-up menu), where the
// dispatcher may run too: Close wakes the loop, whose turn closes it right
// after.
func (a *App) safePoint() {
	if a.framing > 0 || a.evDepth > 0 {
		return
	}
	if a.r != nil {
		a.r.Collect() // what finished frames held: imports' pixel buffers, freed textures (a GPU completion gave the dispatcher this turn)
	}
	a.runSafePoints()
	if a.inspDrain != nil {
		a.inspDrain()
	}
	for _, w := range a.wins {
		w.flushDialogs()
		w.applyCommands()
	}
	// AppKit's own wait for a quit answer (a terminate: someone else sent)
	// runs inside the pump and the dispatcher with it: a window closes then
	// for the app at once, and its platform resources are released when the
	// pump is back (closeWin). Under any other part of the pump (a
	// popped-up menu's tracking) a window waits for the loop's turn.
	if a.pumping && !platform.QuitWaitNative() {
		return
	}
	for i := 0; i < len(a.wins); {
		if w := a.wins[i]; w.closing {
			w.closeWindow() // takes it out of wins
			continue
		}
		i++
	}
	if !a.pumping {
		a.releaseRetired()
	}
}

// idleTurn is a turn of the loop with no window open: the app-wide pump.
func (a *App) idleTurn() {
	a.syncAppMenuBar()
	if a.bench != nil || runtime.GOOS != "darwin" {
		a.done = true // nothing runs without a window here
		return
	}
	platform.WaitApp(-1)
	a.appTurn()
}

// syncAppMenuBar installs the app's menu bar while no window is open: the
// standard menus and the model's, every command enabled.
func (a *App) syncAppMenuBar() {
	if !a.menuDirty || len(a.wins) > 0 || !platform.HasNativeMenus() || !a.started {
		return
	}
	a.menuDirty = false
	a.menuSpec = nativeMenuSpec(a.appModel())
	platform.SetAppMenuBar(&a.menuSpec, a.appStateFn)
}

// appModel is the menu model of the app's bar with no window open: the main
// window's (the root's), else the last bar's.
func (a *App) appModel() *menu.Model {
	if r := a.root; r != nil && r.menu != nil {
		return r.menu
	}
	return a.barModel
}

// appMenuState is the menu state with no window: there is no focus or modal,
// so a command is as the model sets it (SetEnabled, SetChecked), a system
// item without one is enabled, and an edit item without one is not.
func (a *App) appMenuState(id uint16, act platform.MenuAction) (enabled, checked bool) {
	if id == 0 {
		return act != platform.MenuNone && !act.IsEdit(), false
	}
	m := a.appModel()
	if m == nil {
		return false, false
	}
	return m.Enabled(menu.ID(id)), m.Checked(menu.ID(id))
}

// SetDockMenu makes menu idx of model m the Dock menu (macOS), from the next
// safe point; nil m removes it. Its commands arrive as EvCommand, with or
// without a window; the model enables and checks them (SetEnabled,
// SetChecked) whenever the Dock shows the menu.
func (a *App) SetDockMenu(m *menu.Model, idx int) {
	a.AtSafePoint(func() {
		if m == nil {
			platform.SetDockMenu(nil, 0, nil)
			return
		}
		ms := m.Menus()
		spec := platform.MenuSpec{}
		for i := range ms {
			spec.Menus = append(spec.Menus, platform.Menu{Title: ms[i].Title, Items: nativeItems(m, ms[i].Items)})
		}
		a.dockSpec = spec
		platform.SetDockMenu(&a.dockSpec, idx, func(id uint16, _ platform.MenuAction) (enabled, checked bool) {
			return m.Enabled(menu.ID(id)), m.Checked(menu.ID(id))
		})
	})
}
