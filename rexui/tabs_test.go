package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// tabsModel is an app's list of tabs, edited by what the bar reports.
type tabsModel struct {
	bar    rexui.TabBar
	tabs   []rexui.Tab
	sel    int
	closed []uint64
	moves  int
	opts   rexui.TabsOpts
}

func newTabsModel(n int) *tabsModel {
	m := &tabsModel{opts: rexui.TabsOpts{X0: 40, X1: 1600, Y: 20}}
	titles := []string{"Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot"}
	for i := 0; i < n; i++ {
		m.tabs = append(m.tabs, rexui.Tab{ID: uint64(100 + i), Title: titles[i%len(titles)], Detail: " ~/src"})
	}
	return m
}

func (m *tabsModel) build(g *rig) {
	ev := rexui.Tabs(g.k, "Tabs", &m.bar, m.tabs, &m.sel, m.opts)
	if ev.MoveFrom >= 0 {
		t := m.tabs[ev.MoveFrom]
		m.tabs = append(m.tabs[:ev.MoveFrom], m.tabs[ev.MoveFrom+1:]...)
		m.tabs = append(m.tabs[:ev.MoveTo], append([]rexui.Tab{t}, m.tabs[ev.MoveTo:]...)...)
		m.moves++
	}
	if ev.Close >= 0 {
		m.closed = append(m.closed, m.tabs[ev.Close].ID)
		m.tabs = append(m.tabs[:ev.Close], m.tabs[ev.Close+1:]...)
		if m.sel >= len(m.tabs) {
			m.sel = len(m.tabs) - 1
		}
	}
}

func rectCentre(r [4]float32) (float32, float32) { return (r[0] + r[2]) / 2, (r[1] + r[3]) / 2 }

func (m *tabsModel) ids() []uint64 {
	ids := make([]uint64, len(m.tabs))
	for i, t := range m.tabs {
		ids[i] = t.ID
	}
	return ids
}

// A tab is chosen on the press, as AppKit's window tabs are; the close
// button shows on the hovered tab and closes it, and so does a middle click.
func TestTabsChooseCloseAndMiddleClick(t *testing.T) {
	m := newTabsModel(4)
	g := newRig(t, theme.Light, nil, m.build)
	g.w = 1800
	g.settle()
	x, y := rectCentre(m.bar.TabRect(2))
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	if m.sel != 2 {
		t.Fatalf("after the press: tab %d chosen, want 2", m.sel)
	}
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.settle()

	// the close button of the hovered tab 1
	r := m.bar.TabRect(1)
	g.in.MouseX, g.in.MouseY = (r[0]+r[2])/2, (r[1]+r[3])/2
	g.settle()
	cl := g.node(ui.RoleButton, "Close Tab")
	g.click(centre(cl))
	if len(m.closed) != 1 || m.closed[0] != 101 || len(m.tabs) != 3 {
		t.Fatalf("the close button closed %v, %d tabs left", m.closed, len(m.tabs))
	}
	if m.sel != 2 && m.sel != 1 {
		t.Errorf("tab %d chosen after the close", m.sel)
	}

	// a middle click on tab 0
	g.settle()
	x, y = rectCentre(m.bar.TabRect(0))
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonMiddle, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonMiddle, false, 0)
	g.frame()
	if len(m.closed) != 2 || m.closed[1] != 100 {
		t.Fatalf("a middle click closed %v", m.closed)
	}

	// a tab without a close button keeps it
	m.tabs[0].NoClose = true
	g.settle()
	x, y = rectCentre(m.bar.TabRect(0))
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonMiddle, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonMiddle, false, 0)
	g.frame()
	if len(m.closed) != 2 {
		t.Fatalf("a NoClose tab closed: %v", m.closed)
	}
}

// A tab dragged sideways moves through the others, which slide out of its
// way, and settles in its place when released; the choice follows it.
func TestTabsDragReorders(t *testing.T) {
	m := newTabsModel(4)
	g := newRig(t, theme.Light, nil, m.build)
	g.w = 1800
	g.settle()
	r0 := m.bar.TabRect(0)
	slot := m.bar.TabRect(1)[0] - r0[0]
	x, y := rectCentre(r0)
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	for i := 1; i <= 20; i++ { // two and a bit slots to the right
		g.in.MouseX = x + float32(i)*slot*2.2/20
		g.frame()
	}
	if !m.bar.Dragging() {
		t.Fatal("no drag after moving the pointer two slots")
	}
	if got := m.ids(); got[2] != 100 {
		t.Fatalf("while dragging two slots right: order %v, want tab 100 third", got)
	}
	if m.tabs[m.sel].ID != 100 {
		t.Errorf("the dragged tab is not chosen: %d", m.tabs[m.sel].ID)
	}
	// the dragged tab follows the pointer; the others are in their slots
	// once they have slid there
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	if m.bar.Dragging() {
		t.Fatal("still dragging after the release")
	}
	g.settle()
	for i := range m.tabs {
		want := r0[0] + float32(i)*slot
		if got := m.bar.TabRect(i)[0]; got < want-1 || got > want+1 {
			t.Errorf("tab %d settled at x %v, want %v", i, got, want)
		}
	}
	if got := m.ids(); got[0] != 101 || got[1] != 102 || got[2] != 100 || got[3] != 103 {
		t.Errorf("order after the drop %v", got)
	}
	if m.moves != 2 {
		t.Errorf("%d moves, want 2", m.moves)
	}
}

// ⌃Tab and ⌃⇧Tab go round the tabs; ⌘3 chooses the third and ⌘9 the last.
func TestTabsKeys(t *testing.T) {
	m := newTabsModel(4)
	g := newRig(t, theme.Light, nil, m.build)
	prim := ui.PrimaryMod()
	for _, c := range []struct {
		code ui.KeyCode
		mods ui.Mods
		want int
	}{
		{ui.KeyTab, ui.ModCtrl, 1},
		{ui.KeyTab, ui.ModCtrl, 2},
		{ui.KeyTab, ui.ModCtrl | ui.ModShift, 1},
		{ui.KeyTab, ui.ModCtrl | ui.ModShift, 0},
		{ui.KeyTab, ui.ModCtrl | ui.ModShift, 3},
		{'1', prim, 0},
		{'3', prim, 2},
		{'9', prim, 3},
		{ui.KeyLeftBracket, prim | ui.ModShift, 2},
		{ui.KeyRightBracket, prim | ui.ModShift, 3},
	} {
		g.key(c.code, c.mods)
		g.frame()
		if m.sel != c.want {
			t.Fatalf("key %d mods %d: tab %d chosen, want %d", c.code, c.mods, m.sel, c.want)
		}
	}
}

// Tabs that do not fit at their minimum width scroll: the chosen tab is kept
// in view, and the wheel over the strip moves it.
func TestTabsOverflowScrolls(t *testing.T) {
	m := newTabsModel(24)
	m.opts.X1 = 600
	g := newRig(t, theme.Light, nil, m.build)
	g.settle()
	cr := m.bar.CapsuleRect()
	if w := cr[2] - cr[0]; w > 600-40+1 {
		t.Fatalf("the capsule is %v px wide, more than its room", w)
	}
	g.key(ui.KeyCode('9'), ui.PrimaryMod())
	g.settle()
	if m.sel != 23 {
		t.Fatalf("⌘9 chose %d", m.sel)
	}
	r := m.bar.TabRect(23)
	if r[0] < cr[0]-1 || r[2] > cr[2]+1 {
		t.Fatalf("the chosen last tab %v is not in view in %v", r, cr)
	}
	// the wheel back toward the start
	before := m.bar.TabRect(0)[0]
	g.in.MouseX, g.in.MouseY = rectCentre(cr)
	g.in.ScrollY, g.in.Precise = 200, true
	g.frame()
	g.in.ScrollY = 0
	g.settle()
	if after := m.bar.TabRect(0)[0]; after <= before {
		t.Errorf("the wheel did not scroll the strip: tab 0 at %v, was %v", after, before)
	}
}

// The bar is a tab list of tabs, the chosen one selected, each with its
// position.
func TestTabsSemantics(t *testing.T) {
	m := newTabsModel(3)
	m.sel = 1
	g := newRig(t, theme.Light, nil, m.build)
	g.node(ui.RoleTabList, "Tabs")
	n := g.node(ui.RoleTab, "Bravo")
	if n.State&ui.SemSelected == 0 {
		t.Error("the chosen tab is not selected")
	}
	if n := g.node(ui.RoleTab, "Alpha"); n.State&ui.SemSelected != 0 {
		t.Error("an unchosen tab is selected")
	}
}

// Tabs in content take ⌃Tab and the arrows only while they hold the focus,
// so that a window's own tabs keep the window's shortcuts.
func TestTabsInContentTakeKeysWhenFocused(t *testing.T) {
	m := newTabsModel(3)
	m.opts.Focusable = true
	g := newRig(t, theme.Light, nil, m.build)
	g.key(ui.KeyTab, ui.ModCtrl)
	g.key('2', ui.PrimaryMod())
	if m.sel != 0 {
		t.Fatalf("unfocused tabs in content took a window shortcut: tab %d chosen", m.sel)
	}
	x, y := rectCentre(m.bar.TabRect(1))
	g.click(x, y)
	if m.sel != 1 {
		t.Fatalf("the click chose %d", m.sel)
	}
	g.key(ui.KeyTab, ui.ModCtrl)
	g.frame()
	if m.sel != 2 {
		t.Fatalf("⌃Tab with the focus chose %d, want 2", m.sel)
	}
	g.key(ui.KeyLeft, 0)
	g.frame()
	if m.sel != 1 {
		t.Fatalf("Left with the focus chose %d, want 1", m.sel)
	}
}
