package tray_test

import (
	"gonativeui/app"
	"gonativeui/app/tray"
	"gonativeui/menu"
)

const cmdQuit menu.ID = 1

// resident is an app that lives in the menu bar: left click shows the window,
// right click opens the menu.
type resident struct {
	app.DefaultHandler
	icon *tray.Item
}

func (r *resident) Start(a *app.App) error {
	t, err := tray.New(tray.Options{
		Icon:           tray.SymbolIcon("bubble.left.fill"), // or tray.PNGIcon(png, true)
		Tooltip:        "Chat",
		RightClickMenu: true, // left click: an event; right click: the menu
	})
	if err != nil {
		return nil // not macOS: no status item; the app runs without one
	}
	m, err := menu.NewModel(
		[]menu.Command{{ID: cmdQuit, Title: "Quit Chat"}},
		[]menu.Menu{{Title: "Tray", Items: []menu.Item{{Kind: menu.ItemCommand, Command: cmdQuit}}}},
		nil,
	)
	if err != nil {
		return err
	}
	t.SetMenu(m, 0) // the menu's commands arrive as EvCommand
	t.SetTitle("3") // text beside the icon: an unread count
	r.icon = t
	return nil
}

func (r *resident) Event(a *app.App, e *app.Event) {
	if r.icon != nil {
		if click, ok := r.icon.Clicked(e); ok {
			_ = click.Right // a right click opens the menu by itself; a left click shows the window
		}
	}
	if e.Kind == app.EvCommand && e.ID == uint64(cmdQuit) {
		a.RequestQuit()
	}
	a.DefaultEvent(e)
}

// A status item in the system's status area, with a menu and clicks.
func Example() {
	app.Main(app.AppConfig{ID: "dev.example.chat", KeepRunning: true, Policy: app.PolicyAccessory}, &resident{})
}
