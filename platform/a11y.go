package platform

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// The accessibility bridge (phase 6, N7; the contract is
// docs/PHASE6-PLAN.md, "Accessibility"). The app draws its interface itself,
// so an assistive technology sees nothing of it unless the window exposes the
// app's semantic tree (ui.SemTree) as the OS's accessibility objects. The app
// gives the window an AXSource (SetAXSource); the window asks it for the tree
// only once an assistive technology has asked the window about its content
// (lazy activation: until then the bridge costs nothing), and from then on at
// every frame (SyncAX), diffing the new tree with the last one to tell the
// assistive technology what changed.
//
// This file is the part every OS shares: the tree the app hands over (AXTree,
// a copy of ui.SemTree's layout, since platform is below ui), the element
// table and its diff, and the mapping of roles to the OS's (macOS's names,
// which the snapshot dump also uses). a11y_darwin.go is the NSAccessibility
// side. Windows (UI Automation) and Linux (AT-SPI) have no bridge yet:
// SetAXSource returns an error wrapping errors.ErrUnsupported there.

// ErrAXUnsupported is SetAXSource's error where the OS has no bridge yet.
var ErrAXUnsupported = fmt.Errorf("platform: accessibility on %s: %w", runtime.GOOS, errors.ErrUnsupported)

// AXRole is a node's role; the values are ui.Role's.
type AXRole uint8

// The roles, in ui.Role's order.
const (
	AXRoleNone AXRole = iota
	AXRoleWindow
	AXRoleGroup
	AXRoleLabel
	AXRoleImage
	AXRoleButton
	AXRoleCheckbox
	AXRoleSwitch
	AXRoleRadio
	AXRoleRadioGroup
	AXRoleSlider
	AXRoleTextField
	AXRoleSearchField
	AXRoleComboBox
	AXRoleList
	AXRoleListItem
	AXRoleTable
	AXRoleRow
	AXRoleCell
	AXRoleColumnHeader
	AXRoleScrollArea
	AXRoleScrollBar
	AXRoleSplitter
	AXRoleTabList
	AXRoleTab
	AXRoleTabPanel
	AXRoleMenuBar
	AXRoleMenu
	AXRoleMenuItem
	AXRoleMenuItemCheckbox
	AXRoleDialog
	AXRoleTooltip
	AXRoleProgressBar
	AXRoleLink
	AXRoleHeading
	AXRoleToolbar
	AXRoleSheet
	AXRoleHost
	NumAXRoles // the number of roles
)

// AXState are a node's state bits; the values are ui.SemState's.
type AXState uint16

// The state bits, in ui.SemState's order.
const (
	AXChecked AXState = 1 << iota
	AXMixed
	AXSelected
	AXExpanded
	AXDisabled
	AXFocused
	AXEditable
	AXModal
	AXSortAscending
	AXSortDescending
	AXLive
)

// AXAction are the actions a node supports; the values are ui.SemAction's.
type AXAction uint16

// The actions, in ui.SemAction's order.
const (
	AXPress AXAction = 1 << iota
	AXFocus
	AXIncrement
	AXDecrement
	AXExpand
	AXCollapse
	AXScrollIntoView
	AXSetValue
	AXShowMenu
)

// AXTextKind says which text of a node has character geometry (ui.SemTextKind).
type AXTextKind uint8

const (
	// AXTextNone: no text geometry.
	AXTextNone AXTextKind = iota
	// AXTextField: the Value of a text field.
	AXTextField
	// AXTextRich: the Name of a rich text paragraph.
	AXTextRich
)

// AXNode is one node of the tree (ui.SemNode). It is pointer-free; Name and
// Value are spans of AXTree.Bytes, and the tree is linked by indices, -1 for
// none. Nodes[0] is the window.
type AXNode struct {
	// Key is the node's box key: the identity of its element across frames.
	Key uint64
	// Role is the node's role.
	Role AXRole
	// State is its state bits.
	State AXState
	// Actions are the actions it supports.
	Actions AXAction
	Bounds  [4]float32 // x0, y0, x1, y1 in window pixels
	// Parent, FirstChild and NextSibling link the tree (-1: none).
	Parent, FirstChild, NextSibling int32
	Name, Value                     [2]uint32  // spans of AXTree.Bytes
	Range                           [4]float64 // min, max, value, step
	Pos                             [2]int32   // index in its list and the list's length (0: unknown)
	TextSel                         [2]int32   // anchor and caret, bytes of the text (Text)
	Level                           int32      // depth in an outline (1: a root row), a heading's level
	Host                            uint32     // the HostID of an AXRoleHost node
	Text                            AXTextKind // which text has geometry
}

// AXTree is the semantic tree the app hands to the window.
type AXTree struct {
	// Nodes are the nodes, parents before children; Nodes[0] is the window.
	Nodes []AXNode
	// Bytes hold the names and values.
	Bytes []byte
	// Announce is text to announce now (a span of Bytes), empty for none.
	Announce [2]uint32
}

// Text returns the bytes of a span of t.
func (t *AXTree) Text(s [2]uint32) []byte {
	if int(s[0])+int(s[1]) > len(t.Bytes) {
		return nil
	}
	return t.Bytes[s[0] : s[0]+s[1]]
}

// text is the node's text that has geometry: a field's value, a paragraph's
// name.
func (t *AXTree) text(n *AXNode) []byte {
	if n.Text == AXTextRich {
		return t.Text(n.Name)
	}
	return t.Text(n.Value)
}

// Equal reports whether t and u are the same tree.
func (t *AXTree) Equal(u *AXTree) bool {
	if len(t.Nodes) != len(u.Nodes) || string(t.Bytes) != string(u.Bytes) || t.Announce != u.Announce {
		return false
	}
	for i := range t.Nodes {
		if t.Nodes[i] != u.Nodes[i] {
			return false
		}
	}
	return true
}

// AXSource is the app's side of the bridge, called on the main thread
// between frames.
//
// AXExport fills dst with the semantic tree of the last finished frame,
// reusing its storage, and reports whether there is one.
//
// The text functions answer for the node with the given key and the text that
// has geometry (AXNode.Text), in bytes and window pixels: the line an offset
// is on, the bytes of a line, the rectangle a range covers, and the offset
// nearest to a point. AXHelp appends the node's help text (its tooltip).
type AXSource interface {
	AXExport(dst *AXTree) bool
	AXLine(key uint64, off int) (line int, ok bool)
	AXLineRange(key uint64, line int) (from, to int, ok bool)
	AXBounds(key uint64, from, to int) (r [4]float32, ok bool)
	AXOffset(key uint64, x, y float32) (off int, ok bool)
	AXHelp(key uint64, dst []byte) []byte
}

// An assistive technology's request for an action arrives as an Event of
// kind EvAccessibility: Tag is the node's key, Code the AXAction (one bit),
// Amount its argument (AXSetValue: the new value of a range).

// ---- the element table and the diff ----

// axNote is a notification the diff asks for, on an element.
type axNote uint16

const (
	noteCreated axNote = 1 << iota
	noteDestroyed
	noteValue
	noteTitle
	noteSelectedText
	noteSelectedRows // on a list or table whose rows' selection changed
	noteFocus
	noteLayout // the element's children changed (on the parent)
	noteExpanded
	noteCollapsed
	noteLive // a live region changed: its text is announced
)

var noteNames = [...]string{"created", "destroyed", "value", "title", "selected-text", "selected-rows",
	"focus", "layout", "expanded", "collapsed", "live"}

// axSlot is one element of the table: the node with a key, while it is in
// the tree.
type axSlot struct {
	key     uint64
	node    int32  // its index in the current tree
	gen     uint32 // bumped when the slot is freed: an element of an older generation is stale
	stamp   uint32 // the diff that last saw it
	live    bool
	pend    axNote
	parent  uint64 // the key of its parent and of its previous sibling at the last diff (0: none)
	prevSib uint64
	obj     uintptr // the OS element (macOS: a GNUIAXElement), 0 until one was handed out
}

// axEvent is one notification of a diff, in the order they are posted.
type axEvent struct {
	note axNote
	slot int32   // the element's slot (-1: the window's own element, the view)
	obj  uintptr // the element (a destroyed one: no longer in its slot)
	key  uint64
}

// axCore is the element table of a window and its diff: platform-neutral, so
// that it is tested on every OS.
type axCore struct {
	active bool
	trees  [2]AXTree
	cur    int // trees[cur] is the current tree, trees[1-cur] the previous one
	slots  []axSlot
	free   []int32
	byKey  map[uint64]int32
	stamp  uint32
	diffs  int // diffs that found a change (tests)
	events []axEvent
	dead   []axEvent // scratch: the destroyed elements of a diff
	last   []uint64  // scratch: per node, the key of its last child seen
	focus  int32     // the slot of the focused node, -1 for none
	syncs  int       // SyncAX calls that exported a tree (tests)
}

func (c *axCore) tree() *AXTree { return &c.trees[c.cur] }

// node returns slot s's node in the current tree.
func (c *axCore) node(s int32) (*AXNode, bool) {
	if s < 0 || int(s) >= len(c.slots) || !c.slots[s].live {
		return nil, false
	}
	t := c.tree()
	i := c.slots[s].node
	if i <= 0 || int(i) >= len(t.Nodes) {
		return nil, false
	}
	return &t.Nodes[i], true
}

// slotOf returns the slot of node i of the current tree, -1 for the window or
// none.
func (c *axCore) slotOf(i int32) int32 {
	t := c.tree()
	if i <= 0 || int(i) >= len(t.Nodes) {
		return -1
	}
	if s, ok := c.byKey[t.Nodes[i].Key]; ok {
		return s
	}
	return -1
}

// begin makes the bridge active with its first tree (from export) and fills
// the table, posting nothing: no element existed before.
func (c *axCore) begin(export func(*AXTree) bool) {
	c.active = true
	if c.byKey == nil {
		c.byKey = make(map[uint64]int32, 256)
		c.focus = -1
	}
	c.sync(export)
	c.events = c.events[:0]
}

// sync exports the next tree into the other buffer and diffs it with the
// current one. The events are in c.events until the next sync. With no
// change, it allocates nothing and records nothing.
func (c *axCore) sync(export func(*AXTree) bool) {
	c.events = c.events[:0]
	next := &c.trees[1-c.cur]
	if !export(next) || len(next.Nodes) == 0 {
		return
	}
	c.syncs++
	if c.stamp > 0 && next.Equal(c.tree()) {
		return
	}
	c.cur = 1 - c.cur
	c.diff(&c.trees[1-c.cur], c.tree())
}

// diff compares the trees and records the events, in order: created
// elements and the changes of the others (in tree order), the parents whose
// children changed, the destroyed elements, the focus, the announcements.
func (c *axCore) diff(prev, cur *AXTree) {
	c.diffs++
	c.stamp++
	first := c.stamp == 1
	if cap(c.last) < len(cur.Nodes) {
		c.last = make([]uint64, len(cur.Nodes), cap(cur.Nodes))
	}
	c.last = c.last[:len(cur.Nodes)]
	clear(c.last)
	rootLayout := false
	focus := int32(-1)
	for i := 1; i < len(cur.Nodes); i++ {
		n := &cur.Nodes[i]
		var pkey uint64
		if n.Parent > 0 {
			pkey = cur.Nodes[n.Parent].Key
		}
		var prevSib uint64
		if n.Parent >= 0 {
			prevSib = c.last[n.Parent]
			c.last[n.Parent] = n.Key
		}
		s, ok := c.byKey[n.Key]
		if ok && c.slots[s].stamp == c.stamp { // a key twice in a tree: the second is left out
			continue
		}
		if !ok {
			s = c.alloc(n.Key)
			sl := &c.slots[s]
			sl.node, sl.stamp, sl.parent, sl.prevSib = int32(i), c.stamp, pkey, prevSib
			if !first {
				sl.pend |= noteCreated
				rootLayout = c.markLayout(pkey) || rootLayout
			}
			if n.State&AXFocused != 0 {
				focus = s
			}
			continue
		}
		sl := &c.slots[s]
		var o *AXNode
		if j := sl.node; j > 0 && int(j) < len(prev.Nodes) && prev.Nodes[j].Key == n.Key {
			o = &prev.Nodes[j]
		}
		sl.node, sl.stamp = int32(i), c.stamp
		if sl.parent != pkey || sl.prevSib != prevSib {
			rootLayout = c.markLayout(pkey) || rootLayout
			if sl.parent != pkey {
				rootLayout = c.markLayout(sl.parent) || rootLayout
			}
			sl.parent, sl.prevSib = pkey, prevSib
		}
		if n.State&AXFocused != 0 {
			focus = s
		}
		if o == nil {
			continue
		}
		nameCh := string(cur.Text(n.Name)) != string(prev.Text(o.Name))
		valCh := string(cur.Text(n.Value)) != string(prev.Text(o.Value)) || n.Range[2] != o.Range[2] ||
			n.State&(AXChecked|AXMixed) != o.State&(AXChecked|AXMixed)
		if nameCh {
			sl.pend |= noteTitle
			if n.Role == AXRoleLabel || n.Role == AXRoleHeading {
				sl.pend |= noteValue // static text: its name is its value
			}
		}
		if valCh || n.Role != o.Role {
			sl.pend |= noteValue
		}
		if n.TextSel != o.TextSel && n.Text != AXTextNone {
			sl.pend |= noteSelectedText
		}
		if (n.State^o.State)&AXSelected != 0 && n.Parent > 0 {
			if ps, ok := c.byKey[pkey]; ok {
				c.slots[ps].pend |= noteSelectedRows
			}
		}
		if (n.State^o.State)&AXExpanded != 0 {
			if n.State&AXExpanded != 0 {
				sl.pend |= noteExpanded
			} else {
				sl.pend |= noteCollapsed
			}
		}
		if n.State&AXLive != 0 && (nameCh || valCh) {
			sl.pend |= noteLive
		}
	}
	// The elements whose nodes are gone: their parents' children changed.
	c.dead = c.dead[:0]
	for s := range c.slots {
		sl := &c.slots[s]
		if !sl.live || sl.stamp == c.stamp {
			continue
		}
		if !first {
			c.dead = append(c.dead, axEvent{note: noteDestroyed, slot: int32(s), obj: sl.obj, key: sl.key})
		}
		rootLayout = c.markLayout(sl.parent) || rootLayout
		c.release(int32(s))
	}
	// The events, in tree order.
	for i := 1; i < len(cur.Nodes); i++ {
		s, ok := c.byKey[cur.Nodes[i].Key]
		if !ok {
			continue
		}
		if p := c.slots[s].pend &^ noteLayout; p != 0 && !first {
			c.emit(p, s)
		}
	}
	if rootLayout && !first {
		c.events = append(c.events, axEvent{note: noteLayout, slot: -1})
	}
	for i := 1; i < len(cur.Nodes); i++ {
		s, ok := c.byKey[cur.Nodes[i].Key]
		if !ok {
			continue
		}
		sl := &c.slots[s]
		if sl.pend&noteLayout != 0 && !first {
			c.events = append(c.events, axEvent{note: noteLayout, slot: s, obj: sl.obj, key: sl.key})
		}
		sl.pend = 0
	}
	c.events = append(c.events, c.dead...)
	if focus != c.focus {
		c.focus = focus
		if !first {
			ev := axEvent{note: noteFocus, slot: focus}
			if focus >= 0 {
				ev.obj, ev.key = c.slots[focus].obj, c.slots[focus].key
			}
			c.events = append(c.events, ev)
		}
	}
	if !first && cur.Announce[1] > 0 {
		c.events = append(c.events, axEvent{note: noteLive, slot: -1})
	}
}

// emit records the notes p of slot s, one event each, in noteNames' order.
func (c *axCore) emit(p axNote, s int32) {
	for b := axNote(1); b != 0 && b <= p; b <<= 1 {
		if p&b != 0 {
			c.events = append(c.events, axEvent{note: b, slot: s, obj: c.slots[s].obj, key: c.slots[s].key})
		}
	}
}

// markLayout marks the element of key k as having new children; it reports
// true when k is the window (0).
func (c *axCore) markLayout(k uint64) bool {
	if k == 0 {
		return true
	}
	if s, ok := c.byKey[k]; ok {
		c.slots[s].pend |= noteLayout
	}
	return false
}

func (c *axCore) alloc(key uint64) int32 {
	var s int32
	if n := len(c.free); n > 0 {
		s, c.free = c.free[n-1], c.free[:n-1]
	} else {
		s = int32(len(c.slots))
		c.slots = append(c.slots, axSlot{})
	}
	sl := &c.slots[s]
	gen := sl.gen
	*sl = axSlot{key: key, live: true, gen: gen}
	c.byKey[key] = s
	return s
}

// release frees slot s (its element, if any, is the caller's: it was handed
// to the events).
func (c *axCore) release(s int32) {
	sl := &c.slots[s]
	delete(c.byKey, sl.key)
	gen := sl.gen + 1
	*sl = axSlot{gen: gen}
	c.free = append(c.free, s)
	if c.focus == s {
		c.focus = -1
	}
}

// ---- the tree as assistive technologies see it ----

// axChildren appends to dst the children of node i that are exposed: under
// the window, a modal overlay hides everything before it (the content beneath
// a dialog is inert).
func axChildren(t *AXTree, i int32, dst []int32) []int32 {
	if i < 0 || int(i) >= len(t.Nodes) {
		return dst
	}
	start := t.Nodes[i].FirstChild
	if i == 0 {
		for ch := start; ch >= 0; ch = t.Nodes[ch].NextSibling {
			if t.Nodes[ch].State&AXModal != 0 {
				start = ch
			}
		}
	}
	for ch := start; ch >= 0 && int(ch) < len(t.Nodes); ch = t.Nodes[ch].NextSibling {
		dst = append(dst, ch)
	}
	return dst
}

// axHit returns the deepest exposed node at window pixel (x, y), 0 for the
// window itself: the children are tried from the last (drawn on top).
func axHit(t *AXTree, x, y float32) int32 {
	var buf [64]int32
	at := int32(0)
	for {
		kids := axChildren(t, at, buf[:0])
		next := int32(-1)
		for k := len(kids) - 1; k >= 0; k-- {
			b := t.Nodes[kids[k]].Bounds
			if x >= b[0] && x < b[2] && y >= b[1] && y < b[3] {
				next = kids[k]
				break
			}
		}
		if next < 0 {
			return at
		}
		at = next
	}
}

// axExposed reports whether node i is in the exposed tree (not beneath a
// modal overlay).
func axExposed(t *AXTree, i int32) bool {
	for i > 0 {
		p := t.Nodes[i].Parent
		if p == 0 {
			var buf [32]int32
			for _, k := range axChildren(t, 0, buf[:0]) {
				if k == i {
					return true
				}
			}
			return false
		}
		if p < 0 {
			return false
		}
		i = p
	}
	return i == 0
}

// The names of the roles and subroles (macOS's attribute values; the dump
// uses them on every OS).
type axName uint8

const (
	nameNone axName = iota
	nameGroup
	nameStaticText
	nameImage
	nameButton
	nameCheckBox
	nameRadioButton
	nameRadioGroup
	nameSlider
	nameTextField
	namePopUpButton
	nameTable
	nameOutline
	nameRow
	nameCell
	nameScrollArea
	nameScrollBar
	nameSplitter
	nameTabGroup
	nameMenuBar
	nameMenuBarItem
	nameMenu
	nameMenuItem
	nameHelpTag
	nameProgressIndicator
	nameLink
	nameHeading
	nameToolbar
	nameSheet
	// subroles
	nameSwitch
	nameSearchField
	nameTableRow
	nameOutlineRow
	nameSortButton
	nameTabButton
	nameApplicationDialog
	numAXNames
)

var axNames = [numAXNames]string{
	"", "AXGroup", "AXStaticText", "AXImage", "AXButton", "AXCheckBox", "AXRadioButton", "AXRadioGroup",
	"AXSlider", "AXTextField", "AXPopUpButton", "AXTable", "AXOutline", "AXRow", "AXCell", "AXScrollArea",
	"AXScrollBar", "AXSplitter", "AXTabGroup", "AXMenuBar", "AXMenuBarItem", "AXMenu", "AXMenuItem",
	"AXHelpTag", "AXProgressIndicator", "AXLink", "AXHeading", "AXToolbar", "AXSheet",
	"AXSwitch", "AXSearchField", "AXTableRow", "AXOutlineRow", "AXSortButton", "AXTabButton", "AXApplicationDialog",
}

// axRoleOf maps node i to its role and subrole.
func axRoleOf(t *AXTree, i int32) (role, sub axName) {
	n := &t.Nodes[i]
	switch n.Role {
	case AXRoleGroup, AXRoleTabPanel, AXRoleWindow:
		return nameGroup, 0
	case AXRoleLabel:
		return nameStaticText, 0
	case AXRoleImage:
		return nameImage, 0
	case AXRoleButton:
		return nameButton, 0
	case AXRoleCheckbox:
		return nameCheckBox, 0
	case AXRoleSwitch:
		return nameCheckBox, nameSwitch
	case AXRoleRadio:
		return nameRadioButton, 0
	case AXRoleRadioGroup:
		return nameRadioGroup, 0
	case AXRoleSlider:
		return nameSlider, 0
	case AXRoleTextField:
		return nameTextField, 0
	case AXRoleSearchField:
		return nameTextField, nameSearchField
	case AXRoleComboBox:
		return namePopUpButton, 0
	case AXRoleList, AXRoleTable:
		if axOutline(t, i) {
			return nameOutline, 0
		}
		return nameTable, 0
	case AXRoleListItem, AXRoleRow:
		if n.Level > 0 {
			return nameRow, nameOutlineRow
		}
		return nameRow, nameTableRow
	case AXRoleCell:
		return nameCell, 0
	case AXRoleColumnHeader:
		return nameButton, nameSortButton
	case AXRoleScrollArea:
		return nameScrollArea, 0
	case AXRoleScrollBar:
		return nameScrollBar, 0
	case AXRoleSplitter:
		return nameSplitter, 0
	case AXRoleTabList:
		return nameTabGroup, 0
	case AXRoleTab:
		return nameRadioButton, nameTabButton
	case AXRoleMenuBar:
		return nameMenuBar, 0
	case AXRoleMenu:
		return nameMenu, 0
	case AXRoleMenuItem, AXRoleMenuItemCheckbox:
		if p := n.Parent; p >= 0 && t.Nodes[p].Role == AXRoleMenuBar {
			return nameMenuBarItem, 0
		}
		return nameMenuItem, 0
	case AXRoleDialog:
		return nameGroup, nameApplicationDialog
	case AXRoleTooltip:
		return nameHelpTag, 0
	case AXRoleProgressBar:
		return nameProgressIndicator, 0
	case AXRoleLink:
		return nameLink, 0
	case AXRoleHeading:
		return nameHeading, 0
	case AXRoleToolbar:
		return nameToolbar, 0
	case AXRoleSheet:
		return nameSheet, 0
	}
	return nameGroup, 0
}

// axOutline reports whether the list or table node i is an outline: a row
// among its children, or its scroll area's, has a level.
func axOutline(t *AXTree, i int32) bool {
	for ch := t.Nodes[i].FirstChild; ch >= 0; ch = t.Nodes[ch].NextSibling {
		n := &t.Nodes[ch]
		if (n.Role == AXRoleRow || n.Role == AXRoleListItem) && n.Level > 0 {
			return true
		}
		if n.Role == AXRoleScrollArea || n.Role == AXRoleGroup {
			for g := n.FirstChild; g >= 0; g = t.Nodes[g].NextSibling {
				if m := &t.Nodes[g]; (m.Role == AXRoleRow || m.Role == AXRoleListItem) && m.Level > 0 {
					return true
				}
			}
		}
	}
	return false
}

// axValueKind is what kind of value a node has.
type axValueKind uint8

const (
	valueNone   axValueKind = iota
	valueString             // a text: the node's Value (Name for static text)
	valueToggle             // a number: 0, 1, or 2 for mixed
	valueRange              // a number: Range[2]
	valueScroll             // a number from 0 to 1: Range[2] within Range[0:2]
)

// axValueOf is the kind of value of node n.
func axValueOf(n *AXNode) axValueKind {
	switch n.Role {
	case AXRoleCheckbox, AXRoleSwitch, AXRoleRadio, AXRoleTab, AXRoleMenuItemCheckbox:
		return valueToggle
	case AXRoleSlider, AXRoleProgressBar:
		return valueRange
	case AXRoleScrollBar:
		return valueScroll
	case AXRoleLabel, AXRoleHeading:
		return valueString
	}
	if n.Value[1] > 0 || n.Text == AXTextField {
		return valueString
	}
	return valueNone
}

// axString is the string value of node n: a static text's name, others'
// value.
func axString(t *AXTree, n *AXNode) []byte {
	if n.Role == AXRoleLabel || n.Role == AXRoleHeading && n.Value[1] == 0 {
		return t.Text(n.Name)
	}
	return t.Text(n.Value)
}

// axLabel is node n's label (AXDescription): its name, except for static
// text, whose name is its value. An unnamed control that reads out by its
// content (a row, a button around a label) takes the text of its first
// descendant that has a name, as VoiceOver would read it.
func axLabel(t *AXTree, n *AXNode) []byte {
	if n.Role == AXRoleLabel || n.Role == AXRoleHeading && n.Value[1] == 0 {
		return nil
	}
	if n.Name[1] > 0 || !axLabelledByContent(n.Role) {
		return t.Text(n.Name)
	}
	return axContentName(t, n.FirstChild, 3)
}

// axLabelledByContent reports whether an unnamed node of role r is labelled
// by its content.
func axLabelledByContent(r AXRole) bool {
	switch r {
	case AXRoleButton, AXRoleCheckbox, AXRoleSwitch, AXRoleRadio, AXRoleTab, AXRoleMenuItem,
		AXRoleMenuItemCheckbox, AXRoleLink, AXRoleListItem, AXRoleRow, AXRoleCell, AXRoleColumnHeader:
		return true
	}
	return false
}

// axContentName is the name of the first node, depth first from sibling
// chain ch down to depth levels, that has one.
func axContentName(t *AXTree, ch int32, depth int) []byte {
	for ; ch >= 0 && int(ch) < len(t.Nodes); ch = t.Nodes[ch].NextSibling {
		n := &t.Nodes[ch]
		if n.Name[1] > 0 && n.Role != AXRoleHost {
			return t.Text(n.Name)
		}
		if depth > 1 {
			if b := axContentName(t, n.FirstChild, depth-1); len(b) > 0 {
				return b
			}
		}
	}
	return nil
}

// AXLabel is the label the bridge gives node i of t (see AXDump), empty when
// it has none: an assistive technology then reads the element by its role
// alone. Static text has none (its text is its value).
func AXLabel(t *AXTree, i int) []byte {
	if i <= 0 || i >= len(t.Nodes) {
		return nil
	}
	return axLabel(t, &t.Nodes[i])
}

// axNumber is node n's numeric value (valueToggle, valueRange, valueScroll).
func axNumber(n *AXNode) float64 {
	switch axValueOf(n) {
	case valueToggle:
		switch {
		case n.State&AXMixed != 0:
			return 2
		case n.State&(AXChecked|AXSelected) != 0:
			return 1
		}
		return 0
	case valueScroll:
		if d := n.Range[1] - n.Range[0]; d > 0 {
			return min(max((n.Range[2]-n.Range[0])/d, 0), 1)
		}
		return 0
	}
	return n.Range[2]
}

// axActionNames are the actions an element offers, as macOS names them.
var axActionNames = [...]struct {
	act  AXAction
	name string
}{
	{AXPress, "AXPress"}, {AXIncrement, "AXIncrement"}, {AXDecrement, "AXDecrement"},
	{AXShowMenu, "AXShowMenu"}, {AXScrollIntoView, "AXScrollToVisible"},
}

// AXDump writes the tree t as an assistive technology would see it through
// the bridge, one element a line: the role and subrole, the label, the value,
// the states and the actions, with the content beneath a modal overlay left
// out and hosted views as "Host #id". Bounds are left out (they depend on
// the fonts). It is for snapshot tests (widgettest.DumpAX).
func AXDump(t *AXTree) string {
	var b strings.Builder
	if len(t.Nodes) == 0 {
		return ""
	}
	b.WriteString("AXWindow\n")
	var walk func(i int32, depth int)
	walk = func(i int32, depth int) {
		for _, ch := range axChildren(t, i, nil) {
			n := &t.Nodes[ch]
			b.WriteString(strings.Repeat("  ", depth))
			if n.Role == AXRoleHost {
				fmt.Fprintf(&b, "Host #%d\n", n.Host)
				continue
			}
			r, s := axRoleOf(t, ch)
			b.WriteString(axNames[r])
			if s != 0 {
				b.WriteString("/" + axNames[s])
			}
			if l := axLabel(t, n); len(l) > 0 {
				fmt.Fprintf(&b, " %q", l)
			}
			switch axValueOf(n) {
			case valueString:
				if v := axString(t, n); len(v) > 0 {
					fmt.Fprintf(&b, " value=%q", v)
				}
			case valueToggle, valueRange:
				b.WriteString(" value=" + strconv.FormatFloat(axNumber(n), 'g', 4, 64))
			}
			if n.Text != AXTextNone {
				txt := t.text(n)
				lo, hi := min(n.TextSel[0], n.TextSel[1]), max(n.TextSel[0], n.TextSel[1])
				if lo >= 0 && int(hi) <= len(txt) {
					fmt.Fprintf(&b, " sel=%d+%d", utf16Count(txt[:lo]), utf16Count(txt[lo:hi]))
				}
			}
			var st []string
			if n.State&AXDisabled != 0 {
				st = append(st, "disabled")
			}
			if n.State&AXSelected != 0 {
				st = append(st, "selected")
			}
			if n.State&AXExpanded != 0 {
				st = append(st, "expanded")
			}
			if n.State&AXFocused != 0 {
				st = append(st, "focused")
			}
			if n.State&AXLive != 0 {
				st = append(st, "live")
			}
			if n.Level > 0 {
				st = append(st, "level="+strconv.Itoa(int(n.Level)))
			}
			if n.Pos[1] > 0 {
				st = append(st, fmt.Sprintf("index=%d/%d", n.Pos[0], n.Pos[1]))
			}
			if len(st) > 0 {
				b.WriteString(" [" + strings.Join(st, " ") + "]")
			}
			var acts []string
			for _, a := range axActionNames {
				if n.Actions&a.act != 0 {
					acts = append(acts, a.name[2:])
				}
			}
			if n.Actions&(AXExpand|AXCollapse) != 0 {
				acts = append(acts, "Disclose")
			}
			if len(acts) > 0 {
				b.WriteString(" {" + strings.Join(acts, ",") + "}")
			}
			b.WriteByte('\n')
			walk(ch, depth+1)
		}
	}
	walk(0, 1)
	return b.String()
}

// utf16Count is the length of UTF-8 text b in UTF-16 units.
func utf16Count(b []byte) int { return int(utf16Len(b)) }

// byteOfUnit is the byte offset of UTF-16 offset u in UTF-8 text b.
func byteOfUnit(b []byte, u int) int {
	if u <= 0 {
		return 0
	}
	return u16ToByte(b, uintptr(u))
}
