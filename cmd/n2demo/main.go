// Command n2demo is a small demo of the native dialogs and tooltips of phase 6
// (N2): alert sheets, the open and save panels, and native tooltips.
//
//	n2demo              open the window; the buttons start the dialogs, and the last result is shown
//	n2demo -do alert    start a dialog as soon as the window is up: alert, destructive, suppress, open or save
//	n2demo -start dir   where the panels open
//	n2demo -mode dark   light, dark or system (the default): the window and its sheets follow it
//	n2demo -hover Open… post mouse-moved events over that button into the demo's own window (the system pointer
//	                    stays where it is), so the system shows the button's native tooltip; macOS
//	n2demo -quit-after 30s
//	                    exit by itself after this long, for capture runs
//	n2demo -shot dir    macOS: with -do, draw the sheet into dir/<name>.png from inside the process (light only;
//	                    no screen recording needed, so the panels' hosted content is missing), then exit
//
// Where the OS has no native dialogs the buttons say so; an app falls back to
// widget.Dialog there (errors.Is(err, errors.ErrUnsupported)).
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

type demo struct {
	a        *app.App
	k        *widget.Kit
	doName   string // the dialog to start by itself
	shotDir  string
	frame    int
	started  bool
	ticket   platform.DialogTicket
	last     string
	shotDone bool
	mode     theme.Preference
	hover    string     // the label of the button to hover
	hoverBox [4]float32 // its rectangle in pixels, as of the last frame
	quitAt   int64
	quitIn   time.Duration
	hovered  int
	startDir string
	shotAt   int64
}

func main() {
	do := flag.String("do", "", "start a dialog once the window is up: alert, destructive, suppress, open or save")
	shot := flag.String("shot", "", "with -do (macOS): draw the sheet into this directory (light) and exit")
	mode := flag.String("mode", "system", "system, light or dark")
	hover := flag.String("hover", "", "the label of a button to hover with posted mouse events (macOS), to show its tooltip")
	start := flag.String("start", "", "the directory the open and save panels start in (default: the system's choice)")
	quit := flag.Duration("quit-after", 0, "exit after this long (0: never)")
	flag.Parse()
	d := &demo{doName: *do, shotDir: *shot, hover: *hover, quitIn: *quit, startDir: *start}
	switch *mode {
	case "light":
		d.mode = theme.ForceLight
	case "dark":
		d.mode = theme.ForceDark
	}
	cfg := app.Config{Title: "Native dialogs", Width: 1000, Height: 600}
	if err := app.Run(cfg, d); err != nil {
		fmt.Fprintln(os.Stderr, "n2demo:", err)
		os.Exit(1)
	}
}

// Init implements app.Initer.
func (d *demo) Init(a *app.App) error {
	d.a = a
	d.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	d.k.SetPreference(d.mode)
	return nil
}

// Context lets a test or script read the semantic tree.
func (d *demo) Context() *ui.Context { return d.k.Context() }

func (d *demo) start(name string) {
	var err error
	switch name {
	case "alert":
		d.ticket, err = d.a.Alert(platform.AlertSpec{Style: platform.AlertInformational, Message: "Save changes to “Untitled”?",
			Info: "Your changes will be lost if you don’t save them.", Buttons: []string{"Save", "Don’t Save", "Cancel"}})
	case "destructive":
		d.ticket, err = d.a.Alert(platform.AlertSpec{Style: platform.AlertCritical, Message: "Delete 3 notes?",
			Info: "This cannot be undone.", Buttons: []string{"Delete", "Cancel"}, Destructive: []int{0}})
	case "suppress":
		d.ticket, err = d.a.Alert(platform.AlertSpec{Style: platform.AlertWarning, Message: "Quit while a download runs?",
			Info: "The download will stop.", Buttons: []string{"Quit", "Cancel"}, Suppression: "Do not ask again"})
	case "open":
		d.ticket, err = d.a.OpenPanel(platform.OpenSpec{Files: true, Dirs: true, Multiple: true, Types: []string{"public.image", ".md"},
			Message: "Choose images or Markdown files", Start: d.startDir})
	case "save":
		d.ticket, err = d.a.SavePanel(platform.SaveSpec{Name: "Untitled.txt", Types: []string{"public.plain-text"}, Prompt: "Save", Start: d.startDir})
	}
	switch {
	case errors.Is(err, errors.ErrUnsupported):
		d.last = "no native dialogs here: show the drawn Dialog"
	case err != nil:
		d.last = "error: " + err.Error()
	}
}

func describe(r platform.DialogResult) string {
	kind := [...]string{"alert", "open panel", "save panel"}[r.Kind]
	if r.Outcome == platform.DialogCancelled {
		return fmt.Sprintf("%s #%d: cancelled", kind, r.Ticket)
	}
	switch r.Kind {
	case platform.DialogAlert:
		return fmt.Sprintf("%s #%d: button %d, suppressed %v", kind, r.Ticket, r.Button, r.Suppressed)
	default:
		return fmt.Sprintf("%s #%d: %s", kind, r.Ticket, strings.Join(r.Paths, ", "))
	}
}

// Build implements app.Builder.
func (d *demo) Build(c *ui.Context) {
	k := d.k
	d.frame++
	for r, ok := d.a.TakeDialog(); ok; r, ok = d.a.TakeDialog() {
		d.last = describe(r)
	}
	if d.doName != "" && !d.started {
		c.WakeIn(50e6) // an idle app draws no frames: ask for the next
	}
	if d.doName != "" && !d.started && d.frame > 3 {
		d.started = true
		d.start(d.doName)
	}
	d.shoot(c)
	d.pointAt(c)
	k.Begin(c)
	k.NextPad(10)
	k.BeginWindow()
	k.NextHeight(widget.Fill)
	k.BeginCard("card")
	k.LabelStyle("Native dialogs and tooltips", widget.TextSemi)
	k.Row("alerts")
	for _, b := range [...]struct{ label, name, tip string }{
		{"Alert", "alert", "A sheet with three buttons"},
		{"Delete…", "destructive", "A critical alert with a destructive button"},
		{"Quit…", "suppress", "An alert with a suppression checkbox"},
	} {
		s := k.Button(b.label)
		d.track(b.label, s.ID)
		k.Tooltip(s.ID, b.tip)
		if s.Clicked {
			d.start(b.name)
		}
	}
	k.EndRow()
	k.Row("panels")
	open := k.Button("Open…")
	d.track("Open…", open.ID)
	k.Tooltip(open.ID, "Choose files or folders")
	save := k.Button("Save…")
	d.track("Save…", save.ID)
	k.Tooltip(save.ID, "Choose where to save")
	if open.Clicked {
		d.start("open")
	}
	if save.Clicked {
		d.start("save")
	}
	k.EndRow()
	k.LabelStyle(d.last, widget.TextMuted)
	k.EndCard()
	k.EndWindow()
	k.End()
}

// track remembers where the hovered button is.
func (d *demo) track(label string, id ui.ID) {
	if label == d.hover {
		d.hoverBox = d.k.Context().Rect(id)
	}
}

// pointAt quits on time and, with -hover, posts mouse-moved events over the
// button into the window's own queue (a small jiggle: the system's tooltip
// timer starts on a move), every 100 ms, so the tooltip shows and stays.
func (d *demo) pointAt(c *ui.Context) {
	now := c.Input().Time
	if d.quitIn > 0 {
		if d.quitAt == 0 {
			d.quitAt = now + int64(d.quitIn)
		}
		if now >= d.quitAt {
			d.a.Quit()
			return
		}
		c.WakeIn(100e6)
	}
	if d.hover == "" || d.hoverBox == [4]float32{} {
		return
	}
	if d.hovered++; d.hovered == 1 { // when the pointer arrived, for timing the system's tooltip delay
		_ = os.WriteFile(os.TempDir()+"/n2demo-hover.txt", []byte(time.Now().Format(time.RFC3339Nano)+"\n"), 0o644)
	}
	_, _, scale := d.a.Size()
	x := float64(d.hoverBox[0]+d.hoverBox[2]) / 2 / float64(scale)
	y := float64(d.hoverBox[1]+d.hoverBox[3]) / 2 / float64(scale)
	d.postMouse(x+float64(d.hovered%2), y)
	c.WakeIn(100e6)
}
