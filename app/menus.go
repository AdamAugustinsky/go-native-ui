package app

import (
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/ui"
)

// Native menus (macOS, DECISIONS D29): the app's menu.Model becomes the
// system's menu bar, and the menus widgets ask for (ui.Context.RequestMenu)
// are popped up natively after the frame. Elsewhere, and in benches and
// offscreen renders, the widgets draw them.
//
// The bar is the model's bar menus framed by the macOS standard ones: the
// application menu first (About, Settings… for a RoleSettings command,
// Services, Hide, Hide Others, Show All, Quit), a standard Edit menu after
// the first model menu when the model has none, a standard Window menu before
// the Help menu when the model has none, and an empty Help menu, which gets
// the system's search field, when it has none of that either. Commands with an application-menu role move to the
// application menu. It is built once per model, after the first present, so
// startup does not pay for it; the items' enabled and checked state is read
// when a menu opens (menuState), never rebuilt.
//
// Every command a menu item delivers, from the bar, a key equivalent or a
// popped-up menu, comes back as platform.EvCommand and reaches the model's
// next Match like a matched shortcut: Match applies the modal rule and turns
// an edit command into the focused field's edit action. AppKit matches the
// key equivalents (the key that chose an item is not delivered as a key
// too); a shortcut no native item has is matched by Match as before.

// SetMenu sets the app's command and menu model: on macOS the menu bar is
// built from it at the next safe point after the first frame, replacing the
// standard bar every app gets. Call it from Init (or later, to replace the
// model). The kit that matches the model's shortcuts still needs it too
// (widget.Kit.SetMenu).
func (a *App) SetMenu(m *menu.Model) {
	a.menu, a.menuDirty = m, true
}

// syncMenuBar installs the menu bar when it is due: once after the first
// present, and again when SetMenu changed the model.
func (a *App) syncMenuBar() {
	if !a.menuDirty || a.Win == nil || a.frames == 0 || !platform.HasNativeMenus() {
		return
	}
	a.menuDirty = false
	a.menuSpec = nativeMenuSpec(a.menu)
	if a.menuStateFn == nil {
		a.menuStateFn, a.fieldKeysFn = a.menuState, a.fieldTakesKey
	}
	a.Win.SetMenuBar(&a.menuSpec, a.menuStateFn)
	a.Win.SetFieldKeys(a.fieldKeysFn)
}

// menuState is the platform's MenuState: a command's item is enabled as the
// model's State says, given the last frame's modal and focus, the system's
// actions included (a disabled RoleZoom does not zoom; Quit waits for the
// modal unless its command is InModal). A standard item without a command
// (id 0): an edit item when a text field has the focus; a system item (Hide,
// Quit, Minimize …) unless a modal is open, as a command not marked InModal.
func (a *App) menuState(id uint16, act platform.MenuAction) (enabled, checked bool) {
	if id == 0 {
		if act.IsEdit() {
			return a.UI.TextFieldFocused(), false
		}
		return act != platform.MenuNone && !a.UI.ModalOpen(), false
	}
	if a.menu == nil {
		return false, false
	}
	return a.menu.State(a.UI, menu.ID(id))
}

// fieldTakesKey is the platform's FieldKeys: whether the focused text field
// takes key-down e itself, as the next frame will decide it (DECISIONS D28),
// so that no native menu item's key equivalent acts on it.
func (a *App) fieldTakesKey(e *platform.Event) bool {
	code := keyCode(e.Key)
	return code != 0 && a.UI.FieldClaims(ui.KeyEvent{Code: code, Mods: mods(e.Mods), Down: true})
}

// popUpMenus opens the native menu the last frame requested, if any. It is
// called from the loop, outside any frame: AppKit tracks the menu inside the
// call while the frame handler keeps drawing. A list choice reaches its owner
// in the next frame.
func (a *App) popUpMenus() {
	if a.UI == nil || !a.UI.NativeMenus() {
		return
	}
	r, ok := a.UI.TakeMenuRequest()
	if !ok {
		return
	}
	p := platform.Popup{X0: r.Rect[0], Y0: r.Rect[1], X1: r.Rect[2], Y1: r.Rect[3], Selected: -1}
	switch r.Kind {
	case ui.MenuContext, ui.MenuPullDown:
		if a.menu == nil || r.Menu < 0 || r.Menu >= len(a.menu.Menus()) {
			return
		}
		if a.menuDirty || len(a.menuSpec.Menus) == 0 {
			a.menuSpec = nativeMenuSpec(a.menu)
		}
		p.Spec, p.Menu, p.Kind = &a.menuSpec, r.Menu, platform.PopupContext
		if r.Kind == ui.MenuPullDown {
			p.Kind = platform.PopupPullDown
		}
	case ui.MenuList:
		a.listSpec.Menus = a.listSpec.Menus[:0]
		items := a.listItems[:0]
		for _, o := range r.Options {
			items = append(items, platform.MenuItem{Title: o})
		}
		a.listItems = items
		a.listSpec.Menus = append(a.listSpec.Menus, platform.Menu{Items: items})
		p.Spec, p.Menu, p.Kind, p.Selected = &a.listSpec, 0, platform.PopupList, r.Selected
	default:
		return
	}
	i := a.Win.PopUpMenu(&p)
	if r.Kind == ui.MenuList && i >= 0 {
		a.tr.in.SetMenuChoice(r.Owner, i, r.Rev)
		a.tr.dirty = true
		a.p.invalidate()
	}
}

// nativeMenuSpec composes the macOS menu bar of model m (nil: the standard
// menus only). The model's menus keep their indices, so a popped-up menu is
// the model's menu by number; the standard menus follow them.
func nativeMenuSpec(m *menu.Model) platform.MenuSpec {
	var s platform.MenuSpec
	var menus []menu.Menu
	var bar []int
	if m != nil {
		menus, bar = m.Menus(), m.Bar()
	}
	var settings *menu.Command
	var appCmds [platform.MenuQuit + 1]*menu.Command // the model's commands of the application menu's standard items
	hasEdit, hasWindow, hasHelp := false, false, false
	for i := range menus {
		s.Menus = append(s.Menus, platform.Menu{Title: menus[i].Title, Items: nativeItems(m, menus[i].Items)})
		switch menus[i].Role {
		case menu.MenuWindow:
			s.Menus[i].Role, hasWindow = platform.MenuRoleWindow, true
		case menu.MenuHelp:
			s.Menus[i].Role, hasHelp = platform.MenuRoleHelp, true
		}
	}
	if m != nil {
		cmds := m.Commands()
		for i := range cmds {
			switch cmds[i].Role {
			case menu.RoleSettings:
				settings = &cmds[i]
			case menu.RoleAbout, menu.RoleHide, menu.RoleHideOthers, menu.RoleShowAll, menu.RoleQuit:
				appCmds[commandItem(&cmds[i]).Action] = &cmds[i]
			}
		}
	}
	for _, bi := range bar {
		for _, it := range s.Menus[bi].Items {
			hasEdit = hasEdit || it.Action.IsEdit()
		}
	}
	add := func(pm platform.Menu) int {
		s.Menus = append(s.Menus, pm)
		return len(s.Menus) - 1
	}
	act := func(a platform.MenuAction) platform.MenuItem { return platform.MenuItem{Action: a} }
	sep := platform.MenuItem{Kind: platform.MenuItemSeparator}
	// An application menu item is the system's standard one (title and key
	// equivalent as macOS has them), deciding with the model's command of its
	// role, if there is one (enabled, InModal).
	appAct := func(a platform.MenuAction) platform.MenuItem {
		it := act(a)
		if c := appCmds[a]; c != nil {
			it.ID = uint16(c.ID)
		}
		return it
	}

	services := add(platform.Menu{Title: "Services", Role: platform.MenuRoleServices})
	appItems := []platform.MenuItem{appAct(platform.MenuAbout), sep}
	if settings != nil {
		it := commandItem(settings)
		if it.Title == "" {
			it.Title = "Settings…"
		}
		if it.Key == 0 && it.Mods == 0 {
			it.Key, it.Mods = ',', platform.ModSuper
		}
		appItems = append(appItems, it, sep)
	}
	appItems = append(appItems, platform.MenuItem{Kind: platform.MenuItemSubmenu, Submenu: services}, sep,
		appAct(platform.MenuHide), appAct(platform.MenuHideOthers), appAct(platform.MenuShowAll), sep, appAct(platform.MenuQuit))
	s.Bar = append(s.Bar, add(platform.Menu{Title: "Application", Role: platform.MenuRoleApp, Items: appItems}))

	edit := -1
	if !hasEdit {
		edit = add(platform.Menu{Title: "Edit", Items: []platform.MenuItem{act(platform.MenuUndo), act(platform.MenuRedo), sep,
			act(platform.MenuCut), act(platform.MenuCopy), act(platform.MenuPaste), act(platform.MenuDelete), act(platform.MenuSelectAll)}})
	}
	for k, bi := range bar {
		s.Bar = append(s.Bar, bi)
		if k == 0 && edit >= 0 {
			s.Bar = append(s.Bar, edit)
			edit = -1
		}
	}
	if edit >= 0 {
		s.Bar = append(s.Bar, edit)
	}
	if !hasWindow { // before the Help menu
		w := add(platform.Menu{Title: "Window", Role: platform.MenuRoleWindow, Items: []platform.MenuItem{
			act(platform.MenuMinimize), act(platform.MenuZoom), sep, act(platform.MenuBringAllToFront)}})
		at := len(s.Bar)
		for k, bi := range s.Bar {
			if s.Menus[bi].Role == platform.MenuRoleHelp {
				at = k
				break
			}
		}
		s.Bar = append(s.Bar[:at], append([]int{w}, s.Bar[at:]...)...)
	}
	if !hasHelp {
		s.Bar = append(s.Bar, add(platform.Menu{Title: "Help", Role: platform.MenuRoleHelp}))
	}
	return s
}

// nativeItems converts the items of a model menu. Commands with an
// application-menu role are left out (the application menu has them), and so
// are the separators that would then lead, trail or double up.
func nativeItems(m *menu.Model, items []menu.Item) []platform.MenuItem {
	out := make([]platform.MenuItem, 0, len(items))
	for _, it := range items {
		switch it.Kind {
		case menu.ItemSeparator:
			if n := len(out); n > 0 && out[n-1].Kind != platform.MenuItemSeparator {
				out = append(out, platform.MenuItem{Kind: platform.MenuItemSeparator})
			}
		case menu.ItemSubmenu:
			out = append(out, platform.MenuItem{Kind: platform.MenuItemSubmenu, Submenu: it.Submenu, Title: it.Title})
		case menu.ItemCommand:
			cmd, ok := m.Command(it.Command)
			if !ok || appMenuRole(cmd.Role) {
				continue
			}
			out = append(out, commandItem(&cmd))
		}
	}
	if n := len(out); n > 0 && out[n-1].Kind == platform.MenuItemSeparator {
		out = out[:n-1]
	}
	return out
}

// appMenuRole reports whether commands with role r belong to the application
// menu on macOS.
func appMenuRole(r menu.Role) bool {
	switch r {
	case menu.RoleAbout, menu.RoleSettings, menu.RoleHide, menu.RoleHideOthers, menu.RoleShowAll, menu.RoleQuit:
		return true
	}
	return false
}

// commandItem is the native item of a command. A role with system behaviour
// becomes its standard action and keeps its id, so that the model decides
// the item (enabled, the modal rule); an edit role keeps its id too, so that
// the command still reaches Match (the focused field's edit, or the app's own
// copy of a list selection).
func commandItem(cmd *menu.Command) platform.MenuItem {
	it := platform.MenuItem{ID: uint16(cmd.ID), Title: cmd.Title, Symbol: cmd.Symbol}
	it.Key, it.Mods = keyEquivalent(cmd.Shortcut)
	switch cmd.Role {
	case menu.RoleAbout:
		it.Action = platform.MenuAbout
	case menu.RoleQuit:
		it.Action = platform.MenuQuit
	case menu.RoleHide:
		it.Action = platform.MenuHide
	case menu.RoleHideOthers:
		it.Action = platform.MenuHideOthers
	case menu.RoleShowAll:
		it.Action = platform.MenuShowAll
	case menu.RoleMinimize:
		it.Action = platform.MenuMinimize
	case menu.RoleZoom:
		it.Action = platform.MenuZoom
	case menu.RoleCloseWindow:
		it.Action = platform.MenuClose
	case menu.RoleUndo:
		it.Action = platform.MenuUndo
	case menu.RoleRedo:
		it.Action = platform.MenuRedo
	case menu.RoleCut:
		it.Action = platform.MenuCut
	case menu.RoleCopy:
		it.Action = platform.MenuCopy
	case menu.RolePaste:
		it.Action = platform.MenuPaste
	case menu.RoleDelete:
		it.Action = platform.MenuDelete
	case menu.RoleSelectAll:
		it.Action = platform.MenuSelectAll
	}
	return it
}

// keyEquivalent is the native key equivalent of a shortcut on macOS (⌘ for
// the primary modifier); 0 for none or a key that has no character.
func keyEquivalent(s menu.Shortcut) (rune, platform.Mods) {
	if s.IsZero() {
		return 0, 0
	}
	var r rune
	switch k := s.Key; {
	case k >= 'A' && k <= 'Z':
		r = rune(k) + ('a' - 'A')
	case k >= 0x21 && k < 0x7F:
		r = rune(k)
	default:
		named := [...]rune{
			ui.KeyEnter - ui.KeyEnter:     platform.MenuKeyEnter,
			ui.KeySpace - ui.KeyEnter:     ' ',
			ui.KeyEscape - ui.KeyEnter:    platform.MenuKeyEscape,
			ui.KeyTab - ui.KeyEnter:       platform.MenuKeyTab,
			ui.KeyBackspace - ui.KeyEnter: platform.MenuKeyBackspace,
			ui.KeyDelete - ui.KeyEnter:    platform.MenuKeyDelete,
			ui.KeyLeft - ui.KeyEnter:      platform.MenuKeyLeft,
			ui.KeyRight - ui.KeyEnter:     platform.MenuKeyRight,
			ui.KeyUp - ui.KeyEnter:        platform.MenuKeyUp,
			ui.KeyDown - ui.KeyEnter:      platform.MenuKeyDown,
			ui.KeyHome - ui.KeyEnter:      platform.MenuKeyHome,
			ui.KeyEnd - ui.KeyEnter:       platform.MenuKeyEnd,
			ui.KeyPageUp - ui.KeyEnter:    platform.MenuKeyPageUp,
			ui.KeyPageDown - ui.KeyEnter:  platform.MenuKeyPageDown,
		}
		i := int(k) - int(ui.KeyEnter)
		if k < ui.KeyEnter || i >= len(named) {
			return 0, 0
		}
		r = named[i]
	}
	m := s.Mods
	if s.Primary {
		m |= ui.ModSuper // the primary modifier on macOS, the only OS with native menus
	}
	var pm platform.Mods
	if m&ui.ModShift != 0 {
		pm |= platform.ModShift
	}
	if m&ui.ModCtrl != 0 {
		pm |= platform.ModCtrl
	}
	if m&ui.ModAlt != 0 {
		pm |= platform.ModAlt
	}
	if m&ui.ModSuper != 0 {
		pm |= platform.ModSuper
	}
	return r, pm
}

// standardEdit maps the action of a standard edit item without a command
// (platform.FlagStandard) to the focused field's edit action.
func standardEdit(a platform.MenuAction) ui.EditAction {
	switch a {
	case platform.MenuUndo:
		return ui.EditUndo
	case platform.MenuRedo:
		return ui.EditRedo
	case platform.MenuCut:
		return ui.EditCut
	case platform.MenuCopy:
		return ui.EditCopy
	case platform.MenuPaste:
		return ui.EditPaste
	case platform.MenuDelete:
		return ui.EditDelete
	case platform.MenuSelectAll:
		return ui.EditSelectAll
	}
	return 0
}
