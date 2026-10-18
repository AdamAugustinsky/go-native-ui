package gallery

import (
	"gonativeui/menu"
	"gonativeui/ui"
)

const shift = ui.ModShift

// The commands of the gallery's menus.
const (
	cmdOpen menu.ID = iota + 1
	cmdSave
	cmdRecent1
	cmdRecent2
	cmdRecent3
	cmdPrint
	cmdUndo
	cmdRedo
	cmdCut
	cmdCopy
	cmdPaste
	cmdSelectAll
	cmdToggleDark
	cmdRowNumbers
	cmdRowOpen
	cmdRowCopyPath
	cmdRowInfo
)

// Menu indices in the model.
const (
	menuFile = iota
	menuEdit
	menuView
	menuRecent
	menuRow // the context menu of the table rows; not in the bar
)

// newModel builds the command and menu model: a File, Edit and View menu in
// the bar, and the table's row menu.
func newModel() (*menu.Model, error) {
	cmd := func(id menu.ID, title string, key ui.KeyCode) menu.Command {
		c := menu.Command{ID: id, Title: title}
		if key != 0 {
			c.Shortcut = menu.Shortcut{Key: key, Primary: true}
		}
		return c
	}
	cmds := []menu.Command{
		cmd(cmdOpen, "Open…", 'O'),
		cmd(cmdSave, "Save", 'S'),
		cmd(cmdRecent1, "notes.txt", 0),
		cmd(cmdRecent2, "report.md", 0),
		cmd(cmdRecent3, "table.csv", 0),
		cmd(cmdPrint, "Print…", 'P'),
		{ID: cmdUndo, Role: menu.RoleUndo, Shortcut: menu.Shortcut{Key: 'Z', Primary: true}},
		{ID: cmdRedo, Role: menu.RoleRedo, Shortcut: menu.Shortcut{Key: 'Z', Primary: true, Mods: shift}},
		{ID: cmdCut, Role: menu.RoleCut, Shortcut: menu.Shortcut{Key: 'X', Primary: true}},
		{ID: cmdCopy, Role: menu.RoleCopy, Shortcut: menu.Shortcut{Key: 'C', Primary: true}},
		{ID: cmdPaste, Role: menu.RolePaste, Shortcut: menu.Shortcut{Key: 'V', Primary: true}},
		{ID: cmdSelectAll, Role: menu.RoleSelectAll, Shortcut: menu.Shortcut{Key: 'A', Primary: true}},
		{ID: cmdToggleDark, Title: "Toggle Dark Mode", Shortcut: menu.Shortcut{Key: 'D', Primary: true, Mods: shift}},
		cmd(cmdRowNumbers, "Show Row Numbers", 0),
		cmd(cmdRowOpen, "Open", 0),
		cmd(cmdRowCopyPath, "Copy Path", 0),
		cmd(cmdRowInfo, "Show Info", 0),
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	sep := menu.Item{Kind: menu.ItemSeparator}
	menus := []menu.Menu{
		menuFile: {Title: "File", Items: []menu.Item{item(cmdOpen), item(cmdSave),
			{Kind: menu.ItemSubmenu, Submenu: menuRecent, Title: "Open Recent"}, sep, item(cmdPrint)}},
		menuEdit: {Title: "Edit", Items: []menu.Item{item(cmdUndo), item(cmdRedo), sep, item(cmdCut), item(cmdCopy),
			item(cmdPaste), item(cmdSelectAll)}},
		menuView:   {Title: "View", Items: []menu.Item{item(cmdToggleDark), item(cmdRowNumbers)}},
		menuRecent: {Title: "Open Recent", Items: []menu.Item{item(cmdRecent1), item(cmdRecent2), item(cmdRecent3)}},
		menuRow:    {Title: "Row", Items: []menu.Item{item(cmdRowOpen), item(cmdRowCopyPath), sep, item(cmdRowInfo)}},
	}
	return menu.NewModel(cmds, menus, []int{menuFile, menuEdit, menuView})
}
