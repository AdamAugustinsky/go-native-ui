package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// act asks for an assistive technology's action on the node with the role
// and name, and runs a frame.
func (g *rig) act(role ui.Role, name string, a ui.SemAction) {
	g.t.Helper()
	g.in.PushAction(ui.SemRequest{Key: g.node(role, name).Key, Action: a})
	g.frame()
}

// The sidebar reports Changed in the frame a press or a key changed the
// selection.
func TestSidebarReportsChanged(t *testing.T) {
	g, m := newSidebarRig(t)
	changes := 0
	build := g.build
	g.build = func(g *rig) {
		build(g)
		if m.sig.Changed {
			changes++
		}
	}
	g.click(centre(g.node(ui.RoleListItem, "Documents")))
	if m.st.Sel != 2 || changes != 1 {
		t.Fatalf("a press selected %d with %d Changed frames, want 2 and 1", m.st.Sel, changes)
	}
	g.key(ui.KeyDown, 0)
	if m.st.Sel != 3 || changes != 2 {
		t.Fatalf("Down selected %d with %d Changed frames, want 3 and 2", m.st.Sel, changes)
	}
}

// A group's Show or Hide is there for assistive technologies and the
// keyboard while the pointer is elsewhere: their press toggles it, Tab
// reaches it from the sidebar and Space toggles it; a row's disclosure takes
// an assistive technology's press too.
func TestSidebarDisclosuresWithoutThePointer(t *testing.T) {
	g, m := newSidebarRig(t)
	g.act(ui.RoleButton, "Show or Hide", ui.ActPress)
	g.frame()
	if m.open[1] {
		t.Fatal("an assistive technology's press did not hide the group")
	}
	g.act(ui.RoleButton, "Show or Hide", ui.ActPress)
	g.frame()
	if !m.open[1] {
		t.Fatal("a second press did not show the group")
	}
	g.click(centre(g.node(ui.RoleListItem, "Documents")))
	g.key(ui.KeyTab, 0)
	if n := g.node(ui.RoleButton, "Show or Hide"); n.State&ui.SemFocused == 0 {
		t.Fatal("Tab from the sidebar did not reach Show or Hide")
	}
	g.key(ui.KeySpace, 0)
	g.frame()
	if m.open[1] {
		t.Fatal("Space on Show or Hide did not hide the group")
	}
	g.act(ui.RoleButton, "Disclosure", ui.ActPress)
	g.frame()
	if m.open[0] {
		t.Fatal("an assistive technology's press did not close the row")
	}
}

// Moving the selection with the keyboard scrolls the row into view.
func TestSidebarRevealsKeyboardSelection(t *testing.T) {
	g := openRig(t, theme.Light, nil, func(*rig) {})
	g.w, g.h = 1000, 400
	ic := addChromeIcons(g)
	m := &sidebarModel{}
	m.items = []rexui.SidebarItem{{ID: 1, Title: "Places", Header: true}}
	for i := uint32(2); i < 30; i++ {
		m.items = append(m.items, rexui.SidebarItem{ID: i, Title: "Row", Glyph: rexui.Sym(ic.folder)})
	}
	g.build = m.build
	g.settle()
	g.click(centre(g.nodeAt(ui.RoleListItem, 0)))
	for range 20 {
		g.key(ui.KeyDown, 0)
	}
	g.settle()
	if m.st.Sel != 22 {
		t.Fatalf("row %d selected, want 22", m.st.Sel)
	}
	n := g.nodeAt(ui.RoleListItem, int(m.st.Sel-2))
	if top, bottom := n.Bounds[1]/g.scale, n.Bounds[3]/g.scale; top < 52 || bottom > float32(g.h)/g.scale {
		t.Errorf("the selected row lies at %v..%v, outside the view 52..%v", top, bottom, float32(g.h)/g.scale)
	}
}

// A sidebar search with a long label builds its frames without allocating.
func TestSidebarSearchLongLabelAllocatesNothing(t *testing.T) {
	g := openRig(t, theme.Light, nil, func(*rig) {})
	var q ui.TextBuffer
	q.Reserve(16)
	g.build = func(g *rig) {
		c := g.k.Context()
		theme.At(c, 10, 10, 400, 48)
		c.Push(c.Box(theme.Floating, "pane"))
		rexui.SidebarSearch(g.k, "Search the settings of this window and of every other one", &q, "Search")
		c.Pop()
	}
	g.settle()
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Errorf("%v allocations a frame", n)
	}
}

// nodeAt returns the i-th node of the exported tree with the role.
func (g *rig) nodeAt(role ui.Role, i int) ui.SemNode {
	g.t.Helper()
	g.c.Semantics(&g.sem)
	for _, n := range g.sem.Nodes {
		if n.Role == role {
			if i == 0 {
				return n
			}
			i--
		}
	}
	g.t.Fatalf("no %d-th node of role %d", i, role)
	return ui.SemNode{}
}
