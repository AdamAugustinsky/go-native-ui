package ui

// Native menus (macOS, DECISIONS D29): when the app runs with them
// (SetNativeMenus), the menu bar is the system's and the widgets that open a
// menu ask the app for a native one (RequestMenu) instead of building a
// popup overlay. The app shows it after the frame; a chosen command arrives
// as a command (Input.Commands), a chosen option of a list as the owner's
// MenuChoice in a later frame.

// MenuKind is what a MenuRequest opens.
type MenuKind uint8

const (
	// MenuContext is menu Menu of the app's menu model at the point Rect[0], Rect[1].
	MenuContext MenuKind = iota
	// MenuPullDown is menu Menu of the model below the button at Rect.
	MenuPullDown
	// MenuList is the Options of a select over its box at Rect, with Selected
	// over it.
	MenuList
	// MenuItems is a menu described by the request itself (Items), popped up
	// at the point Rect[0], Rect[1] (Context.ContextMenu).
	MenuItems
)

// MenuRequest asks the app for a native menu (RequestMenu).
type MenuRequest struct {
	// Kind is what to open.
	Kind MenuKind
	// Owner is the key of the box that asked; a list choice comes back to it.
	Owner Key
	// Menu is the index of the model's menu (MenuContext, MenuPullDown).
	Menu int
	// Options are the entries of a MenuList and Selected the chosen one (-1
	// none). RequestMenu copies the slice (not the strings).
	Options  []string
	Selected int
	// Rect is the anchor in window pixels (x0, y0, x1, y1); a point for MenuContext.
	Rect [4]float32
	// Items are the entries of a MenuItems menu. They are read when the app
	// shows the menu, right after the frame, and not kept.
	Items []MenuItem
	// Rev identifies the Options (RequestMenu sets it): the app gives it back
	// with the choice (Input.SetMenuChoice), so that a choice made among
	// options that have changed since is dropped.
	Rev uint64
}

// optionsRev identifies a list of options: a hash of their texts and order.
func optionsRev(opts []string) uint64 {
	h := uint64(14695981039346656037) // FNV-1a
	for _, o := range opts {
		for i := 0; i < len(o); i++ {
			h = (h ^ uint64(o[i])) * 1099511628211
		}
		h = (h ^ uint64(len(o)) ^ 0xFF) * 1099511628211 // the end of an option
	}
	return h
}

// SetNativeMenus says whether the app shows native menus (package app sets
// it on macOS). Widgets then build no menu bar and ask for their menus with
// RequestMenu.
func (c *Context) SetNativeMenus(on bool) { c.nativeMenus = on }

// NativeMenus reports whether the app shows native menus (SetNativeMenus).
func (c *Context) NativeMenus() bool { return c.nativeMenus }

// RequestMenu asks the app to open a native menu after this frame. A later
// request in the same frame replaces it.
func (c *Context) RequestMenu(r MenuRequest) {
	// The slice is copied, so that a widget's options need not outlive the
	// frame (nor escape to the heap in every frame that might request).
	// (Escape analysis does not tell a struct's fields apart: r itself is
	// not stored.)
	c.menuOpts = append(c.menuOpts[:0], r.Options...)
	c.menuReq = MenuRequest{Kind: r.Kind, Owner: r.Owner, Menu: r.Menu, Options: c.menuOpts, Selected: r.Selected, Rect: r.Rect,
		Rev: optionsRev(c.menuOpts)}
	c.menuReqSet = true
}

// TakeMenuRequest returns the pending request of RequestMenu and clears it;
// ok is false when there is none. The app calls it after the frame.
func (c *Context) TakeMenuRequest() (r MenuRequest, ok bool) {
	if !c.menuReqSet {
		return MenuRequest{}, false
	}
	r, c.menuReq, c.menuReqSet = c.menuReq, MenuRequest{}, false
	return r, true
}

// MenuChoice reports the option the user chose in the native list menu that
// box id requested (its key is the request's Owner), in the frame the choice
// arrives in (Input.SetMenuChoice); options are the box's options now. The
// menu is tracked while frames go on, so the choice is dropped when it no
// longer applies: when the box takes no input now (disabled, or beneath a
// modal overlay, as for Signal), and when options are not the ones the menu
// showed.
func (c *Context) MenuChoice(id ID, options []string) (index int, ok bool) {
	if id == 0 || !c.in.HasMenuChoice {
		return 0, false
	}
	b := &c.boxes[id]
	if c.in.MenuOwner != b.key || b.flags&Disabled != 0 || c.blocked(b) || c.wasHidden(b) {
		return 0, false
	}
	i := int(c.in.MenuIndex)
	if i < 0 || i >= len(options) || optionsRev(options) != c.in.MenuRev {
		return 0, false
	}
	return i, true
}

// SetMenuChoice delivers the option chosen in a native list menu to its
// owner (Context.MenuChoice), for this frame: index among the options of the
// request whose Rev is rev.
func (in *Input) SetMenuChoice(owner Key, index int, rev uint64) {
	in.MenuOwner, in.MenuIndex, in.MenuRev, in.HasMenuChoice = owner, int32(index), rev, true
}

// MenuMark is the mark in front of a menu item's title.
type MenuMark uint8

const (
	// MarkNone shows no mark.
	MarkNone MenuMark = iota
	// MarkOn shows a check mark.
	MarkOn
	// MarkMixed shows a dash: some, not all, of what the item stands for.
	MarkMixed
)

// MenuItem is one entry of a menu the app describes when it opens it
// (Context.ContextMenu): the content of a right-click menu that depends on
// what was clicked.
type MenuItem struct {
	// ID is the app's name for the item: what ContextMenuResult returns when
	// the user chooses it. It can be any number, 0 included; the app keeps
	// the ones in a menu apart.
	ID int
	// Title is the item's text (for a submenu, the text of the entry).
	Title string
	// Symbol is the SF Symbol shown before the title ("trash"; "" none).
	// Menus drawn by the app do not show it.
	Symbol string
	// Key and Mods show a key equivalent after the title (Key 'F' with
	// ModSuper|ModShift: ⇧⌘F). It is shown, not matched: choosing by key
	// stays with the app's own shortcuts, so it is a hint of one.
	Key  KeyCode
	Mods Mods
	// Mark is the check mark.
	Mark MenuMark
	// Disabled greys the item and keeps it from being chosen.
	Disabled bool
	// Destructive draws the title red (Delete, Block …).
	Destructive bool
	// Separator makes the entry a line between groups; the other fields are
	// ignored.
	Separator bool
	// Sub makes the entry a submenu with these items (ID is not delivered for
	// the submenu itself).
	Sub []MenuItem
}

// MenuSeparator is the entry for a line between groups.
var MenuSeparator = MenuItem{Separator: true}

// MenuOutcome is what happened to a menu popped up by ContextMenu.
type MenuOutcome uint8

const (
	// MenuPending: nothing came back (the zero value: no result this frame).
	MenuPending MenuOutcome = iota
	// MenuChosen: the user chose an item.
	MenuChosen
	// MenuDismissed: the menu closed with no choice (Escape, a click
	// elsewhere).
	MenuDismissed
)

// ContextMenu asks the app to pop up, after this frame, a native menu made of
// items at the window position (x, y) in pixels, usually where a row was
// right-clicked. id is the box it is for: the outcome comes back in a later
// frame through ContextMenuResult(id). It reports whether a menu will open:
// false when the app has no native menus (Windows, Linux, offscreen), where
// nothing opens and no outcome comes back; the app then draws its own menu,
// or none.
//
// The items are read once, when the menu opens, so build them only for the
// frame that asks (on the right-click, not every frame; rexui.OnContextMenu
// does that) and leave them unchanged until the frame ends. A later request
// in the same frame replaces this one.
func (c *Context) ContextMenu(id ID, x, y float32, items []MenuItem) bool {
	if id == 0 || !c.nativeMenus {
		return false
	}
	c.menuReq = MenuRequest{Kind: MenuItems, Owner: c.boxes[id].key, Items: items, Rect: [4]float32{x, y, x, y}}
	c.menuReqSet = true
	return true
}

// ContextMenuResult reports how the menu that box id popped up with
// ContextMenu ended, in the frame the outcome arrives in (Input.SetMenuResult):
// MenuChosen with the item's ID, or MenuDismissed (item 0), else MenuPending.
// The menu is tracked while frames go on, so the outcome is dropped when box
// id no longer takes input (disabled, or beneath a modal overlay).
func (c *Context) ContextMenuResult(id ID) (item int, out MenuOutcome) {
	if id == 0 || c.in.MenuOutcome == MenuPending {
		return 0, MenuPending
	}
	b := &c.boxes[id]
	if c.in.MenuOwner != b.key || b.flags&Disabled != 0 || c.blocked(b) || c.wasHidden(b) {
		return 0, MenuPending
	}
	return int(c.in.MenuItemID), c.in.MenuOutcome
}

// SetMenuResult delivers the outcome of a menu popped up by ContextMenu to
// its owner (Context.ContextMenuResult), for this frame: the chosen item's ID
// with MenuChosen, or MenuDismissed.
func (in *Input) SetMenuResult(owner Key, item int, out MenuOutcome) {
	in.MenuOwner, in.MenuItemID, in.MenuOutcome = owner, int64(item), out
}
