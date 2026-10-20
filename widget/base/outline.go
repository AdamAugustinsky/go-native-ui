package base

import (
	"math/bits"

	"gonativeui/ui"
)

// Tree is a tree as an outline shows it, in pre-order and pointer-free: node
// i's subtree is nodes i to i+Size[i]-1 (Size[i] is 1 for a leaf, and a node
// with more is expandable), and Depth[i] is its level, 0 for a root. A
// node's index is its item id, which the selection and the expansion hold;
// an app that edits its tree remaps them (Selection.Remap) and calls
// OutlineState.TreeChanged.
type Tree struct {
	// Size is each node's subtree size, itself included.
	Size []uint32
	// Depth is each node's level, 0 for a root.
	Depth []uint16
}

// Len is the number of nodes.
func (t *Tree) Len() int { return len(t.Size) }

// Parent is the parent of node, or NoItem for a root: the nearest node before
// it one level up. It scans back over node's earlier siblings and their
// subtrees.
func (t *Tree) Parent(node uint32) uint32 {
	d := t.Depth[node]
	if d == 0 {
		return NoItem
	}
	for i := int(node) - 1; i >= 0; i-- {
		if t.Depth[i] < d {
			return uint32(i)
		}
	}
	return NoItem
}

// OutlineState is the state of an outline: a ListState whose display order
// is the tree's shown nodes, and which nodes are expanded. Make it the zero
// value and Reserve the tree's size, so that expanding never allocates. It
// must not be copied once in use.
type OutlineState struct {
	ListState

	open  []uint64 // the expanded nodes, a bit each
	rows  []uint32 // the shown nodes in display order (ListState.Order)
	nodes int      // the tree's size the rows were made for
	stale bool

	built   [2]int // the rows built in the last frame, for the disclosure clicks
	pend    uint32 // an expansion an assistive technology asked for
	pendOn  bool
	hasPend bool
}

// Reserve makes room for a tree of nodes nodes: the expansion, the shown
// rows and the selection.
func (st *OutlineState) Reserve(nodes int) {
	w := (nodes + 63) >> 6
	if cap(st.open) < w {
		st.open = append(make([]uint64, 0, w), st.open...)
	}
	if cap(st.rows) < nodes {
		st.rows = make([]uint32, 0, nodes)
		st.stale = true
	}
	st.Sel.Reserve(nodes)
}

// Expanded reports whether node shows its children.
func (st *OutlineState) Expanded(node uint32) bool {
	w := int(node >> 6)
	return w < len(st.open) && st.open[w]>>(node&63)&1 != 0
}

// TreeChanged makes the outline read the tree again in its next frame: the app
// added, removed or moved nodes.
func (st *OutlineState) TreeChanged() { st.stale = true }

// Rows are the shown nodes in display order, as of the last frame.
func (st *OutlineState) Rows() []uint32 { return st.rows }

// setOpen sets the expansion bits of nodes lo to hi-1 to on.
func (st *OutlineState) setOpen(lo, hi int, on bool) {
	if hi <= lo {
		return
	}
	if w := (hi + 63) >> 6; w > len(st.open) {
		if w > cap(st.open) {
			st.open = append(make([]uint64, 0, w), st.open...)
		}
		old := len(st.open)
		st.open = st.open[:w]
		clear(st.open[old:])
	}
	for i := lo; i < hi; {
		if i&63 == 0 && i+64 <= hi { // a whole word
			if on {
				st.open[i>>6] = ^uint64(0)
			} else {
				st.open[i>>6] = 0
			}
			i += 64
			continue
		}
		if on {
			st.open[i>>6] |= 1 << uint(i&63)
		} else {
			st.open[i>>6] &^= 1 << uint(i&63)
		}
		i++
	}
}

// Expand shows (on) or hides the children of node in tree t, and with all
// those of every node below it too, as Option with the arrow keys and
// Option-clicking the disclosure do in AppKit. Collapsing deselects the
// nodes it hides; if the cursor was among them, node becomes the cursor and
// is selected, as NSOutlineView does.
func (st *OutlineState) Expand(t *Tree, node uint32, on, all bool) {
	n := len(t.Size)
	if int(node) >= n || t.Size[node] <= 1 {
		return
	}
	end := int(node) + int(t.Size[node])
	if all {
		st.setOpen(int(node), end, on)
	} else {
		st.setOpen(int(node), int(node)+1, on)
	}
	st.stale = true
	if on {
		return
	}
	// deselect what the collapse hides
	s := &st.Sel
	cur, hasCur := s.Cursor()
	hid := false
	for it, ok := s.NextSet(node + 1); ok && int(it) < end; it, ok = s.NextSet(it + 1) {
		s.Set(it, false)
		hid = true
	}
	if hasCur && cur > node && int(cur) < end {
		if hid || s.Count() == 0 {
			s.Set(node, true)
		}
		s.SetCursor(node)
		s.SetAnchor(node)
	}
}

// rebuild makes the shown rows of t from the expansion: O(shown rows).
func (st *OutlineState) rebuild(t *Tree) {
	n := len(t.Size)
	st.rows = st.rows[:0]
	for i := 0; i < n; {
		st.rows = append(st.rows, uint32(i))
		sz := int(t.Size[i])
		if sz > 1 && st.Expanded(uint32(i)) {
			i++
		} else {
			i += max(sz, 1)
		}
	}
	st.Order = st.rows
	st.nodes, st.stale = n, false
}

// rowOf is the display row of node, -1 if it is not shown.
func (st *OutlineState) rowOf(node uint32) int {
	// the rows are increasing: a binary search
	lo, hi := 0, len(st.rows)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if st.rows[m] < node {
			lo = m + 1
		} else {
			hi = m
		}
	}
	if lo < len(st.rows) && st.rows[lo] == node {
		return lo
	}
	return -1
}

// Outline is an open outline: a List of the tree's shown nodes. Build rows
// First to Last-1 with RowState and BeginRow, as a list's, with a disclosure
// (Disclosure) in the rows that are Expandable; then CloseRows, the bar and
// CloseList.
type Outline struct {
	List
	// ExpansionChanged: a node was expanded or collapsed in this frame.
	ExpansionChanged bool

	ost  *OutlineState
	tree *Tree
}

// disclosureKey is the key of the disclosure of the row whose key is row.
func disclosureKey(row ui.Key) ui.Key { return ui.KeyN(row, 0xD15C) }

// BeginOutline starts an outline of tree t with state st: a list of t's shown
// nodes, in the order of the tree, each indented by its level. Besides a
// list's keys, with the focus Right expands the selected nodes and Left
// collapses them, or on a node that is collapsed or a leaf selects its
// parent; with Option (Alt) they expand and collapse the whole subtree. A
// click on a row's disclosure expands or collapses it without selecting it,
// Option-click its whole subtree. Rows are virtualised as a list's, and the
// shown nodes are made again only when the expansion or the tree changes,
// in time proportional to the shown rows.
func (k *Kit) BeginOutline(label string, st *OutlineState, t *Tree, o ListOpts) Outline {
	c := k.c
	ol := Outline{ost: st, tree: t}
	if st.stale || st.nodes != len(t.Size) || len(st.rows) == 0 && len(t.Size) > 0 {
		st.rebuild(t)
	}
	_, ident := ui.SplitLabel(label)
	okey := ui.HashString(c.ParentKey(), ident)
	if !k.Disabled() {
		ol.input(k, okey)
	}
	if st.stale {
		st.rebuild(t)
	}
	ol.List = k.beginList(label, &st.ListState, len(st.rows), false, o)
	st.built = [2]int{ol.First, ol.Last}
	return ol
}

// input applies the disclosure clicks and the assistive technology's requests
// of the last frame and the Left and Right keys of this one.
func (ol *Outline) input(k *Kit, okey ui.Key) {
	st, t := ol.ost, ol.tree
	c := k.c
	in := c.Input()
	all := in.Mods&ui.ModAlt != 0
	if st.hasPend {
		st.hasPend = false
		st.Expand(t, st.pend, st.pendOn, false)
		ol.ExpansionChanged = true
	}
	for r := max(st.built[0], 0); r < st.built[1] && r < len(st.rows); r++ {
		node := st.rows[r]
		if t.Size[node] <= 1 {
			continue
		}
		if c.PeekSignal(disclosureKey(ui.KeyN(okey, 1<<32|uint64(node)))).Clicked {
			st.Expand(t, node, !st.Expanded(node), all)
			ol.ExpansionChanged = true
		}
	}
	if c.FocusKey() != ui.KeyN(okey, 1) || len(st.rows) == 0 {
		return
	}
	right, left := false, false
	switch {
	case c.TakeKey(ui.KeyRight, 0):
		right = true
	case c.TakeKey(ui.KeyRight, ui.ModAlt):
		right, all = true, true
	case c.TakeKey(ui.KeyLeft, 0):
		left = true
	case c.TakeKey(ui.KeyLeft, ui.ModAlt):
		left, all = true, true
	default:
		return
	}
	s := &st.Sel
	acted := false
	// every selected node that can go the asked way goes; the selection is
	// of shown nodes, and Expand only deselects below the node it collapses
	for it, ok := s.NextSet(0); ok; it, ok = s.NextSet(it + 1) {
		if int(it) >= len(t.Size) || t.Size[it] <= 1 {
			continue
		}
		open := st.Expanded(it)
		switch {
		case right && (!open || all):
			st.Expand(t, it, true, all)
			acted = true
		case left && (open || all):
			st.Expand(t, it, false, all)
			acted = true
		}
	}
	ol.ExpansionChanged = ol.ExpansionChanged || acted
	if left && !acted && s.Count() == 1 { // a collapsed node or a leaf: its parent
		cur, _ := s.NextSet(0)
		if p := t.Parent(cur); p != NoItem {
			if st.stale {
				st.rebuild(t)
			}
			if r := st.rowOf(p); r >= 0 {
				st.MoveTo(r, false)
			}
		}
	}
	c.SetFocusVisible(true)
	c.RequestFrame()
}

// RowState reads the state of display row r: a list row's, with the node's
// level and expansion.
func (ol *Outline) RowState(r int) RowState {
	node := ol.ost.rows[r]
	rs := ol.List.RowState(r, node)
	t := ol.tree
	rs.Level = int(t.Depth[node])
	rs.Expandable = t.Size[node] > 1
	rs.Expanded = rs.Expandable && ol.ost.Expanded(node)
	return rs
}

// BeginRow builds the row rs as List.BeginRow does, and takes an assistive
// technology's requests to expand or collapse it (applied in the next frame).
func (ol *Outline) BeginRow(rs *RowState, name []byte, flags ui.Flags) ui.ID {
	id := ol.List.BeginRow(rs, name, flags)
	if !rs.Expandable {
		return id
	}
	c := ol.List.k.c
	if a := c.Signal(id).Action; a&(ui.ActExpand|ui.ActCollapse) != 0 {
		st := ol.ost
		st.pend, st.pendOn, st.hasPend = rs.Item, a&ui.ActExpand != 0, true
		c.RequestFrame()
	}
	return id
}

// Disclosure builds the disclosure of the row being built, an expandable
// one: a clickable box of w × h pixels at x, y in the row (floating), in the
// style the recipe set with ui's Next calls and its drawing flags, with the
// recipe's glyph as its child. A click expands or collapses the node (in the
// next frame) without selecting it. It is pushed; close it with c.Pop.
func (ol *Outline) Disclosure(rs *RowState, x, y, w, h float32, flags ui.Flags) ui.ID {
	c := ol.List.k.c
	c.NextPos(x, y)
	c.NextSize(ui.Px(w), ui.Px(h))
	f := flags | ui.FloatingX | ui.FloatingY
	if !rs.Disabled {
		f |= ui.Clickable
	}
	id := c.BoxKeyed(f, disclosureKey(rs.Key), nil)
	c.Push(id)
	return id
}

// Tree is the outline's tree.
func (ol *Outline) Tree() *Tree { return ol.tree }

// countOpen is the number of expanded nodes (for tests).
func (st *OutlineState) countOpen() int {
	n := 0
	for _, w := range st.open {
		n += bits.OnesCount64(w)
	}
	return n
}
