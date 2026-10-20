// Command windowsdemo opens several windows of one app (DECISIONS D30, phase
// 6's F4), to try by hand what the tests cannot look at: each window has its
// own text field, native list menu, animation, sheet and worker, and the
// window commands are its buttons. ⌘N (File > New Window) opens another
// window; the menu bar follows the key window.
//
//	windowsdemo           one window; ⌘N for more
//	windowsdemo -n 3      start with three
//	windowsdemo -keep     the app runs on with no window open (the Dock's icon opens one again)
//
// On Windows and Linux a second window is refused (app.ErrOneWindow); the
// demo shows the error in its window.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"gonativeui/app"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

const (
	cmdNew menu.ID = iota + 1
	cmdClose
)

func main() {
	n := flag.Int("n", 1, "the windows to open at start")
	keep := flag.Bool("keep", false, "keep running with no window open")
	flag.Parse()
	d := &demo{n: max(*n, 1)}
	if err := app.Main(app.AppConfig{KeepRunning: *keep}, d); err != nil {
		fmt.Fprintln(os.Stderr, "windowsdemo:", err)
		os.Exit(1)
	}
}

// demo is the app: it opens the windows.
type demo struct {
	n, opened int
	last      string // the last error of an OpenWindow
	wins      map[int]*app.Win
}

func (d *demo) Start(a *app.App) error {
	for i := 0; i < d.n; i++ {
		if err := d.open(a); err != nil && i == 0 {
			return err
		}
	}
	return nil
}

// The posts of the windows' timers (app.Post, from their goroutines): the
// window commands run here, on the main thread.
const (
	postShow = iota + 1
	postAttention
)

func (d *demo) Event(a *app.App, e *app.Event) {
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

// window is one window's builder.
type window struct {
	d       *demo
	n       int
	a       *app.App
	k       *widget.Kit
	model   *menu.Model
	field   ui.TextBuffer
	sel     int
	animate bool
	x       float32
	results chan string
	status  string
	edited  bool
	renames int
}

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

// CloseRequested refuses the close button while the document is edited.
func (w *window) CloseRequested(*app.App) bool {
	if w.edited {
		w.status = "close refused: the document is edited (untick Edited)"
		return false
	}
	return true
}

func (w *window) Build(c *ui.Context) {
	win := w.a.Window()
	for r, ok := w.a.TakeDialog(); ok; r, ok = w.a.TakeDialog() {
		w.status = fmt.Sprintf("sheet answered: button %d", r.Button)
	}
	for drained := false; !drained; {
		select {
		case s := <-w.results:
			w.status = s
		default:
			drained = true
		}
	}
	k := w.k
	if k.Command(cmdNew) {
		w.d.open(w.a) // inside a frame: opens after it
	}
	if k.Command(cmdClose) {
		win.Close()
	}
	k.Begin(c)
	k.NextPad(14)
	k.BeginWindow()
	k.NextHeight(widget.Fill)
	k.BeginCard("card")
	k.LabelStyle(fmt.Sprintf("Window %d (id %d) of %d open", w.n, win.ID(), len(w.a.Windows(nil))), widget.TextSemi)
	k.TextField("Text", &w.field, "Type here: keys go to the key window")
	k.Select("Choice", &w.sel, []string{"Red", "Green", "Blue"})

	k.Row("frame")
	if k.Button("Animate").Clicked {
		w.animate = !w.animate
	}
	if k.Button("Sheet").Clicked {
		if _, err := w.a.Alert(platform.AlertSpec{Message: fmt.Sprintf("A sheet on window %d", w.n), Buttons: []string{"OK", "Cancel"}}); err != nil {
			w.status = err.Error()
		}
	}
	if k.Button("Worker").Clicked {
		go func(n int) { // a result for this window only: the others draw nothing
			time.Sleep(time.Second)
			w.results <- fmt.Sprintf("worker done at %s", time.Now().Format("15:04:05"))
			win.Wake()
		}(w.n)
		w.status = "worker running (1 s)"
	}
	if k.Button("New window").Clicked {
		w.d.open(w.a)
	}
	k.EndRow()

	k.Row("commands")
	if k.Button("Rename").Clicked {
		w.renames++
		win.SetTitle(fmt.Sprintf("Window %d, renamed %d", w.n, w.renames))
	}
	if k.Button("Move").Clicked {
		x, y, wd, h := win.Frame()
		win.SetFrame(x+40, y+30, wd, h)
	}
	if k.Button("Full screen").Clicked {
		win.ToggleFullscreen()
	}
	if k.Button("Dock").Clicked {
		win.Miniaturize()
	}
	if k.Button("Hide 2 s").Clicked {
		win.Hide()
		time.AfterFunc(2*time.Second, func() { app.Post(app.Event{ID: uint64(w.n*10 + postShow)}, nil) })
	}
	k.EndRow()

	k.Row("more")
	if k.Button("Attention in 3 s").Clicked {
		time.AfterFunc(3*time.Second, func() { app.Post(app.Event{ID: uint64(w.n*10 + postAttention)}, nil) })
		w.status = "switch to another app: the Dock icon bounces in 3 s"
	}
	if k.Button("Edited").Clicked {
		w.edited = !w.edited
	}
	win.SetDocumentEdited(w.edited) // unchanged, it does nothing
	if k.Button("Close").Clicked {
		win.Close()
	}
	k.EndRow()
	k.LabelStyle(w.status, widget.TextMuted)
	if w.d.last != "" {
		k.LabelStyle("OpenWindow: "+w.d.last, widget.TextMuted)
	}
	k.EndCard()
	k.EndWindow()
	if w.animate {
		w.x = float32(int(w.x+4) % 300)
		c.NextPos(c.Pt(20+w.x), c.Pt(320))
		c.NextSize(ui.Px(c.Pt(24)), ui.Px(c.Pt(24)))
		c.NextFlags(ui.FloatingX | ui.FloatingY)
		c.Box(0, "mover")
		c.RequestFrame()
	}
	k.End()
}
