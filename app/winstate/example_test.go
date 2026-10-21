package winstate_test

import (
	"fmt"
	"log"

	"gonativeui/app"
	"gonativeui/app/winstate"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

type view struct{ kit *widget.Kit }

func (v *view) Init(a *app.App) error {
	v.kit = widget.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}
func (v *view) Build(c *ui.Context) { v.kit.Begin(c); v.kit.End() }

// Restoring a window where the user left it: load the saved state before the
// window exists, let Apply fit it to the screens there are now, open the
// window with the result, and save the state as the window changes. The file
// is read and written off the UI thread (app/prefs); nothing here runs per
// frame.
func Example() {
	st, err := winstate.Load("dev.example.notes")
	if err != nil {
		log.Print(err) // unreadable or corrupt file: st still works, on the defaults
	}
	defer st.Close() // at quit: writes what is still pending

	pl := winstate.Apply(
		winstate.Config{Width: 900, Height: 640, MinWidth: 480, MinHeight: 320},
		st.State(),
		winstate.ScreensFromPlatform(app.Screens(nil)),
	)
	cfg := app.WindowConfig{Title: "Notes", Width: 900, Height: 640}
	if pl.Restored {
		cfg.Width, cfg.Height = float32(pl.Frame.W), float32(pl.Frame.H)
	}
	app.Main(app.AppConfig{ID: "dev.example.notes"}, &host{cfg: cfg})

	// later, on every move, resize and change:
	st.Save(winstate.State{Frame: winstate.Rect{X: 100, Y: 80, W: 900, H: 640}})
}

type host struct {
	app.DefaultHandler
	cfg app.WindowConfig
}

func (h *host) Start(a *app.App) error {
	_, err := a.OpenWindow(h.cfg, &view{})
	return err
}

// Apply is arithmetic on plain values: a window saved on a monitor that is no
// longer there comes back, kept on the screen that exists.
func ExampleApply() {
	screens := []winstate.Screen{{
		Frame:   winstate.Rect{X: 0, Y: 0, W: 1440, H: 900},
		Visible: winstate.Rect{X: 0, Y: 25, W: 1440, H: 850}, // without the menu bar and the Dock
		Scale:   2,
	}}
	saved := winstate.State{
		Frame:  winstate.Rect{X: 3000, Y: 200, W: 900, H: 640}, // on a monitor that was unplugged
		Screen: winstate.Screen{ID: "external"},
	}
	pl := winstate.Apply(winstate.Config{Width: 900, Height: 640}, saved, screens)
	fmt.Println(pl.Restored, pl.Adjusted, pl.Frame)
	// Output: true true {270 130 900 640}
}
