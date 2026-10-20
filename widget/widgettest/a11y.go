package widgettest

import (
	"fmt"
	"strings"
	"testing"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/ui"
)

// DumpAX writes the semantic tree as an assistive technology sees it through
// the window's accessibility bridge (platform.AXDump): macOS's roles and
// subroles, labels, values, states and actions, with the content beneath a
// modal dialog left out and hosted views in place. Bounds are left out. A
// golden file of it (AssertAX) shows, as a diff, any change to what VoiceOver
// would find.
func DumpAX(t *ui.SemTree) string {
	var ax platform.AXTree
	app.ExportAX(t, &ax)
	return platform.AXDump(&ax)
}

// AssertAX fails the test unless the tree dumps (DumpAX) to the text in
// testdata/<name>.txt; -update writes it (see AssertTree).
func AssertAX(t testing.TB, name string, tree *ui.SemTree) {
	t.Helper()
	AssertText(t, name, DumpAX(tree))
}

// AssertAX is the package function on the tree of the rig's last frame.
func (r *Rig) AssertAX(name string) { r.t.Helper(); AssertAX(r.t, name, r.Tree()) }

// NameAllow accepts an unnamed interactive node that LintNames would report,
// with the reason: a node of Role (any role when RoleNone) whose path (as the
// finding writes it) contains Where.
type NameAllow struct {
	// Role is the role of the nodes it accepts; ui.RoleNone for any.
	Role ui.Role
	// Where is text the finding's path contains, such as an ancestor's name.
	Where string
	// Reason says why the node needs no name.
	Reason string
}

// needsName reports whether node n is interactive, so that an assistive
// technology has to read it out by name: a control of an interactive role,
// or any node that can be pressed.
func needsName(n *ui.SemNode) bool {
	switch n.Role {
	case ui.RoleButton, ui.RoleCheckbox, ui.RoleSwitch, ui.RoleRadio, ui.RoleSlider, ui.RoleTextField,
		ui.RoleSearchField, ui.RoleComboBox, ui.RoleTab, ui.RoleMenuItem, ui.RoleMenuItemCheckbox,
		ui.RoleLink, ui.RoleListItem, ui.RoleRow, ui.RoleColumnHeader:
		return true
	case ui.RoleHost, ui.RoleWindow:
		return false
	}
	return n.Actions&ui.ActPress != 0
}

// semPath describes node i: its role, then its named ancestors.
func semPath(t *ui.SemTree, i int) string {
	var b strings.Builder
	b.WriteString(RoleName(t.Nodes[i].Role))
	for p := t.Nodes[i].Parent; p > 0; p = t.Nodes[p].Parent {
		n := &t.Nodes[p]
		if name := t.Text(n.Name); len(name) > 0 {
			fmt.Fprintf(&b, " in %s %q", RoleName(n.Role), name)
		}
	}
	if n := &t.Nodes[i]; n.Pos[1] > 0 {
		fmt.Fprintf(&b, " (item %d of %d)", n.Pos[0], n.Pos[1])
	}
	return b.String()
}

// LintNames fails t, naming the scene, for each interactive node of the tree
// that has no label through the accessibility bridge (no name, nor, for a
// control that reads out by its content such as a row, a named descendant:
// see platform.AXLabel), so that an assistive technology could only say
// "button", unless an allowance accepts it. It returns the findings, accepted ones included. An
// allowance without a reason is a mistake in the test and fails it too.
func LintNames(t testing.TB, scene string, tree *ui.SemTree, allow ...NameAllow) []string {
	t.Helper()
	for _, a := range allow {
		if a.Reason == "" {
			t.Errorf("%s: name allowance %s %q has no reason", scene, RoleName(a.Role), a.Where)
		}
	}
	var ax platform.AXTree
	app.ExportAX(tree, &ax)
	var found []string
next:
	for i := 1; i < len(tree.Nodes); i++ {
		n := &tree.Nodes[i]
		if !needsName(n) || len(platform.AXLabel(&ax, i)) > 0 {
			continue
		}
		where := semPath(tree, i)
		found = append(found, where)
		for _, a := range allow {
			if (a.Role == ui.RoleNone || a.Role == n.Role) && strings.Contains(where, a.Where) {
				continue next
			}
		}
		t.Errorf("%s: %s has no accessible name", scene, where)
	}
	return found
}

// LintNames runs the lint on the tree of the rig's last frame.
func (r *Rig) LintNames(scene string, allow ...NameAllow) []string {
	r.t.Helper()
	return LintNames(r.t, scene, r.Tree(), allow...)
}
