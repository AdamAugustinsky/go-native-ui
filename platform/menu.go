package platform

// Native menus (macOS; DECISIONS D29, D41): the application's menu bar, and
// context, pull-down and list menus popped up over the window. The app
// describes them in a MenuSpec; Window.SetMenuBar installs the bar and
// Window.PopUpMenu shows one menu at a point. Elsewhere there are none:
// HasNativeMenus is false, SetMenuBar does nothing and PopUpMenu returns at
// once, and the app draws its menus itself.
//
// An item the user chooses arrives as EvCommand with the item's ID, from the
// bar, from a key equivalent and from a popped-up menu alike. The items of
// the standard edit actions (MenuUndo … MenuSelectAll) go through the
// responder chain (undo:, copy: …) to the window's view, which delivers them
// the same way, so that a native view with the keyboard focus would answer
// them itself; an edit item without an ID arrives with FlagStandard and the
// MenuAction in Tag. The other standard actions are the system's (Hide,
// Minimize, the About panel …) and arrive as nothing, or as the event the
// system's action causes: Close and Quit as EvClose.
//
// AppKit handles the key equivalents (performKeyEquivalent:): a key-down
// that chose an item is not queued as EvKey, so a shortcut fires exactly
// once. A key whose item is disabled is the window's as usual.
//
// Whether an item is enabled and checked is asked when a menu opens and
// before a key equivalent acts (validateMenuItem:), through the MenuState
// function given to SetMenuBar, on the main thread; it must not allocate.

// MenuSpec describes native menus: Menus are all of them, Bar the indices of
// those in the menu bar, in order. A menu is shown in the bar, as the submenu
// of an item, or popped up by itself (PopUpMenu). The first bar menu with
// MenuRoleApp is the application menu, whose title the system replaces with
// the application's name.
type MenuSpec struct {
	// Menus are the menus; items name submenus by index into it.
	Menus []Menu
	// Bar lists the menus of the menu bar, in order.
	Bar []int
}

// Menu is one native menu.
type Menu struct {
	// Title is the menu's title: its name in the bar, or the title of a submenu.
	Title string
	// Items are its entries, in order.
	Items []MenuItem
	// Role gives the menu its system place and contents.
	Role MenuRole
}

// MenuRole is the system place of a menu.
type MenuRole uint8

const (
	// MenuRolePlain is an ordinary menu.
	MenuRolePlain MenuRole = iota
	// MenuRoleApp is the application menu, the first of the bar.
	MenuRoleApp
	// MenuRoleServices is the Services menu: the system fills it.
	MenuRoleServices
	// MenuRoleWindow is the Window menu: the system lists the windows in it.
	MenuRoleWindow
	// MenuRoleHelp is the Help menu: the system adds its search field.
	MenuRoleHelp
)

// MenuItemKind selects what a MenuItem is.
type MenuItemKind uint8

const (
	// MenuItemAction is an item the user chooses: it delivers its ID, or does
	// its standard Action.
	MenuItemAction MenuItemKind = iota
	// MenuItemSeparator is a line between groups.
	MenuItemSeparator
	// MenuItemSubmenu opens the menu at index Submenu.
	MenuItemSubmenu
)

// MenuItem is one entry of a native menu.
type MenuItem struct {
	// Kind selects which of the other fields apply.
	Kind MenuItemKind
	// ID is the command an action item delivers (EvCommand's Tag) and that
	// MenuState is asked about; 0 for an item that only does its Action.
	ID uint16
	// Action is the item's standard action, MenuNone for an app command.
	Action MenuAction
	// Title is the item's text; empty gives a standard action its standard
	// title, and a submenu item the title of its menu.
	Title string
	// Key is the key equivalent, as the character it types without modifiers
	// ('s', ',', MenuKeyBackspace …; 0: none), and Mods the modifiers it needs.
	// AppKit localises it to the keyboard layout.
	Key  rune
	Mods Mods
	// Symbol is the SF Symbol shown before the title ("" none).
	Symbol string
	// Submenu is the index of the menu a MenuItemSubmenu opens.
	Submenu int
	// Checked shows the item checked when it is built (list popups); MenuState
	// decides it for items with an ID.
	Checked bool
}

// MenuAction is the standard action of a menu item.
type MenuAction uint8

const (
	// MenuNone is no standard action: the item delivers its ID.
	MenuNone MenuAction = iota
	// MenuAbout shows the standard About panel.
	MenuAbout
	// MenuHide hides the application; MenuHideOthers the other ones;
	// MenuShowAll shows them all again.
	MenuHide
	MenuHideOthers
	MenuShowAll
	// MenuQuit asks to quit: EvClose, as the window's close button does.
	MenuQuit
	// MenuUndo … MenuSelectAll are the edit actions, sent through the
	// responder chain (undo:, redo:, cut:, copy:, paste:, delete:, selectAll:)
	// to the view with the keyboard focus.
	MenuUndo
	MenuRedo
	MenuCut
	MenuCopy
	MenuPaste
	MenuDelete
	MenuSelectAll
	// MenuMinimize, MenuZoom and MenuClose act on the key window
	// (performMiniaturize:, performZoom:, performClose:); MenuBringAllToFront
	// orders every window of the application front (arrangeInFront:).
	MenuMinimize
	MenuZoom
	MenuClose
	MenuBringAllToFront
)

// IsEdit reports whether a is one of the edit actions, MenuUndo to
// MenuSelectAll.
func (a MenuAction) IsEdit() bool { return a >= MenuUndo && a <= MenuSelectAll }

// Key equivalents that are not printable characters (MenuItem.Key).
const (
	MenuKeyEnter     rune = '\r'
	MenuKeyTab       rune = '\t'
	MenuKeyEscape    rune = 0x1B
	MenuKeyBackspace rune = 0x08
	MenuKeyDelete    rune = 0xF728 // forward delete
	MenuKeyUp        rune = 0xF700
	MenuKeyDown      rune = 0xF701
	MenuKeyLeft      rune = 0xF702
	MenuKeyRight     rune = 0xF703
	MenuKeyF1        rune = 0xF704 // F2 … F12 follow
	MenuKeyHome      rune = 0xF729
	MenuKeyEnd       rune = 0xF72B
	MenuKeyPageUp    rune = 0xF72C
	MenuKeyPageDown  rune = 0xF72D
)

// MenuState says whether the item with command id (and standard action act)
// is enabled and checked now. id is 0 for an edit item of the platform's own
// (no app command). It runs on the main thread and must not allocate.
type MenuState func(id uint16, act MenuAction) (enabled, checked bool)

// PopupKind is how PopUpMenu places a menu.
type PopupKind uint8

const (
	// PopupContext opens the menu with its top left corner at the point
	// (Popup.X0, Y0): a context menu at the pointer.
	PopupContext PopupKind = iota
	// PopupPullDown opens the menu below the rectangle, left-aligned and at
	// least as wide: the menu of a pull-down button.
	PopupPullDown
	// PopupList opens the menu over the rectangle with item Selected over it
	// and checked, at least as wide, in the control's font: the options of a
	// pop-up button (a select).
	PopupList
)

// Popup is a menu for PopUpMenu: menu number Menu of Spec, placed as Kind
// says against the rectangle X0, Y0, X1, Y1 (window pixels).
type Popup struct {
	// Spec and Menu name the menu.
	Spec *MenuSpec
	Menu int
	// Kind is the placement.
	Kind PopupKind
	// X0, Y0, X1, Y1 is the anchor in window pixels (a point for PopupContext).
	X0, Y0, X1, Y1 float32
	// Selected is the item placed over the anchor (PopupList), -1 none.
	Selected int
}
