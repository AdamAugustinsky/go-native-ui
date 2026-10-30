package ui

import (
	"gonativeui/core"
	"gonativeui/internal/uitypes"
	"slices"
)

// Semantics (phase 5, D28): the roles, states and actions an accessibility
// bridge (phase 6) needs, recorded by the boxes that have a role and exported
// as a pointer-free tree.

// Role is what a box is, for assistive technologies. The zero value marks a
// box that is not part of the semantic tree.
type Role = uitypes.Role

const (
	// RoleNone is not part of the semantic tree.
	RoleNone Role = iota
	// RoleWindow is the window, the root of the tree.
	RoleWindow
	// RoleGroup is a container with no behaviour of its own.
	RoleGroup
	// RoleLabel is static text.
	RoleLabel
	// RoleImage is a picture or icon.
	RoleImage
	// RoleButton is a push button.
	RoleButton
	// RoleCheckbox is a check box.
	RoleCheckbox
	// RoleSwitch is an on/off switch.
	RoleSwitch
	// RoleRadio is a radio button.
	RoleRadio
	// RoleRadioGroup is the container of a set of radio buttons.
	RoleRadioGroup
	// RoleSlider is a slider (its value is a range, see SemNode.Range).
	RoleSlider
	// RoleTextField is an editable text field.
	RoleTextField
	// RoleSearchField is a text field for searching.
	RoleSearchField
	// RoleComboBox is a drop-down choice.
	RoleComboBox
	// RoleList is a list of items.
	RoleList
	// RoleListItem is an item of a list.
	RoleListItem
	// RoleTable is a table.
	RoleTable
	// RoleRow is a row of a table.
	RoleRow
	// RoleCell is a cell of a table row.
	RoleCell
	// RoleColumnHeader is the header of a table column.
	RoleColumnHeader
	// RoleScrollArea is a scrollable region.
	RoleScrollArea
	// RoleScrollBar is a scroll bar (its value is a range, see SemNode.Range).
	RoleScrollBar
	// RoleSplitter is a draggable divider between two panes.
	RoleSplitter
	// RoleTabList is the container of a set of tabs.
	RoleTabList
	// RoleTab is a tab.
	RoleTab
	// RoleTabPanel is the content shown for a tab.
	RoleTabPanel
	// RoleMenuBar is a menu bar.
	RoleMenuBar
	// RoleMenu is a menu or popup menu.
	RoleMenu
	// RoleMenuItem is an item of a menu.
	RoleMenuItem
	// RoleMenuItemCheckbox is a menu item that can be checked.
	RoleMenuItemCheckbox
	// RoleDialog is a dialog.
	RoleDialog
	// RoleTooltip is a tooltip.
	RoleTooltip
	// RoleProgressBar is a progress indicator (its value is a range, see SemNode.Range).
	RoleProgressBar
	// RoleLink is a link or a mention in rich text (a child of the text's
	// node, named by its text; pressing it is RichSignal.Clicked).
	RoleLink
	// RoleHeading is the heading of a section; SemNode.Level is its level (1
	// for the top one).
	RoleHeading
	// RoleToolbar is a toolbar: a group of controls for its window or pane.
	RoleToolbar
	// RoleSheet is a sheet: a modal pane attached to its window.
	RoleSheet
	// RoleHost is a hosted native view (Context.Host): SemNode.Host is its
	// platform.HostID. The export adds one for each host box built; the
	// accessibility bridge puts the native view itself in the tree there.
	RoleHost
)

// SemState are the state bits of a node.
type SemState = uitypes.SemState

const (
	SemChecked        SemState = 1 << iota // checked, on or pressed
	SemMixed                               // neither checked nor unchecked
	SemSelected                            // part of the selection
	SemExpanded                            // open (a menu, a combo box, a tree row)
	SemDisabled                            // set by the export for boxes with the Disabled flag
	SemFocused                             // set by the export for the focused box
	SemEditable                            // the value can be edited (text fields)
	SemModal                               // a modal overlay: everything beneath it is inert
	SemSortAscending                       // a column header the table is sorted by, ascending
	SemSortDescending                      // a column header the table is sorted by, descending
	SemLive                                // a live region: a change of its name or value is announced
	SemSecure                              // a secure text field (NextSecureField): its value is bullets, never the text
)

// SemAction are the actions a node supports (SemNode.Actions) and that an
// assistive technology can request (Input.Actions, Signal.Action).
type SemAction = uitypes.SemAction

const (
	ActPress          SemAction = 1 << iota // activate: click a button, toggle a checkbox
	ActFocus                                // give the box keyboard focus
	ActIncrement                            // step a range up
	ActDecrement                            // step a range down
	ActExpand                               // open
	ActCollapse                             // close
	ActScrollIntoView                       // scroll the box into view
	ActSetValue                             // set the value (SemRequest.Value for ranges)
	ActShowMenu                             // show the box's context menu
)

// semTracking owns the optional bridge snapshots and sibling ordinals.
// Standalone contexts record semantics by default; app disables it until AX activates.
type semTracking struct {
	disabled   bool
	skip       bool // latched at Begin; activation during a build waits for the next frame
	ordinals   []semOrdinal
	trees      [2]SemTree
	current    int
	frame      uint32
	generation uint64
}

type semOrdinal struct{ index, children int32 }

// SetSemanticsActive enables semantic records for subsequent builds. Contexts
// default to active; an app that never reads Semantics and wants the records'
// cost back switches them off, and an AX client's first query switches them
// on again.
func (c *Context) SetSemanticsActive(active bool) {
	if c.semTrack.disabled == !active {
		return
	}
	c.semTrack.disabled = !active
	c.semTrack.frame = 0
}

// SemanticsActive reports whether builds record accessibility semantics.
func (c *Context) SemanticsActive() bool { return !c.semTrack.disabled }

func (c *Context) recordSemantics(id ID) {
	n := &c.semNext
	if c.semTrack.skip {
		c.semNext = semNext{}
		return
	}
	ord := &c.semTrack.ordinals
	if len(*ord) < len(c.boxes) {
		*ord = append(*ord, make([]semOrdinal, cap(c.boxes)-len(*ord))...)
	}
	b := &c.boxes[id]
	(*ord)[id] = semOrdinal{}
	if b.parent != 0 && b.flags&floatingMask == 0 {
		(*ord)[id].index = (*ord)[b.parent].children
		(*ord)[b.parent].children++
	}
	if n.role != RoleNone {
		c.sems = append(c.sems, semRec{id: id, role: n.role, state: n.state, actions: n.actions, hasActions: n.hasActions,
			name: n.name, value: n.value, rng: n.rng, hasRange: n.hasRange, pos: n.pos, hasPos: n.hasPos, level: n.level})
		b.sem = int32(len(c.sems))
	}
	c.semNext = semNext{}
}

// SemanticSnapshot returns a read-only tree and a generation that changes only
// when its contents change. It exports at most once per frame, compares exactly,
// and retains the previous tree for bridge diffing. The tree is borrowed until
// the next call. Disabled or not-yet-built semantics return nil and generation 0.
func (c *Context) SemanticSnapshot() (*SemTree, uint64) {
	st := &c.semTrack
	if st.disabled || st.skip || len(c.sems) == 0 {
		return nil, 0
	}
	if st.frame != c.frame || st.generation == 0 {
		next, prev := &st.trees[1-st.current], &st.trees[st.current]
		c.Semantics(next)
		if st.generation == 0 || !slices.Equal(next.Nodes, prev.Nodes) || !slices.Equal(next.Bytes, prev.Bytes) ||
			next.Dropped != prev.Dropped || next.Announce != prev.Announce || next.AnnounceSeq != prev.AnnounceSeq || next.AnnouncePolite != prev.AnnouncePolite {
			st.current = 1 - st.current
			st.generation++
		}
		st.frame = c.frame
	}
	return &st.trees[st.current], st.generation
}

// semNext is the semantics given to the next box (the NextRole family).
type semNext struct {
	role       Role
	state      SemState
	actions    SemAction
	hasActions bool
	name       core.Span
	value      core.Span
	rng        [4]float64
	hasRange   bool
	pos        [2]int32
	hasPos     bool
	level      int32
}

// semRec is the semantics of one box that has a role, in build order.
type semRec struct {
	id         ID
	role       Role
	state      SemState
	actions    SemAction
	hasActions bool
	name       core.Span
	value      core.Span
	rng        [4]float64
	hasRange   bool
	pos        [2]int32
	hasPos     bool
	level      int32
	sel        [2]int32
	hasSel     bool
	node       int32 // its index in the exported tree (scratch of Semantics)
	lastChild  int32
}

// NextRole gives the next box built a role; boxes without one are not part of
// the semantic tree. It also starts the semantics of that box: the other Next…
// setters below apply to it too and are cleared with it.
func (c *Context) NextRole(r Role) { c.semNext.role = r }

// NextSemName sets the accessible name (the label) of the next box. The text
// is copied.
func (c *Context) NextSemName(s string) {
	if !c.semTrack.skip {
		c.semNext.name = c.semA.AppendString(s)
	}
}

// SetSemNameBytes names box id, which was built in this frame with a role, from
// bytes (copied): for a name that is known only from the box's content, after
// the box was built. It does nothing for a box without a role.
func (c *Context) SetSemNameBytes(id ID, b []byte) {
	if r := c.semOf(id); r != nil {
		r.name = c.semA.AppendBytes(b)
	}
}

// NextSemNameBytes is NextSemName for bytes (such as a Fmt result).
func (c *Context) NextSemNameBytes(b []byte) {
	if !c.semTrack.skip {
		c.semNext.name = c.semA.AppendBytes(b)
	}
}

// NextSemValueBytes sets the accessible value (the text of a field, the
// current choice) of the next box. The text is copied.
func (c *Context) NextSemValueBytes(b []byte) {
	if !c.semTrack.skip {
		c.semNext.value = c.semA.AppendBytes(b)
	}
}

// NextSemState sets the state bits of the next box. SemDisabled, SemFocused
// and SemModal are added by the export.
func (c *Context) NextSemState(s SemState) { c.semNext.state = s }

// NextSemRange gives the next box a range: its minimum, maximum, current value
// and the step of one increment (sliders, scroll bars, progress bars).
func (c *Context) NextSemRange(min, max, val, step float64) {
	c.semNext.rng, c.semNext.hasRange = [4]float64{min, max, val, step}, true
}

// NextSemPos says that the next box is item index (from 0) of total in its
// list, table or group. Rows of a virtual list build only the visible part;
// this reports their place in the whole. Direct children of a ListRange box
// that set none get it from ListRange.
func (c *Context) NextSemPos(index, total int) {
	c.semNext.pos, c.semNext.hasPos = [2]int32{int32(index), int32(total)}, true
}

// NextSemLevel says that the next box is at depth level of a tree (an
// outline's row): 1 for a root, 2 for its children, and so on.
func (c *Context) NextSemLevel(level int) { c.semNext.level = int32(level) }

// NextSemActions sets the actions the next box supports. When it is not called
// the box has its role's default actions.
func (c *Context) NextSemActions(a SemAction) {
	c.semNext.actions, c.semNext.hasActions = a, true
}

const (
	// MaxSemNodes is the most nodes one export holds; the boxes beyond it, in
	// build order, are left out (SemTree.Dropped counts them).
	MaxSemNodes = 1 << 14
	// MaxSemBytes is the most name and value text one export holds; a node
	// whose text does not fit gets an empty one.
	MaxSemBytes = 1 << 20
	// maxSemValue is the longest value a text field exports; longer text is cut
	// at a character boundary.
	maxSemValue = 4096
)

// SemNode is one node of the exported semantic tree. It is pointer-free; Name
// and Value are (offset, length) pairs into SemTree.Bytes, and the tree is
// linked by indices into SemTree.Nodes, -1 for none.
type SemNode = uitypes.SemNode

// SemTextKind is which text of a node has character geometry (SemNode.Text).
type SemTextKind = uitypes.SemTextKind

const (
	// SemTextNone: the node's text has no geometry to ask about.
	SemTextNone SemTextKind = iota
	// SemTextField: the Value of a text field (one line).
	SemTextField
	// SemTextRich: the Name of a RichText box (its paragraph).
	SemTextRich
)

// SemTree is a snapshot of the semantic tree. Nodes are in build order, parents
// before children; Nodes[0] is the window.
type SemTree struct {
	// Nodes are the tree's nodes in build order; Nodes[0] is the window.
	Nodes []SemNode
	Bytes []byte // the text of the names and values
	// Dropped is the number of boxes with a role left out of the last export
	// because the tree reached MaxSemNodes.
	Dropped int
	// Announce is the text the frame asked to be announced (Context.Announce),
	// a span of Bytes; empty when it asked for none.
	Announce [2]uint32
	// AnnounceSeq numbers the announcement (it grows with every
	// Context.Announce), so that the same text asked for again in the next
	// frame is announced again, and a tree exported twice is announced once.
	AnnounceSeq uint32
	// AnnouncePolite is set when the announcement waits for the screen reader
	// to finish (Context.AnnouncePolite) rather than interrupting it.
	AnnouncePolite bool
}

// Reserve makes room for the given numbers of nodes and text bytes, so that
// exporting a tree of that size does not allocate.
func (t *SemTree) Reserve(nodes, bytes int) {
	if cap(t.Nodes) < nodes {
		t.Nodes = append(make([]SemNode, 0, nodes), t.Nodes...)
	}
	if cap(t.Bytes) < bytes {
		t.Bytes = append(make([]byte, 0, bytes), t.Bytes...)
	}
}

// Text returns the text of a Name or Value span (aliasing Bytes).
func (t *SemTree) Text(span [2]uint32) []byte { return t.Bytes[span[0] : span[0]+span[1]] }

// defaultActions are the actions of a role when NextSemActions is not used.
func defaultActions(r Role) SemAction {
	switch r {
	case RoleButton, RoleCheckbox, RoleSwitch, RoleRadio, RoleMenuItem, RoleMenuItemCheckbox:
		return ActPress | ActFocus
	case RoleTab, RoleListItem, RoleRow:
		return ActPress | ActFocus | ActScrollIntoView
	case RoleComboBox:
		return ActFocus | ActExpand | ActCollapse | ActShowMenu
	case RoleSlider:
		return ActFocus | ActIncrement | ActDecrement | ActSetValue
	case RoleScrollBar:
		return ActIncrement | ActDecrement | ActSetValue
	case RoleSplitter:
		return ActFocus | ActIncrement | ActDecrement
	case RoleTextField, RoleSearchField:
		return ActFocus | ActSetValue
	case RoleScrollArea, RoleList, RoleTable:
		return ActScrollIntoView
	}
	return 0
}

// Semantics exports the semantic tree of the last frame (call it after End)
// into dst, replacing what it held and reusing its storage: a snapshot that
// stays valid until the next export. Only boxes that were given a role
// (NextRole) are in it, and the window root; a node's parent is the nearest
// ancestor with a role. The export holds at most MaxSemNodes nodes and
// MaxSemBytes of text and drops the rest (see SemTree.Dropped).
//
// State bits that ui knows are added to the ones the box set: SemDisabled for a
// box with the Disabled flag (its Actions are then empty), SemFocused for the
// box with the keyboard focus, SemModal for a modal overlay. A box under a
// ListRange box that did not say NextSemPos reports its place in the whole list.
func (c *Context) Semantics(dst *SemTree) {
	dst.Nodes, dst.Bytes, dst.Dropped, dst.Announce, dst.AnnounceSeq = dst.Nodes[:0], dst.Bytes[:0], 0, [2]uint32{}, 0
	dst.AnnouncePolite = false
	if c.semTrack.skip {
		return
	}
	if c.shell.frame == c.frame && c.shell.nHosts > 0 {
		c.semWithHosts(dst)
	} else {
		for i := range c.sems {
			if !c.semAppend(dst, &c.sems[i]) {
				dst.Dropped = len(c.sems) - i
				break
			}
		}
	}
	c.semFocusBubble(dst)
	c.semAnnounce(dst)
}

// semAppend appends the node of record rec (and its link children) to dst; it
// reports false when the tree is full.
func (c *Context) semAppend(dst *SemTree, rec *semRec) bool {
	b := &c.boxes[rec.id]
	if b.hid { // hidden by a flow's MaxLines
		rec.node = -1
		return true
	}
	if len(dst.Nodes) >= MaxSemNodes {
		return false
	}
	n := SemNode{
		Key: b.key, Role: rec.role, State: rec.state, Actions: rec.actions,
		Bounds: b.rect, Parent: -1, FirstChild: -1, NextSibling: -1,
	}
	if !rec.hasActions {
		n.Actions = defaultActions(rec.role)
	}
	if b.flags&Disabled != 0 {
		n.State |= SemDisabled
		n.Actions = 0
	}
	if c.focusKey == b.key {
		n.State |= SemFocused
	}
	if b.ovRoot && b.ovl > 0 && int(b.ovl) <= c.nOvs && c.ovs[b.ovl-1].kind == OverlayModal {
		n.State |= SemModal
	}
	if rec.hasRange {
		n.Range = rec.rng
	}
	switch {
	case rec.hasPos:
		n.Pos = rec.pos
	case b.parent != 0 && c.boxes[b.parent].listRows > 0 && b.flags&floatingMask == 0:
		pb := &c.boxes[b.parent]
		idx := pb.listFirst + int(c.semTrack.ordinals[rec.id].index)
		n.Pos = [2]int32{int32(idx), int32(pb.listRows)}
	}
	n.Level = rec.level
	n.Name = c.semText(dst, rec.name)
	n.Value = c.semText(dst, rec.value)
	if rec.sel != [2]int32{} {
		// The selection is offsets into the exported value: when the value
		// did not fit the byte budget it is empty, and so is the selection.
		n.TextSel = [2]int32{min(rec.sel[0], int32(n.Value[1])), min(rec.sel[1], int32(n.Value[1]))}
	}
	c.semTextOf(b, rec, &n)
	c.semLink(dst, b, rec, &n)
	if b.rich != 0 {
		c.semLinks(dst, b, rec)
	}
	return true
}

// semLink links node n of box b (record rec) into dst under the node of the
// nearest ancestor with a role (an overlay hangs from the window) and appends
// it.
func (c *Context) semLink(dst *SemTree, b *box, rec *semRec, n *SemNode) {
	p := b.parent
	for p != 0 && c.boxes[p].sem == 0 {
		p = c.boxes[p].parent
	}
	if p == 0 && b.ovRoot && c.boxes[c.root].sem != 0 {
		p = c.root
	}
	self := int32(len(dst.Nodes))
	rec.node, rec.lastChild = self, -1
	if p != 0 {
		pr := &c.sems[c.boxes[p].sem-1]
		n.Parent = pr.node
		if pr.lastChild < 0 {
			dst.Nodes[pr.node].FirstChild = self
		} else {
			dst.Nodes[pr.lastChild].NextSibling = self
		}
		pr.lastChild = self
	}
	dst.Nodes = append(dst.Nodes, *n)
}

// semFocusBubble marks the node of the nearest ancestor with a role as focused
// when the focused box has none of its own (a widget whose inner box takes the
// keyboard, such as the scrolling body of a table): the focus is then reported
// on the widget. The window's node is never marked this way.
func (c *Context) semFocusBubble(dst *SemTree) {
	if c.focusKey == 0 {
		return
	}
	for i := range dst.Nodes {
		if dst.Nodes[i].State&SemFocused != 0 {
			return
		}
	}
	v, ok := c.table.Get(uint64(c.focusKey))
	if !ok {
		return
	}
	for p := c.boxes[v].parent; p != 0 && p != c.root; p = c.boxes[p].parent {
		if sem := c.boxes[p].sem; sem != 0 {
			if n := c.sems[sem-1].node; n >= 0 && int(n) < len(dst.Nodes) {
				dst.Nodes[n].State |= SemFocused
			}
			return
		}
	}
}

// semText copies the span from the frame's arena to the export's byte store.
func (c *Context) semText(dst *SemTree, sp core.Span) [2]uint32 {
	if sp.Len == 0 || len(dst.Bytes)+int(sp.Len) > MaxSemBytes {
		return [2]uint32{}
	}
	off := len(dst.Bytes)
	dst.Bytes = append(dst.Bytes, c.semA.Bytes(sp)...)
	return [2]uint32{uint32(off), sp.Len}
}

// semOf is the semantics record of box id, nil if it has no role.
func (c *Context) semOf(id ID) *semRec {
	if s := c.boxes[id].sem; s != 0 {
		return &c.sems[s-1]
	}
	return nil
}

// semActions handles the accessibility requests ui can do itself: focusing a
// box (at Begin).
func (c *Context) semActions() {
	for i := 0; i < c.in.NActions; i++ {
		r := &c.in.Actions[i]
		if r.Action&ActFocus == 0 {
			continue
		}
		if v := c.focusBox(r.Key); v != 0 {
			if b := &c.boxes[v]; b.lastFrame == c.frame-1 && (b.flags&Focusable != 0 || r.Key != b.key) && b.flags&Disabled == 0 && !b.hid && !c.blocked(b) {
				c.focusKey = r.Key
				c.focusVisible = true
			}
		}
	}
}

// semScroll scrolls the boxes that were asked to be scrolled into view (after
// layout): the nearest scrolling ancestor makes the smallest move that shows
// the box.
func (c *Context) semScroll() {
	for i := 0; i < c.in.NActions; i++ {
		r := &c.in.Actions[i]
		if r.Action&ActScrollIntoView == 0 {
			continue
		}
		v, ok := c.table.Get(uint64(r.Key))
		if !ok || c.boxes[v].lastFrame != c.frame || c.boxes[v].flags&Disabled != 0 || c.boxes[v].hid {
			continue
		}
		b := &c.boxes[v]
		for p := b.parent; p != 0; p = c.boxes[p].parent {
			sc := &c.boxes[p]
			if sc.flags&scrollMask == 0 {
				continue
			}
			// The rectangles are as shown: at the offset plus a trackpad's
			// stretch (layout), which the content coordinates take back.
			ox, oy := sc.scroll[0]+c.stretchOf(sc, 0), sc.scroll[1]+c.stretchOf(sc, 1)
			x, y := float32(ox)-sc.rect[0], float32(oy)-sc.rect[1]
			r := [4]float32{b.rect[0] + x, b.rect[1] + y, b.rect[2] + x, b.rect[3] + y}
			if sc.axis == AxisFlow && sc.flow.rtl { // an RTL flow's x counts from its right edge (flow.go)
				s := float32(ox)
				r[0], r[2] = sc.rect[2]-b.rect[2]+s, sc.rect[2]-b.rect[0]+s
			}
			c.ScrollIntoView(p, r)
			break
		}
	}
}
