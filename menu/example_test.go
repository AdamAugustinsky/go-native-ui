package menu_test

import (
	"fmt"

	"gonativeui/gpu"
	"gonativeui/internal/fakeui"
	"gonativeui/menu"
	"gonativeui/ui"
)

const (
	cmdNew menu.ID = iota + 1
	cmdRemove
	cmdZoomIn
)

// A command is an id, a title and a shortcut; menus list commands; NewModel
// checks them all. Each frame, Build calls Match first and then asks for the
// commands that fired, whether a shortcut or a menu item delivered them. In an
// app, App.SetMenu makes the model the native menu bar on macOS and
// widget.Kit.MenuBar draws it elsewhere.
func Example() {
	primary := func(k ui.KeyCode, m ui.Mods) menu.Shortcut { return menu.Shortcut{Key: k, Mods: m, Primary: true} }
	m, err := menu.NewModel(
		[]menu.Command{
			{ID: cmdNew, Title: "New Note", Shortcut: primary('N', 0)},
			{ID: cmdRemove, Title: "Remove…", Shortcut: primary(ui.KeyBackspace, 0)},
			menu.StandardCommand(cmdZoomIn, menu.RoleZoomIn),
		},
		[]menu.Menu{
			{Title: "Note", Items: []menu.Item{
				{Kind: menu.ItemCommand, Command: cmdNew},
				{Kind: menu.ItemCommand, Command: cmdRemove},
			}},
			{Title: "View", Items: []menu.Item{{Kind: menu.ItemCommand, Command: cmdZoomIn}}},
		},
		[]int{0, 1}, // the menus in the bar
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	m.SetEnabled(cmdRemove, false) // set each frame, from the app's state

	c := ui.New(fakeui.New())
	var in ui.Input
	var list gpu.DrawList
	in.Inside, in.Focused = true, true

	// the user presses the primary modifier and N
	in.PushKey(ui.KeyEvent{Code: 'N', Mods: ui.PrimaryMod(), Down: true})
	c.Begin(&in, 400, 300, 1, 1.0/60)
	m.Match(c) // first thing in Build
	if m.Fired(cmdNew) {
		fmt.Println("new note")
	}
	if m.Fired(cmdRemove) {
		fmt.Println("remove")
	}
	c.End(&list)
	// Output: new note
}
