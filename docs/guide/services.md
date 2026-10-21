# Services

An application talks to its operating system: it posts notifications, badges the
Dock icon, puts an icon in the menu bar, keeps a token in the Keychain, remembers
its settings and where its window was, and starts when the user logs in. Each of
those is a small package under `app/`, with one contract in common, and this
chapter builds a window that uses all of them: Pings, in
[`examples/services`](../../examples/services).

Every block is an excerpt of that example and is checked by the test of the guide.

```sh
go run ./examples/services                               # works from the shell; says which services need a bundle
cd examples/services && go run ../../cmd/gnui run       # a signed development bundle: notifications and the login item too
go test ./examples/services                              # every service replaced by a recorder
```

## What needs what

| Service | Package | Needs a signed bundle (`gnui run`)? | Windows | Linux |
|---|---|---|---|---|
| Notification, with actions and a reply | `app/notify` | **Yes.** From a bare `go run`, `Setup` returns `ErrUnavailable` and `Post` `ErrNotSetUp` | `ErrUnsupported` | `ErrUnsupported` |
| Dock badge and bounce, activation policy | `app/dock` | No | `ErrUnsupported` | `ErrUnsupported` |
| Status item (menu bar) | `app/tray` | No | `ErrUnsupported` | `ErrUnsupported` |
| Keychain | `app/secret` | No; but see below | Credential Manager | Secret Service (D-Bus) |
| Preferences, directories | `app/prefs`, `app/paths` | No | works | works |
| Login item | `app/login` | **Yes.** Else `ErrUnavailable` | `ErrUnsupported` | `ErrUnsupported`; `StartHidden` works everywhere |
| Window state | `app/winstate` | No | works (units are physical pixels) | works; no screens are read yet, so only the size is kept |

- **`ErrUnsupported`** is `errors.ErrUnsupported`, wrapped with the package's name:
  `errors.Is(err, errors.ErrUnsupported)` is the test, whichever package it came
  from. A service that returns it did nothing, and an app that wants to run
  everywhere shows the feature as unavailable instead of failing.
- **A signed bundle** is what `gnui run` and `gnui package` make, in the next
  chapter. The notification center and the login items work only in a bundle with
  an identifier, and the packages say so. `gnui run` signs with a stable self-signed identity, so that the grant the user
  gave (a notification permission, a Keychain item, an approved login item)
  survives a rebuild. A bare `go run` is a new program every time.
- **The Keychain does not ask for a bundle,** but an item is readable without a
  prompt only by the program that created it, so a rebuilt program, signed
  differently, may be asked by the user. A `gnui run` bundle keeps its signature.
- **The Dock and the status item** are exercised without a bundle by their packages'
  own tests, and work from a bare `go run`.
- Only macOS has a native version of the first three. The others are marked "not
  yet" in their packages.

## 1. How a service answers

A service never waits for the system on the main thread, and says so when it
cannot do something. There are three ways an answer arrives:

- **Returned**, for what is immediate: `dock.SetBadgeCount` returns an error.
- **As an app event**, for what the system answers later: a notification's
  permission, a post's result, a click, a reply and a status item's click are
  `EvService` events that `notify.ResultOf`, `notify.ResponseOf` and `Item.Clicked`
  decode in the handler's `Event`. No call takes a callback.
- **From a goroutine**, for what blocks: the Keychain and the login item ask other
  processes, and may wait for the user to unlock. Call them from a goroutine,
  never from a frame, and hand the result back as the app-object chapter
  shows.

The example keeps its services behind one small interface. The real host calls
them; the tests give the app a recorder, so that a test never posts a
notification, touches the Dock or writes to the Keychain:

```go examples/services/system.go
type host interface {
	Post(n notify.Note) error
	SetBadge(n int)
	SetStatusTitle(title string)
	StoreToken(token []byte) error
	LoadToken() ([]byte, error)
	SetLogin(on bool) (login.State, error)
}
```

An error from any of them is turned into words for the window in one place:

```go examples/services/pings.go
func describe(err error) string {
	switch {
	case errors.Is(err, errors.ErrUnsupported):
		return "not available on this OS yet"
	case errors.Is(err, notify.ErrNotSetUp), errors.Is(err, notify.ErrUnavailable), errors.Is(err, login.ErrUnavailable):
		return "needs a signed bundle (gnui run)"
	case errors.Is(err, login.ErrNotFound):
		return "this bundle has no \"hidden\" launch agent (see gnui.json)"
	}
	return err.Error()
}
```

## 2. A notification with a reply

`Setup` opts the app in, and **it is called before `app.Main`**: the click that
launched the app reaches the system's delegate while the application finishes
launching, and a `Setup` called from `Start` is too late (`ErrTooLate`). Outside
a bundle it returns `ErrUnavailable`; the example logs it and runs on without
notifications:

```go examples/services/main.go
func main() {
	// Before app.Main: the click that launched the app is not lost. Outside a
	// bundle, or off macOS, Setup says why and the app runs without
	// notifications.
	if err := notify.Setup(notify.Options{Foreground: true}); err != nil {
		log.Print("notifications: ", err)
	}
```

A **category** is a kind of note with its actions: here a *Reply* action that is a
text field with its own send button and a *Mark as Read* button. Register them,
then ask the user's permission; the system asks once and remembers the answer
for the app:

```go examples/services/pings.go
	if err := notify.RegisterCategories(notify.Category{ID: catMessage, Actions: []notify.Action{
		{ID: actReply, Title: "Reply", Reply: true, ReplyButton: "Send", ReplyPlaceholder: "Write back…"},
		{ID: actRead, Title: "Mark as Read"},
	}}); err != nil {
		p.line = "notifications: " + err.Error()
	} else {
		notify.RequestPermission(notify.Alert | notify.Sound | notify.Badge)
	}
```

`Post` takes a `Note`. Its `Thread` groups notes (a conversation), its `Category`
names the actions it shows, and its `UserData` comes back with the response. A
note with `UserData` or a category reserves a response slot, so that a reply
cannot be lost on its way to the app, and `Post` says when every slot is taken
(the note then posts without its actions). It may be called from any goroutine:

```go examples/services/pings.go
func (p *pings) notify() {
	err := p.sys.Post(notify.Note{
		Thread: "chat", Title: p.name.String(), Body: "Lunch?",
		Sound: notify.SoundDefault, Category: catMessage, UserData: []byte("chat/1"),
	})
	if err != nil {
		p.line = "notify: " + describe(err)
	} else {
		p.line = "notification posted"
	}
}
```

The reply, the button and the click come back to `Event`, between frames, as a
`Response`. Its slices are valid for the call only:

```go examples/services/pings.go
	if r, ok := notify.ResponseOf(e); ok {
		switch r.Action {
		case actReply:
			p.line = "reply: " + string(r.Reply)
		case actRead:
			p.unread = 0
			p.sys.SetBadge(0)
			p.line = "marked as read"
		case notify.ActionClick:
			p.front(a)
		}
		return
	}
// …
	if r, ok := notify.ResultOf(e); ok && r.Kind == notify.ResultPermission && !r.Granted {
		p.line = "notifications are off in System Settings"
		return
	}
```

`Foreground: true` in `Options` shows notes while the app is the active app, which
the system does not do by default. `notify.Remove` and `RemoveThread` take notes
back, and `Attachment` shows an image or a sound with a note.

## 3. The Dock badge

`dock.SetBadgeCount(n)` shows an unread count on the Dock icon; zero clears it and
more than 999 shows "999+". The Dock is the main thread's, so the example's
`SetBadge` runs at a safe point: it can be called from a frame. `dock.Bounce`
asks for attention, once or until the app is active, and `dock.SetPolicy`
(`app.PolicyAccessory`) takes the app out of the Dock and the switcher, as a
menu-bar app is.

```go examples/services/system.go
func (s *system) SetBadge(n int) {
	s.a.AtSafePoint(func() { dock.SetBadgeCount(n) })
}
```

## 4. A status item

`tray.New` puts an icon in the menu bar's right side: a template SF Symbol or
a PNG, a tooltip, optional text, and a native menu. With `RightClickMenu` the left
click is an event and the right click opens the menu, which is how a resident app
behaves. The menu is a `menu.Model`, and its commands arrive as `EvCommand`, so
there is nothing to register:

```go examples/services/system.go
func newStatusItem() (*tray.Item, error) {
	item, err := tray.New(tray.Options{Icon: tray.SymbolIcon("bell.fill"), Tooltip: "Pings", RightClickMenu: true})
	if err != nil {
		return nil, err
	}
	m, err := menu.NewModel(
		[]menu.Command{{ID: cmdOpen, Title: "Open Pings"}, {ID: cmdQuit, Title: "Quit Pings"}},
		[]menu.Menu{{Title: "Pings", Items: []menu.Item{
			{Kind: menu.ItemCommand, Command: cmdOpen},
			{Kind: menu.ItemSeparator},
			{Kind: menu.ItemCommand, Command: cmdQuit},
		}}}, nil)
	if err != nil {
		item.Remove()
		return nil, err
	}
	return item, item.SetMenu(m, 0)
}
```

The click comes to `Event` as a `tray.Click`, and the example brings its window
forward, opening it again if it was closed. The item's title, here an unread
count, is set like the badge:

```go examples/services/system.go
func (s *system) SetStatusTitle(title string) {
	s.a.AtSafePoint(func() {
		if s.status != nil {
			s.status.SetTitle(title)
		}
	})
}
```

A status item belongs to the process: it goes when the app ends, or earlier with
`Remove`. For an app that is only a status item, `AppConfig.KeepRunning` keeps it
running with no window and `app.PolicyAccessory` keeps it out of the Dock.

## 5. The Keychain

`secret.Open(service)` is a store for one service name, normally the app's ID, and
`Get`, `Set` and `Delete` take an account. A secret that was never set is
`ErrNotFound`. Every call talks to the OS and **may block**: the Keychain may be
asking the user to unlock it. So the example calls it from a goroutine and hands the
answer back:

```go examples/services/system.go
// StoreToken and LoadToken use the Keychain (Credential Manager on Windows, the
// Secret Service on Linux). They block: call them from a goroutine.
func (s *system) StoreToken(token []byte) error { return s.vault.Set(account, token) }
func (s *system) LoadToken() ([]byte, error)    { return s.vault.Get(account) }
```

```go examples/services/pings.go
	if k.Button("Save token").Clicked {
		name := p.name.String()
		go func() { p.finish(win, result{text: "token saved"}, p.sys.StoreToken([]byte("token-for-"+name))) }()
	}
// …
	if k.Button("Read token").Clicked {
		go func() {
			b, err := p.sys.LoadToken()
			p.finish(win, result{text: "token: " + string(b)}, err)
		}()
	}
```

`finish` puts the result in a channel the window drains in `Build`, and wakes the
window; this is the pattern of the app-object chapter's section 5:

```go examples/services/pings.go
func (p *pings) finish(win *app.Win, r result, err error) {
	if err != nil {
		r = result{text: describe(err), failed: true}
	}
	select {
	case p.results <- r:
		if win != nil { // nil offscreen
			win.Wake() // any goroutine may
		}
	default:
	}
}
// …
func (p *pings) drain() {
	for {
		select {
		case r := <-p.results:
			p.line = r.text
			if !r.failed && r.login {
				p.atLogin = true
			}
		default:
			return
		}
	}
}
```

The package does not wipe memory: it keeps secrets at rest in the OS's store,
and does not defend a running process against someone who reads its memory. It
uses the user's default keychain and not the data-protection one, so no
provisioning profile or access-group entitlement is needed. On Windows a secret is
at most 2560 bytes; on Linux, with no session bus or no Secret Service, the calls
return `ErrUnavailable`, and a locked collection is `ErrLocked`.

## 6. Preferences

`prefs.Open(path, defaults, options)` reads a JSON file once, at start, into a typed
value. `Get` and `Update` never touch the disk and never wait, so they are safe in
a frame; a change is written back atomically, on a worker, a moment after the
last one. A missing field keeps its default, so adding a field needs no migration,
and a corrupt file is moved aside to `<path>.corrupt` and the store starts on the
defaults, together with an error for you to log.

`paths.Config(id)` is the directory by the OS's rules (Application Support on macOS,
`%APPDATA%` on Windows, `$XDG_CONFIG_HOME` on Linux). It only computes the name.

```go examples/services/pings.go
type Settings struct {
	Name     string // the text field
	Launches int
}
// …
func newPings(id, dir string) (*pings, error) {
	if dir == "" {
		d, err := paths.Config(id)
		if err != nil {
			return nil, err
		}
		dir = d
	}
	p := &pings{dir: dir, results: make(chan result, 4), sys: &system{vault: secret.Open(id)}}
	var err error
	if p.prefs, err = prefs.Open(filepath.Join(dir, "settings.json"), Settings{Name: "Ana"}, prefs.Options{}); err != nil {
		log.Print(err)
	}
	if p.state, err = winstate.LoadWith(id, winstate.Options{Dir: dir}); err != nil {
		log.Print(err)
	}
	p.name.Max = 40
	p.name.Reserve(40)
	p.name.SetString(p.prefs.Get().Name)
	return p, nil
}
```

In `Build`, the text field edits a buffer, and the preference follows it:

```go examples/services/pings.go
	if f := k.TextField("Name", &p.name, "Your name"); f.Changed {
		p.prefs.Update(func(s *Settings) { s.Name = p.name.String() })
	}
```

`Flush` writes what is pending, and blocks: the example calls it when the app is
asked to quit (the app-object chapter's section 4), after which a quit that is
allowed ends the process.

## 7. A login item

An app that starts at login should not open a window or jump in the Dock. The
main app's login item (`login.App()`) is launched with no arguments, so it cannot
start hidden. A **launch agent** can pass them, and `gnui.json` declares one:

```json examples/services/gnui.json
	"loginAgents": [{"name": "hidden", "arguments": ["--start-hidden"]}]
```

`gnui run` and `gnui package` write its property list into the bundle, with the
label `<bundle id>.hidden`. `login.Agent("hidden")` is that agent, and the app
learns it was started that way: `AppConfig.StartHidden` is set from the
`--start-hidden` argument, and windows opened in `Start` stay hidden until
`EvReopen` or `Win.Show`. The example's status item, with its "Open Pings"
command, is what the user then has.

```go examples/services/system.go
// SetLogin makes the app start at login, or not. It uses the launch agent that
// gnui.json declares, which starts the app with --start-hidden: no window, no
// Dock jump. It blocks, like the Keychain.
func (s *system) SetLogin(on bool) (login.State, error) {
	if on {
		return login.Agent("hidden").Enable()
	}
	return login.Agent("hidden").Disable()
}
```

**The first `Enable` returns `RequiresApproval`.** macOS lets the user decide
which login items run: the item is registered, and the user switches it on in
System Settings, Login Items. `login.OpenSettings()` opens that pane. `Enable`
again, or `Status`, tells when it happened. A development bundle has its own
agent, `<bundle id>.dev.hidden`, and never collides with the installed app's.

## 8. Remembering the window

`winstate` keeps a window's frame, whether it was maximised or full screen, the
screen it was on, its zoom and a few bytes of the app's own, in a small file
(`window-main.json`, written by `prefs`). It is the framework's own file, and not
`NSWindow`'s autosave, so that the rule is the same everywhere and a
saved state is clamped to the screens that exist **now**.

`Load` before the window exists; `Apply` decides where it goes. A frame that is
on a screen stays; one that sticks out of the visible area is moved in; one that
is on no screen (a monitor was unplugged) is centred on the screen it was saved
on if that still exists, and on the primary screen otherwise. A state that is
missing or not sensible is ignored: `Placement.Restored` is false, and the app
opens at its defaults.

```go examples/services/pings.go
func (p *pings) open(a *app.App) error {
	cfg := p.config()
	p.place = winstate.Apply(winstate.Config{Width: float64(cfg.Width), Height: float64(cfg.Height), MinWidth: float64(cfg.MinWidth), MinHeight: float64(cfg.MinHeight)},
		p.state.State(), winstate.ScreensFromPlatform(app.Screens(nil)))
	win, err := a.OpenWindow(cfg, p)
	if err != nil {
		return err
	}
	if f := p.place.Frame; p.place.Restored && p.place.HasPosition {
		win.SetFrame(f.X, f.Y, f.W, f.H)
	}
	if p.place.Fullscreen {
		win.ToggleFullscreen()
	} else if p.place.Maximised {
		win.SetZoomed(true)
	}
	return nil
}
```

`Save` changes memory and schedules a write, so the example calls it whenever the
frame changes:

```go examples/services/pings.go
func (p *pings) keepFrame() {
	win := p.a.Window()
	if win == nil {
		return
	}
	x, y, w, h := win.Frame()
	if f := [4]float64{x, y, w, h}; f != p.saved && w > 0 && h > 0 {
		p.saved = f
		p.state.Save(winstate.State{Frame: winstate.Rect{X: x, Y: y, W: w, H: h}, Fullscreen: win.Fullscreen(), Maximised: win.Zoomed()})
	}
}
```

The units differ by OS: points on macOS and Linux, **physical pixels on Windows**,
which are points times the display's scale. `winstate.FromPoints` and `ToPoints`
convert a length in points to the frame's units and back, and an app never
multiplies by a scale by hand.

## 9. What the tests do

`examples/services` has seven tests, and none of them posts a notification, writes
to the Keychain, changes the Dock, opens a URL or starts another app. The recorder
takes the host's place:

- the buttons reach the services (a post with the category and the name, the badge
  and the status title, the token written and read, the login item asked for) and
  the Keychain and login calls run off the frame;
- an `ErrUnsupported`, a missing bundle or a missing agent is shown in words;
- a preference and a window state survive a restart, in a temporary directory, and
  a corrupt file starts the app on its defaults;
- an action marked as read clears the badge;
- the layout is audited, every control has a name, and a steady frame allocates
  nothing.

What only the real services show is tried by hand, with `gnui run`. The packages
have opt-in live tests for it: `GNUI_LIVE_NOTIFY=1 go test -run Live ./app/notify`
posts through a real bundle and `GNUI_LIVE_NOTIFY=hand` asks you to click Allow
and type a reply.
