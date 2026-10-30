// Package menu is the OS-independent command and menu model (DECISIONS D29):
// the commands an app has, the shortcuts that trigger them, the menus that
// show them, and the matching of shortcuts against ui.Input.
//
// An app builds a Model once with NewModel, which validates it. The model's
// structure is fixed afterwards; only the enabled and checked state of the
// commands changes (SetEnabled, SetChecked), set by the focused window's Build
// each frame. Each frame the Build calls Match first, which claims the
// shortcut keys and gathers the commands the system delivered (a native menu
// item), and then asks for them: Fired for one command, Next for all of them
// in order. On macOS the model becomes the native menu bar (app.App.SetMenu),
// and the commands its items deliver reach Match like shortcuts; on Windows
// and Linux the widget package draws it.
//
//	if m.Match(c); m.Fired(cmdSave) { save() }
//
// The package imports only ui, platform (SetStrings hands the native menus
// their titles) and the standard library. Matching allocates nothing.
package menu

import (
	"errors"
	"fmt"
	"sort"

	"gonativeui/ui"
)

// ID names a command. Zero is not a command.
type ID uint16

// Scope says whom a command is for.
type Scope uint8

const (
	// ScopeWindow commands act on the focused window (Save, Close Window) and
	// reach that window's next Build.
	ScopeWindow Scope = iota
	// ScopeApp commands act on the application (Quit, About, New Window) and go
	// to the app's command handler, which runs also when no window is open.
	ScopeApp
)

// Role marks the standard commands the operating system knows. On macOS they
// get their system behaviour and place in the native menus; the edit roles
// act on the focused text field everywhere (see Match).
type Role uint8

const (
	// RoleNone is a command without a role.
	RoleNone Role = iota
	// RoleAbout is the standard About command.
	RoleAbout
	// RoleQuit is the standard Quit command.
	RoleQuit
	// RoleHide is the standard Hide command (hide the application).
	RoleHide
	// RoleHideOthers is the standard Hide Others command.
	RoleHideOthers
	// RoleShowAll is the standard Show All command.
	RoleShowAll
	// RoleUndo is the standard Undo command; it acts on the focused text field.
	RoleUndo
	// RoleRedo is the standard Redo command; it acts on the focused text field.
	RoleRedo
	// RoleCut is the standard Cut command; it acts on the focused text field.
	RoleCut
	// RoleCopy is the standard Copy command; it acts on the focused text field.
	RoleCopy
	// RolePaste is the standard Paste command; it acts on the focused text field.
	RolePaste
	// RoleSelectAll is the standard Select All command; it acts on the focused text field.
	RoleSelectAll
	// RoleDelete is the standard Delete command; it acts on the focused text field.
	RoleDelete
	// RoleMinimize is the standard Minimize command (minimize the window).
	RoleMinimize
	// RoleZoom is the standard Zoom command (zoom the window).
	RoleZoom
	// RoleCloseWindow is the standard Close Window command.
	RoleCloseWindow
	// RoleSettings is the application's Settings command (⌘, on macOS, where
	// it sits in the application menu). It is the app's to act on.
	RoleSettings
	// RoleZoomIn, RoleZoomOut and RoleActualSize are the View menu's app zoom
	// commands (⌘+, ⌘− and ⌘0 on macOS; Ctrl elsewhere). The app handles
	// them: the window's zoom steps up or down, or returns to 100 %, and
	// Fired never reports them (see Model.TakeZoom). StandardCommand makes
	// them with the standard title and shortcuts. RoleZoom is another
	// command, the window's own zoom (its green button on macOS).
	RoleZoomIn
	RoleZoomOut
	RoleActualSize
	// RolePasteAndMatchStyle is the standard Paste and Match Style command
	// (⌥⇧⌘V on macOS): a hosted native text view pastes the text without its
	// style; the focused drawn text field, all plain, pastes.
	RolePasteAndMatchStyle
	// RoleFullScreen is the standard full screen command of the View menu
	// (⌃⌘F on macOS): "Enter Full Screen", which AppKit retitles "Exit Full
	// Screen" while the window is in full screen. Elsewhere it is the app's
	// to act on.
	RoleFullScreen

	roleEnd // one past the last role
)

// editAction is the ui edit action of an edit role, 0 for the others.
func (r Role) editAction() ui.EditAction {
	switch r {
	case RoleUndo:
		return ui.EditUndo
	case RoleRedo:
		return ui.EditRedo
	case RoleCut:
		return ui.EditCut
	case RoleCopy:
		return ui.EditCopy
	case RolePaste, RolePasteAndMatchStyle:
		return ui.EditPaste
	case RoleSelectAll:
		return ui.EditSelectAll
	case RoleDelete:
		return ui.EditDelete
	}
	return 0
}

// Shortcut is a key chord. Primary adds the platform's primary modifier to
// Mods: ⌘ on macOS, Ctrl elsewhere.
type Shortcut struct {
	// Key is the key of the chord.
	Key ui.KeyCode
	// Mods are the modifiers the chord needs besides the primary one.
	Mods ui.Mods
	// Primary adds the platform's primary modifier (⌘ on macOS, Ctrl elsewhere).
	Primary bool
}

// IsZero reports whether the shortcut is unset.
func (s Shortcut) IsZero() bool { return s == Shortcut{} }

// Effective returns the modifiers a key event must carry, exactly, to match:
// Mods, plus the primary modifier if Primary is set.
func (s Shortcut) Effective() ui.Mods {
	if s.Primary {
		return s.Mods | ui.PrimaryMod()
	}
	return s.Mods
}

// Command is something the user can ask the app to do.
type Command struct {
	// ID names the command; it is what a menu item, a shortcut match and the command handler refer to.
	ID       ID
	Title    string   // the menu text; may be empty for a command with a Role (the system supplies it)
	Shortcut Shortcut // zero: none
	// Alias is a second chord that triggers the command, such as ⌘= for
	// Zoom In (the shortcut is ⌘+). Match takes it like Shortcut; menus show
	// only Shortcut. Zero: none.
	Alias Shortcut
	Scope Scope
	Role  Role
	// Symbol is the SF Symbol shown before the title in native menus
	// ("doc.on.doc"; "" none). Drawn menus do not show it yet.
	Symbol string
	// Mnemonic is the letter or digit that chooses the command's item from
	// the keyboard in a drawn menu (Windows and Linux: underlined, typed
	// with the menu open); 0 lets the menu pick a free one (widget/base's
	// Mnemonics). It is ignored by native menus.
	Mnemonic rune
	// InModal lets the command act while a modal overlay (a dialog) is open.
	// Without it every command and shortcut is held back then, except the edit
	// roles for a text field with the focus inside the modal (see Match). Mark
	// the few that belong to everything: Quit, a Help window.
	InModal bool
}

// ItemKind selects what a menu item is.
type ItemKind uint8

const (
	ItemCommand   ItemKind = iota // runs Command
	ItemSeparator                 // a line between groups
	ItemSubmenu                   // opens the menu at index Submenu
)

// Item is one entry of a menu.
type Item struct {
	// Kind selects which of the other fields apply.
	Kind    ItemKind
	Command ID     // ItemCommand: the command
	Submenu int    // ItemSubmenu: index into the model's menus
	Title   string // ItemSubmenu: the entry's text; empty uses the submenu's Title
}

// Menu is a list of items with a title. A menu is shown as a top-level menu of
// the bar (Model bar), as a submenu of an item, or by itself as a context menu.
type Menu struct {
	// Title is the menu's name (a bar entry or the title of a submenu).
	Title string
	// Items are the menu's entries, in order.
	Items []Item
	// Role marks the bar's Window and Help menus, which the system extends
	// on macOS (the list of windows, the Help search field).
	Role MenuRole
	// Mnemonic is the letter or digit of the menu's title in a drawn menu
	// bar (Alt+F opens File) and of its entry as a submenu; 0 lets the menu
	// pick a free one. It is ignored by native menus.
	Mnemonic rune
}

// MenuRole marks a menu the operating system knows (Menu.Role).
type MenuRole uint8

const (
	// MenuPlain is an ordinary menu.
	MenuPlain MenuRole = iota
	// MenuWindow is the Window menu. On macOS the system lists the windows in
	// it; without one the bar gets a standard Window menu.
	MenuWindow
	// MenuHelp is the Help menu. On macOS the system adds its search field;
	// without one the bar gets an empty Help menu that has it.
	MenuHelp
	// MenuServices names the application menu's Services submenu on macOS (its
	// Title; the system fills it). It is not shown by itself: keep it out of
	// the bar and of every item. Without one, or with an empty Title, the
	// submenu is called "Services".
	MenuServices
)

// Model is the validated commands and menus plus the commands' mutable state.
// It is not safe for concurrent use.
type Model struct {
	cmds  []Command
	menus []Menu
	bar   []int

	byID  []idIndex // sorted by id, for lookup
	keys  []keyIndex
	off   []uint64 // bit i set: command i is disabled (the default is enabled)
	lim   []uint64 // bit i set: command i is at its limit (SetLimited): shown disabled, but still delivered
	check []uint64 // bit i set: command i is checked

	// the commands of the frame being built (see Match)
	q      [queueSize]ID
	qn, qh int
	qc     *ui.Context
	qframe uint32

	zq [zoomQueueSize]ZoomAction // zoom commands of the frame, not taken yet (zoom.go)
	zn int
}

type idIndex struct {
	id  ID
	idx int
}

type keyIndex struct {
	key  ui.KeyCode
	mods ui.Mods
	idx  int
}

// queueSize is how many commands a frame can deliver; more are dropped.
const queueSize = 16

// NewModel validates the commands, menus and bar and returns the model. bar
// lists the indices of the menus shown as the menu bar, in order. It copies its
// arguments. The errors are: a zero or repeated command id, a command without a
// title or role, an unknown scope or role, a role used twice (a command's or a
// menu's), a shortcut with
// modifiers but no key or one that two commands share, an item that names an
// unknown command or submenu, a submenu that is its own ancestor, a submenu
// used twice, and a bar entry out of range or repeated.
func NewModel(commands []Command, menus []Menu, bar []int) (*Model, error) {
	m := &Model{
		cmds:  append([]Command(nil), commands...),
		menus: make([]Menu, len(menus)),
		bar:   append([]int(nil), bar...),
	}
	for i := range menus {
		m.menus[i] = Menu{Title: menus[i].Title, Items: append([]Item(nil), menus[i].Items...), Role: menus[i].Role, Mnemonic: menus[i].Mnemonic}
	}
	roles := make(map[Role]ID)
	shortcuts := make(map[keyIndex]ID)
	for i := range m.cmds {
		c := &m.cmds[i]
		switch {
		case c.ID == 0:
			return nil, fmt.Errorf("menu: command %d has id 0", i)
		case c.Title == "" && c.Role == RoleNone:
			return nil, fmt.Errorf("menu: command %d has neither a title nor a role", c.ID)
		case c.Scope > ScopeApp:
			return nil, fmt.Errorf("menu: command %d has an unknown scope %d", c.ID, c.Scope)
		case c.Role >= roleEnd:
			return nil, fmt.Errorf("menu: command %d has an unknown role %d", c.ID, c.Role)
		}
		if c.Role != RoleNone {
			if other, dup := roles[c.Role]; dup {
				return nil, fmt.Errorf("menu: commands %d and %d have the same role", other, c.ID)
			}
			roles[c.Role] = c.ID
		}
		for _, s := range [...]Shortcut{c.Shortcut, c.Alias} {
			if s.IsZero() {
				continue
			}
			if s.Key == 0 {
				return nil, fmt.Errorf("menu: command %d has a shortcut with modifiers but no key", c.ID)
			}
			k := keyIndex{key: s.Key, mods: s.Effective(), idx: i}
			sk := keyIndex{key: k.key, mods: k.mods}
			if other, dup := shortcuts[sk]; dup {
				return nil, fmt.Errorf("menu: commands %d and %d have the same shortcut", other, c.ID)
			}
			shortcuts[sk] = c.ID
			m.keys = append(m.keys, k)
		}
		m.byID = append(m.byID, idIndex{c.ID, i})
	}
	sort.Slice(m.byID, func(a, b int) bool { return m.byID[a].id < m.byID[b].id })
	for i := 1; i < len(m.byID); i++ {
		if m.byID[i].id == m.byID[i-1].id {
			return nil, fmt.Errorf("menu: command id %d is used twice", m.byID[i].id)
		}
	}
	words := (len(m.cmds) + 63) / 64
	m.off, m.lim, m.check = make([]uint64, words), make([]uint64, words), make([]uint64, words)

	parent := make([]int, len(m.menus)) // 1 + the index of the menu that holds it as a submenu
	menuRoles := make(map[MenuRole]int)
	for mi := range m.menus {
		switch r := m.menus[mi].Role; {
		case r > MenuServices:
			return nil, fmt.Errorf("menu: menu %d has an unknown role %d", mi, r)
		case r != MenuPlain:
			if other, dup := menuRoles[r]; dup {
				return nil, fmt.Errorf("menu: menus %d and %d have the same role", other, mi)
			}
			menuRoles[r] = mi
		}
		for ii, it := range m.menus[mi].Items {
			switch it.Kind {
			case ItemCommand:
				if _, ok := m.index(it.Command); !ok {
					return nil, fmt.Errorf("menu: menu %d item %d names the unknown command %d", mi, ii, it.Command)
				}
			case ItemSeparator:
			case ItemSubmenu:
				if it.Submenu < 0 || it.Submenu >= len(m.menus) {
					return nil, fmt.Errorf("menu: menu %d item %d names the unknown submenu %d", mi, ii, it.Submenu)
				}
				if parent[it.Submenu] != 0 {
					return nil, fmt.Errorf("menu: menu %d is a submenu twice", it.Submenu)
				}
				parent[it.Submenu] = mi + 1
			default:
				return nil, fmt.Errorf("menu: menu %d item %d has an unknown kind %d", mi, ii, it.Kind)
			}
		}
	}
	for mi := range m.menus { // following the parents must end at a menu with none
		for at, n := mi, 0; parent[at] != 0; at, n = parent[at]-1, n+1 {
			if n > len(m.menus) {
				return nil, errors.New("menu: a submenu is its own ancestor")
			}
		}
	}
	seen := make(map[int]bool)
	for _, b := range m.bar {
		if b < 0 || b >= len(m.menus) {
			return nil, fmt.Errorf("menu: the bar names the unknown menu %d", b)
		}
		if seen[b] {
			return nil, fmt.Errorf("menu: menu %d is in the bar twice", b)
		}
		if parent[b] != 0 {
			return nil, fmt.Errorf("menu: menu %d is in the bar and a submenu", b)
		}
		seen[b] = true
	}
	return m, nil
}

// Commands returns the commands in registration order. Do not modify them.
func (m *Model) Commands() []Command { return m.cmds }

// Menus returns the menus. Do not modify them.
func (m *Model) Menus() []Menu { return m.menus }

// Bar returns the indices of the menus of the menu bar, in order.
func (m *Model) Bar() []int { return m.bar }

// Command returns the command with the given id.
func (m *Model) Command(id ID) (Command, bool) {
	i, ok := m.index(id)
	if !ok {
		return Command{}, false
	}
	return m.cmds[i], true
}

func (m *Model) index(id ID) (int, bool) {
	lo, hi := 0, len(m.byID)
	for lo < hi {
		mid := (lo + hi) / 2
		if m.byID[mid].id < id {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(m.byID) && m.byID[lo].id == id {
		return m.byID[lo].idx, true
	}
	return 0, false
}

// SetEnabled enables or disables a command; commands start enabled. A disabled
// command does not match its shortcut, and a widget shows it dimmed. Unknown
// ids are ignored.
func (m *Model) SetEnabled(id ID, on bool) {
	if i, ok := m.index(id); ok {
		setBit(m.off, i, !on)
	}
}

// Enabled reports whether the command exists and shows enabled: it is neither
// disabled (SetEnabled) nor at its limit (SetLimited).
func (m *Model) Enabled(id ID) bool {
	i, ok := m.index(id)
	return ok && !hasBit(m.off, i) && !hasBit(m.lim, i)
}

// SetLimited marks a command as at its limit, such as Zoom In at the largest
// zoom: menus show it disabled (Enabled, State), but unlike SetEnabled it is
// still delivered, because the limit is the app's to enforce where the command
// acts. Commands that arrive together act in the order they were delivered, and
// an earlier one may lift the limit a later one needs (Zoom In, then Actual
// Size, at 100 %). Unknown ids are ignored.
func (m *Model) SetLimited(id ID, on bool) {
	if i, ok := m.index(id); ok {
		setBit(m.lim, i, on)
	}
}

// SetChecked sets the check mark of a command (a menu item that toggles
// something). Unknown ids are ignored.
func (m *Model) SetChecked(id ID, on bool) {
	if i, ok := m.index(id); ok {
		setBit(m.check, i, on)
	}
}

// Checked reports whether the command exists and is checked.
func (m *Model) Checked(id ID) bool {
	i, ok := m.index(id)
	return ok && hasBit(m.check, i)
}

func setBit(w []uint64, i int, on bool) {
	if on {
		w[i/64] |= 1 << uint(i%64)
	} else {
		w[i/64] &^= 1 << uint(i%64)
	}
}

func hasBit(w []uint64, i int) bool { return w[i/64]&(1<<uint(i%64)) != 0 }
