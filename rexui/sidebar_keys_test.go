package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// sideKeys is a search field over a sidebar of 30 rows in two groups, whose
// rows the app builds (SidebarOpts.Row), with a context menu.
type sideKeys struct {
	st        rexui.SidebarState
	q         ui.TextBuffer
	items     []rexui.SidebarItem
	o         rexui.SidebarOpts
	sig       rexui.SidebarSignal
	activated uint32
	renamed   uint32
	menuRow   uint32
	menuItem  int
	built     int
}

func newSideKeys() *sideKeys {
	m := &sideKeys{}
	names := []string{"Ávila", "Bento", "Coração", "Dália", "Érica"}
	m.items = append(m.items, rexui.SidebarItem{ID: 1000, Title: "Recent", Header: true})
	for i := range 30 {
		if i == 15 {
			m.items = append(m.items, rexui.SidebarItem{ID: 1001, Title: "Older", Header: true})
		}
		m.items = append(m.items, rexui.SidebarItem{ID: uint32(i + 1), Title: names[i%5] + " " + string(rune('a'+i/5)), Height: 28 + float32(i%2)*16})
	}
	m.o = rexui.SidebarOpts{Row: m.row, Menu: m.menu}
	return m
}

func (m *sideKeys) row(k *base.Kit, it *rexui.SidebarItem, row *rexui.SidebarRow) {
	m.built++
	if row.Renaming {
		row.RenameField()
		return
	}
	k.NextWidth(ui.Grow(1))
	rexui.Label(k, it.Title+"###t", rexui.LabelOpts{Color: rexui.OfRGBA(row.Text)})
	rexui.Label(k, "3m###when", rexui.LabelOpts{Color: rexui.OfRGBA(row.Text2)})
}

func (m *sideKeys) menu(id uint32, items *[]ui.MenuItem) {
	*items = append(*items, ui.MenuItem{ID: 7, Title: "Rename"}, ui.MenuItem{ID: 8, Title: "Delete"})
}

func (m *sideKeys) build(g *rig) {
	k, c := g.k, g.k.Context()
	theme.At(c, 0, 0, k.Pt(240), float32(g.h))
	c.NextAxis(ui.AxisY)
	c.Push(c.Box(theme.Floating, "pane"))
	m.st.Steer(c)
	rexui.SearchField(k, "Search", &m.q, "Search", rexui.FieldOpts{})
	k.NextSize(ui.Pct(1), ui.Grow(1))
	m.sig = rexui.Sidebar(k, "Sidebar", &m.st, m.items, m.o)
	if m.sig.Activated != 0 {
		m.activated = m.sig.Activated
	}
	if m.sig.Renamed != 0 {
		m.renamed = m.sig.Renamed
	}
	if m.sig.MenuChosen {
		m.menuRow, m.menuItem = m.sig.MenuRow, m.sig.MenuItem
	}
	c.Pop()
}

// TestSidebarKeysAndTypeToFind: the focused sidebar's Home, End, Page Down
// and Page Up move over the visible rows, past the headers; Enter activates;
// typing finds a row by its title, ignoring accents.
func TestSidebarKeysAndTypeToFind(t *testing.T) {
	m := newSideKeys()
	g := newRig(t, theme.Light, nil, m.build)
	g.h = 600
	g.settle()
	g.click(centre(g.node(ui.RoleListItem, "Bento a")))
	if m.st.Sel != 2 {
		t.Fatalf("a click selected %d", m.st.Sel)
	}
	g.key(ui.KeyEnd, 0)
	g.settle()
	if m.st.Sel != 30 {
		t.Errorf("End selected %d, want 30", m.st.Sel)
	}
	g.key(ui.KeyHome, 0)
	g.settle()
	if m.st.Sel != 1 {
		t.Errorf("Home selected %d, want 1", m.st.Sel)
	}
	g.key(ui.KeyPageDown, 0)
	g.settle()
	if m.st.Sel <= 2 {
		t.Errorf("Page Down selected %d", m.st.Sel)
	}
	if b := g.node(ui.RoleListItem, m.items[rowIndex(m, m.st.Sel)].Title).Bounds; b[3] > float32(g.h)+0.5 {
		t.Errorf("the row Page Down selected is below the window: %v", b)
	}
	g.key(ui.KeyPageUp, 0)
	g.settle()
	if m.st.Sel != 1 {
		t.Errorf("Page Up selected %d, want 1", m.st.Sel)
	}
	g.typeText("co")
	g.settle()
	if m.st.Sel != 3 {
		t.Errorf("\"co\" selected %d, want 3 (Coração a)", m.st.Sel)
	}
	g.key(ui.KeyEnter, 0)
	g.settle()
	if m.activated != 3 {
		t.Errorf("Enter activated %d, want 3", m.activated)
	}
	if got := selected(g); got != "Coração a" {
		t.Errorf("the tree says %q is selected", got)
	}
}

// rowIndex is the index of the item with the id.
func rowIndex(m *sideKeys, id uint32) int {
	for i := range m.items {
		if m.items[i].ID == id {
			return i
		}
	}
	return -1
}

// TestSidebarSteeredByASearchField: with the caret in a search field that
// steers the sidebar, Down and Up (as keys and as macOS's edit actions) move
// the selection and Enter activates it; the field's text does not change.
func TestSidebarSteeredByASearchField(t *testing.T) {
	m := newSideKeys()
	g := newRig(t, theme.Light, nil, m.build)
	g.settle()
	g.click(centre(g.node(ui.RoleSearchField, "Search")))
	g.key(ui.KeyDown, 0)
	g.key(ui.KeyDown, 0)
	g.settle()
	if m.st.Sel != 2 {
		t.Fatalf("two Downs from the field selected %d, want 2", m.st.Sel)
	}
	for range 14 {
		g.in.PushEdit(ui.EditMoveDown)
		g.frame()
	}
	g.settle()
	if m.st.Sel != 16 {
		t.Errorf("fourteen more Downs selected %d, want 16 (past the header)", m.st.Sel)
	}
	g.in.PushEdit(ui.EditMoveUp)
	g.frame()
	g.key(ui.KeyEnter, 0)
	g.settle()
	if m.activated != 15 || m.q.String() != "" {
		t.Errorf("Enter from the field activated %d (field %q), want 15", m.activated, m.q.String())
	}
}

// TestSidebarRowsOfTheApp: the app's rows show its content with the row's
// state, rename in place (Enter confirms) and take a context menu.
func TestSidebarRowsOfTheApp(t *testing.T) {
	m := newSideKeys()
	g := newRig(t, theme.Light, nil, m.build)
	g.settle()
	if m.built == 0 || !g.has(ui.RoleLabel, "3m") {
		t.Fatal("the app's row function built nothing")
	}
	m.st.Select(4)
	g.settle()
	if got := selected(g); got != "Dália a" {
		t.Errorf("selected %q", got)
	}
	m.st.Rename(4, "Dália")
	g.settle()
	g.typeText(" 2")
	g.key(ui.KeyEnter, 0)
	g.settle()
	if m.renamed != 4 || m.st.RenameText() != "Dália 2" || m.st.Renaming() != 0 {
		t.Errorf("renamed %d to %q (renaming %d), want 4 to \"Dália 2\"", m.renamed, m.st.RenameText(), m.st.Renaming())
	}

	g.c.SetNativeMenus(true)
	x, y := centre(g.node(ui.RoleListItem, "Bento a"))
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonRight, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonRight, false, 0)
	g.frame()
	r, ok := g.c.TakeMenuRequest()
	if !ok || len(r.Items) != 2 {
		t.Fatalf("menu request %+v (%v)", r, ok)
	}
	g.in.SetMenuResult(r.Owner, 8, ui.MenuChosen)
	g.frame()
	if m.menuRow != 2 || m.menuItem != 8 {
		t.Errorf("menu chose item %d on row %d, want 8 on 2", m.menuItem, m.menuRow)
	}
}
