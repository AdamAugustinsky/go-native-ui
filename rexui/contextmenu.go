package rexui

import (
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// OnContextMenu pops up a native context menu for the component with root box
// id (a row's Signal.ID) when it is right-clicked (or clicked with Ctrl), and
// reports the item chosen from it in a later frame. build runs only on the
// click, to put the menu's items in *items for what was clicked: the chat's
// state, the user's labels as a submenu with check marks, enabled and disabled
// entries. The menu is native where the app has native menus
// (ui.Context.NativeMenus); elsewhere nothing opens.
//
//	s := rexui.ChatListRow(k, label, title, preview, when, opts)
//	if item, ok := rexui.OnContextMenu(k, s.ID, func(m *[]ui.MenuItem) { *m = append(*m, ui.MenuItem{ID: 1, Title: "Archive"}) }); ok { archive(item) }
//
// A frame without a click does no work and allocates nothing.
func OnContextMenu(k *base.Kit, id ui.ID, build func(items *[]ui.MenuItem)) (item int, ok bool) {
	c := k.Context()
	if c.Signal(id).RightClicked {
		items := k.MenuScratch()
		build(items)
		in := c.Input()
		c.ContextMenu(id, in.MouseX, in.MouseY, *items)
	}
	item, out := c.ContextMenuResult(id)
	return item, out == ui.MenuChosen
}
