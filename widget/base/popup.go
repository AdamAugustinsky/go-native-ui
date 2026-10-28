package base

import (
	"gonativeui/menu"
	"gonativeui/ui"
)

// MaxMenuDepth is how deeply submenus of a popup chain nest.
const MaxMenuDepth = 4

// PopupKind is what the open popup of a window is.
type PopupKind uint8

const (
	// PopupNone: no popup is open.
	PopupNone PopupKind = iota
	// PopupMenu is a menu of the model opened from a menu bar title or a menu
	// button.
	PopupMenu
	// PopupContext is a menu of the model opened at a point.
	PopupContext
	// PopupSelect is the options of a drop-down.
	PopupSelect
	// PopupItems is a context menu made of items the app built for the
	// click (Kit.MenuItems), drawn where the app has no native menus
	// (rexui.ContextMenu). Owner is the key of the box that was clicked. A
	// frame that does not build it (KeepPopup) closes it at the next Begin,
	// so a menu whose row went away does not stay open unseen.
	PopupItems
)

// Popup is the one popup chain a window has open: a menu or a drop-down, and
// the submenu open at each level. The Kit keeps it (Kit.Popup); the
// components that open popups read and change it. A press outside the popup
// and Escape dismiss it (ui.OverlayPopup), the focus moves into it when it
// opens and back when it closes.
type Popup struct {
	// Kind is what is open.
	Kind PopupKind
	// Owner is the key of the component that opened it (a bar title, a menu
	// button, a drop-down).
	Owner ui.Key
	// Menu is the model's menu shown, for menus.
	Menu int
	// At is the anchor of a context menu, in window pixels.
	At [4]float32
	// Hi is the submenu item open at each level, -1 for none; Row the key of
	// that item's row.
	Hi  [MaxMenuDepth]int32
	Row [MaxMenuDepth]ui.Key
	// Restore is the box that had the focus when the popup opened (the text
	// field an Edit menu acts on); a choice gives the focus back to it at
	// once, so that the command reaches it in the next frame.
	Restore ui.Key
	// Bar is the key of the menu bar the open menu belongs to.
	Bar ui.Key
}

// Reset closes the chain.
func (p *Popup) Reset() {
	bar := p.Bar
	*p = Popup{Bar: bar}
	for i := range p.Hi {
		p.Hi[i] = -1
	}
}

// KeepPopup tells the kit that this frame built the open PopupItems menu;
// without it the menu closes at the next Begin.
func (k *Kit) KeepPopup() { k.popKept = true }

// IsOpen reports whether a popup is open.
func (p *Popup) IsOpen() bool { return p.Kind != PopupNone }

// OpenBy reports whether a popup of the kind opened by owner is open.
func (p *Popup) OpenBy(kind PopupKind, owner ui.Key) bool { return p.Kind == kind && p.Owner == owner }

// Popup is the window's popup chain.
func (k *Kit) Popup() *Popup { return &k.pop }

// PopupKeyBase keys the popup overlays.
var PopupKeyBase = ui.HashString(KeyRoot, "popup")

// CloseMenus closes the open menu or drop-down, if any.
func (k *Kit) CloseMenus() { k.pop.Reset() }

// MenuOpen reports whether a menu or drop-down is open.
func (k *Kit) MenuOpen() bool { return k.pop.IsOpen() }

// OpenPopup opens a popup of the kind for owner, showing menu (for menus),
// at anchor (for context menus), replacing any open one; the focus to give
// back when it closes is the one the replaced popup would have given back,
// else the focus now. It asks for a frame.
func (k *Kit) OpenPopup(kind PopupKind, owner ui.Key, menu int, at [4]float32) {
	r := k.c.FocusKey()
	if k.pop.IsOpen() {
		r = k.pop.Restore
	}
	k.pop.Reset()
	k.pop.Restore = r
	k.pop.Kind, k.pop.Owner, k.pop.Menu, k.pop.At = kind, owner, menu, at
	k.pop.Row[0] = owner
	k.c.RequestFrame()
}

// OpenContextMenu opens menu number menuIdx of the model (see menu.Model.Menus)
// as a context menu at the window position (x, y) in pixels, usually the
// ContextAt of a list that was right-clicked. A styled kit builds it at the end
// of the frame and in the frames after, until a choice or a click elsewhere
// closes it. With native menus (ui.Context.NativeMenus) the app opens the
// system's context menu after the frame instead.
func (k *Kit) OpenContextMenu(menuIdx int, x, y float32) {
	if k.menu == nil || menuIdx < 0 || menuIdx >= len(k.menu.Menus()) {
		return
	}
	if k.c.NativeMenus() {
		k.c.RequestMenu(ui.MenuRequest{Kind: ui.MenuContext, Menu: menuIdx, Rect: [4]float32{x, y, x, y}})
		return
	}
	k.OpenPopup(PopupContext, 0, menuIdx, [4]float32{x, y, x, y})
	k.pop.Row[0] = 0
}

// ChooseCommand hands a command the user picked in a drawn menu to the
// model's queue for the next frame, as a native menu item would, and closes
// the popups. The focus goes back at once to the box that had it when the menu
// opened (the menu rows have it now, and give it up only at the end of the
// next frame), so that the next frame's Begin sees the text field an edit
// command is meant for.
func (k *Kit) ChooseCommand(id menu.ID) {
	if k.npend < len(k.pend) {
		k.pend[k.npend] = id
		k.npend++
	}
	if r := k.pop.Restore; r != 0 {
		k.c.SetFocus(r)
	}
	k.pop.Reset()
	k.c.RequestFrame()
}

// OpenSubmenu opens the submenu of item i at level of the open menu, whose row
// has key row, closing the deeper ones.
func (k *Kit) OpenSubmenu(level, i int, row ui.Key) {
	if k.pop.Hi[level] != int32(i) {
		for l := level + 1; l < MaxMenuDepth; l++ {
			k.pop.Hi[l] = -1
		}
		k.pop.Hi[level], k.pop.Row[level] = int32(i), row
		k.c.RequestFrame()
	}
}

// StepMenuBar opens the menu next to the open one in the model's bar, d = ±1,
// as Left and Right do in an open menu.
func (k *Kit) StepMenuBar(d int) {
	bar := k.menu.Bar()
	if len(bar) == 0 {
		return
	}
	cur := -1
	for bi, mi := range bar {
		if mi == k.pop.Menu {
			cur = bi
		}
	}
	if cur < 0 {
		return
	}
	n := (cur + d + len(bar)) % len(bar)
	k.pop.Menu = bar[n]
	k.pop.Owner = ui.KeyN(k.pop.Bar, uint64(n))
	for l := range k.pop.Hi {
		k.pop.Hi[l] = -1
	}
	k.c.RequestFrame()
}

// PopupNav moves the focus between the items of a popup list with Up and
// Down, wrapping at the ends. The items are built in order and the next one's
// key is not known when Down is pressed, so the move is done when it arrives:
// call Arrive for each focusable item as it is built, At after its own keys,
// and End after the last.
type PopupNav struct {
	prev, first, last  ui.Key
	pendNext, wrapBack bool
}

// Arrive is called for each focusable item as it is built, before its keys.
func (n *PopupNav) Arrive(c *ui.Context, key ui.Key) {
	if n.pendNext {
		c.SetFocus(key)
		c.SetFocusVisible(true)
		n.pendNext = false
	}
}

// At takes Up and Down for the focusable item key.
func (n *PopupNav) At(c *ui.Context, key ui.Key, focused bool) {
	if focused {
		switch {
		case c.TakeKey(ui.KeyDown, 0):
			n.pendNext = true
		case c.TakeKey(ui.KeyUp, 0):
			if n.prev != 0 {
				c.SetFocus(n.prev)
				c.SetFocusVisible(true)
			} else {
				n.wrapBack = true
			}
		}
	}
	if n.first == 0 {
		n.first = key
	}
	n.prev, n.last = key, key
}

// End wraps a move past either end.
func (n *PopupNav) End(c *ui.Context) {
	if n.pendNext && n.first != 0 {
		c.SetFocus(n.first)
		c.SetFocusVisible(true)
	}
	if n.wrapBack && n.last != 0 {
		c.SetFocus(n.last)
		c.SetFocusVisible(true)
	}
}
