# The app object

[The guide](../GUIDE.md) opened one window with `app.Run`. That is the simplest
program: one `Builder`, one window, and the program ends when the window closes.
An application is more than a window. It has a Dock icon that can be clicked
with no window open, a quit that other programs and the system ask for, windows
that come and go, and work that finishes on other goroutines. That is the **app
object**, and `app.Main` runs it.

Every block below is an excerpt, checked by the same test as the guide's.
They come from [`cmd/windowsdemo`](../../cmd/windowsdemo), which opens several
windows of one app to try what tests cannot look at, and from
[`examples/services`](../../examples/services). The decisions behind it are
D30 (windows) and D37 (the app object) in [DECISIONS.md](../../DECISIONS.md).

## 1. Run and Main

`Run(cfg, b)` is one window over the same object, with nothing to configure.
When the program needs more than that, call `Main` with an `AppConfig` and a
`Handler`:

```go cmd/windowsdemo/main.go
	if err := app.Main(app.AppConfig{KeepRunning: *keep}, d); err != nil {
		fmt.Fprintln(os.Stderr, "windowsdemo:", err)
		os.Exit(1)
	}
```

`Main` takes the main thread until the app ends. `AppConfig.ID` is the app's
reverse-DNS name; left empty, it is the bundle's, which is what a `gnui` bundle
has (see [packaging](packaging.md)). Services key their files and system
registrations by it. `KeepRunning` keeps the app running when its last window
closes, as a macOS app does; on Windows and Linux the app ends with its last
window.

## 2. The Handler: Start and Event

A `Handler` has two methods. `Start` runs once, with the app object ready and
before any window; it opens the first windows. `Event` receives what is not
about one window, on the main thread, between frames, in order. Embed
`app.DefaultHandler` and write only what you need.

```go cmd/windowsdemo/main.go
func (d *demo) Start(a *app.App) error {
	for i := 0; i < d.n; i++ {
		if err := d.open(a); err != nil && i == 0 {
			return err
		}
	}
	return nil
}
// …
func (d *demo) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvReopen && e.A == 0 && len(a.Windows(nil)) == 0 {
		d.open(a) // the Dock's icon with no window open (-keep): a new one
		return
	}
	if e.Kind == app.EvPost {
		if w := d.wins[int(e.ID/10)]; w != nil && w.Open() {
			switch e.ID % 10 {
			case postShow:
				w.Show()
			case postAttention:
				w.RequestAttention(false) // nothing while the app is active
			}
		}
		return
	}
	a.DefaultEvent(e)
}
```

`DefaultEvent` is the default behaviour for an event: it answers a quit request
once every window agrees to close, and shows a hidden window on a reopen. An
`Event` that handles something calls it for the rest. An event is a
fixed-size record; its data (`e.Data()`, `e.Strings()`) is valid during the
call only.

The kinds are in `app/events.go`: `EvReopen` (the Dock icon was clicked),
`EvActivated` and `EvDeactivated`, `EvQuitRequested`, `EvOpenURLs` and
`EvOpenFiles`, `EvWillSleep` and `EvDidWake`, `EvScreensChanged`,
`EvCommand` (a menu command with no window to take it), `EvWindowClosed`,
`EvPost` and `EvService`, which is how the [services](services.md) deliver
their answers.

## 3. Windows

`App.OpenWindow(cfg, builder)` opens a window with its own `Builder` and
returns a `*Win`, the handle for window commands. A window opened from `Start`
or a safe point opens at once; one asked for during an event or a frame opens
at the next safe point (between frames, outside `Build`), and a failure is
then `Win.Err`. The window draws its first frame before it appears, as the
first window does.

```go cmd/windowsdemo/main.go
// open opens a window: at once from Start, after the frame from a Build.
func (d *demo) open(a *app.App) error {
	d.opened++
	w := &window{d: d, n: d.opened, results: make(chan string, 8)}
	cfg := app.WindowConfig{Title: fmt.Sprintf("Window %d", d.opened), Width: 560, Height: 380, MinWidth: 420, MinHeight: 300}
	h, err := a.OpenWindow(cfg, w)
	if err != nil {
		d.last = err.Error()
		return err
	}
	if d.wins == nil {
		d.wins = map[int]*app.Win{}
	}
	d.wins[w.n] = h
	return nil
}
```

Each window has an `App` of its own, which its builder's `Init` receives: its
platform window, `ui.Context` and frame pacing. The renderer, the text system
and the glyph atlas are shared. `Win` has the window commands, each of which
is kept as state and applied at the next safe point, so that asking every
frame costs nothing: `SetTitle`, `SetFrame`, `Show`, `Hide`, `Focus`,
`Miniaturize`, `ToggleFullscreen`, `SetZoomed`, `SetDocumentEdited`,
`RequestAttention` and `Close`.

```go cmd/windowsdemo/main.go
	if k.Button("Rename").Clicked {
		w.renames++
		win.SetTitle(fmt.Sprintf("Window %d, renamed %d", w.n, w.renames))
	}
// …
	if k.Button("Move").Clicked {
		x, y, wd, h := win.Frame()
		win.SetFrame(x+40, y+30, wd, h)
	}
// …
	win.SetDocumentEdited(w.edited) // unchanged, it does nothing
```

**On Windows and Linux there is one window per app** for now. `OpenWindow`
refuses a second with `app.ErrOneWindow`, which satisfies
`errors.Is(err, errors.ErrUnsupported)`. The demo keeps the error and shows it
in its window; an app that wants more than one window checks it.

## 4. Closing and quitting

A window's builder can refuse its own closing by implementing
`CloseRequester`. It is asked when the user closes the window and when the app
is asked to quit:

```go cmd/windowsdemo/main.go
// CloseRequested refuses the close button while the document is edited.
func (w *window) CloseRequested(*app.App) bool {
	if w.edited {
		w.status = "close refused: the document is edited (untick Edited)"
		return false
	}
	return true
}
```

Every route to quitting arrives as `EvQuitRequested`: the Quit menu item, the
Dock, a logout, `SIGTERM`, an updater, or your own `App.RequestQuit`. The
event's `QuitReason` says which. It is answered with `App.ReplyQuit(e.ID,
allow)`, which `DefaultEvent` does for you by asking the windows. The answer
may come later, however long it takes; until it does, macOS waits. So **save
before you allow**: do the durable work, then reply. `EvWillTerminate` comes
last, and on macOS the process exits when it returns.

`examples/services` keeps its preferences and its window frame in two files
that `Flush` writes, and flushes both while the quit request is open:

```go examples/services/pings.go
	case e.Kind == app.EvQuitRequested:
		// Save before allowing: Flush writes what is pending.
		p.prefs.Flush()
		p.state.Flush()
	}
	a.DefaultEvent(e)
```

`App.Quit` is not a quit request: it ends the loop at once without asking.
`AppConfig.QuitDeadline` bounds how long a request may stay unanswered.

## 5. From other goroutines

Everything runs on the main thread, and the frame loop sleeps when nothing is
owed. Work on another goroutine must hand its result back and wake the loop.

`Win.Wake` is for one window: the demo's Worker button sends its result on a
channel that the window drains in `Build`, then wakes the window so that a
frame is drawn.

```go cmd/windowsdemo/main.go
	if k.Button("Worker").Clicked {
		go func(n int) { // a result for this window only: the others draw nothing
			time.Sleep(time.Second)
			w.results <- fmt.Sprintf("worker done at %s", time.Now().Format("15:04:05"))
			win.Wake()
		}(w.n)
		w.status = "worker running (1 s)"
	}
```

`app.Post(app.Event{...}, data)` is for the handler: it queues an `EvPost`,
never blocks, allocates nothing and returns false when the queue is full. The
demo's "Hide 2 s" shows a window again after two seconds through it:

```go cmd/windowsdemo/main.go
	if k.Button("Hide 2 s").Clicked {
		win.Hide()
		time.AfterFunc(2*time.Second, func() { app.Post(app.Event{ID: uint64(w.n*10 + postShow)}, nil) })
	}
```

And `App.AtSafePoint(f)` runs `f` on the main thread at the next safe point,
for the calls that must not run inside a frame or from a goroutine. The
services chapter's Dock badge uses it:

```go examples/services/system.go
func (s *system) SetBadge(n int) {
	s.a.AtSafePoint(func() { dock.SetBadgeCount(n) })
}
```

For a pool of workers with a bounded queue and results that are polled in
`Build` instead of posted, see package `task`; the image cache is built on it.

## 6. One menu per window

Each window sets the menu model it wants the bar to show (`App.SetMenu`), and
the bar follows the key window. With no window open, a command from the Dock
menu (`App.SetDockMenu`) or the bar arrives as `EvCommand` in the handler.
The commands, shortcuts and roles are those of [the guide's section
8](../GUIDE.md#8-commands-menus-and-shortcuts).

```go cmd/windowsdemo/main.go
func (w *window) Init(a *app.App) error {
	w.a = a
	w.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	primary := func(k ui.KeyCode) menu.Shortcut { return menu.Shortcut{Key: k, Primary: true} }
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	m, err := menu.NewModel([]menu.Command{
		{ID: cmdNew, Title: "New Window", Shortcut: primary('N')},
		{ID: cmdClose, Title: fmt.Sprintf("Close Window %d", w.n), Shortcut: primary('W')},
	}, []menu.Menu{{Title: "File", Items: []menu.Item{item(cmdNew), item(cmdClose)}}}, []int{0})
	if err != nil {
		return err
	}
	w.model = m // a model of its own: the bar shows the key window's
	w.k.SetMenu(m)
	a.SetMenu(m)
	w.field.Max = 200
	w.status = "ready"
	return nil
}
```
