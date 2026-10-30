// Package workspace is a layout of panes for apps whose window is a set of
// panes: a tree of splits, side by side and stacked, whose leaves are panes
// with identities of their own, the pane in use (the focus), and the layout
// saved and restored as JSON.
//
// A Layout is app-owned, pointer-free state in fixed arrays, like
// base.SplitState: changing it (a split, a close, a drag) allocates nothing,
// and neither does a frame. Panes are named by the app's PaneID, which stays
// the same wherever the pane goes; Slot gives each pane a small index that
// stays the same while the pane lives, for the app's own per-pane arrays and
// theme.Surface.Slot.
//
// A frame of an app on a Layout:
//
//	ev := l.Update(c, workspace.Keys{Dir: ui.ModSuper | ui.ModAlt, Cycle: true})
//	l.Arrange(work, workspace.Metrics{Scale: s, Gutter: [2]float32{g, g}, Min: [2]float32{m, m}})
//	for i := 0; i < l.Len(); i++ {
//		id := l.Pane(i)
//		l.NextPane(c, id, title) // the pane's semantics, on the box its surface builds
//		… build pane id at l.Rect(id), keyed l.PaneKey(id)
//	}
//	for i := 0; i < l.NumDividers(); i++ {
//		d := l.Divider(i)
//		l.NextDivider(c, &d)
//		th.Chrome.Divider(c, th, d.Key, d.Axis, d.Rect, d.Gutter)
//	}
//
// The dividers move as base.Splitter's do: dragged, or focused and moved with
// the arrow keys (16 pt a step, 64 pt with Shift), Home and End, and the
// accessibility increment and decrement; no pane goes below its minimum.
package workspace

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// MaxPanes is the most panes a Layout holds.
const MaxPanes = 16

// maxNodes is the most nodes of the tree: the panes and the splits between them.
const maxNodes = 2*MaxPanes - 1

// PaneID names a pane. The app chooses it; 0 is no pane.
type PaneID uint32

// Dir is a direction: where a new pane goes beside another (Split), and where
// the focus moves (FocusDir).
type Dir uint8

const (
	// Right is after along the x axis.
	Right Dir = iota
	// Down is after along the y axis.
	Down
	// Left is before along the x axis.
	Left
	// Up is before along the y axis.
	Up
)

func (d Dir) axis() ui.Axis {
	if d == Down || d == Up {
		return ui.AxisY
	}
	return ui.AxisX
}

func (d Dir) after() bool { return d == Right || d == Down }

// Limits bound a pane's size, in points along x (0) and y (1). A zero Min
// takes the Metrics' least size; a zero Max is no bound.
type Limits struct {
	Min, Max [2]float32 // least and greatest size, x and y, in points (see above)
	// Yield makes the pane give up its room, rather than shrink below Min or
	// squeeze its neighbour below its own, when the room is too small for
	// both (Zap's side pane, which shows only while it has 240 pt beside the
	// chat's 320). While it has none it is not Shown. Its least size does not
	// count toward the room the layout asks of its parent.
	Yield bool
}

// fixed says which child of a split keeps a size in points.
const (
	fixedNone   = 0
	fixedFirst  = 1
	fixedSecond = 2
)

type node struct {
	used   bool
	leaf   bool
	slot   int8    // leaf: the pane's slot
	axis   ui.Axis // split: AxisX side by side, AxisY stacked
	fixed  uint8   // split: fixedNone, or the child whose size is fixedPt
	ratio  float32 // split: the first child's share of the room
	fixPt  float32 // split: the fixed child's size in points
	a, b   int8    // split: the children
	parent int8    // -1 at the root
	uid    uint32  // split: the divider's identity

	// The latest Arrange.
	open   bool // split: both children have room (its divider shows)
	rect   [4]float32
	gut    [4]float32 // split: the gutter between the children
	avail  float32    // split: the room the children share
	lo, hi float32    // split: the first child's least and greatest size
	press  float32    // split: the ratio when a drag began
}

type slot struct {
	id      PaneID
	lim     Limits
	hidden  bool // SetHidden: in the tree, with no room
	empty   bool // SetEmpty: shown, but the focus passes it by
	dropped bool // the latest Arrange left it no room (Limits.Yield)
}

// Layout is a workspace: the panes, the tree that places them, the focused
// pane and a maximised one. The zero value has no panes; Reset starts one.
type Layout struct {
	// Key is the base of the keys of the panes' and the dividers' boxes
	// (PaneKey, DividerKey): set it once, to a key of the app's.
	Key ui.Key
	// SoloKeepsFocus makes SetSolo leave the focus where it is, for an app
	// whose solo pane covers the pane in use without taking its place (Zap's
	// side pane over the chat in a narrow window, which stays translucent
	// because the chat is still in use).
	SoloKeepsFocus bool
	// DividersOffTab keeps the dividers out of the Tab order (NextDivider),
	// for an app with a Tab order of its own; they still drag and still
	// have their accessibility role and value.
	DividersOffTab bool

	nodes  [maxNodes]node
	slots  [MaxPanes]slot
	root   int8
	serial uint32 // the last divider identity handed out

	focus PaneID
	mru   [MaxPanes]PaneID // the panes focused, most recent first
	zoom  PaneID
	solo  PaneID // the one pane shown (SetSolo), 0: all

	// In order, refreshed after every change: the panes' slots as they read
	// (left to right, top to bottom) and the splits, parents first.
	order [MaxPanes]int8
	n     int
	divs  [MaxPanes - 1]int8
	nd    int
	live  [MaxPanes - 1]int8 // the splits whose dividers show, as of the latest Arrange
	nl    int
	m     Metrics
	kbd   bool // the keyboard moved the focus: NextPane announces it
}

// Reset makes the layout a single pane, focused.
func (l *Layout) Reset(id PaneID) {
	key := l.Key
	*l = Layout{Key: key, root: -1}
	if id == 0 {
		return
	}
	s := l.newSlot(id)
	n := l.newNode()
	l.nodes[n].leaf, l.nodes[n].slot = true, s
	l.root = n
	l.reindex()
	l.SetFocus(id)
}

// Len is the number of panes.
func (l *Layout) Len() int { return l.n }

// Pane is the i-th pane in reading order (left to right, top to bottom
// through the tree), from 0 to Len.
func (l *Layout) Pane(i int) PaneID {
	if i < 0 || i >= l.n {
		return 0
	}
	return l.slots[l.order[i]].id
}

// Has reports whether pane id is in the layout.
func (l *Layout) Has(id PaneID) bool { return l.Slot(id) >= 0 }

// Slot is pane id's slot, 0 to MaxPanes−1, which stays the same while the
// pane is in the layout; −1 for a pane that is not.
func (l *Layout) Slot(id PaneID) int {
	if id == 0 {
		return -1
	}
	for i := range l.slots {
		if l.slots[i].id == id {
			return i
		}
	}
	return -1
}

// Rect is where pane id was placed by the latest Arrange (x0, y0, x1, y1);
// zero for a pane that is not in the layout or not shown.
func (l *Layout) Rect(id PaneID) [4]float32 {
	if n := l.leafOf(int8(l.Slot(id))); n >= 0 {
		return l.nodes[n].rect
	}
	return [4]float32{}
}

// SetLimits bounds pane id's size. Limits are not saved with the layout.
func (l *Layout) SetLimits(id PaneID, lim Limits) {
	if s := l.Slot(id); s >= 0 {
		l.slots[s].lim = lim
	}
}

// Split puts the new pane id beside pane target, in direction d, sharing
// target's room: the new pane takes share of it (0.5 when share is not
// between 0 and 1). The focus stays where it was. It returns the identity of
// the new divider between the two, or 0 when target is not in the layout, id
// is 0 or already in it, or the layout is full.
func (l *Layout) Split(target, id PaneID, d Dir, share float32) uint32 {
	ts := int8(l.Slot(target))
	leaf := l.leafOf(ts)
	if leaf < 0 || id == 0 || l.Has(id) || l.n >= MaxPanes {
		return 0
	}
	if !(share > 0 && share < 1) {
		share = 0.5
	}
	s := l.newSlot(id)
	nl := l.newNode()
	ns := l.newNode()
	l.nodes[nl].leaf, l.nodes[nl].slot = true, s
	parent := l.nodes[leaf].parent
	sp := &l.nodes[ns]
	l.serial++
	sp.uid, sp.axis, sp.parent = l.serial, d.axis(), parent
	if d.after() {
		sp.a, sp.b, sp.ratio = leaf, nl, 1-share
	} else {
		sp.a, sp.b, sp.ratio = nl, leaf, share
	}
	l.nodes[leaf].parent, l.nodes[nl].parent = ns, ns
	l.replaceChild(parent, leaf, ns)
	l.reindex()
	return sp.uid
}

// Close takes pane id out of the layout and gives its room to its neighbour.
// When it had the focus, the focus goes back to the pane focused before it,
// or to the nearest pane of its neighbour if none was. It reports false for a
// pane not in the layout and for the last one.
func (l *Layout) Close(id PaneID) bool {
	s := int8(l.Slot(id))
	leaf := l.leafOf(s)
	if leaf < 0 || l.nodes[leaf].parent < 0 {
		return false
	}
	p := l.nodes[leaf].parent
	pn := &l.nodes[p]
	sib := pn.a
	if sib == leaf {
		sib = pn.b
	}
	g := pn.parent
	l.nodes[sib].parent = g
	l.replaceChild(g, p, sib)
	l.slots[s] = slot{}
	l.nodes[leaf] = node{}
	l.nodes[p] = node{}
	if l.zoom == id {
		l.zoom = 0
	}
	if l.solo == id {
		l.solo = 0
	}
	l.forget(id)
	l.reindex()
	if l.focus == id {
		l.focus = 0
		l.refocus(sib)
	}
	return true
}

// SetHidden hides pane id, or shows it again: a hidden pane keeps its place
// in the tree and its size for when it comes back (Zap's list under ⌘B, a
// side pane that is not open), its neighbour takes its room, and the focus
// leaves it as on Close.
func (l *Layout) SetHidden(id PaneID, hidden bool) {
	s := l.Slot(id)
	if s < 0 || l.slots[s].hidden == hidden {
		return
	}
	l.slots[s].hidden = hidden
	if !hidden {
		return
	}
	if l.zoom == id {
		l.zoom = 0
	}
	if l.solo == id {
		l.solo = 0
	}
	if l.focus == id {
		l.refocus(l.leafOf(int8(s)))
	}
}

// Hidden reports whether pane id is hidden (SetHidden).
func (l *Layout) Hidden(id PaneID) bool {
	s := l.Slot(id)
	return s >= 0 && l.slots[s].hidden
}

// SetEmpty says pane id has nothing to use (Zap's chat pane with no chat
// open): it stays on screen, but the focus passes it by, a press in it does
// not take the focus, and a focus it had goes to the pane used before it, as
// on Close. SetEmpty(id, false) makes it focusable again; it does not focus
// it.
func (l *Layout) SetEmpty(id PaneID, empty bool) {
	s := l.Slot(id)
	if s < 0 {
		return
	}
	l.slots[s].empty = empty
	if empty && l.focus == id {
		l.refocus(l.leafOf(int8(s)))
	}
}

// Empty reports whether pane id is empty (SetEmpty).
func (l *Layout) Empty(id PaneID) bool {
	s := l.Slot(id)
	return s >= 0 && l.slots[s].empty
}

// Shown reports whether pane id has room as of the latest Arrange: it is in
// the layout, not hidden, not left out by SetSolo, and not yielding (Limits).
func (l *Layout) Shown(id PaneID) bool {
	r := l.Rect(id)
	return r[2] > r[0] && r[3] > r[1]
}

// Solo is the one pane shown, 0 when all are (SetSolo).
func (l *Layout) Solo() PaneID { return l.solo }

// SetSolo shows pane id alone, over the whole work area, and focuses it
// (unless SoloKeepsFocus); 0
// shows the layout again. It is how a narrow window shows one pane at a time
// (Zap below 720 pt: the list, the chat, or a side pane covering the window):
// the others keep their places for when the window widens, and the focus
// cannot reach them.
func (l *Layout) SetSolo(id PaneID) {
	if id != 0 && !l.Has(id) {
		return
	}
	l.solo = id
	if id != 0 && !l.SoloKeepsFocus {
		l.SetFocus(id)
	}
}

// Zoomed is the maximised pane, 0 when none is.
func (l *Layout) Zoomed() PaneID { return l.zoom }

// SetZoom maximises pane id over the others and focuses it; 0 restores the
// layout. Unlike SetSolo, the others keep their rectangles (Rect), so that the
// app can animate the zoomed pane from its place to the work area and back;
// the app draws it over the others.
func (l *Layout) SetZoom(id PaneID) {
	if id != 0 && !l.Has(id) {
		return
	}
	l.zoom = id
	if id != 0 {
		l.SetFocus(id)
	}
}

// SetFixed makes the split that holds pane id keep id's size along its axis
// at pt points when the room changes, instead of a share of the room; the
// user's drags change that size (Fixed). pt 0 or less returns to a share,
// as Unfix does, so clamp a stored size before passing it. It does nothing
// for the last pane.
func (l *Layout) SetFixed(id PaneID, pt float32) {
	leaf := l.leafOf(int8(l.Slot(id)))
	if leaf < 0 || l.nodes[leaf].parent < 0 {
		return
	}
	p := &l.nodes[l.nodes[leaf].parent]
	switch {
	case pt <= 0:
		p.fixed = fixedNone
	case p.a == leaf:
		p.fixed, p.fixPt = fixedFirst, pt
	default:
		p.fixed, p.fixPt = fixedSecond, pt
	}
}

// Unfix returns the split that holds pane id to a share of the room.
func (l *Layout) Unfix(id PaneID) { l.SetFixed(id, 0) }

// Fixed is the size in points pane id keeps (SetFixed, as dragged since),
// 0 when it has a share of the room instead.
func (l *Layout) Fixed(id PaneID) float32 {
	leaf := l.leafOf(int8(l.Slot(id)))
	if leaf < 0 || l.nodes[leaf].parent < 0 {
		return 0
	}
	p := &l.nodes[l.nodes[leaf].parent]
	if p.fixed == fixedFirst && p.a == leaf || p.fixed == fixedSecond && p.b == leaf {
		return p.fixPt
	}
	return 0
}

// ---- the tree ----

func (l *Layout) newNode() int8 {
	for i := range l.nodes {
		if !l.nodes[i].used {
			l.nodes[i] = node{used: true, parent: -1}
			return int8(i)
		}
	}
	return -1 // not reached: a full layout refuses before
}

func (l *Layout) newSlot(id PaneID) int8 {
	for i := range l.slots {
		if l.slots[i].id == 0 {
			l.slots[i] = slot{id: id}
			return int8(i)
		}
	}
	return -1 // not reached: a full layout refuses before
}

// leafOf is the node that holds slot s, −1 if none does.
func (l *Layout) leafOf(s int8) int8 {
	if s < 0 {
		return -1
	}
	for i := range l.nodes {
		if n := &l.nodes[i]; n.used && n.leaf && n.slot == s {
			return int8(i)
		}
	}
	return -1
}

func (l *Layout) firstLeaf(n int8) int8 {
	for !l.nodes[n].leaf {
		n = l.nodes[n].a
	}
	return n
}

// hasRoot reports whether the layout has panes (the zero value has none).
func (l *Layout) hasRoot() bool { return l.root >= 0 && l.nodes[l.root].used }

// replaceChild makes to the child of parent that was from (the root when
// parent is −1).
func (l *Layout) replaceChild(parent, from, to int8) {
	switch {
	case parent < 0:
		l.root = to
	case l.nodes[parent].a == from:
		l.nodes[parent].a = to
	default:
		l.nodes[parent].b = to
	}
}

// reindex lists the panes in reading order and the splits parents first.
func (l *Layout) reindex() {
	l.n, l.nd = 0, 0
	if l.hasRoot() {
		l.walk(l.root)
	}
	l.live, l.nl = l.divs, l.nd
}

func (l *Layout) walk(n int8) {
	nd := &l.nodes[n]
	if nd.leaf {
		l.order[l.n] = nd.slot
		l.n++
		return
	}
	l.divs[l.nd] = n
	l.nd++
	l.walk(nd.a)
	l.walk(nd.b)
}

// ---- placing ----

// Metrics are the lengths Arrange places the panes with, in pixels.
type Metrics struct {
	// Scale is pixels per point: Limits and fixed sizes are in points.
	Scale float32
	// Gutter is the room between panes side by side (0) and stacked (1).
	Gutter [2]float32
	// Min is the least size of a pane along x and y that sets no Limits.
	Min [2]float32
}

// Arrange places the panes in work (x0, y0, x1, y1, pixels), on whole
// pixels. The first child of a split gets its share of the room less the
// gutter, or its fixed size, kept between the two children's least and
// greatest sizes (shared in proportion to their least sizes when the room is
// too small for both).
//
// A hidden pane (SetHidden), the panes SetSolo leaves out, and a yielding pane
// with too little room get none: their neighbours take it, with no gutter. If
// the focused pane is left with none, the focus goes back as on Close.
func (l *Layout) Arrange(work [4]float32, m Metrics) {
	l.m = m
	if m.Scale <= 0 {
		l.m.Scale = 1
	}
	if !l.hasRoot() {
		return
	}
	for i := range l.slots {
		l.slots[i].dropped = false
	}
	l.place(l.root, work)
	l.nl = 0
	for i := 0; i < l.nd; i++ {
		if l.nodes[l.divs[i]].open {
			l.live[l.nl] = l.divs[i]
			l.nl++
		}
	}
	if s := l.Slot(l.focus); s >= 0 && !l.keeps(s) {
		l.refocus(l.leafOf(int8(s)))
	}
}

// gone reports whether node n has no pane that is shown regardless of room:
// every pane under it is hidden or left out by the solo pane.
func (l *Layout) gone(n int8) bool {
	nd := &l.nodes[n]
	if nd.leaf {
		s := &l.slots[nd.slot]
		return s.hidden || l.solo != 0 && s.id != l.solo
	}
	return l.gone(nd.a) && l.gone(nd.b)
}

// yields reports whether node n is a pane that gives up its room (Limits.Yield).
func (l *Layout) yields(n int8) bool {
	nd := &l.nodes[n]
	return nd.leaf && l.slots[nd.slot].lim.Yield
}

// clear gives every pane under n no room: dropped when it yielded it.
func (l *Layout) clear(n int8, dropped bool) {
	nd := &l.nodes[n]
	nd.rect, nd.open, nd.avail = [4]float32{}, false, 0
	if nd.leaf {
		l.slots[nd.slot].dropped = dropped
		return
	}
	l.clear(nd.a, dropped)
	l.clear(nd.b, dropped)
}

// leafMin is the least size of leaf n along ax.
func (l *Layout) leafMin(n int8, ax int) float32 {
	return max(l.m.Min[ax], l.slots[l.nodes[n].slot].lim.Min[ax]*l.m.Scale)
}

// minPx is the least size of node n along ax that its parent makes room
// for: panes that are gone, and panes that yield, ask none.
func (l *Layout) minPx(n int8, ax int) float32 {
	nd := &l.nodes[n]
	if nd.leaf {
		if l.gone(n) || l.yields(n) {
			return 0
		}
		return l.leafMin(n, ax)
	}
	a, b := l.minPx(nd.a, ax), l.minPx(nd.b, ax)
	switch {
	case int(nd.axis) != ax:
		return max(a, b)
	case l.gone(nd.a) || l.yields(nd.a):
		return b
	case l.gone(nd.b) || l.yields(nd.b):
		return a
	}
	return a + b + l.m.Gutter[ax]
}

// maxPx is the greatest size of node n along axis ax; 0 is none.
func (l *Layout) maxPx(n int8, ax int) float32 {
	nd := &l.nodes[n]
	if nd.leaf {
		return l.slots[nd.slot].lim.Max[ax] * l.m.Scale
	}
	switch {
	case l.gone(nd.a):
		return l.maxPx(nd.b, ax)
	case l.gone(nd.b):
		return l.maxPx(nd.a, ax)
	}
	a, b := l.maxPx(nd.a, ax), l.maxPx(nd.b, ax)
	switch {
	case int(nd.axis) != ax && a > 0 && b > 0:
		return min(a, b)
	case int(nd.axis) != ax:
		return max(a, b)
	case a > 0 && b > 0:
		return a + b + l.m.Gutter[ax]
	}
	return 0
}

func (l *Layout) place(n int8, r [4]float32) {
	nd := &l.nodes[n]
	nd.rect, nd.open, nd.avail = r, false, 0
	if nd.leaf {
		if l.gone(n) {
			nd.rect = [4]float32{}
		}
		return
	}
	switch {
	case l.gone(nd.a):
		l.clear(nd.a, false)
		l.place(nd.b, r)
		return
	case l.gone(nd.b):
		l.clear(nd.b, false)
		l.place(nd.a, r)
		return
	}
	ax := int(nd.axis)
	gut := l.m.Gutter[ax]
	a0, a1 := r[ax], r[ax+2]
	avail := max(0, a1-a0-gut)
	minA, minB := l.minPx(nd.a, ax), l.minPx(nd.b, ax)
	lo, hi := minA, avail-minB
	if mx := l.maxPx(nd.a, ax); mx > 0 {
		hi = min(hi, mx)
	}
	if mx := l.maxPx(nd.b, ax); mx > 0 {
		lo = max(lo, avail-mx)
	}
	first := avail * nd.ratio
	switch nd.fixed {
	case fixedFirst:
		first = nd.fixPt * l.m.Scale
	case fixedSecond:
		first = avail - nd.fixPt*l.m.Scale
	}
	if lo > hi {
		// Too little room for both: a pane that yields gives up its room.
		// (Its own least size is not in minA or minB: compare it here.)
		switch {
		case l.yields(nd.b):
			l.clear(nd.b, true)
			l.place(nd.a, r)
			return
		case l.yields(nd.a):
			l.clear(nd.a, true)
			l.place(nd.b, r)
			return
		}
	}
	switch {
	case l.yields(nd.b) && avail-minA < l.leafMin(nd.b, ax):
		l.clear(nd.b, true)
		l.place(nd.a, r)
		return
	case l.yields(nd.a) && avail-minB < l.leafMin(nd.a, ax):
		l.clear(nd.a, true)
		l.place(nd.b, r)
		return
	}
	if l.yields(nd.b) {
		hi = min(hi, avail-l.leafMin(nd.b, ax))
	}
	if l.yields(nd.a) {
		lo = max(lo, l.leafMin(nd.a, ax))
	}
	if lo <= hi {
		first = min(max(first, lo), hi)
	} else {
		first = avail * minA / (minA + minB)
		lo, hi = first, first
	}
	nd.avail, nd.lo, nd.hi, nd.open = avail, lo, hi, true
	if nd.fixed != fixedNone && avail > 0 {
		nd.ratio = first / avail // where a drag starts from
	}
	mid := theme.Snap(a0 + first)
	ra, rb, gr := r, r, r
	ra[ax+2] = mid
	rb[ax] = mid + gut
	gr[ax], gr[ax+2] = mid, mid+gut
	nd.gut = gr
	l.place(nd.a, ra)
	l.place(nd.b, rb)
}

// ---- dividers ----

// Divider is a split's divider: the gutter between its two children.
type Divider struct {
	// ID is the divider's identity, the same across frames while its split
	// lives; Key is the key of its box (DividerKey).
	ID  uint32
	Key ui.Key
	// Index is its place in the layout's dividers, from 0.
	Index int
	// Axis is the split's: AxisX for panes side by side.
	Axis ui.Axis
	// Rect is the room the split's two children share and Gutter the gutter
	// between them, as of the latest Arrange.
	Rect, Gutter [4]float32
	// Share is the first child's share of the room.
	Share float32
}

// NumDividers is the number of dividers that show: one fewer than the panes
// shown, as of the latest Arrange (before the first, one fewer than the
// panes).
func (l *Layout) NumDividers() int { return l.nl }

// Divider is divider i of those that show, parents before their children.
func (l *Layout) Divider(i int) Divider {
	if i < 0 || i >= l.nl {
		return Divider{}
	}
	nd := &l.nodes[l.live[i]]
	return Divider{ID: nd.uid, Key: l.DividerKey(nd.uid), Index: i, Axis: nd.axis, Rect: nd.rect, Gutter: nd.gut, Share: nd.ratio}
}

// SetShare gives the first child of divider id's split share of the room.
func (l *Layout) SetShare(id uint32, share float32) {
	if n := l.divNode(id); n >= 0 && share > 0 && share < 1 {
		nd := &l.nodes[n]
		nd.ratio = share
		if nd.fixed != fixedNone && nd.avail > 0 {
			l.setFirst(nd, share*nd.avail)
		}
	}
}

func (l *Layout) divNode(id uint32) int8 {
	for i := 0; i < l.nd; i++ {
		if l.nodes[l.divs[i]].uid == id {
			return l.divs[i]
		}
	}
	return -1
}

// Press starts a move of divider id: Drag moves it from where it is now.
func (l *Layout) Press(id uint32) {
	if n := l.divNode(id); n >= 0 {
		l.nodes[n].press = l.nodes[n].ratio
	}
}

// Drag moves divider id to where it was at Press plus delta pixels along its
// axis, within the children's least and greatest sizes, as of the latest
// Arrange.
func (l *Layout) Drag(id uint32, delta float32) {
	n := l.divNode(id)
	if n < 0 {
		return
	}
	nd := &l.nodes[n]
	if nd.avail <= 0 {
		return
	}
	l.setFirst(nd, min(max(nd.press*nd.avail+delta, nd.lo), nd.hi))
}

// setFirst makes the first child of split nd first pixels long.
func (l *Layout) setFirst(nd *node, first float32) {
	nd.ratio = first / nd.avail
	switch nd.fixed {
	case fixedFirst:
		nd.fixPt = first / l.m.Scale
	case fixedSecond:
		nd.fixPt = (nd.avail - first) / l.m.Scale
	}
}

// PaneKey is the key of pane id's box.
func (l *Layout) PaneKey(id PaneID) ui.Key { return ui.KeyN(ui.KeyN(l.Key, 1), uint64(id)) }

// DividerKey is the key of divider id's box.
func (l *Layout) DividerKey(id uint32) ui.Key { return ui.KeyN(ui.KeyN(l.Key, 2), uint64(id)) }
