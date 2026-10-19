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
// chain; the other standard actions are AppKit's own (hide:,
// performMiniaturize: …). validateMenuItem: (on GNUIMenuTarget for commands,
// on GNUIView for the edit actions) asks the app's MenuState and sets the
// check mark. Every menu has GNUIMenuTarget as its delegate: while one is
// open (menuWillOpen: … menuDidClose:) the display link runs the frame
// handler, as in a live resize, so the content keeps drawing during AppKit's
// tracking loop.
//
// A menu tree is built once per MenuSpec (SetMenuBar) and once per popped-up
// menu, cached and rebuilt only when its items change (PopUpMenu).

var (
	selInitWithTitle   = objc.Sel("initWithTitle:")
	selInitItem        = objc.Sel("initWithTitle:action:keyEquivalent:")
	selInitPlain       = objc.Sel("init")
	selAddItem         = objc.Sel("addItem:")
	selSeparatorItem   = objc.Sel("separatorItem")
	selSetSubmenu      = objc.Sel("setSubmenu:")
	selSetKeyMask      = objc.Sel("setKeyEquivalentModifierMask:")
	selSetState        = objc.Sel("setState:")
	selState           = objc.Sel("state")
	selAction          = objc.Sel("action")
	selMenuOfItem      = objc.Sel("menu")
	selIndexOfItem     = objc.Sel("indexOfItem:")
	selItemAtIndex     = objc.Sel("itemAtIndex:")
	selNumberOfItems   = objc.Sel("numberOfItems")
	selSetMainMenu     = objc.Sel("setMainMenu:")
	selSetServicesMenu = objc.Sel("setServicesMenu:")
	selSetWindowsMenu  = objc.Sel("setWindowsMenu:")
	selSetHelpMenu     = objc.Sel("setHelpMenu:")
	selSetMinWidth     = objc.Sel("setMinimumWidth:")
	selSetMenuFont     = objc.Sel("setFont:")
	selSystemFontSize  = objc.Sel("systemFontOfSize:")
	selPopUpMenu       = objc.Sel("popUpMenuPositioningItem:atLocation:inView:")
	selProcessInfo     = objc.Sel("processInfo")
	selProcessName     = objc.Sel("processName")
	selInitBytesStr    = objc.Sel("initWithBytes:length:encoding:")
	selValidateItem    = objc.Sel("validateMenuItem:")
	selMenuWillOpen    = objc.Sel("menuWillOpen:")
	selMenuDidClose    = objc.Sel("menuDidClose:")
	selGnuiCommand     = objc.Sel("gnuiCommand:")
	selGnuiChoose      = objc.Sel("gnuiChoose:")
	selGnuiQuit        = objc.Sel("gnuiQuit:")
	selGnuiWillSend    = objc.Sel("gnuiMenuWillSend:")

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
	target  objc.ID   // the GNUIMenuTarget instance
	state   MenuState // the app's validation
	bar     objc.ID   // the main menu we installed (retained)
	appName string
	depth   int // menus open now (menuWillOpen: minus menuDidClose:)
	chosen  int // the index of the item the last action came from, -1 none
	popups  []popupMenu
}

// popupMenu is a cached menu tree for PopUpMenu: the tree built for menu idx
// of spec, and a copy of the items it was built from, menu by menu in the
// order the build visited them.
type popupMenu struct {
	spec  *MenuSpec
	idx   int
	items [][]MenuItem
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
func (w *Window) SetMenuBar(spec *MenuSpec, state MenuState) {
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
	objc.Send1(w.app, selSetMainMenu, uintptr(bar))
	if windows != 0 {
		objc.Send1(w.app, selSetWindowsMenu, uintptr(windows))
	}
	if help != 0 {
		objc.Send1(w.app, selSetHelpMenu, uintptr(help))
	}
	if nativeMenus.bar != 0 {
		objc.Release(nativeMenus.bar)
	}
	nativeMenus.bar = bar
}

// PopUpMenu shows menu p.Menu of p.Spec over the window, placed as p.Kind
// says, and returns when the user has chosen an item or dismissed it: the
// index of the chosen item in that menu (an item of a submenu counts as -1),
// -1 for none. A chosen command also arrives as EvCommand. AppKit runs its
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
	nativeMenus.chosen = -1
	objc.F.Reset().Arg(uintptr(m)).Arg(uintptr(selPopUpMenu)).Arg(uintptr(item)).ArgPoint(x, y).Arg(uintptr(w.view)).Call(objc.MsgSend)
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
		if k := 0; sameTree(spec, idx, pm.items, &k, 0) {
			return pm
		}
		objc.Release(pm.menu)
		pm.items = pm.items[:0]
		pm.menu, pm.font = buildMenu(spec, idx, 0, &pm.items), false
		return pm
	}
	if len(nativeMenus.popups) == maxPopups {
		objc.Release(nativeMenus.popups[0].menu)
		nativeMenus.popups = append(nativeMenus.popups[:0], nativeMenus.popups[1:]...)
	}
	nativeMenus.popups = append(nativeMenus.popups, popupMenu{spec: spec, idx: idx})
	pm := &nativeMenus.popups[len(nativeMenus.popups)-1]
	pm.menu = buildMenu(spec, idx, 0, &pm.items)
	return pm
}

// sameTree reports whether menu idx of spec and its submenus have the items
// recorded in items, from position *k on, in the order buildMenu visits them.
func sameTree(spec *MenuSpec, idx int, items [][]MenuItem, k *int, depth int) bool {
	if *k >= len(items) || depth > maxMenuDepth {
		return false
	}
	have, want := items[*k], spec.Menus[idx].Items
	*k++
	if len(have) != len(want) {
		return false
	}
	for i := range want {
		if have[i] != want[i] {
			return false
		}
		if s := want[i].Submenu; want[i].Kind == MenuItemSubmenu && s >= 0 && s < len(spec.Menus) && !sameTree(spec, s, items, k, depth+1) {
			return false
		}
	}
	return true
}

// maxMenuDepth bounds the nesting of submenus (a spec whose menus contain
// each other stops there).
const maxMenuDepth = 8

// buildMenu builds menu idx of spec and its submenus, retained; record, if
// not nil, gets a copy of each menu's items in the order they are visited.
func buildMenu(spec *MenuSpec, idx, depth int, record *[][]MenuItem) objc.ID {
	m := &spec.Menus[idx]
	menu := objc.Send(clsMenu, selAlloc)
	t := newNSString(m.Title)
	menu = objc.Send1(menu, selInitWithTitle, uintptr(t))
	objc.Release(t)
	objc.Send1(menu, selSetDelegate, uintptr(nativeMenus.target))
	if record != nil {
		*record = append(*record, append([]MenuItem(nil), m.Items...))
	}
	if m.Role == MenuRoleServices {
		objc.Send1(nsApp, selSetServicesMenu, uintptr(menu))
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
		if it.Action == MenuQuit {
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

// chose records the item an action came from (PopUpMenu's result) and that
// a menu acted during this event (handle drops the key-down that chose it;
// menuWillSend does that for every item).
func chose(item objc.ID) {
	nativeMenus.chosen = -1
	if item != 0 && objc.SendBool1(item, selRespondsTo, uintptr(selMenuOfItem)) {
		if m := objc.Send(item, selMenuOfItem); m != 0 {
			nativeMenus.chosen = int(objc.Send1(m, selIndexOfItem, uintptr(item)))
		}
	}
	if w := theWindow.Load(); w != nil {
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
		if w := theWindow.Load(); w != nil {
			w.q.push(Event{Kind: EvCommand, Tag: uint64(id), Time: Now()})
		}
	}
}

// menuChoose is gnuiChoose:, the action of an item without a command (a list
// popup's option): PopUpMenu returns its index.
func menuChoose(c *ffi.CB) { chose(objc.ID(c.Int(2))) }

// menuQuit is gnuiQuit: (MenuQuit): EvClose, until the application object
// has a quit request of its own.
func menuQuit(c *ffi.CB) {
	chose(objc.ID(c.Int(2)))
	if w := theWindow.Load(); w != nil {
		w.q.push(Event{Kind: EvClose, Time: Now()})
	}
}

// viewEditAction is GNUIView's undo:, redo:, cut:, copy:, paste:, delete:
// and selectAll:: EvCommand with the item's command, or with FlagStandard and
// the action for an item without one.
func viewEditAction(c *ffi.CB) {
	sender := objc.ID(c.Int(2))
	act := editActionOf(objc.SEL(c.Int(1)))
	chose(sender)
	w := theWindow.Load()
	if w == nil || act == MenuNone {
		return
	}
	if id := itemTag(sender); id != 0 {
		w.q.push(Event{Kind: EvCommand, Tag: uint64(id), Time: Now()})
	} else {
		w.q.push(Event{Kind: EvCommand, Tag: uint64(act), Flags: FlagStandard, Time: Now()})
	}
}

// menuValidate is GNUIMenuTarget's validateMenuItem:.
func menuValidate(c *ffi.CB) {
	item := objc.ID(c.Int(2))
	if objc.SEL(objc.Send(item, selAction)) != selGnuiCommand {
		c.RetBool(true) // a list option, Quit
		return
	}
	c.RetBool(validate(item, uint16(objc.Send(item, selTag)), MenuNone))
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
func validate(item objc.ID, id uint16, act MenuAction) bool {
	fn := nativeMenus.state
	if fn == nil {
		return id != 0
	}
	on, checked := fn(id, act)
	setItemState(item, checked)
	return on
}

// menuWillSend observes NSMenuWillSendActionNotification: a menu item is
// about to send its action, whoever its target is.
func menuWillSend(*ffi.CB) {
	if w := theWindow.Load(); w != nil {
		w.menuActed = true
	}
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
