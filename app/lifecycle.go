package app

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"time"

	"gonativeui/internal/sysinfo"
	"gonativeui/menu"
	"gonativeui/platform"
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
// reopen with no visible window shows the window. Other events need nothing.
func (a *App) DefaultEvent(e *Event) {
	switch e.Kind {
	case EvQuitRequested:
		a.ReplyQuit(e.ID, a.windowsAgreeToClose())
	case EvReopen:
		if e.A == 0 && a.Win != nil {
			a.Win.Show()
		}
	}
}

// windowsAgreeToClose asks every window's builder whether it may close.
func (a *App) windowsAgreeToClose() bool {
	if a.Win == nil {
		return true
	}
	if cr, ok := a.b.(CloseRequester); ok {
		return cr.CloseRequested(a)
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
	if err := a.bindApp(true); err != nil {
		return err
	}
	defer a.unbindApp()
	if err := h.Start(a); err != nil {
		a.close()
		return err
	}
	a.started = true
	a.flushStartup()
	err := a.loop()
	a.finish()
	return err
}

// newApp makes an App with no window yet. Post's storage is made here, so
// that no Post allocates or waits for it.
func newApp() *App {
	platform.PreparePosts()
	return &App{tr: newTranslator(), p: newPacer(), lat: newSampler(8192), diagBuf: make([]byte, 0, 2048), menuDirty: true}
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
		Signals: signals,
	})
	if err != nil {
		platform.SetAppSink(nil)
		return fmt.Errorf("app: application object: %w", err)
	}
	return nil
}

func (a *App) unbindApp() { platform.SetAppSink(nil) }

// finish delivers EvWillTerminate (if the system did not) and closes the
// window.
func (a *App) finish() {
	if !a.terminated {
		a.terminated = true
		a.deliver(&platform.AppEvent{Kind: platform.AppEvWillTerminate}, nil)
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

// ErrOneWindow is OpenWindow's error while a window is open: more than one
// window comes with phase 6's F4.
var ErrOneWindow = fmt.Errorf("app: a second window: %w", errors.ErrUnsupported)

// Win is a window the app opened (OpenWindow).
type Win struct {
	a   *App
	id  uint32
	err error
}

// Err is the error that kept a window asked for during an event from opening
// at its safe point (OpenWindow), nil otherwise.
func (w *Win) Err() error { return w.err }

// ID is the window's id (Event.Window); never 0.
func (w *Win) ID() uint32 { return w.id }

// Open reports whether the window is still open.
func (w *Win) Open() bool { return w.a.Win != nil && w.a.winID == w.id }

// Show shows the window and activates the app.
func (w *Win) Show() {
	if w.Open() {
		w.a.Win.Show()
	}
}

// Close closes the window at the next safe point, without asking its
// CloseRequester: between frames, at the loop's turn or the application
// object's dispatch (which also runs while a quit answer is pending).
func (w *Win) Close() {
	if w.Open() {
		w.a.closing = true
		platform.Wake()
	}
}

// OpenWindow opens a window with builder b and draws its first frame
// (content-first: it appears with that frame, unless the app starts hidden).
// From Start or a safe point it opens at once; asked for during an event, it
// opens at the next safe point, and a failure then is reported by Win.Err.
// One window at a time for now (ErrOneWindow).
func (a *App) OpenWindow(cfg WindowConfig, b Builder) (*Win, error) {
	if a.Win != nil {
		return nil, ErrOneWindow
	}
	w := &Win{a: a}
	if a.evDepth > 0 {
		a.AtSafePoint(func() { w.err = a.openWindowNow(cfg, b, w) })
		return w, nil
	}
	if err := a.openWindowNow(cfg, b, w); err != nil {
		return nil, err
	}
	return w, nil
}

func (a *App) openWindowNow(cfg WindowConfig, b Builder, w *Win) error {
	if a.Win != nil {
		return ErrOneWindow
	}
	var st StartupReport
	if err := a.openWin(cfg, b, &st, true, platform.Now(), sysinfo.ProcessAgeNs()); err != nil {
		return err
	}
	a.Win.SetFrameHandler(a.frameHandler)
	a.nextWinID++
	a.winID, w.id = a.nextWinID, a.nextWinID
	return nil
}

// closeRequested is the window's close button (EvClose): its CloseRequester
// may refuse; the window closes, and the app ends with it unless it keeps
// running.
func (a *App) closeRequested() {
	if cr, ok := a.b.(CloseRequester); ok && !cr.CloseRequested(a) {
		return
	}
	if a.mainApp && a.appCfg.KeepRunning && runtime.GOOS == "darwin" {
		a.closing = true // at the turn's safe point
		return
	}
	a.done = true
}

// appObject is the application object's part of App.
type appObject struct {
	handler     Handler
	appCfg      AppConfig
	mainApp     bool // Main runs the app (not Run or a bench)
	started     bool // Start returned (Run: the window is open)
	launched    bool // the late setup ran (AppLaunched)
	terminated  bool // EvWillTerminate was delivered
	startHidden bool
	closing     bool // the window closes at the next safe point
	pumping     bool // the loop is inside the window's Wait or a popped-up menu: the window stays open under it
	inSafe      bool
	winID       uint32
	nextWinID   uint32

	evs        [8]Event // the events being delivered, by nesting depth
	evDepth    int
	startup    []startupEvent
	safe       []func()
	safeSpare  []func()
	appStateFn platform.MenuState
	dockSpec   platform.MenuSpec
}

// closeWindow closes the window at a safe point; the app ends with it unless
// it keeps running.
func (a *App) closeWindow() {
	a.closing = false
	if a.Win == nil {
		return
	}
	a.close()
	a.UI = nil
	a.menuDirty = true // the app's bar, without the window's state
	if !a.appCfg.KeepRunning || runtime.GOOS != "darwin" {
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
// dialogs asked for, a window to close. Not inside a frame or an event. The
// window is not closed under the loop's own call into it (its Wait, a
// popped-up menu), where the dispatcher may run too: Close wakes the loop,
// whose turn closes it right after.
func (a *App) safePoint() {
	if a.inFrame || a.evDepth > 0 {
		return
	}
	a.runSafePoints()
	a.flushDialogs()
	if a.closing && !a.pumping {
		a.closeWindow()
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
	if !a.menuDirty || a.Win != nil || !platform.HasNativeMenus() || !a.started {
		return
	}
	a.menuDirty = false
	a.menuSpec = nativeMenuSpec(a.menu)
	platform.SetAppMenuBar(&a.menuSpec, a.appStateFn)
}

// appMenuState is the menu state with no window: there is no focus or modal,
// so a command is as the model sets it (SetEnabled, SetChecked), a system
// item without one is enabled, and an edit item without one is not.
func (a *App) appMenuState(id uint16, act platform.MenuAction) (enabled, checked bool) {
	if id == 0 {
		return act != platform.MenuNone && !act.IsEdit(), false
	}
	if a.menu == nil {
		return false, false
	}
	return a.menu.Enabled(menu.ID(id)), a.menu.Checked(menu.ID(id))
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
