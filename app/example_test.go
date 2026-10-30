package app_test

import (
	"errors"
	"log"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// editor is the content of a window: a Builder, with an optional Init that
// runs once the window, renderer and text system exist.
type editor struct {
	kit  *widget.Kit
	name string
}

func (e *editor) Init(a *app.App) error {
	e.kit = widget.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (e *editor) Build(c *ui.Context) {
	k := e.kit
	k.Begin(c)
	k.Label(e.name)
	k.End()
}

// notes is the application: Start opens its windows, Event receives the app's
// events one at a time on the main thread, between frames.
type notes struct {
	app.DefaultHandler // no window at Start, and DefaultEvent for every event
	settings           *app.Win
}

func (n *notes) Start(a *app.App) error {
	_, err := a.OpenWindow(app.WindowConfig{Title: "Notes", Width: 960, Height: 600}, &editor{name: "main"})
	return err
}

func (n *notes) Event(a *app.App, e *app.Event) {
	switch e.Kind {
	case app.EvReopen: // the Dock icon was clicked and no window is open
		if len(a.Windows(nil)) == 0 {
			a.OpenWindow(app.WindowConfig{Title: "Notes", Width: 960, Height: 600}, &editor{name: "main"})
		}
		return
	case app.EvPost: // from app.Post, sent by a goroutine
		log.Println("posted", e.ID, string(e.Data())) // Data is valid in this call only
		return
	case app.EvCommand: // a menu command chosen with no window open
		if e.ID == 1 {
			n.openSettings(a)
		}
		return
	}
	a.DefaultEvent(e) // quit requests, reopen with a hidden window
}

// openSettings opens a second window. Linux has one window per app so far:
// the call fails with app.ErrOneWindow there.
func (n *notes) openSettings(a *app.App) {
	if n.settings != nil && n.settings.Open() {
		n.settings.Focus()
		return
	}
	w, err := a.OpenWindow(app.WindowConfig{Title: "Settings", Width: 480, Height: 360}, &editor{name: "settings"})
	if errors.Is(err, app.ErrOneWindow) {
		return // show the settings in the main window instead
	}
	n.settings = w
}

// The application object: Main owns the main thread until the app ends and
// takes a Handler, so the app can have no window, one or several. Run, for an
// app of one window, is Main with a Handler that opens it.
func Example() {
	err := app.Main(app.AppConfig{ID: "dev.example.notes", KeepRunning: true}, &notes{})
	if err != nil {
		log.Fatal(err)
	}
}

// A goroutine tells the application something with Post: it never blocks and
// allocates nothing, and returns false when too many posts are waiting, so
// the caller keeps the event and tries again. The Handler gets it as EvPost.
func ExamplePost() {
	go func() {
		for !app.Post(app.Event{ID: 42, A: 1}, []byte("sync finished")) {
			time.Sleep(10 * time.Millisecond) // the queue is full: keep it and try again
		}
	}()
}

// A simulation jumps across idle hours and retains a replay trace. Builders
// that own workers implement app.SimCloser to join them before clock restore.
func ExampleSim() {
	s, err := app.NewSim(app.Config{}, 640, 400, 1, &editor{name: "Notes"}, app.SimOptions{Seed: 7})
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	if err = s.Advance(time.Second); err != nil {
		log.Fatal(err)
	}
	if err = s.Event(0, platform.Event{Kind: platform.EvVisible}); err != nil {
		log.Fatal(err)
	}
	if _, err = s.Idle(4*time.Hour, 0); err != nil {
		log.Fatal(err)
	}
	if err = s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}); err != nil {
		log.Fatal(err)
	}
	if err = s.Advance(time.Second); err != nil {
		log.Fatal(err)
	}
	log.Printf("replay %016x", s.TraceHash())
}

// An app with one window: the Builder is the whole program.
func ExampleRun() {
	err := app.Run(app.Config{Title: "Hello", Width: 480, Height: 320}, &editor{name: "hello"})
	if err != nil {
		log.Fatal(err)
	}
}
