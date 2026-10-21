package pilot

import "gonativeui/menu"

// Pilot's menu model: a View menu with the window's zoom (⌘+, ⌘−, ⌘0). The
// bar's Edit menu is the standard one the app adds (its items act on the text
// field that has the keyboard: the filter, the path); Pilot's own shortcuts
// stay with its key handler.
const (
	cmdZoomIn menu.ID = iota + 1
	cmdZoomOut
	cmdActualSize
)

func newMenuModel() (*menu.Model, error) {
	cmds := []menu.Command{
		menu.StandardCommand(cmdZoomIn, menu.RoleZoomIn),
		menu.StandardCommand(cmdZoomOut, menu.RoleZoomOut),
		menu.StandardCommand(cmdActualSize, menu.RoleActualSize),
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	return menu.NewModel(cmds, []menu.Menu{{Title: "View", Items: []menu.Item{item(cmdActualSize), item(cmdZoomIn), item(cmdZoomOut)}}}, []int{0})
}
