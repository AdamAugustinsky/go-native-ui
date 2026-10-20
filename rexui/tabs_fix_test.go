package rexui_test

import (
	"testing"

	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// Under Reduce Motion a choice moves the pill at once, and nothing animates
// after it.
func TestTabsReduceMotion(t *testing.T) {
	m := newTabsModel(4)
	sys := theme.SystemValues{Known: true, Settings: theme.Settings{ReduceMotion: true}}
	g := newRig(t, theme.Light, &sys, m.build)
	g.settle()
	g.key('3', ui.PrimaryMod())
	g.frame()
	if p, r := m.bar.PillRect(), m.bar.TabRect(2); p[0] != r[0] || p[2] != r[2] {
		t.Errorf("one frame after the choice the pill is at %v, want the tab's %v", p, r)
	}
	if g.c.Animating() {
		t.Error("the bar still animates under Reduce Motion")
	}
}

// ringQuads counts the drawn quads in the focus ring's colour.
func ringQuads(g *rig) int {
	ring := g.k.Theme().Colors[theme.FocusRing] >> 8
	n := 0
	for _, q := range g.list.Quads {
		if q.Border > 0 && q.BorderColor>>8 == ring && q.BorderColor&0xFF != 0 {
			n++
		}
	}
	return n
}

// Tabs in content show their focus ring only in the key window.
func TestTabsNoFocusRingInactive(t *testing.T) {
	m := newTabsModel(3)
	m.opts.Focusable = true
	g := newRig(t, theme.Light, nil, m.build)
	g.w = 1800
	g.settle()
	g.click(rectCentre(m.bar.TabRect(0)))
	g.key(ui.KeyRight, 0) // the keyboard: the ring shows
	g.settle()
	if ringQuads(g) == 0 {
		t.Fatal("no focus ring on the focused tab in the key window")
	}
	g.in.Focused = false
	g.settle()
	if n := ringQuads(g); n != 0 {
		t.Errorf("%d focus ring quads in an inactive window", n)
	}
}

// In a disabled region the tabs take no press and are disabled to assistive
// technologies.
func TestTabsDisabled(t *testing.T) {
	m := newTabsModel(3)
	g := newRig(t, theme.Light, nil, func(g *rig) {
		g.k.BeginDisabled(true)
		m.build(g)
		g.k.EndDisabled()
	})
	g.click(rectCentre(m.bar.TabRect(2)))
	if m.sel != 0 {
		t.Fatalf("a press chose tab %d in a disabled bar", m.sel)
	}
	for _, name := range []string{"Alpha", "Bravo"} {
		if n := g.node(ui.RoleTab, name); n.State&ui.SemDisabled == 0 || n.Actions&ui.ActPress != 0 {
			t.Errorf("tab %s: state %b, actions %b; want disabled without a press", name, n.State, n.Actions)
		}
	}
}

// An assistive technology closes a tab with its Close Tab button, with the
// pointer elsewhere.
func TestTabsCloseWithoutThePointer(t *testing.T) {
	m := newTabsModel(3)
	g := newRig(t, theme.Light, nil, m.build)
	g.settle()
	n := g.closeIn(m.bar.TabRect(1))
	g.in.PushAction(ui.SemRequest{Key: n.Key, Action: ui.ActPress})
	g.frame()
	g.frame()
	if len(m.closed) != 1 || m.closed[0] != 101 {
		t.Fatalf("the press closed %v, want [101]", m.closed)
	}
}

// A tab the app chooses, or chosen at the start, comes into view in a strip
// that scrolls.
func TestTabsRevealAppChoice(t *testing.T) {
	m := newTabsModel(24)
	m.opts.X1 = 600
	m.sel = 23
	g := newRig(t, theme.Light, nil, m.build)
	g.settle()
	cr := m.bar.CapsuleRect()
	if r := m.bar.TabRect(23); r[0] < cr[0]-1 || r[2] > cr[2]+1 {
		t.Fatalf("the tab chosen at the start %v is not in view in %v", r, cr)
	}
	m.sel = 0
	g.settle()
	if r := m.bar.TabRect(0); r[0] < cr[0]-1 || r[2] > cr[2]+1 {
		t.Fatalf("the tab the app chose %v is not in view in %v", r, cr)
	}
}

// startDrag presses tab i of m and moves the pointer dx pixels in steps.
func startDrag(g *rig, m *tabsModel, i int, dx float32, steps int, each func()) (x0 float32) {
	x, y := rectCentre(m.bar.TabRect(i))
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	for s := 1; s <= steps; s++ {
		g.in.MouseX = x + float32(s)*dx/float32(steps)
		g.frame()
		if each != nil {
			each()
		}
	}
	return x
}

// In the frame a drag moves a tab, the tab marked selected is the dragged
// one.
func TestTabsReorderFrameKeepsSelection(t *testing.T) {
	m := newTabsModel(4)
	g := newRig(t, theme.Light, nil, m.build)
	g.w = 1800
	g.settle()
	slot := m.bar.TabRect(1)[0] - m.bar.TabRect(0)[0]
	moves, checked := 0, 0
	startDrag(g, m, 0, slot*2.2, 20, func() {
		if m.moves != moves {
			moves = m.moves
			checked++
			if n := g.node(ui.RoleTab, "Alpha"); n.State&ui.SemSelected == 0 {
				t.Errorf("move %d: the dragged tab is not selected in its frame", moves)
			}
		}
	})
	if checked == 0 {
		t.Fatal("the drag moved nothing")
	}
}

// A press keeps the pointer's place in the tab when the app's list changed
// since the bar was shown.
func TestTabsGrabFollowsTheTabNotItsIndex(t *testing.T) {
	m := newTabsModel(6)
	m.opts.X1 = 1700
	g := newRig(t, theme.Light, nil, m.build)
	g.w = 1800
	g.settle()
	b := m.bar.TabRect(1) // Bravo
	m.tabs = m.tabs[1:]   // the app closes Alpha before the next frame
	px, py := b[0]+20, (b[1]+b[3])/2
	g.in.MouseX, g.in.MouseY = px, py
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	for s := 1; s <= 10; s++ {
		g.in.MouseX = px + float32(s)*6
		g.frame()
	}
	if !m.bar.Dragging() {
		t.Fatal("no drag")
	}
	i := -1
	for j, tb := range m.tabs {
		if tb.ID == 101 {
			i = j
		}
	}
	if got, want := m.bar.TabRect(i)[0], g.in.MouseX-20; got < want-2 || got > want+2 {
		t.Errorf("the dragged tab's left edge is %v, want %v (20 left of the pointer, where it was grabbed)", got, want)
	}
}

// Escape during a drag puts the tab back where it started, as browsers do.
func TestTabsEscapeCancelsDrag(t *testing.T) {
	m := newTabsModel(4)
	g := newRig(t, theme.Light, nil, m.build)
	g.w = 1800
	g.settle()
	slot := m.bar.TabRect(1)[0] - m.bar.TabRect(0)[0]
	startDrag(g, m, 0, slot*2.2, 20, nil)
	if got := m.ids(); got[2] != 100 {
		t.Fatalf("the drag did not move the tab: %v", got)
	}
	g.key(ui.KeyEscape, 0)
	g.frame()
	if got := m.ids(); got[0] != 100 || got[1] != 101 || got[2] != 102 || got[3] != 103 {
		t.Fatalf("after Escape the order is %v, want the starting one", got)
	}
	if m.bar.Dragging() {
		t.Fatal("still dragging after Escape")
	}
	g.in.MouseX += slot
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.settle()
	if got := m.ids(); got[0] != 100 {
		t.Errorf("the pointer moved the tab after Escape: %v", got)
	}
	if m.tabs[m.sel].ID != 100 {
		t.Errorf("tab %d chosen after Escape, want the dragged one", m.tabs[m.sel].ID)
	}
}

var _ = rexui.Tabs
