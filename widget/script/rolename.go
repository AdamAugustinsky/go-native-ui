package script

import (
	"fmt"
	"strconv"
	"strings"

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

// ParseRole is the role that RoleName writes under the name, compared without
// regard to case ("button", "ListItem", "textfield"); ok is false for a name
// no role has. It reads the "Role<n>" form of a role RoleName has no name for.
func ParseRole(name string) (r ui.Role, ok bool) {
	for role, n := range roleNames {
		if strings.EqualFold(n, name) {
			return role, true
		}
	}
	if v, found := strings.CutPrefix(name, "Role"); found {
		if n, err := strconv.ParseUint(v, 10, 8); err == nil && n != 0 && roleNames[ui.Role(n)] == "" {
			return ui.Role(n), true
		}
	}
	return ui.RoleNone, false
}
