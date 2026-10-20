package gallery

import (
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// Node is one element of the scene's semantic tree, its frame in window
// points, as tools/appkitscenes reports AppKit's.
type Node struct {
	Role  string
	Name  string
	Frame [4]float32 // x, y, w, h
}

// Measure renders the scene at rest and returns the frames, in points, of
// its elements: groups, controls, labels, the sidebar's rows and the toolbar's
// items.
func Measure(o ShotOptions) ([]Node, error) {
	if o.Scale <= 0 {
		o.Scale = 2
	}
	off, err := runScene(o)
	if err != nil {
		return nil, err
	}
	defer off.Close()
	var t ui.SemTree
	off.UI.Semantics(&t)
	var out []Node
	for i := range t.Nodes {
		n := &t.Nodes[i]
		switch n.Role {
		case ui.RoleButton, ui.RoleGroup, ui.RoleRadioGroup, ui.RoleRadio, ui.RoleComboBox, ui.RoleSearchField, ui.RoleSwitch,
			ui.RoleSlider, ui.RoleCheckbox, ui.RoleTextField, ui.RoleLabel, ui.RoleListItem, ui.RoleTab, ui.RoleTabList, ui.RoleList,
			ui.RoleScrollArea, ui.RoleScrollBar:
		default:
			continue
		}
		b := n.Bounds
		s := o.Scale
		out = append(out, Node{Role: widgettest.RoleName(n.Role), Name: string(t.Text(n.Name)),
			Frame: [4]float32{b[0] / s, b[1] / s, (b[2] - b[0]) / s, (b[3] - b[1]) / s}})
	}
	return out, nil
}
