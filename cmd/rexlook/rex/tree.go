package rex

import "gonativeui/ui"

// The model: tabs, each holding a binary tree of splits whose leaves are
// panes. Everything is held in fixed arrays so that changing the layout (a
// split, a close, a tab) allocates nothing.

const (
	maxTabs  = 6
	maxPanes = 8
	maxNodes = 2 * maxPanes
)

type paneKind uint8

const (
	kindAgent paneKind = iota // the focused session of the reference
	kindGit                   // a git TUI
	kindSnake                 // a small terminal game
	kindBun                   // a dev server
	kindShell                 // a fish prompt
)

type pane struct {
	used bool
	uid  uint32
	kind paneKind

	// born is set until the pane has been drawn once: it grows out of from.
	born bool
	from [4]float32
}

type node struct {
	used   bool
	uid    uint32
	leaf   bool
	pane   int8    // leaf: slot in tab.panes
	axis   ui.Axis // split: AxisX = children side by side, AxisY = stacked
	ratio  float32 // split: share of the space (minus the gutter) given to the first child
	a, b   int8    // split: children
	parent int8    // -1 at the root

	// Results of the latest layout.
	rect   [4]float32
	gut    [4]float32 // split: the gutter between the children
	avail  float32    // split: space along the axis shared by the children (rect minus the gutter)
	lo, hi float32    // split: allowed size of the first child
	press  float32    // split: ratio when a drag started
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
	style      tabStyle

	root  int8
	nodes [maxNodes]node
	panes [maxPanes]pane
	focus uint32 // uid of the focused pane
	max   uint32 // uid of the maximised pane, 0 = none

	rects [maxPanes][4]float32 // tree layout of the panes, by slot
}

// ---- construction ----

func (t *tab) newNode() int8 {
	for i := range t.nodes {
		if !t.nodes[i].used {
			t.nodes[i] = node{used: true, parent: -1}
			return int8(i)
		}
	}
	return -1
}

func (t *tab) numPanes() int {
	n := 0
	for i := range t.panes {
		if t.panes[i].used {
			n++
		}
	}
	return n
}

// newPane allocates a pane slot; the caller supplies the uid.
func (t *tab) newPane(uid uint32, kind paneKind) int8 {
	for i := range t.panes {
		if !t.panes[i].used {
			t.panes[i] = pane{used: true, uid: uid, kind: kind}
			return int8(i)
		}
	}
	return -1
}

func (t *tab) slotOf(uid uint32) int8 {
	for i := range t.panes {
		if t.panes[i].used && t.panes[i].uid == uid {
			return int8(i)
		}
	}
	return -1
}

// leafOf is the node that holds the pane in slot s.
func (t *tab) leafOf(s int8) int8 {
	for i := range t.nodes {
		if n := &t.nodes[i]; n.used && n.leaf && n.pane == s {
			return int8(i)
		}
	}
	return -1
}

// setRoot makes a single pane the content of an empty tab.
func (t *tab) setRoot(uid uint32, kind paneKind, nodeUID uint32) {
	p := t.newPane(uid, kind)
	n := t.newNode()
	t.nodes[n].leaf, t.nodes[n].pane, t.nodes[n].uid = true, p, nodeUID
	t.root = n
	t.focus = uid
}

// splitLeaf replaces the leaf holding pane slot s by a split whose children
// are that leaf and a new pane. It returns the new pane's slot, -1 if the tab
// is full.
func (t *tab) splitLeaf(s int8, axis ui.Axis, ratio float32, newPaneUID uint32, kind paneKind, splitUID, leafUID uint32) int8 {
	leaf := t.leafOf(s)
	if leaf < 0 || t.numPanes() >= maxPanes {
		return -1
	}
	np := t.newPane(newPaneUID, kind)
	nl, ns := t.newNode(), t.newNode()
	if np < 0 || nl < 0 || ns < 0 {
		if np >= 0 {
			t.panes[np] = pane{}
		}
		if nl >= 0 {
			t.nodes[nl] = node{}
		}
		if ns >= 0 {
			t.nodes[ns] = node{}
		}
		return -1
	}
	t.nodes[nl].leaf, t.nodes[nl].pane, t.nodes[nl].uid = true, np, leafUID

	parent := t.nodes[leaf].parent
	sp := &t.nodes[ns]
	sp.uid, sp.axis, sp.ratio, sp.a, sp.b, sp.parent = splitUID, axis, ratio, leaf, nl, parent
	t.nodes[leaf].parent, t.nodes[nl].parent = ns, ns
	if parent < 0 {
		t.root = ns
	} else if pn := &t.nodes[parent]; pn.a == leaf {
		pn.a = ns
	} else {
		pn.b = ns
	}
	return np
}

// closeLeaf removes the pane in slot s and gives its space to its sibling.
// It reports false for the last pane of the tab. The focus moves to the
// first pane of the sibling when the closed pane had it.
func (t *tab) closeLeaf(s int8) bool {
	leaf := t.leafOf(s)
	if leaf < 0 || t.nodes[leaf].parent < 0 {
		return false
	}
	p := t.nodes[leaf].parent
	pn := &t.nodes[p]
	sib := pn.a
	if sib == leaf {
		sib = pn.b
	}
	g := pn.parent
	t.nodes[sib].parent = g
	if g < 0 {
		t.root = sib
	} else if gn := &t.nodes[g]; gn.a == p {
		gn.a = sib
	} else {
		gn.b = sib
	}
	uid := t.panes[s].uid
	t.panes[s] = pane{}
	t.nodes[leaf] = node{}
	t.nodes[p] = node{}
	if t.max == uid {
		t.max = 0
	}
	if t.focus == uid {
		t.focus = t.panes[t.nodes[t.firstLeaf(sib)].pane].uid
	}
	return true
}

func (t *tab) firstLeaf(n int8) int8 {
	for !t.nodes[n].leaf {
		n = t.nodes[n].a
	}
	return n
}

// ---- layout ----

// metrics the layout needs, in pixels.
type gutters struct {
	x, y       float32 // gutter between side-by-side and between stacked panes
	minW, minH float32 // smallest pane
}

func (t *tab) minSize(n int8, axis ui.Axis, g *gutters) float32 {
	nd := &t.nodes[n]
	if nd.leaf {
		if axis == ui.AxisX {
			return g.minW
		}
		return g.minH
	}
	a, b := t.minSize(nd.a, axis, g), t.minSize(nd.b, axis, g)
	if nd.axis != axis {
		return max(a, b)
	}
	if axis == ui.AxisX {
		return a + b + g.x
	}
	return a + b + g.y
}

// layout places the tree in the work rectangle (whole pixels). The first
// child of a split gets ratio × (space − gutter), kept between the two
// children's minimum sizes (shared in proportion when the window is too
// small for both), and the boundary snaps to a pixel.
func (t *tab) layout(work [4]float32, g *gutters) {
	t.place(t.root, work, g)
}

func (t *tab) place(n int8, r [4]float32, g *gutters) {
	nd := &t.nodes[n]
	nd.rect = r
	if nd.leaf {
		t.rects[nd.pane] = r
		return
	}
	ax := 0
	gut := g.x
	if nd.axis == ui.AxisY {
		ax, gut = 1, g.y
	}
	a0, a1 := r[ax], r[ax+2]
	avail := max(0, a1-a0-gut)
	minA, minB := t.minSize(nd.a, nd.axis, g), t.minSize(nd.b, nd.axis, g)
	lo, hi := minA, avail-minB
	first := avail * nd.ratio
	if lo <= hi {
		first = min(max(first, lo), hi)
	} else {
		first = avail * minA / (minA + minB)
		lo, hi = first, first
	}
	nd.avail, nd.lo, nd.hi = avail, lo, hi
	mid := snap(a0 + first)
	ra, rb, gr := r, r, r
	ra[ax+2] = mid
	rb[ax] = mid + gut
	gr[ax], gr[ax+2] = mid, mid+gut
	nd.gut = gr
	t.place(nd.a, ra, g)
	t.place(nd.b, rb, g)
}

// dragSplit moves the divider of split node n to where it was at the start of
// the drag plus delta pixels, within the children's minimum sizes.
func (t *tab) dragSplit(n int8, delta float32) {
	nd := &t.nodes[n]
	if nd.avail <= 0 {
		return
	}
	first := min(max(nd.press*nd.avail+delta, nd.lo), nd.hi)
	nd.ratio = first / nd.avail
}
