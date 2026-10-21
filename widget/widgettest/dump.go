package widgettest

import (
	"fmt"
	"strings"

	"gonativeui/ui"
)

var semStateNames = [...]struct {
	bit  ui.SemState
	name string
}{
	{ui.SemChecked, "checked"}, {ui.SemMixed, "mixed"}, {ui.SemSelected, "selected"},
	{ui.SemExpanded, "expanded"}, {ui.SemDisabled, "disabled"}, {ui.SemFocused, "focused"},
	{ui.SemEditable, "editable"}, {ui.SemModal, "modal"}, {ui.SemLive, "live"}, {ui.SemSecure, "secure"},
}

// Dump writes the semantic tree as indented text: roles, names, values, states,
// ranges and positions in a list, without bounds (which depend on the fonts).
// It is what a golden file of the tree holds: a test compares it with the file
// and a change of what assistive technology would read shows as a diff.
func Dump(t *ui.SemTree) string {
	var b strings.Builder
	depth := make([]int, len(t.Nodes))
	for i, n := range t.Nodes {
		if n.Parent >= 0 {
			depth[i] = depth[n.Parent] + 1
		}
		b.WriteString(strings.Repeat("  ", depth[i]))
		b.WriteString(RoleName(n.Role))
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
