// Command paneldemo tries the panel windows and global hotkeys of phase 8
// (W2, W7; DECISIONS D69, D70 in docs/PHASE8-PLAN.md): a Spotlight-style
// palette that ⌥⌘K shows and hides from any app, and a recording bar that
// floats above everything on every Space, never takes the keyboard and is
// left out of screen captures. Both are Liquid Glass panels in the Rex look.
//
//	paneldemo                     the palette on ⌥⌘K (Esc hides it), the bar shown
//	paneldemo -mode dark          force the dark (or light) appearance
//	paneldemo -noglass            draw the panels' shapes instead of glass
//	paneldemo -keep               the palette stays when the app deactivates
//	paneldemo -measure 50         hotkey → palette on glass, 50 times; JSON on stdout
//
// -measure presses the hotkey through Carbon's own handler
// (hotkey.Simulate) and times each press to the palette's first frame on
// the display (Metal's present feedback), warm. On Windows and Linux there
// are no panels or hotkeys yet: the demo says so and exits.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"gonativeui/app"
	"gonativeui/app/hotkey"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

const paletteKey hotkey.ID = 1

var (
	mode    = flag.String("mode", "", "light or dark (default: the system's)")
	noGlass = flag.Bool("noglass", false, "draw the panels' shapes instead of Liquid Glass")
	keep    = flag.Bool("keep", false, "keep the palette when the app deactivates (no HideOnDeactivate)")
	measure = flag.Int("measure", 0, "measure hotkey → palette on glass this many times, print JSON and quit")
)

func main() {
	flag.Parse()
	if !platform.PanelsSupported() {
		fmt.Fprintln(os.Stderr, "paneldemo: no panel windows on this OS yet (phase 8 W2)")
		os.Exit(2)
	}
	d := &demo{}
	if err := app.Main(app.AppConfig{KeepRunning: true, Policy: platform.PolicyAccessory}, d); err != nil {
		fmt.Fprintln(os.Stderr, "paneldemo:", err)
		os.Exit(1)
	}
}

// demo is the app: the hotkey, the palette and the bar.
type demo struct {
	palette, bar *app.Win
	pal          *panel
	hotkeyErr    error
	m            *measurement // -measure (measure_darwin.go)
}

func (d *demo) Start(a *app.App) error {
	backdrop := platform.BackdropGlass
	if *noGlass {
		backdrop = platform.BackdropNone
	}
	d.pal = &panel{d: d, palette: true, radius: 26, glass: !*noGlass}
	var err error
	d.palette, err = a.OpenWindow(app.Config{Title: "Palette", Width: 640, Height: 260, Backdrop: backdrop,
		Panel: &platform.Panel{Floating: true, HideOnDeactivate: !*keep, Shadow: true, Radius: 26}}, d.pal)
	if err != nil {
		return err
	}
	d.bar, err = a.OpenWindow(app.Config{Title: "Recording", Width: 300, Height: 44, Backdrop: backdrop,
		Panel: &platform.Panel{StatusBar: true, NonActivating: true, AllSpaces: true, ExcludeFromCapture: true, Shadow: true, Radius: 22}},
		&panel{d: d, radius: 22, glass: !*noGlass})
	if err != nil {
		return err
	}
	d.place(a)
	d.hotkeyErr = hotkey.Register(paletteKey, hotkey.Shortcut{Key: 'K', Mods: ui.ModAlt, Primary: true})
	if errors.Is(d.hotkeyErr, hotkey.ErrTaken) {
		fmt.Fprintln(os.Stderr, "paneldemo: ⌥⌘K is taken by another app")
	}
	if *measure > 0 {
		d.startMeasure(a)
		return nil
	}
	// Shown once the launch is over: a HideOnDeactivate panel shown while
	// an app that cannot activate finishes launching is hidden again.
	time.AfterFunc(300*time.Millisecond, func() { app.Post(app.Event{ID: postShow}, nil) })
	return nil
}

// place puts the palette in the upper third of the main screen and the bar
// at its top, as a3-kb does.
func (d *demo) place(a *app.App) {
	scr := app.Screens(nil)
	if len(scr) == 0 {
		return
	}
	v := scr[0]
	d.palette.SetFrame(v.VisibleX+(v.VisibleW-640)/2, v.VisibleY+v.VisibleH*0.14, 640, 260)
	d.bar.SetFrame(v.VisibleX+(v.VisibleW-300)/2, v.VisibleY+14, 300, 44)
}

func (d *demo) Event(a *app.App, e *app.Event) {
	if id, ok := hotkey.Pressed(e); ok && id == paletteKey {
		d.pressed(a)
		return
	}
	if e.Kind == app.EvPost && d.post(a, e) {
		return
	}
	a.DefaultEvent(e)
}

// pressed toggles the palette (and, measuring, times the press).
func (d *demo) pressed(a *app.App) {
	if d.m != nil {
		d.m.pressed(d)
		return
	}
	if d.pal.shown {
		d.palette.Hide()
		d.pal.shown = false
		return
	}
	d.pal.query.Reset()
	d.palette.Show()
}

// panel is the palette's builder, or the bar's.
type panel struct {
	d       *demo
	a       *app.App
	k       *base.Kit
	palette bool
	glass   bool
	radius  float32
	shown   bool
	query   ui.TextBuffer
	built   func() // measuring: called on each build
}

var results = []string{"Liquid Glass panels (D69)", "Global hotkeys without permissions (D70)", "Windows parity, phase 8"}

func (p *panel) Init(a *app.App) error {
	p.a = a
	p.k = base.NewKit(a.UI, a.Text, rex.Theme())
	switch *mode {
	case "dark":
		p.k.SetMode(theme.Dark)
	case "light":
		p.k.SetMode(theme.Light)
	}
	p.query.Max = 200
	return nil
}

func (p *panel) Build(c *ui.Context) {
	if p.built != nil {
		p.built()
	}
	p.shown = true
	k := p.k
	k.Begin(c)
	// The material is the panel's field (rex.md §2); a square field or scrim
	// would square the panel's corners, so only -noglass paints its shape.
	k.Theme().SetBackdrop(p.glass && p.a.Win != nil && p.a.Win.Backdrop() != platform.BackdropNone)
	if !p.glass {
		c.NextBG(k.Theme().Role(theme.Window))
		c.NextRadius(p.radius)
	}
	k.NextWidth(ui.Pct(1))
	k.NextHeight(ui.Pct(1))
	if p.palette {
		k.NextPad(18)
		k.NextGap(10)
		k.Column("palette")
		if c.TakeKey(ui.KeyEscape, 0) {
			p.d.palette.Hide()
			p.shown = false
		}
		rexui.SearchField(k, "Search", &p.query, "Search the knowledge base", rexui.FieldOpts{Size: theme.SizeLarge})
		for _, r := range results {
			rexui.Label(k, r, rexui.LabelOpts{Style: rexui.TextHeadline})
		}
		c.NextSize(ui.Pct(1), ui.Grow(1)) // the hint at the bottom
		c.Box(0, "spacer")
		rexui.Label(k, p.d.hint(), rexui.LabelOpts{Style: rexui.TextFootnote, Color: rexui.Of(theme.Label2), MonoPt: 10})
		k.EndColumn()
	} else {
		k.NextPadding(18, 0, 8, 0)
		k.NextGap(10)
		k.Row("bar")
		rexui.Label(k, "● Recording", rexui.LabelOpts{Style: rexui.TextHeadline})
		k.NextWidth(ui.Grow(1)) // the time takes the room, Stop sits at the end
		rexui.Label(k, "0:42", rexui.LabelOpts{Style: rexui.TextHeadline, Color: rexui.Of(theme.Label2), MonoPt: 12})
		if rexui.Button(k, "Stop", rexui.ButtonOpts{}).Clicked {
			p.a.Quit()
		}
		k.EndRow()
	}
	k.End()
}

func (d *demo) hint() string {
	switch {
	case d.hotkeyErr != nil:
		return "⌥⌘K: " + d.hotkeyErr.Error()
	default:
		return "⌥⌘K shows and hides this palette · Esc hides it"
	}
}
