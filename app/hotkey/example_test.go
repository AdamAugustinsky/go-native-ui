package hotkey_test

import (
	"errors"

	"gonativeui/app"
	"gonativeui/app/hotkey"
	"gonativeui/ui"
)

const paletteKey hotkey.ID = 1

// palette is a resident app whose palette panel opens on ⌥⌘K (Ctrl+Alt+K on
// Windows).
type palette struct {
	app.DefaultHandler
	win   *app.Win
	taken bool
}

func (p *palette) Start(a *app.App) error {
	err := hotkey.Register(paletteKey, hotkey.Shortcut{Key: 'K', Mods: ui.ModAlt, Primary: true})
	p.taken = errors.Is(err, hotkey.ErrTaken) // the settings offer another chord
	return nil                                // no hotkeys on this OS: the app runs without one
}

func (p *palette) Event(a *app.App, e *app.Event) {
	if id, ok := hotkey.Pressed(e); ok && id == paletteKey && p.win != nil {
		p.win.Show()
	}
	a.DefaultEvent(e)
}

// Example registers a global hotkey and opens a panel when it is pressed.
func Example() {
	_ = &palette{}
}
