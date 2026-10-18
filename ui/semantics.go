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
func (c *Context) NextSemName(s string) { c.semNext.name = c.fr.Cur().AppendString(s) }

// NextSemNameBytes is NextSemName for bytes (such as a Fmt result).
func (c *Context) NextSemNameBytes(b []byte) { c.semNext.name = c.fr.Cur().AppendBytes(b) }

// NextSemValueBytes sets the accessible value (the text of a field, the
// current choice) of the next box. The text is copied.
func (c *Context) NextSemValueBytes(b []byte) { c.semNext.value = c.fr.Cur().AppendBytes(b) }

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
