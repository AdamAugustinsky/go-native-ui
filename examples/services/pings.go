package main

import (
	"errors"
	"fmt"
	"log"
	"path/filepath"

	"gonativeui/app"
	"gonativeui/app/login"
	"gonativeui/app/notify"
	"gonativeui/app/paths"
	"gonativeui/app/prefs"
	"gonativeui/app/secret"
	"gonativeui/app/tray"
	"gonativeui/app/winstate"
	"gonativeui/menu"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

const appID = "dev.gonativeui.services"

// The status item's menu commands.
const (
	cmdOpen menu.ID = iota + 1
	cmdQuit
)

// The actions of the notification's category.
const (
	catMessage       = "message"
	actReply   int64 = 1
	actRead    int64 = 2
)

// Settings are the app's preferences, kept as JSON.
type Settings struct {
	Name     string // the text field
	Launches int
}

// result is what a goroutine that blocked on a service has to say.
type result struct {
	text   string
	login  bool
	failed bool
}

// pings is the app: the Handler that Main runs and the Builder of its window.
type pings struct {
	app.DefaultHandler
	sys host
	dir string // where preferences and window state live; "" is the app's config directory

	prefs *prefs.Store[Settings]
	state *winstate.Store
	place winstate.Placement

	a       *app.App
	k       *widget.Kit
	status  *tray.Item
	name    ui.TextBuffer
	unread  int
	atLogin bool
	line    string // the result of the last thing asked
	results chan result
	saved   [4]float64 // the window frame last handed to winstate
	haveWin bool
}

// newPings loads the preferences and the window state. A file that is missing
// is not an error; one that is corrupt is reported, and the app starts on the
// defaults.
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

func (p *pings) config() app.Config {
	return app.Config{Title: "Pings", Width: 480, Height: 380, MinWidth: 420, MinHeight: 320, Clear: [4]float32{1, 1, 1, 1}}
}

// Start opens the window where it was closed, makes the status item and asks
// for permission to notify.
func (p *pings) Start(a *app.App) error {
	p.prefs.Update(func(s *Settings) { s.Launches++ })

	if err := p.open(a); err != nil {
		return err
	}
	var err error
	if p.status, err = newStatusItem(); err != nil {
		p.line = "status item: " + err.Error()
	}
	if s, ok := p.sys.(*system); ok {
		s.status = p.status
	}
	if err := notify.RegisterCategories(notify.Category{ID: catMessage, Actions: []notify.Action{
		{ID: actReply, Title: "Reply", Reply: true, ReplyButton: "Send", ReplyPlaceholder: "Write back…"},
		{ID: actRead, Title: "Mark as Read"},
	}}); err != nil {
		p.line = "notifications: " + err.Error()
	} else {
		notify.RequestPermission(notify.Alert | notify.Sound | notify.Badge)
	}
	return nil
}

// open opens the window where it was when the app last quit, if that is still
// on a screen. The state is in the units of the platform's window frame:
// Apply makes it fit the screens that exist now.
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
	return nil
}

// front brings the window forward, opening it again if it was closed.
func (p *pings) front(a *app.App) {
	if w := a.Window(); w != nil {
		w.Show()
		w.Focus()
	} else {
		p.open(a)
	}
}

// Event receives what is not about a window: the answers of the notification
// center, clicks on the status item and the request to quit.
func (p *pings) Event(a *app.App, e *app.Event) {
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
	if r, ok := notify.ResultOf(e); ok && r.Kind == notify.ResultPermission && !r.Granted {
		p.line = "notifications are off in System Settings"
		return
	}
	if p.status != nil {
		if _, ok := p.status.Clicked(e); ok {
			p.front(a)
			return
		}
	}
	switch {
	case e.Kind == app.EvCommand && menu.ID(e.ID) == cmdOpen:
		p.front(a)
	case e.Kind == app.EvReopen && len(a.Windows(nil)) == 0:
		p.front(a)
		return
	case e.Kind == app.EvCommand && menu.ID(e.ID) == cmdQuit:
		a.RequestQuit()
	case e.Kind == app.EvQuitRequested:
		// Save before allowing: Flush writes what is pending.
		p.prefs.Flush()
		p.state.Flush()
	}
	a.DefaultEvent(e)
}

// Init makes the window's kit. The real host needs the app.
func (p *pings) Init(a *app.App) error {
	p.a = a
	p.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	if s, ok := p.sys.(*system); ok {
		s.a = a
	}
	a.UI.Reserve(256)
	return nil
}

// Build draws the window and acts on its buttons.
func (p *pings) Build(c *ui.Context) {
	p.drain()
	p.keepFrame()
	k := p.k
	k.Begin(c)
	k.BeginWindow()
	k.BeginCard("services")
	k.LabelStyle("Services", widget.TextSemi)

	k.Row("notify")
	if k.Button("Notify").Clicked {
		p.notify()
	}
	if k.Button("Add unread").Clicked {
		p.unread++
		p.sys.SetBadge(p.unread)
		p.sys.SetStatusTitle(fmt.Sprint(p.unread))
	}
	if k.Button("Clear").Clicked {
		p.unread = 0
		p.sys.SetBadge(0)
		p.sys.SetStatusTitle("")
	}
	k.EndRow()

	k.Row("keychain")
	if k.Button("Save token").Clicked {
		name := p.name.String()
		go func() { p.finish(result{text: "token saved"}, p.sys.StoreToken([]byte("token-for-"+name))) }()
	}
	if k.Button("Read token").Clicked {
		go func() {
			b, err := p.sys.LoadToken()
			p.finish(result{text: "token: " + string(b)}, err)
		}()
	}
	k.EndRow()

	k.Row("name")
	k.NextWidth(widget.Fill)
	if f := k.TextField("Name", &p.name, "Your name"); f.Changed {
		p.prefs.Update(func(s *Settings) { s.Name = p.name.String() })
	}
	k.EndRow()

	if k.Switch("Start at login", &p.atLogin).Changed {
		on := p.atLogin
		go func() {
			st, err := p.sys.SetLogin(on)
			p.finish(result{text: "login item: " + st.String(), login: st == login.Enabled}, err)
		}()
	}
	k.LabelStyle(p.line, widget.TextMuted)
	k.LabelBytesStyle(c.Fmt().Int(p.prefs.Get().Launches).Str(" launches, ").Int(p.unread).Str(" unread").Bytes(), widget.TextMuted)
	k.EndCard()
	k.EndWindow()
	k.End()
}

// notify posts a notification whose category offers a reply.
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

// finish hands the result of a blocking call to the window, from its
// goroutine: it fills the result's text and wakes the window.
func (p *pings) finish(r result, err error) {
	if err != nil {
		r = result{text: describe(err), failed: true}
	}
	select {
	case p.results <- r:
		if w := p.a.Window(); w != nil {
			w.Wake()
		}
	default:
	}
}

// drain takes the results that arrived since the last frame.
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

// describe says what an error means in the window's words.
func describe(err error) string {
	switch {
	case errors.Is(err, errors.ErrUnsupported):
		return "not available on this OS yet"
	case errors.Is(err, notify.ErrNotSetUp), errors.Is(err, notify.ErrUnavailable), errors.Is(err, login.ErrUnavailable):
		return "needs a signed bundle (gnui run)"
	}
	return err.Error()
}

// keepFrame hands winstate the window's frame when it changed.
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
