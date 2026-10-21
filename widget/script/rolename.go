package script

import (
	"fmt"

	"gonativeui/ui"
)

var roleNames = map[ui.Role]string{
	ui.RoleWindow: "Window", ui.RoleGroup: "Group", ui.RoleLabel: "Label", ui.RoleImage: "Image",
	ui.RoleButton: "Button", ui.RoleCheckbox: "Checkbox", ui.RoleSwitch: "Switch", ui.RoleRadio: "Radio",
	ui.RoleRadioGroup: "RadioGroup", ui.RoleSlider: "Slider", ui.RoleTextField: "TextField",
	ui.RoleSearchField: "SearchField", ui.RoleComboBox: "ComboBox", ui.RoleList: "List",
	ui.RoleListItem: "ListItem", ui.RoleTable: "Table", ui.RoleRow: "Row", ui.RoleCell: "Cell",
	ui.RoleColumnHeader: "ColumnHeader", ui.RoleScrollArea: "ScrollArea", ui.RoleScrollBar: "ScrollBar",
	ui.RoleSplitter: "Splitter", ui.RoleTabList: "TabList", ui.RoleTab: "Tab", ui.RoleTabPanel: "TabPanel",
	ui.RoleMenuBar: "MenuBar", ui.RoleMenu: "Menu", ui.RoleMenuItem: "MenuItem",
	ui.RoleMenuItemCheckbox: "MenuItemCheckbox", ui.RoleDialog: "Dialog", ui.RoleTooltip: "Tooltip",
	ui.RoleProgressBar: "ProgressBar", ui.RoleLink: "Link", ui.RoleHeading: "Heading",
	ui.RoleToolbar: "Toolbar", ui.RoleSheet: "Sheet", ui.RoleHost: "Host",
}

// RoleName is the name of a role as Dump writes it ("Button", "ListItem").
func RoleName(r ui.Role) string {
	if n := roleNames[r]; n != "" {
		return n
	}
	return fmt.Sprintf("Role%d", r)
}
