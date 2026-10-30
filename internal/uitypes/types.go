// Package uitypes holds the plain records shared by ui and the platform bridge.
package uitypes

import (
	"context"
	"time"
)

// Key identifies a box across frames.
type Key = uint64

type Cursor uint8

const (
	CursorArrow      Cursor = iota // the default arrow: a box with it marks no region
	CursorPointer                  // pointing hand (links, tabs, buttons)
	CursorText                     // I-beam
	CursorResizeEW                 // horizontal resize (left-right arrows)
	CursorResizeNS                 // vertical resize (up-down arrows)
	CursorResizeNWSE               // diagonal resize, top-left to bottom-right
	CursorResizeNESW               // diagonal resize, top-right to bottom-left
	CursorGrab                     // open hand: something can be dragged (Windows: the pointing hand)
	CursorGrabbing                 // closed hand: it is being dragged (Windows: the four-way move arrows)
)

// CursorRegion is a screen rectangle (x0, y0, x1, y1 in pixels, origin top
// left) in which the pointer shows Cursor.
type CursorRegion struct {
	// Rect is x0, y0, x1, y1 in pixels.
	Rect [4]float32
	// Cursor is the shape shown inside Rect.
	Cursor Cursor
}

// DragRegion is a rectangle of the window's drag map. Rects with Block false
// are WindowDrag areas; Block true is a box that takes input and sits above
// them. The topmost (last) rectangle containing a point decides: a point is
// in the titlebar drag area when that rectangle is not a Block.
type DragRegion struct {
	// Rect is x0, y0, x1, y1 in pixels.
	Rect [4]float32
	// Block true makes the rectangle keep its presses; false makes it a window-drag area.
	Block bool
}

type TooltipRegion struct {
	// Rect is x0, y0, x1, y1 in pixels.
	Rect [4]float32
	// Key identifies the box; TooltipText returns its text.
	Key uint64
}

type Role uint8

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
type SemState uint16

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

type SemNode struct {
	// Key is the key of the box.
	Key Key
	// Role is the box's role.
	Role Role
	// State is the node's state bits.
	State SemState
	// Actions are the actions the node supports.
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
	// Level is the node's depth in a tree (NextSemLevel): 1 for a root row
	// of an outline, 2 for its children, and so on; 0 for a node not in a
	// tree. Whether the row shows its children is SemExpanded.
	Level int32
	// Host is the platform.HostID of the native view hosted in the node's
	// box: a RoleHost node, or a host box given a role of its own (the native
	// view is then inside the node); 0 for others.
	Host uint32
	// Text says which text of the node has character geometry (lines,
	// ranges, the selection), for the text functions of assistive
	// technologies: SemTextField (the Value of a text field) or SemTextRich
	// (the Name of a RichText box; TextSel is then its selection in a
	// selection scope, see Context.RichSelection).
	Text SemTextKind
}

type SemTextKind uint8

const (
	// SemTextNone: the node's text has no geometry to ask about.
	SemTextNone SemTextKind = iota
	// SemTextField: the Value of a text field (one line).
	SemTextField
	// SemTextRich: the Name of a RichText box (its paragraph).
	SemTextRich
)

type SystemColor uint8

// The system colours, each the NSColor of the same name in the comment.
const (
	ColorWindowBackground                      SystemColor = iota // windowBackgroundColor
	ColorUnderPageBackground                                      // underPageBackgroundColor
	ColorControlBackground                                        // controlBackgroundColor
	ColorTextBackground                                           // textBackgroundColor
	ColorLabel                                                    // labelColor
	ColorSecondaryLabel                                           // secondaryLabelColor
	ColorTertiaryLabel                                            // tertiaryLabelColor
	ColorQuaternaryLabel                                          // quaternaryLabelColor
	ColorQuinaryLabel                                             // quinaryLabelColor
	ColorText                                                     // textColor
	ColorPlaceholderText                                          // placeholderTextColor
	ColorDisabledControlText                                      // disabledControlTextColor
	ColorHeaderText                                               // headerTextColor
	ColorSystemFill                                               // systemFillColor
	ColorSecondarySystemFill                                      // secondarySystemFillColor
	ColorTertiarySystemFill                                       // tertiarySystemFillColor
	ColorQuaternarySystemFill                                     // quaternarySystemFillColor
	ColorQuinarySystemFill                                        // quinarySystemFillColor
	ColorSeparator                                                // separatorColor
	ColorGrid                                                     // gridColor
	ColorControlAccent                                            // controlAccentColor
	ColorSelectedContentBackground                                // selectedContentBackgroundColor
	ColorUnemphasizedSelectedContentBackground                    // unemphasizedSelectedContentBackgroundColor
	ColorAlternateSelectedControlText                             // alternateSelectedControlTextColor
	ColorSelectedTextBackground                                   // selectedTextBackgroundColor (the user's highlight colour)
	ColorUnemphasizedSelectedTextBackground                       // unemphasizedSelectedTextBackgroundColor
	ColorSelectedText                                             // selectedTextColor
	ColorKeyboardFocusIndicator                                   // keyboardFocusIndicatorColor
	ColorAlternatingContentBackground                             // alternatingContentBackgroundColors[1]
	ColorLink                                                     // linkColor
	ColorTextInsertionPoint                                       // textInsertionPointColor
	ColorFindHighlight                                            // findHighlightColor
	ColorSystemRed                                                // systemRedColor
	ColorSystemOrange                                             // systemOrangeColor
	ColorSystemYellow                                             // systemYellowColor
	ColorSystemGreen                                              // systemGreenColor
	ColorSystemMint                                               // systemMintColor
	ColorSystemTeal                                               // systemTealColor
	ColorSystemCyan                                               // systemCyanColor
	ColorSystemBlue                                               // systemBlueColor
	ColorSystemIndigo                                             // systemIndigoColor
	ColorSystemPurple                                             // systemPurpleColor
	ColorSystemPink                                               // systemPinkColor
	ColorSystemBrown                                              // systemBrownColor
	ColorSystemGray                                               // systemGrayColor
	ColorControlText                                              // controlTextColor
	// NumSystemColors is the number of system colours.
	NumSystemColors
)

type SystemValues struct {
	// Known is false when nothing was read: not macOS, or the app does not
	// follow the system appearance (yet). The other fields are then zero.
	Known bool
	// Colors are the system colours in the light (0) and the dark (1)
	// appearance, sRGB 0xRRGGBBAA with the colour's own alpha. With
	// IncreaseContrast they are the high-contrast appearances' colours. A
	// colour the system does not have (an older macOS) is 0.
	Colors [2][NumSystemColors]uint32
	// ReduceMotion, ReduceTransparency and IncreaseContrast are the
	// accessibility display settings of the same names.
	ReduceMotion, ReduceTransparency, IncreaseContrast bool
	// LegacyScrollers is true when the preferred scroller style is legacy
	// (always-visible scroll bars that take space) rather than overlay.
	LegacyScrollers bool
}

type DragKinds uint8

const (
	// DragFiles are file URLs (local files and folders).
	DragFiles DragKinds = 1 << iota
	// DragImage is image data.
	DragImage
	// DragText is plain text.
	DragText

	// DragAny is every kind.
	DragAny = DragFiles | DragImage | DragText
)

// DragOp is a drag operation, or a set of them where a set is meant (the
// operations a source allows, the ones a target can do). The zero value is
// no operation.
type DragOp uint8

const (
	// DragOpNone is no operation: the drop is refused.
	DragOpNone DragOp = 0
	// DragOpCopy copies the dragged items.
	DragOpCopy DragOp = 1
	// DragOpLink makes a link or alias to them.
	DragOpLink DragOp = 2
	// DragOpMove moves them.
	DragOpMove DragOp = 4

	// DragOpAll is every operation.
	DragOpAll = DragOpCopy | DragOpLink | DragOpMove
)

// Pick returns the operation to answer when a target can do the operations of
// can and the source allows allowed: the first of copy, link and move in both,
// DragOpNone when there is none. A target that names none can copy.
func Pick(can, allowed DragOp) DragOp {
	if can == DragOpNone {
		can = DragOpCopy
	}
	both := can & allowed
	switch {
	case both&DragOpCopy != 0:
		return DragOpCopy
	case both&DragOpLink != 0:
		return DragOpLink
	case both&DragOpMove != 0:
		return DragOpMove
	}
	return DragOpNone
}

type FilePromise struct {
	// Name is the file name (with its extension) the destination creates.
	Name string
	// Type is the uniform type identifier of the file ("public.png",
	// "com.adobe.pdf"); empty is "public.data".
	Type string
	// Write creates the file at dest (a path in the destination folder;
	// write it fully, then return). It runs on a worker goroutine of its own.
	// ctx is cancelled when the window closes or after Timeout; a writer that
	// outlives it is answered for already, and its result is ignored. A nil
	// error completes the promise, any other error fails it.
	Write func(ctx context.Context, dest string) error
	// Timeout bounds Write; zero means DefaultPromiseTimeout.
	Timeout time.Duration
}

// MaxPaste bounds retained clipboard and drop data, in bytes.
const MaxPaste = 16 << 20
