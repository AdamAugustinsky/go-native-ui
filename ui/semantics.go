package ui

import "gonativeui/core"

// Semantics (phase 5, D28): the roles, states and actions an accessibility
// bridge (phase 6) needs, recorded by the boxes that have a role and exported
// as a pointer-free tree.

// Role is what a box is, for assistive technologies. The zero value marks a
// box that is not part of the semantic tree.
type Role uint8

const (
	RoleNone Role = iota
	RoleWindow
	RoleGroup
	RoleLabel
	RoleImage
	RoleButton
	RoleCheckbox
	RoleSwitch
	RoleRadio
	RoleRadioGroup
	RoleSlider
	RoleTextField
	RoleSearchField
	RoleComboBox
	RoleList
	RoleListItem
	RoleTable
	RoleRow
	RoleCell
	RoleColumnHeader
	RoleScrollArea
	RoleScrollBar
	RoleSplitter
	RoleTabList
	RoleTab
	RoleTabPanel
	RoleMenuBar
	RoleMenu
	RoleMenuItem
	RoleMenuItemCheckbox
	RoleDialog
	RoleTooltip
	RoleProgressBar
)

// SemState are the state bits of a node.
type SemState uint16

const (
	SemChecked  SemState = 1 << iota // checked, on or pressed
	SemMixed                         // neither checked nor unchecked
	SemSelected                      // part of the selection
	SemExpanded                      // open (a menu, a combo box, a tree row)
	SemDisabled                      // set by the export for boxes with the Disabled flag
	SemFocused                       // set by the export for the focused box
	SemEditable                      // the value can be edited (text fields)
	SemModal                         // a modal overlay: everything beneath it is inert
)

// SemAction are the actions a node supports (SemNode.Actions) and that an
// assistive technology can request (Input.Actions, Signal.Action).
type SemAction uint16

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
func (c *Context) NextSemName(s string) { c.semNext.name = c.semA.AppendString(s) }

// NextSemNameBytes is NextSemName for bytes (such as a Fmt result).
func (c *Context) NextSemNameBytes(b []byte) { c.semNext.name = c.semA.AppendBytes(b) }

// NextSemValueBytes sets the accessible value (the text of a field, the
// current choice) of the next box. The text is copied.
func (c *Context) NextSemValueBytes(b []byte) { c.semNext.value = c.semA.AppendBytes(b) }

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
type SemNode struct {
	Key     Key
	Role    Role
	State   SemState
	Actions SemAction
	Bounds  [4]float32 // x0, y0, x1, y1 in window pixels
	// Parent is the nearest ancestor box that has a role (an overlay's is the
	// window node), FirstChild and NextSibling follow build order.
	Parent, FirstChild, NextSibling int32
	Name, Value                     [2]uint32
	// Range is min, max, value and step of a range (sliders, scroll bars,
	// progress bars); all zero for other nodes.
	Range [4]float64
	// Pos is the node's index in its list and the list's length (virtual rows
	// report their place in the whole list); Pos[1] is 0 when unknown.
	Pos [2]int32
	// TextSel is the selection of a text field in byte offsets of Value: the
	// anchor and the caret. Both are 0 for other nodes.
	TextSel [2]int32
}

// SemTree is a snapshot of the semantic tree. Nodes are in build order, parents
// before children; Nodes[0] is the window.
type SemTree struct {
	Nodes []SemNode
	Bytes []byte // the text of the names and values
	// Dropped is the number of boxes with a role left out of the last export
	// because the tree reached MaxSemNodes.
	Dropped int
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
	dst.Nodes, dst.Bytes, dst.Dropped = dst.Nodes[:0], dst.Bytes[:0], 0
	for i := range c.sems {
		rec := &c.sems[i]
		b := &c.boxes[rec.id]
		if len(dst.Nodes) >= MaxSemNodes {
			dst.Dropped = len(c.sems) - i
			break
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
			idx := pb.listFirst
			for q := b.prev; q != 0; q = c.boxes[q].prev {
				if c.boxes[q].flags&floatingMask == 0 {
					idx++
				}
			}
			n.Pos = [2]int32{int32(idx), int32(pb.listRows)}
		}
		if rec.sel != [2]int32{} {
			n.TextSel = rec.sel
		}
		n.Name = c.semText(dst, rec.name)
		n.Value = c.semText(dst, rec.value)

		// The parent: the nearest ancestor with a role; an overlay hangs from the window.
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
		dst.Nodes = append(dst.Nodes, n)
	}
	c.semFocusBubble(dst)
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
		if v, ok := c.table.Get(uint64(r.Key)); ok {
			if b := &c.boxes[v]; b.lastFrame == c.frame-1 && b.flags&(Focusable|Disabled) == Focusable && !c.blocked(b) {
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
		if !ok || c.boxes[v].lastFrame != c.frame || c.boxes[v].flags&Disabled != 0 {
			continue
		}
		b := &c.boxes[v]
		for p := b.parent; p != 0; p = c.boxes[p].parent {
			sc := &c.boxes[p]
			if sc.flags&scrollMask == 0 {
				continue
			}
			x, y := float32(sc.scroll[0])-sc.rect[0], float32(sc.scroll[1])-sc.rect[1]
			c.ScrollIntoView(p, [4]float32{b.rect[0] + x, b.rect[1] + y, b.rect[2] + x, b.rect[3] + y})
			break
		}
	}
}
