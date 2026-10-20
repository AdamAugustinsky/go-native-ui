//go:build darwin

package platform

import (
	"runtime"
	"unicode/utf8"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Native menus: NSMenu trees built from a MenuSpec (menu.go has the
// contract).
//
// App commands target GNUIMenuTarget's gnuiCommand:, with the command id in
// the item's tag. The edit actions are nil-targeted standard selectors
// (copy:, paste: …) that GNUIView implements, so they travel the responder
// chain. The other standard actions keep AppKit's selectors (hide:,
// performMiniaturize: …), so that AppKit knows the items for what they are,
// but target GNUIMenuTarget, which validates them with the app's MenuState
// and AppKit's own target for the action (the window, the application) and
// then sends the action on to that target. validateMenuItem: (on
// GNUIMenuTarget, and on GNUIView for the edit actions) asks the app's
// MenuState and sets the check mark; while a key-down the app's focused text
// field takes is being sent, it disables every item (FieldKeys). Every menu
// has GNUIMenuTarget as its delegate: while one is
// open (menuWillOpen: … menuDidClose:) the display link runs the frame
// handler, as in a live resize, so the content keeps drawing during AppKit's
// tracking loop.
//
// A menu tree is built once per MenuSpec (SetMenuBar) and once per popped-up
// menu, cached and rebuilt only when its items change (PopUpMenu).

var (
	selInitWithTitle       = objc.Sel("initWithTitle:")
	selInitItem            = objc.Sel("initWithTitle:action:keyEquivalent:")
	selInitPlain           = objc.Sel("init")
	selAddItem             = objc.Sel("addItem:")
	selSeparatorItem       = objc.Sel("separatorItem")
	selSetSubmenu          = objc.Sel("setSubmenu:")
	selSetKeyMask          = objc.Sel("setKeyEquivalentModifierMask:")
	selSetState            = objc.Sel("setState:")
	selState               = objc.Sel("state")
	selAction              = objc.Sel("action")
	selMenuOfItem          = objc.Sel("menu")
	selIndexOfItem         = objc.Sel("indexOfItem:")
	selItemAtIndex         = objc.Sel("itemAtIndex:")
	selNumberOfItems       = objc.Sel("numberOfItems")
	selSetMainMenu         = objc.Sel("setMainMenu:")
	selSetServicesMenu     = objc.Sel("setServicesMenu:")
	selSetWindowsMenu      = objc.Sel("setWindowsMenu:")
	selSetHelpMenu         = objc.Sel("setHelpMenu:")
	selServicesMenu        = objc.Sel("servicesMenu")
	selSupermenu           = objc.Sel("supermenu")
	selIndexWithSubmenu    = objc.Sel("indexOfItemWithSubmenu:")
	selWindows             = objc.Sel("windows")
	selWinTitle            = objc.Sel("title")
	selArrCount            = objc.Sel("count")
	selArrAt               = objc.Sel("objectAtIndex:")
	selExcludedFromWinMenu = objc.Sel("isExcludedFromWindowsMenu")
	selIsVisible           = objc.Sel("isVisible")
	selIsMiniaturized      = objc.Sel("isMiniaturized")
	selAddWindowsItem      = objc.Sel("addWindowsItem:title:filename:")
	selSetMinWidth         = objc.Sel("setMinimumWidth:")
	selSetMenuFont         = objc.Sel("setFont:")
	selSystemFontSize      = objc.Sel("systemFontOfSize:")
	selPopUpMenu           = objc.Sel("popUpMenuPositioningItem:atLocation:inView:")
	selProcessInfo         = objc.Sel("processInfo")
	selProcessName         = objc.Sel("processName")
	selInitBytesStr        = objc.Sel("initWithBytes:length:encoding:")
	selValidateItem        = objc.Sel("validateMenuItem:")
	selMenuWillOpen        = objc.Sel("menuWillOpen:")
	selMenuDidClose        = objc.Sel("menuDidClose:")
	selGnuiCommand         = objc.Sel("gnuiCommand:")
	selGnuiChoose          = objc.Sel("gnuiChoose:")
	selGnuiQuit            = objc.Sel("gnuiQuit:")
	selGnuiWillSend        = objc.Sel("gnuiMenuWillSend:")
	selTargetForAction     = objc.Sel("targetForAction:to:from:")
	selSendActionTo        = objc.Sel("sendAction:to:from:")
	selValidateUIItem      = objc.Sel("validateUserInterfaceItem:")

	// the selectors of the standard actions, by MenuAction
	actionSel = [...]objc.SEL{
		MenuAbout:           objc.Sel("orderFrontStandardAboutPanel:"),
		MenuHide:            objc.Sel("hide:"),
		MenuHideOthers:      objc.Sel("hideOtherApplications:"),
		MenuShowAll:         objc.Sel("unhideAllApplications:"),
		MenuQuit:            selGnuiQuit,
		MenuUndo:            objc.Sel("undo:"),
		MenuRedo:            objc.Sel("redo:"),
		MenuCut:             objc.Sel("cut:"),
		MenuCopy:            objc.Sel("copy:"),
		MenuPaste:           objc.Sel("paste:"),
		MenuDelete:          objc.Sel("delete:"),
		MenuSelectAll:       objc.Sel("selectAll:"),
		MenuMinimize:        objc.Sel("performMiniaturize:"),
		MenuZoom:            objc.Sel("performZoom:"),
		MenuClose:           objc.Sel("performClose:"),
		MenuBringAllToFront: objc.Sel("arrangeInFront:"),
	}

	menuTargetClass                                      objc.ID
	clsMenu, clsMenuItem, clsString, clsFont, clsNSImage objc.ID // cached: objc.Cls allocates
	nsApp                                                objc.ID
	keyBuf                                               [utf8.UTFMax]byte // a key equivalent's UTF-8 (heap memory for the call)
)

// nativeMenus is the application's native menu state (one per process, like
// the menu bar).
var nativeMenus struct {
	target   objc.ID   // the GNUIMenuTarget instance
	state    MenuState // the app's validation
	bar      objc.ID   // the main menu we installed (retained)
	appName  string
	depth    int     // menus open now (menuWillOpen: minus menuDidClose:)
	chosen   int     // the index of the item the last action came from, -1 none
	root     objc.ID // the menu PopUpMenu shows, while it does
	services objc.ID // the Services menu every bar shares (servicesMenu), retained
	popups   []popupMenu
	owner    *Window // the window whose bar is installed (SetMenuBar): the bar's commands are its; nil: the app's
	model    any     // the model the installed bar was built from (SetWindowMenus), nil: none known
	popup    *Window // the window PopUpMenu shows a menu over, while it does
}

// popupMenu is a cached menu tree for PopUpMenu: the tree built for menu idx
// of spec, and a copy of the menus it was built from (title, role, items),
// in the order the build visited them.
type popupMenu struct {
	spec  *MenuSpec
	idx   int
	menus []Menu
	menu  objc.ID // retained
	font  bool    // set to the control font (PopupList)
}

// maxPopups is how many popped-up menu trees are kept.
const maxPopups = 16

// HasNativeMenus reports whether the OS has native menus: true on macOS.
func HasNativeMenus() bool { return true }

// registerMenuTarget defines GNUIMenuTarget, the target of the app commands
// and the delegate of every menu.
func registerMenuTarget() {
	t := objc.Define("GNUIMenuTarget", objc.Cls("NSObject"), []string{"NSMenuDelegate", "NSMenuItemValidation"})
	t.Method(selGnuiCommand, "v@:@", menuCommand)
	t.Method(selGnuiChoose, "v@:@", menuChoose)
	t.Method(selGnuiQuit, "v@:@", menuQuit)
	t.Method(selValidateItem, "c@:@", menuValidate)
	t.Method(selMenuWillOpen, "v@:@", menuWillOpen)
	t.Method(selMenuDidClose, "v@:@", menuDidClose)
	t.Method(selGnuiWillSend, "v@:@", menuWillSend)
	for a := MenuAbout; int(a) < len(actionSel); a++ {
		if !a.IsEdit() && a != MenuQuit {
			t.Method(actionSel[a], "v@:@", menuSystem)
		}
	}
	menuTargetClass = t.Register()
}

// registerViewEdit adds the standard edit actions (undo:, copy: …) to
// GNUIView, and their validation.
func registerViewEdit(v *objc.ClassDef) {
	for a := MenuUndo; a <= MenuSelectAll; a++ {
		v.Method(actionSel[a], "v@:@", viewEditAction)
	}
	v.Method(selValidateItem, "c@:@", viewValidate)
}

func ensureMenuTarget() {
	if nativeMenus.target == 0 {
		clsMenu, clsMenuItem, clsString = objc.Cls("NSMenu"), objc.Cls("NSMenuItem"), objc.Cls("NSString")
		clsFont, clsNSImage = objc.Cls("NSFont"), objc.Cls("NSImage")
		nsApp = objc.Send(objc.Cls("NSApplication"), selSharedApplication)
		nativeMenus.target = objc.Send(objc.Send(menuTargetClass, selAlloc), selInitPlain)
		nativeMenus.chosen = -1
		// Every item's action, ours or AppKit's (hide:, performClose: …),
		// tells us it is sent, so that the key-down of its key equivalent is
		// not delivered as well (handle).
		nc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
		name := objc.Str("NSMenuWillSendActionNotification")
		objc.Send4(nc, objc.Sel("addObserver:selector:name:object:"), uintptr(nativeMenus.target), uintptr(selGnuiWillSend), uintptr(name), 0)
		pi := objc.Send(objc.Cls("NSProcessInfo"), selProcessInfo)
		nativeMenus.appName = string(objc.AppendUTF8(nil, objc.Send(pi, selProcessName)))
	}
}

// SetMenuBar makes spec the application's menu bar, replacing the one there,
// and state the function that enables and checks the items. Main thread;
// building the menus allocates, so call it at a safe point, not every frame.
// The spec is read during the call only.
//
// Its commands arrive as this window's EvCommand. With several windows open,
// the bar is the key window's: SetWindowMenus gives each window its own, and
// installs it the moment that window becomes key.
func (w *Window) SetMenuBar(spec *MenuSpec, state MenuState) {
	setMenuBar(w.app, spec, state)
	nativeMenus.owner, nativeMenus.model = w, nil
}

// windowMenus is what SetWindowMenus gave the window.
type windowMenus struct {
	spec  *MenuSpec
	state MenuState
	model any
}

// SetWindowMenus gives the window its menu bar and the function that enables
// and checks the items of its menus: its bar's while that bar is installed,
// and its popped-up menus' (PopUpMenu) always. The installed bar is the key
// window's: this one's is installed now if the window is key, owns the bar
// already (a new spec: the bar is built again) or no open window owns it,
// and otherwise the moment the window becomes key, before any later event,
// so that a key equivalent or a menu item always acts on the key window.
// Windows with the same model (a pointer, nil for none) share the bar built
// for it: taking it over switches its commands and state only. When the
// window that owns the bar closes, the next key window's bar is installed.
// spec is kept until the next call or until the window closes; call again
// when it changes. Main thread, at a safe point: building a bar allocates.
func (w *Window) SetWindowMenus(spec *MenuSpec, state MenuState, model any) {
	w.menus = windowMenus{spec, state, model}
	if o := nativeMenus.owner; o == w || o == nil || !o.registered || objc.SendBool(w.win, selIsKeyWindow) {
		w.installMenus(o == w)
	}
}

// installMenus makes the window's bar (SetWindowMenus) the installed one: the
// one installed already, with its commands and state switched to this
// window, when it was built for the same model and rebuild is false; else a
// new build.
func (w *Window) installMenus(rebuild bool) {
	m := &w.menus
	if m.spec == nil {
		return
	}
	if !rebuild && nativeMenus.bar != 0 && m.model != nil && m.model == nativeMenus.model {
		nativeMenus.state, nativeMenus.owner = m.state, w
		return
	}
	setMenuBar(w.app, m.spec, m.state)
	nativeMenus.owner, nativeMenus.model = w, m.model
}

// adoptMenuBar installs the bar of the window the menus are for now (the key
// window, else the one an event would go to) once the bar's owner has
// closed; with none, the closed window's bar stays with no state and no
// owner (its commands are the application's or the next window's).
func adoptMenuBar() {
	if nativeMenus.owner != nil {
		return
	}
	if w := fallbackWindow(); w != nil && w.menus.spec != nil {
		w.installMenus(false)
	}
}

// UseMenuBar makes the installed bar this window's without building it
// again: its commands become this window's and state enables and checks its
// items. For a window whose menus are the same as those of the window that
// installed the bar. Main thread; allocates nothing.
func (w *Window) UseMenuBar(state MenuState) {
	nativeMenus.state, nativeMenus.owner = state, w
}

// MenuBarOwner reports whether the installed bar is this window's (SetMenuBar,
// UseMenuBar) and has not been replaced since. Main thread.
func (w *Window) MenuBarOwner() bool { return nativeMenus.owner == w && nativeMenus.bar != 0 }

// SetAppMenuBar installs the menu bar of the application with no window open
// (the app object's menus), as Window.SetMenuBar does with one: the bar is
// the application's either way. Main thread, at a safe point.
func SetAppMenuBar(spec *MenuSpec, state MenuState) {
	if appObj.started {
		setMenuBar(appObj.app, spec, state)
		nativeMenus.owner, nativeMenus.model = nil, nil
	}
}

// setMenuBar builds spec's bar and makes it NSApp's main menu, with the
// windows and help menus registered.
func setMenuBar(app objc.ID, spec *MenuSpec, state MenuState) {
	ensureMenuTarget()
	nativeMenus.state = state
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	bar := objc.Send(objc.Send(clsMenu, selAlloc), selInitPlain)
	var windows, help objc.ID
	for _, bi := range spec.Bar {
		if bi < 0 || bi >= len(spec.Menus) {
			continue
		}
		m := &spec.Menus[bi]
		sub := buildMenu(spec, bi, 0, nil)
		item := newMenuItem(m.Title, 0, 0, 0)
		objc.Send1(item, selSetSubmenu, uintptr(sub))
		objc.Send1(bar, selAddItem, uintptr(item))
		objc.Release(item)
		switch m.Role {
		case MenuRoleWindow:
			windows = sub
		case MenuRoleHelp:
			help = sub
		}
		objc.Release(sub)
	}
	objc.Send1(app, selSetMainMenu, uintptr(bar))
	if windows != 0 {
		objc.Send1(app, selSetWindowsMenu, uintptr(windows))
		listWindows(app)
	}
	if help != 0 {
		objc.Send1(app, selSetHelpMenu, uintptr(help))
	}
	if nativeMenus.bar != 0 {
		objc.Release(nativeMenus.bar)
	}
	nativeMenus.bar = bar
}

// SetFieldKeys sets the function that says which key-downs the app's focused
// text field takes itself: while such a key is sent, every menu item
// validates as disabled, so no key equivalent acts on it and it reaches the
// app as EvKey (DECISIONS D28: the focused field first). nil: none.
func (w *Window) SetFieldKeys(f FieldKeys) { w.fieldKeys = f }

// PopUpMenu shows menu p.Menu of p.Spec over the window, placed as p.Kind
// says, and returns when the user has chosen an item or dismissed it: the
// index of the chosen item in that menu, -1 for none and for an item of a
// submenu (whose command arrives all the same). A chosen command also
// arrives as EvCommand. AppKit runs its
// tracking loop meanwhile, during which the frame handler draws. Main thread,
// at a safe point (not inside the frame handler). The tree is cached: it is
// built again only when the spec's items change.
func (w *Window) PopUpMenu(p *Popup) int {
	if p.Spec == nil || p.Menu < 0 || p.Menu >= len(p.Spec.Menus) || w.inHandler {
		return -1
	}
	ensureMenuTarget()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	pm := popupFor(p.Spec, p.Menu)
	m := pm.menu
	s := float64(w.scale)
	x0, y0, x1, y1 := float64(p.X0)/s, float64(p.Y0)/s, float64(p.X1)/s, float64(p.Y1)/s
	var item objc.ID
	var x, y float64
	switch p.Kind {
	case PopupContext:
		x, y = x0, w.hPt-y0
	case PopupPullDown:
		objc.F.Reset().Arg(uintptr(m)).Arg(uintptr(selSetMinWidth)).ArgF64(x1 - x0).Call(objc.MsgSend)
		x, y = x0, w.hPt-y1-pullDownGap
	case PopupList:
		n := int(objc.Send(m, selNumberOfItems))
		for i := 0; i < n; i++ {
			it := objc.Send1(m, selItemAtIndex, uintptr(i))
			setItemState(it, i == p.Selected)
			if i == p.Selected {
				item = it
			}
		}
		if !pm.font {
			objc.Send1(m, selSetMenuFont, uintptr(controlFont()))
			pm.font = true
		}
		// The selected item's title sits over the control's: AppKit places
		// the item's top left corner at the point; the inset of its title is
		// listInsetX.
		objc.F.Reset().Arg(uintptr(m)).Arg(uintptr(selSetMinWidth)).ArgF64(x1 - x0 + listInsetX).Call(objc.MsgSend)
		x, y = x0-listInsetX+listTextX, w.hPt-(y0+y1)/2+listItemH/2
	}
	nativeMenus.chosen, nativeMenus.root, nativeMenus.popup = -1, m, w
	depth := nativeMenus.depth
	objc.F.Reset().Arg(uintptr(m)).Arg(uintptr(selPopUpMenu)).Arg(uintptr(item)).ArgPoint(x, y).Arg(uintptr(w.view)).Call(objc.MsgSend)
	nativeMenus.root, nativeMenus.popup = 0, nil
	// The tracking is over: a menu cancelled as it opened gets no
	// menuDidClose:, and must not leave the app in a native loop.
	nativeMenus.depth = min(nativeMenus.depth, depth)
	return nativeMenus.chosen
}

// Placement of popped-up menus, in points: the gap between a pull-down
// button and its menu; for list menus, the left inset of an item's title in
// a menu, the left inset of the text in the control, and the height of an
// item in the control font.
const (
	pullDownGap = 4
	listInsetX  = 30
	listTextX   = 8
	listItemH   = 22
)

// controlFont is the system font at the regular control size (13 pt), the
// font of a pop-up button's menu.
func controlFont() objc.ID {
	return objc.ID(objc.F.Reset().Arg(uintptr(clsFont)).Arg(uintptr(selSystemFontSize)).ArgF64(13).Call(objc.MsgSend).R0())
}

// popupFor returns the cached tree of menu idx of spec, built again if its
// items changed, or a new one.
func popupFor(spec *MenuSpec, idx int) *popupMenu {
	for i := range nativeMenus.popups {
		pm := &nativeMenus.popups[i]
		if pm.spec != spec || pm.idx != idx {
			continue
		}
		if k := 0; sameTree(spec, idx, pm.menus, &k, 0) {
			return pm
		}
		objc.Release(pm.menu)
		pm.menus = pm.menus[:0]
		pm.menu, pm.font = buildMenu(spec, idx, 0, &pm.menus), false
		return pm
	}
	if len(nativeMenus.popups) == maxPopups {
		objc.Release(nativeMenus.popups[0].menu)
		nativeMenus.popups = append(nativeMenus.popups[:0], nativeMenus.popups[1:]...)
	}
	nativeMenus.popups = append(nativeMenus.popups, popupMenu{spec: spec, idx: idx})
	pm := &nativeMenus.popups[len(nativeMenus.popups)-1]
	pm.menu = buildMenu(spec, idx, 0, &pm.menus)
	return pm
}

// sameTree reports whether menu idx of spec and its submenus are the menus
// recorded in menus, from position *k on, in the order buildMenu visits
// them: the same titles (a submenu item without a title of its own shows its
// menu's), roles and items.
func sameTree(spec *MenuSpec, idx int, menus []Menu, k *int, depth int) bool {
	if *k >= len(menus) || depth > maxMenuDepth {
		return false
	}
	have, want := &menus[*k], &spec.Menus[idx]
	*k++
	if have.Title != want.Title || have.Role != want.Role || len(have.Items) != len(want.Items) {
		return false
	}
	for i := range want.Items {
		it := &want.Items[i]
		if have.Items[i] != *it {
			return false
		}
		if s := it.Submenu; it.Kind == MenuItemSubmenu && s >= 0 && s < len(spec.Menus) && !sameTree(spec, s, menus, k, depth+1) {
			return false
		}
	}
	return true
}

// servicesMenu is the Services menu of the menu bar titled title, retained:
// always the same NSMenu. AppKit keeps the first menu it is given
// (setServicesMenu: with another one does nothing, even after nil) and fills
// only that one, so a rebuilt bar takes it over from the bar before, where it
// is a submenu still (a menu has one supermenu).
func servicesMenu(title string) objc.ID {
	m := nativeMenus.services
	if m == 0 {
		if m = objc.Send(nsApp, selServicesMenu); m != 0 { // given to AppKit before us
			objc.Retain(m)
		} else {
			t := newNSString(title)
			m = objc.Send1(objc.Send(clsMenu, selAlloc), selInitWithTitle, uintptr(t))
			objc.Release(t)
			objc.Send1(m, selSetDelegate, uintptr(nativeMenus.target))
			objc.Send1(nsApp, selSetServicesMenu, uintptr(m))
		}
		nativeMenus.services = m
	} else {
		if sup := objc.Send(m, selSupermenu); sup != 0 {
			if i := int(objc.Send1(sup, selIndexWithSubmenu, uintptr(m))); i >= 0 {
				objc.Send1(objc.Send1(sup, selItemAtIndex, uintptr(i)), selSetSubmenu, 0)
			}
		}
		t := newNSString(title)
		objc.Send1(m, selSetTitle, uintptr(t))
		objc.Release(t)
	}
	return objc.Retain(m)
}

// listWindows lists the application's windows in the Window menu just
// installed: AppKit adds a window to it when the window is ordered in or
// retitled, so the windows already on screen (the app's own, shown before
// its first frame installs the bar) would be missing from it, and from every
// rebuilt one, until they change.
func listWindows(app objc.ID) {
	wins := objc.Send(app, selWindows)
	for i := 0; i < int(objc.Send(wins, selArrCount)); i++ {
		win := objc.Send1(wins, selArrAt, uintptr(i))
		if objc.SendBool(win, selExcludedFromWinMenu) || !(objc.SendBool(win, selIsVisible) || objc.SendBool(win, selIsMiniaturized)) {
			continue
		}
		objc.Send3(app, selAddWindowsItem, uintptr(win), uintptr(objc.Send(win, selWinTitle)), 0)
	}
}

// maxMenuDepth bounds the nesting of submenus (a spec whose menus contain
// each other stops there).
const maxMenuDepth = 8

// buildMenu builds menu idx of spec and its submenus, retained; record, if
// not nil, gets a copy of each menu in the order they are visited (a
// popped-up menu). nil builds the menu bar, whose Services menu is the one
// every bar shares (servicesMenu).
func buildMenu(spec *MenuSpec, idx, depth int, record *[]Menu) objc.ID {
	m := &spec.Menus[idx]
	if m.Role == MenuRoleServices && record == nil {
		return servicesMenu(m.Title)
	}
	menu := objc.Send(clsMenu, selAlloc)
	t := newNSString(m.Title)
	menu = objc.Send1(menu, selInitWithTitle, uintptr(t))
	objc.Release(t)
	objc.Send1(menu, selSetDelegate, uintptr(nativeMenus.target))
	if record != nil {
		*record = append(*record, Menu{Title: m.Title, Role: m.Role, Items: append([]MenuItem(nil), m.Items...)})
	}
	for i := range m.Items {
		it := &m.Items[i]
		var item objc.ID
		switch it.Kind {
		case MenuItemSeparator:
			item = objc.Retain(objc.Send(clsMenuItem, selSeparatorItem))
		case MenuItemSubmenu:
			if it.Submenu < 0 || it.Submenu >= len(spec.Menus) || depth >= maxMenuDepth {
				continue
			}
			title := it.Title
			if title == "" {
				title = spec.Menus[it.Submenu].Title
			}
			item = newMenuItem(title, 0, 0, 0)
			sub := buildMenu(spec, it.Submenu, depth+1, record)
			objc.Send1(item, selSetSubmenu, uintptr(sub))
			objc.Release(sub)
			if it.Symbol != "" {
				if img := symbolImageOf(it.Symbol); img != 0 {
					objc.Send1(item, selSetImage, uintptr(img))
				}
			}
		default:
			item = actionItem(it)
		}
		objc.Send1(menu, selAddItem, uintptr(item))
		objc.Release(item)
	}
	return menu
}

// actionItem makes the NSMenuItem of an action item, retained.
func actionItem(it *MenuItem) objc.ID {
	title, key, mods := it.Title, it.Key, it.Mods
	if title == "" {
		title = standardTitle(it.Action)
	}
	if key == 0 && mods == 0 {
		key, mods = standardKey(it.Action)
	}
	var sel objc.SEL
	var target objc.ID
	switch {
	case it.Action == MenuNone && it.ID != 0:
		sel, target = selGnuiCommand, nativeMenus.target
	case it.Action == MenuNone:
		sel, target = selGnuiChoose, nativeMenus.target
	case int(it.Action) < len(actionSel):
		sel = actionSel[it.Action]
		if !it.Action.IsEdit() { // validated by the app, then sent on (menuSystem)
			target = nativeMenus.target
		}
	}
	item := newMenuItem(title, sel, key, mods)
	if target != 0 {
		objc.Send1(item, selSetTarget, uintptr(target))
	}
	objc.Send1(item, selSetTag, uintptr(it.ID))
	if it.Checked {
		objc.Send1(item, selSetState, 1)
	}
	if it.Symbol != "" {
		if img := symbolImageOf(it.Symbol); img != 0 {
			objc.Send1(item, selSetImage, uintptr(img))
		}
	}
	return item
}

// newMenuItem makes an NSMenuItem, retained.
func newMenuItem(title string, sel objc.SEL, key rune, mods Mods) objc.ID {
	t := newNSString(title)
	kn := 0
	if key != 0 {
		// A letter with Shift is the uppercase one, as AppKit's own Redo is
		// ("Z"): NSMenu matches an item "z" with Shift only against an event
		// whose charactersIgnoringModifiers is "z", and an uppercase item
		// against both "Z" and "z" (what a key with Shift types depends on the
		// layout and on how the event was made).
		if mods&ModShift != 0 && key >= 'a' && key <= 'z' {
			key -= 'a' - 'A'
		}
		kn = utf8.EncodeRune(keyBuf[:], key)
	}
	k := newNSStringBytes(keyBuf[:kn])
	item := objc.Send(clsMenuItem, selAlloc)
	item = objc.Send3(item, selInitItem, uintptr(t), uintptr(sel), uintptr(k))
	objc.Release(t)
	objc.Release(k)
	if key != 0 {
		objc.Send1(item, selSetKeyMask, keyMask(mods))
	}
	return item
}

// keyMask is the NSEventModifierFlags of mods.
func keyMask(m Mods) uintptr {
	var f uintptr
	if m&ModShift != 0 {
		f |= 1 << 17
	}
	if m&ModCtrl != 0 {
		f |= 1 << 18
	}
	if m&ModAlt != 0 {
		f |= 1 << 19
	}
	if m&ModSuper != 0 {
		f |= 1 << 20
	}
	return f
}

// symbolImageOf is the SF Symbol named name, or 0.
func symbolImageOf(name string) objc.ID {
	s := newNSString(name)
	img := objc.Send2(clsNSImage, selSymbolImage, uintptr(s), 0)
	objc.Release(s)
	return img
}

// newNSString makes an NSString of s, retained, without keeping a C copy.
func newNSString(s string) objc.ID {
	return newNSStringBytes(unsafe.Slice(unsafe.StringData(s), len(s)))
}

func newNSStringBytes(b []byte) objc.ID {
	alloc := objc.Send(clsString, selAlloc) // before the init call is assembled: both use the frame
	var p unsafe.Pointer
	if len(b) > 0 {
		p = unsafe.Pointer(&b[0])
	}
	str := objc.ID(objc.F.Reset().Arg(uintptr(alloc)).Arg(uintptr(selInitBytesStr)).
		ArgPtr(p).Arg(uintptr(len(b))).Arg(4 /* NSUTF8StringEncoding */).Call(objc.MsgSend).R0())
	runtime.KeepAlive(b)
	return str
}

// standardTitle is the title of a standard action whose item has none.
func standardTitle(a MenuAction) string {
	name := nativeMenus.appName
	switch a {
	case MenuAbout:
		return "About " + name
	case MenuHide:
		return "Hide " + name
	case MenuHideOthers:
		return "Hide Others"
	case MenuShowAll:
		return "Show All"
	case MenuQuit:
		return "Quit " + name
	case MenuUndo:
		return "Undo"
	case MenuRedo:
		return "Redo"
	case MenuCut:
		return "Cut"
	case MenuCopy:
		return "Copy"
	case MenuPaste:
		return "Paste"
	case MenuDelete:
		return "Delete"
	case MenuSelectAll:
		return "Select All"
	case MenuMinimize:
		return "Minimize"
	case MenuZoom:
		return "Zoom"
	case MenuClose:
		return "Close"
	case MenuBringAllToFront:
		return "Bring All to Front"
	}
	return ""
}

// standardKey is the key equivalent of a standard action whose item has none.
func standardKey(a MenuAction) (rune, Mods) {
	switch a {
	case MenuHide:
		return 'h', ModSuper
	case MenuHideOthers:
		return 'h', ModSuper | ModAlt
	case MenuQuit:
		return 'q', ModSuper
	case MenuUndo:
		return 'z', ModSuper
	case MenuRedo:
		return 'z', ModSuper | ModShift
	case MenuCut:
		return 'x', ModSuper
	case MenuCopy:
		return 'c', ModSuper
	case MenuPaste:
		return 'v', ModSuper
	case MenuSelectAll:
		return 'a', ModSuper
	case MenuMinimize:
		return 'm', ModSuper
	case MenuClose:
		return 'w', ModSuper
	}
	return 0, 0
}

// editActionOf is the edit action whose selector is sel, MenuNone if none.
func editActionOf(sel objc.SEL) MenuAction {
	for a := MenuUndo; a <= MenuSelectAll; a++ {
		if actionSel[a] == sel {
			return a
		}
	}
	return MenuNone
}

// setItemState sets the item's check mark, if it changed.
func setItemState(item objc.ID, on bool) {
	var s uintptr
	if on {
		s = 1 // NSControlStateValueOn
	}
	if uintptr(objc.Send(item, selState)) != s {
		objc.Send1(item, selSetState, s)
	}
}

// chose records the item an action came from (PopUpMenu's result: its index
// in the menu popped up, -1 for an item of a submenu) and that a menu acted
// during this event (handle drops the key-down that chose it; menuWillSend
// does that for every item).
func chose(item objc.ID) {
	nativeMenus.chosen = -1
	if item != 0 && nativeMenus.root != 0 && objc.SendBool1(item, selRespondsTo, uintptr(selMenuOfItem)) {
		if m := objc.Send(item, selMenuOfItem); m == nativeMenus.root {
			nativeMenus.chosen = int(objc.Send1(m, selIndexOfItem, uintptr(item)))
		}
	}
	if w := sending; w != nil {
		w.menuActed = true
	}
}

// itemTag is the tag of sender if it has one (a menu item, a control), else 0.
func itemTag(sender objc.ID) uintptr {
	if sender == 0 || !objc.SendBool1(sender, selRespondsTo, uintptr(selTag)) {
		return 0
	}
	return uintptr(objc.Send(sender, selTag))
}

// menuCommand is gnuiCommand:, the action of an app command: EvCommand with
// the item's tag.
func menuCommand(c *ffi.CB) {
	sender := objc.ID(c.Int(2))
	chose(sender)
	if id := itemTag(sender); id != 0 {
		if w := menuWindow(); w != nil {
			w.q.push(Event{Kind: EvCommand, Tag: uint64(id), Time: Now()})
		} else {
			appCommand(id) // no window: the application's (F3)
		}
	}
}

// menuChoose is gnuiChoose:, the action of an item without a command (a list
// popup's option): PopUpMenu returns its index.
func menuChoose(c *ffi.CB) { chose(objc.ID(c.Int(2))) }

// menuQuit is gnuiQuit: (MenuQuit): the application object's quit request
// (QuitMenu), as AppKit's own terminate: would be (appobj_darwin.go).
func menuQuit(c *ffi.CB) {
	sender := objc.ID(c.Int(2))
	chose(sender)
	requestTerminate(sender)
}

// menuSystem is GNUIMenuTarget's hide:, performZoom: … (the standard actions
// other than Quit and the edit ones): the item was validated by the app, so
// the action goes on to AppKit's own target for it, through the responder
// chain, as the item would have sent it.
func menuSystem(c *ffi.CB) {
	sender := objc.ID(c.Int(2))
	chose(sender)
	objc.Send3(nsApp, selSendActionTo, uintptr(c.Int(1)), 0, uintptr(sender))
}

// actionOf is the standard action whose selector is sel, MenuNone if none.
func actionOf(sel objc.SEL) MenuAction {
	for a := MenuAbout; int(a) < len(actionSel); a++ {
		if actionSel[a] == sel {
			return a
		}
	}
	return MenuNone
}

// systemValid is AppKit's own validation of a standard action's item: the
// object the action would go to (the key window, the application …) must
// exist and enable it.
func systemValid(item objc.ID, sel objc.SEL) bool {
	t := objc.Send3(nsApp, selTargetForAction, uintptr(sel), 0, uintptr(item))
	switch {
	case t == 0:
		return false
	case objc.SendBool1(t, selRespondsTo, uintptr(selValidateItem)):
		return objc.SendBool1(t, selValidateItem, uintptr(item))
	case objc.SendBool1(t, selRespondsTo, uintptr(selValidateUIItem)):
		return objc.SendBool1(t, selValidateUIItem, uintptr(item))
	}
	return true
}

// viewEditAction is GNUIView's undo:, redo:, cut:, copy:, paste:, delete:
// and selectAll:: EvCommand with the item's command, or with FlagStandard and
// the action for an item without one.
func viewEditAction(c *ffi.CB) {
	sender := objc.ID(c.Int(2))
	act := editActionOf(objc.SEL(c.Int(1)))
	chose(sender)
	w := viewWindow(objc.ID(c.Int(0)))
	if w == nil || act == MenuNone {
		return
	}
	if id := itemTag(sender); id != 0 {
		w.q.push(Event{Kind: EvCommand, Tag: uint64(id), Time: Now()})
	} else {
		w.q.push(Event{Kind: EvCommand, Tag: uint64(act), Flags: FlagStandard, Time: Now()})
	}
}

// menuValidate is GNUIMenuTarget's validateMenuItem:: an app command, Quit
// and the system's actions ask the app (and the system's actions AppKit's
// target as well); a list option is enabled.
func menuValidate(c *ffi.CB) {
	item := objc.ID(c.Int(2))
	sel := objc.SEL(objc.Send(item, selAction))
	id := uint16(objc.Send(item, selTag))
	switch sel {
	case selGnuiChoose:
		c.RetBool(true)
	case selGnuiCommand:
		c.RetBool(validate(item, id, MenuNone))
	case selGnuiQuit:
		c.RetBool(validate(item, id, MenuQuit))
	default:
		act := actionOf(sel)
		c.RetBool(act != MenuNone && validate(item, id, act) && systemValid(item, sel))
	}
}

// viewValidate is GNUIView's validateMenuItem:: the edit actions ask the
// app, anything else the view might be asked about is enabled.
func viewValidate(c *ffi.CB) {
	item := objc.ID(c.Int(2))
	act := editActionOf(objc.SEL(objc.Send(item, selAction)))
	if act == MenuNone {
		c.RetBool(true)
		return
	}
	c.RetBool(validate(item, uint16(objc.Send(item, selTag)), act))
}

// validate asks the app's MenuState about an item and sets its check mark.
// While the key-down being sent is the focused text field's, every item is
// disabled: no key equivalent may take it from the field.
func validate(item objc.ID, id uint16, act MenuAction) bool {
	if w := sending; w != nil && w.fieldKey {
		return false
	}
	fn := nativeMenus.state
	if p := nativeMenus.popup; p != nil && p.menus.state != nil {
		fn = p.menus.state // a popped-up menu is its window's, whichever window owns the bar
	}
	if fn == nil {
		return id != 0 || (act != MenuNone && !act.IsEdit())
	}
	on, checked := fn(id, act)
	setItemState(item, checked)
	return on
}

// menuWillSend observes NSMenuWillSendActionNotification: a menu item is
// about to send its action, whoever its target is.
func menuWillSend(*ffi.CB) {
	if w := sending; w != nil {
		w.menuActed = true
	}
}

// menuWindow is the window a command from a menu is for: the one a popped-up
// menu is over, else the one whose bar is installed, else (no window's bar
// is installed yet) the key window or the window an event would go to; nil
// with none open, when the command is the application's.
func menuWindow() *Window {
	if w := nativeMenus.popup; w != nil && w.registered {
		return w
	}
	if w := nativeMenus.owner; w != nil && w.registered {
		return w
	}
	return fallbackWindow()
}

func menuWillOpen(c *ffi.CB) {
	nativeMenus.depth++
	menuOpened++
	if menuOpenHook != nil {
		menuOpenHook(objc.ID(c.Int(2)))
	}
}

// menuOpened counts the menus opened (DebugOpenBarMenu).
var menuOpened int

// menuOpenHook, if set (tests), runs when a menu opens, inside AppKit's
// tracking loop.
var menuOpenHook func(menu objc.ID)

func menuDidClose(*ffi.CB) {
	if nativeMenus.depth > 0 {
		nativeMenus.depth--
	}
}

// menuTracking reports whether a native menu is open (AppKit's tracking loop
// runs inside sendEvent or PopUpMenu).
func menuTracking() bool { return nativeMenus.depth > 0 }
