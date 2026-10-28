//go:build darwin

package tray

import (
	"runtime"
	"unsafe"

	"gonativeui/app"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/menu"
	"gonativeui/platform"
)

// macOS: an NSStatusItem per Item, with a target object of our own class
// (GNUITrayTarget, one instance per item, whose Index is the item's serial)
// for the button's click, the menu's commands and their validation. The
// button's click and the menu's commands are delivered as the application
// object's events: a command as the app's EvCommand (platform.DeliverAppCommand,
// as the Dock menu's), a click as an EvService of platform.SourceTray through a
// small ring (the status item's action runs inside AppKit's tracking, so it
// is not delivered synchronously).
//
// The menu is built by platform.BuildCommandMenu, the same builder as the
// menu bar's and the Dock's; its command items are retargeted at ours.

// Everything here runs on the main thread, between frames or at a safe point.

const (
	nsVariableStatusItemLength = -1.0
	nsLeftMouseUp              = 2
	nsRightMouseUp             = 4
	nsEventMaskLeftMouseUp     = 1 << nsLeftMouseUp
	nsEventMaskRightMouseUp    = 1 << nsRightMouseUp
	nsControlKeyMask           = 1 << 18
	nsImageOnly                = 1
	nsImageLeft                = 2
)

var (
	selClick       = objc.Sel("gnuiTrayClick:")
	selCommand     = objc.Sel("gnuiTrayCommand:")
	selValidate    = objc.Sel("validateMenuItem:")
	selAlloc       = objc.Sel("alloc")
	selInit        = objc.Sel("init")
	selSystemBar   = objc.Sel("systemStatusBar")
	selStatusItem  = objc.Sel("statusItemWithLength:")
	selRemoveItem  = objc.Sel("removeStatusItem:")
	selButton      = objc.Sel("button")
	selSetTarget   = objc.Sel("setTarget:")
	selSetAction   = objc.Sel("setAction:")
	selSendActOn   = objc.Sel("sendActionOn:")
	selSetImage    = objc.Sel("setImage:")
	selSetImgPos   = objc.Sel("setImagePosition:")
	selSetTitle    = objc.Sel("setTitle:")
	selSetToolTip  = objc.Sel("setToolTip:")
	selSetMenu     = objc.Sel("setMenu:")
	selSetVisible  = objc.Sel("setVisible:")
	selIsVisible   = objc.Sel("isVisible")
	selSetTemplate = objc.Sel("setTemplate:")
	selSetSize     = objc.Sel("setSize:")
	selSize        = objc.Sel("size")
	selPerformClk  = objc.Sel("performClick:")
	selCurrentEv   = objc.Sel("currentEvent")
	selType        = objc.Sel("type")
	selModFlags    = objc.Sel("modifierFlags")
	selTag         = objc.Sel("tag")
	selSetState    = objc.Sel("setState:")
	selSymbolImage = objc.Sel("imageWithSystemSymbolName:accessibilityDescription:")
	selInitData    = objc.Sel("initWithData:")
	selDataBytes   = objc.Sel("dataWithBytes:length:")
)

var tr struct {
	registered bool
	def        *objc.ClassDef
	cls        objc.ID
	ring       *platform.Ring
	items      map[uint32]*Item
	next       uint32
}

// register defines the target class and makes the click ring, once.
func register() {
	if tr.registered {
		return
	}
	tr.registered = true
	tr.items = map[uint32]*Item{}
	tr.ring = platform.NewRing(platform.SourceTray, 16, 64)
	d := objc.Define("GNUITrayTarget", objc.Cls("NSObject"), []string{"NSMenuItemValidation"})
	d.Method(selClick, "v@:@", click)
	d.Method(selCommand, "v@:@", command)
	d.Method(selValidate, "c@:@", validate)
	tr.def, tr.cls = d, d.Register()
}

// Item is one status item.
type Item struct {
	serial    uint32
	si        objc.ID // the NSStatusItem, retained
	button    objc.ID
	target    objc.ID // retained
	menu      objc.ID // the NSMenu, retained, 0 for none
	model     *menu.Model
	image     objc.ID // retained, 0 for none
	title     string
	rightMenu bool
	removed   bool
}

func itemOf(self objc.ID) *Item { return tr.items[uint32(tr.def.Index().Get(self))] }

// New creates a status item in the system's status bar.
func New(o Options) (*Item, error) {
	if !platform.AppStarted() {
		return nil, ErrNotStarted
	}
	register()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	img, err := makeImage(o.Icon)
	if err != nil {
		return nil, err
	}
	bar := objc.Send(objc.Cls("NSStatusBar"), selSystemBar)
	si := objc.ID(objc.F.Reset().Arg(uintptr(bar)).Arg(uintptr(selStatusItem)).ArgF64(nsVariableStatusItemLength).Call(objc.MsgSend).R0())
	if si == 0 {
		releaseNonZero(img)
		return nil, ErrUnsupported
	}
	objc.Retain(si)
	button := objc.Send(si, selButton)
	if button == 0 { // not seen since macOS 10.10; never half-made
		objc.Send1(bar, selRemoveItem, uintptr(si))
		objc.Release(si)
		releaseNonZero(img)
		return nil, ErrUnsupported
	}
	tr.next++
	t := &Item{serial: tr.next, si: si, button: button, image: img, title: o.Title, rightMenu: o.RightClickMenu}
	t.target = objc.Send(objc.Send(tr.cls, selAlloc), selInit)
	tr.def.Index().Set(t.target, uintptr(t.serial))
	tr.items[t.serial] = t
	objc.Send1(button, selSetTarget, uintptr(t.target))
	objc.Send1(button, selSetAction, uintptr(selClick))
	objc.Send1(button, selSendActOn, nsEventMaskLeftMouseUp|nsEventMaskRightMouseUp)
	if img != 0 {
		objc.Send1(button, selSetImage, uintptr(img))
	}
	t.layout()
	if o.Title != "" {
		objc.Send1(button, selSetTitle, uintptr(objc.Str(o.Title)))
	}
	if o.Tooltip != "" {
		objc.Send1(button, selSetToolTip, uintptr(objc.Str(o.Tooltip)))
	}
	if o.Hidden {
		objc.Send1(si, selSetVisible, 0)
	}
	return t, nil
}

func releaseNonZero(id objc.ID) {
	if id != 0 {
		objc.Release(id)
	}
}

// layout places the image and the title beside each other.
func (t *Item) layout() {
	pos := uintptr(nsImageOnly)
	if t.title != "" {
		pos = nsImageLeft
	}
	objc.Send1(t.button, selSetImgPos, pos)
}

// makeImage makes the NSImage of ic, retained; 0 when ic has none.
func makeImage(ic Icon) (objc.ID, error) {
	var img objc.ID
	switch {
	case ic.Symbol != "":
		img = objc.Send2(objc.Cls("NSImage"), selSymbolImage, uintptr(objc.Str(ic.Symbol)), 0)
		if img == 0 {
			return 0, ErrImage
		}
		objc.Retain(img)
	case len(ic.PNG) > 0:
		data := objc.Send2(objc.Cls("NSData"), selDataBytes, uintptr(unsafe.Pointer(&ic.PNG[0])), uintptr(len(ic.PNG)))
		runtime.KeepAlive(ic.PNG)
		img = objc.Send1(objc.Send(objc.Cls("NSImage"), selAlloc), selInitData, uintptr(data))
		if img == 0 {
			return 0, ErrImage
		}
		h := ic.Size
		if h <= 0 {
			h = DefaultSize
		}
		if w0, h0 := objc.SendPoint(img, selSize); w0 > 0 && h0 > 0 {
			objc.F.Reset().Arg(uintptr(img)).Arg(uintptr(selSetSize)).ArgPoint(h*w0/h0, h).Call(objc.MsgSend)
		}
	default:
		return 0, nil
	}
	if ic.Template || ic.Symbol != "" {
		objc.Send1(img, selSetTemplate, 1)
	}
	return img, nil
}

// SetIcon changes the image; an Icon with neither Symbol nor PNG removes it.
func (t *Item) SetIcon(ic Icon) error {
	if t.removed {
		return ErrRemoved
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	img, err := makeImage(ic)
	if err != nil {
		return err
	}
	objc.Send1(t.button, selSetImage, uintptr(img))
	releaseNonZero(t.image)
	t.image = img
	return nil
}

// SetTitle changes the text beside the image ("" none).
func (t *Item) SetTitle(s string) error {
	if t.removed {
		return ErrRemoved
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	t.title = s
	objc.Send1(t.button, selSetTitle, uintptr(objc.Str(s)))
	t.layout()
	return nil
}

// SetTooltip changes the tooltip ("" none).
func (t *Item) SetTooltip(s string) error {
	if t.removed {
		return ErrRemoved
	}
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	var ns objc.ID
	if s != "" {
		ns = objc.Str(s)
	}
	objc.Send1(t.button, selSetToolTip, uintptr(ns))
	return nil
}

// SetVisible shows or hides the item, keeping it.
func (t *Item) SetVisible(on bool) error {
	if t.removed {
		return ErrRemoved
	}
	var v uintptr
	if on {
		v = 1
	}
	objc.Send1(t.si, selSetVisible, v)
	return nil
}

// Visible reports whether the item is shown now: not hidden by SetVisible or
// Options.Hidden (or removed).
func (t *Item) Visible() bool { return !t.removed && objc.SendBool(t.si, selIsVisible) }

// SetMenu makes menu idx of m the item's menu, replacing the one it had; a
// nil m removes the menu. The menu is built now; the model's SetEnabled and
// SetChecked decide the state of its items each time it opens. See the package
// documentation for how the menu and the clicks share the item.
func (t *Item) SetMenu(m *menu.Model, idx int) error {
	if t.removed {
		return ErrRemoved
	}
	var built objc.ID
	if m != nil {
		ms := m.Menus()
		if idx < 0 || idx >= len(ms) {
			return ErrMenu
		}
		spec := specOf(m)
		built = objc.ID(platform.BuildCommandMenu(&spec, idx, uintptr(t.target), uintptr(selCommand)))
		if built == 0 {
			return ErrMenu
		}
	}
	if !t.rightMenu {
		objc.Send1(t.si, selSetMenu, uintptr(built))
	}
	releaseNonZero(t.menu)
	t.menu, t.model = built, m
	return nil
}

// Clicked decodes a click on this item from an app event.
func (t *Item) Clicked(e *app.Event) (Click, bool) {
	if e.Kind != app.EvService || e.Source != platform.SourceTray || uint32(e.A) != t.serial {
		return Click{}, false
	}
	return Click{Right: e.B&1 != 0}, true
}

// Balloon shows a balloon from the icon (Windows' NIF_INFO). macOS has
// none: it returns ErrNoBalloon; post a notification (app/notify).
func (t *Item) Balloon(title, body string) error { return ErrNoBalloon }

// Remove takes the item off the status bar and frees it. Later calls return
// ErrRemoved; a second Remove does nothing.
func (t *Item) Remove() {
	if t.removed {
		return
	}
	t.removed = true
	delete(tr.items, t.serial)
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	objc.Send1(t.si, selSetMenu, 0)
	objc.Send1(t.button, selSetTarget, 0)
	objc.Send1(objc.Send(objc.Cls("NSStatusBar"), selSystemBar), selRemoveItem, uintptr(t.si))
	objc.Release(t.si)
	objc.Release(t.target)
	releaseNonZero(t.menu)
	releaseNonZero(t.image)
	t.menu, t.image, t.model = 0, 0, nil
}

// click is the button's action: sender is the button.
func click(c *ffi.CB) {
	t := itemOf(objc.ID(c.Int(0)))
	if t == nil || t.removed {
		return
	}
	right := false
	if ev := objc.Send(objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication")), selCurrentEv); ev != 0 {
		typ := uintptr(objc.Send(ev, selType))
		right = typ == nsRightMouseUp || (typ == nsLeftMouseUp && uintptr(objc.Send(ev, selModFlags))&nsControlKeyMask != 0)
	}
	if right && t.rightMenu && t.menu != 0 {
		// A status item shows its menu only while it has one: attached for
		// the click, and taken off again so that the next left click is ours.
		objc.Send1(t.si, selSetMenu, uintptr(t.menu))
		objc.Send1(t.button, selPerformClk, 0)
		objc.Send1(t.si, selSetMenu, 0)
		return
	}
	var b int64
	if right {
		b = 1
	}
	tr.ring.Offer(int64(t.serial), b, nil)
}

// command is a menu command's action: sender is the NSMenuItem.
func command(c *ffi.CB) {
	if t := itemOf(objc.ID(c.Int(0))); t != nil && !t.removed {
		if id := uint16(objc.Send(objc.ID(c.Int(2)), selTag)); id != 0 {
			platform.DeliverAppCommand(id)
		}
	}
}

// validate is validateMenuItem: for the items that target us: the model
// enables and checks them.
func validate(c *ffi.CB) {
	t := itemOf(objc.ID(c.Int(0)))
	item := objc.ID(c.Int(2))
	if t == nil || t.model == nil || item == 0 {
		c.RetBool(true)
		return
	}
	id := menu.ID(objc.Send(item, selTag))
	var st uintptr
	if t.model.Checked(id) {
		st = 1
	}
	objc.Send1(item, selSetState, st)
	c.RetBool(t.model.Enabled(id))
}

// specOf is the platform's form of the model's menus: commands (whatever
// their roles) as items that deliver their ids.
func specOf(m *menu.Model) platform.MenuSpec {
	var spec platform.MenuSpec
	for _, mm := range m.Menus() {
		items := make([]platform.MenuItem, 0, len(mm.Items))
		for _, it := range mm.Items {
			switch it.Kind {
			case menu.ItemSeparator:
				if n := len(items); n > 0 && items[n-1].Kind != platform.MenuItemSeparator {
					items = append(items, platform.MenuItem{Kind: platform.MenuItemSeparator})
				}
			case menu.ItemSubmenu:
				items = append(items, platform.MenuItem{Kind: platform.MenuItemSubmenu, Submenu: it.Submenu, Title: it.Title})
			case menu.ItemCommand:
				cmd, ok := m.Command(it.Command)
				if !ok {
					continue
				}
				title := cmd.Title
				if title == "" {
					title = menu.RoleTitle(cmd.Role)
				}
				if title == "" {
					continue
				}
				items = append(items, platform.MenuItem{ID: uint16(cmd.ID), Title: title, Symbol: cmd.Symbol})
			}
		}
		if n := len(items); n > 0 && items[n-1].Kind == platform.MenuItemSeparator {
			items = items[:n-1]
		}
		spec.Menus = append(spec.Menus, platform.Menu{Title: mm.Title, Items: items})
	}
	return spec
}
