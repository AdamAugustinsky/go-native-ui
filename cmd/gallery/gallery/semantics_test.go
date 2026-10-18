package gallery

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gonativeui/ui"
)

var update = flag.Bool("update", false, "rewrite the golden files of testdata")

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
	ui.RoleProgressBar: "ProgressBar",
}

var semStateNames = [...]struct {
	bit  ui.SemState
	name string
}{
	{ui.SemChecked, "checked"}, {ui.SemMixed, "mixed"}, {ui.SemSelected, "selected"},
	{ui.SemExpanded, "expanded"}, {ui.SemDisabled, "disabled"}, {ui.SemFocused, "focused"},
	{ui.SemEditable, "editable"}, {ui.SemModal, "modal"},
}

// dump writes the semantic tree as indented text: roles, names, values, states,
// ranges and positions in a list, without bounds (which depend on the fonts).
func dump(t *ui.SemTree) string {
	var b strings.Builder
	depth := make([]int, len(t.Nodes))
	for i, n := range t.Nodes {
		if n.Parent >= 0 {
			depth[i] = depth[n.Parent] + 1
		}
		b.WriteString(strings.Repeat("  ", depth[i]))
		role := roleNames[n.Role]
		if role == "" {
			role = fmt.Sprintf("Role%d", n.Role)
		}
		b.WriteString(role)
		if name := t.Text(n.Name); len(name) > 0 {
			fmt.Fprintf(&b, " %q", name)
		}
		if v := t.Text(n.Value); len(v) > 0 {
			fmt.Fprintf(&b, " value=%q", v)
		}
		var st []string
		for _, s := range semStateNames {
			if n.State&s.bit != 0 {
				st = append(st, s.name)
			}
		}
		if len(st) > 0 {
			fmt.Fprintf(&b, " [%s]", strings.Join(st, " "))
		}
		if n.Range != [4]float64{} && n.Role != ui.RoleScrollBar { // a scroll bar's range is the layout's, in float noise
			fmt.Fprintf(&b, " range=%g..%g value=%.3g step=%g", n.Range[0], n.Range[1], n.Range[2], n.Range[3])
		}
		if n.Pos[1] > 0 {
			fmt.Fprintf(&b, " pos=%d/%d", n.Pos[0], n.Pos[1])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run the test with -update to write it)", err)
	}
	if !bytes.Equal(want, []byte(got)) {
		wl, gl := strings.Split(string(want), "\n"), strings.Split(got, "\n")
		for i := 0; i < len(wl) || i < len(gl); i++ {
			var w, g string
			if i < len(wl) {
				w = wl[i]
			}
			if i < len(gl) {
				g = gl[i]
			}
			if w != g {
				t.Fatalf("%s differs from the semantic tree at line %d:\n  want %s\n  got  %s\n(run the test with -update if the change is intended)", path, i+1, w, g)
			}
		}
	}
}

// The semantic tree of the window as it opens, and with the dialog open over
// four selected rows.
func TestSemanticsGolden(t *testing.T) {
	r := newRig(t, Options{Still: true})
	r.frames(5)
	golden(t, "semantics-main.txt", dump(r.tree()))

	r.run(click(rowRole, "").nthOf(3), click(rowRole, "").nthOf(6).with(modShift),
		click(roleButton, "Delete…"), waitFor(ui.RoleDialog, ""), settle(), idle(2))
	golden(t, "semantics-dialog.txt", dump(r.tree()))
}
