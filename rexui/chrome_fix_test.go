package rexui_test

import (
	"testing"

	"gonativeui/icon"
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

// The path bar's components are tab stops; Space on one reports it.
func TestPathBarKeyboard(t *testing.T) {
	g := openRig(t, theme.Light, nil, func(*rig) {})
	g.w = 1600
	ic := addChromeIcons(g)
	m := &pathModel{w: 700, got: -1, items: []rexui.PathItem{
		{Title: "Macintosh HD", Glyph: rexui.Sym(ic.doc)}, {Title: "Users", Glyph: rexui.Sym(ic.folder)},
		{Title: "adam", Glyph: rexui.Sym(ic.folder)}, {Title: "Documents", Glyph: rexui.Sym(ic.folder)},
	}}
	g.build = m.build
	g.settle()
	g.key(ui.KeyTab, 0)
	g.key(ui.KeyTab, 0)
	if n := g.node(ui.RoleButton, "Users"); n.State&ui.SemFocused == 0 {
		t.Fatal("two Tabs did not reach the second component")
	}
	g.key(ui.KeySpace, 0)
	g.frame()
	if m.got != 1 {
		t.Fatalf("Space reported %d, want 1", m.got)
	}
}

// A path longer than the bar lays out reports indexes into the whole path.
func TestPathBarLongPathIndexes(t *testing.T) {
	g := openRig(t, theme.Light, nil, func(*rig) {})
	g.w = 1600
	m := &pathModel{w: 700, got: -1}
	for i := range 40 {
		m.items = append(m.items, rexui.PathItem{Title: "Folder " + string(rune('A'+i))})
	}
	g.build = m.build
	g.settle()
	g.act(ui.RoleButton, "Folder "+string(rune('A'+39)), ui.ActPress)
	g.frame()
	if m.got != 39 {
		t.Fatalf("the last component reported %d, want 39", m.got)
	}
}

// An assistive technology opens a collapsed picker as a pop-up button: Show
// Menu or Expand open its options (without native menus: the next one).
func TestToolbarPickerAssistiveOpen(t *testing.T) {
	g, f := newFinderToolbar(t, theme.ToolbarUnified)
	g.act(ui.RoleComboBox, "View", ui.ActShowMenu)
	g.frame()
	if f.view != 1 {
		t.Fatalf("Show Menu chose %d, want the next option 1", f.view)
	}
	g.act(ui.RoleComboBox, "View", ui.ActExpand)
	g.frame()
	if f.view != 0 {
		t.Fatalf("Expand chose %d, want 0", f.view)
	}
}

// The back and forward halves show the focus ring when they have the
// keyboard.
func TestToolbarNavFocusRing(t *testing.T) {
	g, _ := newFinderToolbar(t, theme.ToolbarUnified)
	g.c.SetFocus(g.node(ui.RoleButton, "Back").Key)
	g.c.SetFocusVisible(true)
	g.settle()
	if ringQuads(g) == 0 {
		t.Fatal("no focus ring on the focused back button")
	}
}

// Items that do not fit their pane go into the overflow menu at the band's
// end, as NSToolbar's do: none lies past its pane, the » button lists them,
// and its menu's choice reports the item clicked.
func TestToolbarOverflowMenu(t *testing.T) {
	g := openRig(t, theme.Light, nil, func(*rig) {})
	g.w, g.h = 2*620, 400
	ic := addChromeIcons(g)
	var view int
	var action, back bool
	var ov rexui.ToolbarOverflow
	g.build = func(g *rig) {
		o := rexui.ToolbarOpts{Style: theme.ToolbarUnified, Title: "Documents", Panes: [2]float32{g.k.Pt(180), g.k.Pt(520)}}
		tb := rexui.BeginToolbar(g.k, "Toolbar", o)
		tb.Item(ic.sidebar, "Sidebar")
		tb.NextPane()
		b, _ := tb.Nav(ic.back, ic.fwd, true, false)
		back = back || b.Clicked
		tb.Title()
		tb.Flexible()
		tb.Picker("View", &view, []icon.ID{ic.grid, ic.list}, []string{"as Icons", "as List"}, true)
		tb.Menu(ic.group, "Group", -1)
		tb.BeginGroup("share-tag")
		tb.Item(ic.share, "Share")
		tb.Item(ic.tag, "Tags")
		tb.EndGroup()
		if tb.Menu(ic.action, "Action", -1).Clicked {
			action = true
		}
		tb.SearchButton(ic.search, "Search")
		tb.NextPane()
		tb.Item(ic.inspector, "Inspector")
		ov = tb.End()
	}
	g.settle()
	if ov.N == 0 || !g.has(ui.RoleButton, "Show More") {
		t.Fatal("nothing overflowed in a 340-pt pane")
	}
	names := ov.Names[:ov.N]
	if n := testing.AllocsPerRun(20, g.frame); n != 0 {
		t.Errorf("%v allocations a frame with items in the overflow", n)
	}
	g.c.Semantics(&g.sem)
	for _, n := range g.sem.Nodes {
		name := string(g.sem.Text(n.Name))
		end := float32(520) // the content pane's; the last pane's is the window's
		if name == "Inspector" || name == "Show More" {
			end = 620
		}
		if (n.Role == ui.RoleButton || n.Role == ui.RoleComboBox) && n.Bounds[2]/g.scale > end+0.5 {
			t.Errorf("%q ends at %v, past its pane's end %v", name, n.Bounds[2]/g.scale, end)
		}
	}
	i := -1
	for j, n := range names {
		if n == "Action" {
			i = j
		}
	}
	if i < 0 {
		t.Fatalf("Action is not in the overflow menu %v", names)
	}
	if g.has(ui.RoleButton, "Action") {
		t.Error("Action is both in the band and in the menu")
	}
	before := names
	g.frames(5)
	if after := ov.Names[:ov.N]; len(after) != len(before) {
		t.Fatalf("the overflow changes from frame to frame: %v, then %v", before, after)
	}
	g.in.SetMenuChoice(g.node(ui.RoleButton, "Show More").Key, i, 0)
	g.frame()
	if !action {
		t.Error("choosing Action in the overflow menu did not click it")
	}
	if back {
		t.Error("the choice clicked Back too")
	}
	// wide enough again: everything is back in the band
	g.w = 2 * 1120
	g.build = func(g *rig) {
		o := rexui.ToolbarOpts{Style: theme.ToolbarUnified, Title: "Documents", Panes: [2]float32{g.k.Pt(180), g.k.Pt(890)}}
		tb := rexui.BeginToolbar(g.k, "Toolbar", o)
		tb.NextPane()
		tb.Menu(ic.action, "Action", -1)
		ov = tb.End()
	}
	g.settle()
	if ov.N != 0 || !g.has(ui.RoleButton, "Action") || g.has(ui.RoleButton, "Show More") {
		t.Errorf("with room: overflow %v", ov.Names[:ov.N])
	}
}
