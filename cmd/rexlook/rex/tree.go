package rex

import (
	"gonativeui/ui"
	"gonativeui/widget/workspace"
)

// The model: tabs, each holding a workspace (widget/workspace): a tree of
// splits whose leaves are panes, the focus and the maximised pane. Everything
// is held in fixed arrays so that changing the layout (a split, a close, a
// tab) allocates nothing.

const (
	maxTabs  = 6
	maxPanes = 8
)

type paneKind uint8

const (
	kindAgent paneKind = iota // the focused session of the reference
	kindGit                   // a git TUI
	kindSnake                 // a small terminal game
	kindBun                   // a dev server
	kindShell                 // a fish prompt
)

// pane is what the scene keeps of a pane, by its slot in the tab's layout
// (workspace.Layout.Slot).
type pane struct {
	used bool
	uid  uint32 // the pane's workspace.PaneID
	kind paneKind

	// born is set until the pane has been drawn once: it grows out of from.
	born bool
	from [4]float32
}

type tabStyle struct {
	front, border uint32    // front card (RGBA), its outline
	backs         [2]uint32 // cards peeking out behind, 0xRRGGBB (0 = none)
	step          float32   // reference px between the cards
	iconX, textX  float32   // reference px from the tab's left edge
	icon          iconID
	symbol        uint32 // 0xRRGGBB
}

type tab struct {
	used       bool
	uid        uint32
	name, path string // the title; path is drawn lighter on an inactive tab
	label      string // name and path in one string, for a native tab
	style      tabStyle

	// ws is the tab's panes: the split tree, the focus and the maximised
	// pane (workspace.Layout); panes is the scene's own state of each, by
	// slot.
	ws    workspace.Layout
	panes [workspace.MaxPanes]pane
}

// ---- construction ----

func (t *tab) numPanes() int { return t.ws.Len() }

func (t *tab) slotOf(uid uint32) int8 { return int8(t.ws.Slot(workspace.PaneID(uid))) }

// focus is the uid of the focused pane, max that of the maximised one (0:
// none).
func (t *tab) focus() uint32 { return uint32(t.ws.Focus()) }
func (t *tab) max() uint32   { return uint32(t.ws.Zoomed()) }

// rect is where the layout put the pane in slot s.
func (t *tab) rect(s int8) [4]float32 { return t.ws.Rect(workspace.PaneID(t.panes[s].uid)) }

func (t *tab) paneKey(uid uint32) ui.Key { return t.ws.PaneKey(workspace.PaneID(uid)) }

// setRoot makes a single pane the content of an empty tab.
func (t *tab) setRoot(uid uint32, kind paneKind) {
	t.ws.Key = keyWorkspace
	t.ws.Reset(workspace.PaneID(uid))
	t.panes = [workspace.MaxPanes]pane{}
	t.panes[t.slotOf(uid)] = pane{used: true, uid: uid, kind: kind}
}

// splitLeaf puts a new pane after the pane in slot s along axis, the first
// of the two keeping ratio of their room. It returns the new pane's slot, -1
// if the tab is full.
func (t *tab) splitLeaf(s int8, axis ui.Axis, ratio float32, uid uint32, kind paneKind) int8 {
	if s < 0 || !t.panes[s].used || t.numPanes() >= maxPanes {
		return -1
	}
	d := workspace.Right
	if axis == ui.AxisY {
		d = workspace.Down
	}
	div := t.ws.Split(workspace.PaneID(t.panes[s].uid), workspace.PaneID(uid), d, 0.5)
	if div == 0 {
		return -1
	}
	t.ws.SetShare(div, ratio)
	np := t.slotOf(uid)
	t.panes[np] = pane{used: true, uid: uid, kind: kind}
	return np
}

// closeLeaf removes the pane in slot s and gives its space to its sibling.
// It reports false for the last pane of the tab. The focus goes back to the
// pane focused before (workspace.Layout.Close).
func (t *tab) closeLeaf(s int8) bool {
	if s < 0 || !t.panes[s].used || !t.ws.Close(workspace.PaneID(t.panes[s].uid)) {
		return false
	}
	t.panes[s] = pane{}
	return true
}

// ---- layout ----

// metrics the layout needs, in pixels.
type gutters struct {
	x, y       float32 // gutter between side-by-side and between stacked panes
	minW, minH float32 // smallest pane
}

// layout places the tree in the work rectangle (whole pixels): the first
// child of a split gets ratio × (space − gutter), kept between the two
// children's minimum sizes (workspace.Layout.Arrange).
func (t *tab) layout(work [4]float32, g *gutters, scale float32) {
	t.ws.Arrange(work, workspace.Metrics{Scale: scale, Gutter: [2]float32{g.x, g.y}, Min: [2]float32{g.minW, g.minH}})
}
