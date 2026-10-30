package rexui

import (
	"gonativeui/icon"
	"gonativeui/menu"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// OnContextMenu pops up a native context menu for the component with root box
// id (a row's Signal.ID) when it is right-clicked (or clicked with Ctrl), and
// reports the item chosen from it in a later frame. build runs only on the
// click, to put the menu's items in *items for what was clicked: the chat's
// state, the user's labels as a submenu with check marks, enabled and disabled
// entries. The menu is native where the app has native menus
// (ui.Context.NativeMenus); elsewhere nothing opens (ContextMenu draws one).
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

// ContextMenu is the one call for a context menu on a component (the phase 8
// plan's C5): the native menu where the app has native menus (macOS,
// OnContextMenu), and a drawn menu with the same items elsewhere (Windows,
// Linux, offscreen), so an app writes one path. It takes OnContextMenu's
// arguments and reports the chosen item the same way.
//
// The drawn menu opens on a right-click at the pointer, and on Shift+F10 or
// the accessibility "show menu" action below the component. It is a popup
// overlay built inside this call, in this call's frames, until a choice, a
// press outside or Escape closes it: the arrow keys move between the items,
// Right opens a submenu and Left closes it, Enter and Space choose. Its items
// are the ones build made on the click (base.Kit.MenuItems), shown until it
// closes, so a submenu's slice must stay unchanged while it is open. It
// shows check marks, key equivalents (as hints), disabled and destructive
// items, separators and submenus; symbols are not drawn. Each item has a
// mnemonic (base.Mnemonics): its Key when it has no modifiers (Key 'N' shows
// "N" and N chooses the item), else a free letter of its title; typing it, with
// or without Alt, chooses the item while the menu is open, and it is
// underlined when the keyboard opened the menu (off macOS). A choice is reported
// in the frame it is made; a frame that does not call ContextMenu for the
// open menu's component closes it.
func ContextMenu(k *base.Kit, id ui.ID, build func(items *[]ui.MenuItem)) (item int, ok bool) {
	c := k.Context()
	if c.NativeMenus() || id == 0 {
		return OnContextMenu(k, id, build)
	}
	pop := k.Popup()
	owner := c.KeyOf(id)
	keyed := false
	if !k.Disabled() {
		s := c.Signal(id)
		keyed = s.Action&ui.ActShowMenu != 0 || (c.Focused(id) && c.TakeKey(ui.KeyF10, ui.ModShift))
		if s.RightClicked || keyed {
			items := k.MenuScratch()
			build(items)
			at := c.Rect(id)
			at = [4]float32{at[0], at[3], at[0], at[3]}
			if !keyed {
				in := c.Input()
				at = [4]float32{in.MouseX, in.MouseY, in.MouseX, in.MouseY}
			}
			k.OpenPopup(base.PopupItems, owner, 0, at)
		}
	}
	if !pop.OpenBy(base.PopupItems, owner) {
		return 0, false
	}
	k.KeepPopup()
	cm := contextMenu{k: k, chosen: -1, keyed: keyed}
	cm.panel(0, k.MenuItems(), pop.At, ui.PlaceBelow, ui.HashString(owner, "contextmenu"))
	if cm.chosen >= 0 {
		return cm.item, true
	}
	return 0, false
}

// MenuRecipe is the look of a drawn menu (ContextMenu off macOS): AppKit's
// menu metrics (theme.ListSizes' 24 pt items, 5 pt padding, 11 pt
// separators) on the raised surface, with the emphasised selection.
type MenuRecipe struct {
	// RowH is an item's height, Pad the panel's padding, SepH a separator's
	// height, RowPad an item's inset from the panel's sides, Gap the space
	// between an item's parts, all in points; Radius is the panel's corner
	// radius, RowRadius an item's, MinW the narrowest the menu is.
	RowH, Pad, SepH, RowPad, Gap, Radius, RowRadius, MinW float32
	// Selected is the highlighted item's fill; Text, Disabled, Destructive
	// and Shortcut colour an item's title and its key equivalent, OnSelected
	// a highlighted one's; Separator is a separator's line.
	Selected, Text, Disabled, Destructive, Shortcut, OnSelected, Separator Tint
}

// MenuLook is the drawn menus' recipe.
var MenuLook = MenuRecipe{
	RowH: 24, Pad: 5, SepH: 11, RowPad: 8, Gap: 6, Radius: 10, RowRadius: 6, MinW: 160,
	Selected: Of(theme.Selection), Text: Of(theme.Label), Disabled: Of(theme.Label3),
	Destructive: Of(theme.Red), Shortcut: Of(theme.Label2), OnSelected: Of(theme.OnSelection),
	Separator: Of(theme.Separator),
}

// contextMenu builds the drawn menu of one frame.
type contextMenu struct {
	k      *base.Kit
	keyed  bool // the keyboard opened the menu this frame
	chosen int  // the level the choice was made at, -1 for none
	item   int  // the chosen item's ID
}

// labelBuf holds a drawn menu's key equivalent while it is measured and
// drawn (main thread). A local array escaped to the heap through the text
// system's interface, an allocation in every frame of an open menu.
var labelBuf [48]byte

// shortcutLabel is the key equivalent of it as text ("" for none), in buf.
func shortcutLabel(buf []byte, it *ui.MenuItem) []byte {
	if it.Key == 0 {
		return buf[:0]
	}
	key := it.Key
	if key >= 'a' && key <= 'z' {
		key -= 'a' - 'A'
	}
	return menu.Shortcut{Key: key, Mods: it.Mods}.AppendLabel(buf[:0])
}

// panel builds the menu of items at nesting level, placed against anchor,
// and the submenu open from it.
func (m *contextMenu) panel(level int, items []ui.MenuItem, anchor [4]float32, place ui.Placement, okey ui.Key) {
	k := m.k
	c, r := k.Context(), k.Theme()
	pop := k.Popup()
	rc := &MenuLook
	sc := k.Scale()
	if level >= base.MaxMenuDepth {
		return
	}
	font := r.Styles[theme.StyleMenu].Font
	rowH, pad, sepH, rowPad, gap := pt(sc, rc.RowH), pt(sc, rc.Pad), pt(sc, rc.SepH), pt(sc, rc.RowPad), pt(sc, rc.Gap)
	checkW, _ := k.IconSize(icon.Check)
	chevW, chevH := k.IconSize(icon.ChevronRight)
	checkW, chevW = theme.Snap(checkW), theme.Snap(chevW)

	// the widths: the widest title and the widest key equivalent
	buf := &labelBuf
	var lw, sw float32
	hasSub := false
	for i := range items {
		it := &items[i]
		if it.Separator {
			continue
		}
		lw = max(lw, k.TextWidth(font, it.Title))
		sw = max(sw, k.TextWidthBytes(font, shortcutLabel(buf[:], it)))
		hasSub = hasSub || it.Sub != nil
	}
	lw, sw = ui.CeilText(lw), theme.Snap(sw) // a title's box is never narrower than its text
	rowW := 2*rowPad + checkW + gap + lw
	if sw > 0 {
		rowW += 2*gap + sw
	}
	if hasSub {
		rowW += gap + chevW
	}
	w := max(theme.Snap(rowW+2*pad), pt(sc, rc.MinW))
	rowW = w - 2*pad

	// the mnemonics: an item's Key without modifiers, else a letter of its title
	var mns base.Mnemonics
	for i := range items {
		it := &items[i]
		var want rune
		if it.Key < 0x80 && it.Mods == 0 {
			want = rune(it.Key)
		}
		if it.Separator {
			mns.Add("", 0)
		} else {
			mns.Add(it.Title, want)
		}
	}
	mns.Resolve()
	if !k.Disabled() && pop.Hi[level] < 0 { // the innermost open menu takes its letters
		m.letter(level, items, &mns, okey)
	}
	show := k.ShowMnemonics()

	// the panel: a menu's raised surface
	el := &r.Elevations[theme.ElevRaised]
	sh := el.Shadows[0]
	c.NextBG(theme.Over(r.Colors[theme.Raised], r.Colors[theme.Window]|0xFF))
	c.NextBorder(r.Colors[el.EdgeRole], r.Lines.Hairline)
	c.NextRadius(pt(sc, rc.Radius))
	c.NextShadow(ui.Shadow{OffY: sh.Y, Sigma: sh.Blur, Color: sh.Color(r.Colors[theme.Shadow], r.T)})
	c.NextPadding(pad, pad, pad, pad)
	c.NextAxis(ui.AxisY)
	c.NextSize(ui.Px(w), ui.Children(1))
	c.NextRole(ui.RoleMenu)
	c.NextFlags(ui.DrawBackground | ui.DrawBorder | ui.DrawShadow)
	os := c.BeginOverlay(okey, ui.OverlayPopup, anchor, place)
	if os.Dismissed {
		if level == 0 {
			m.close()
		} else {
			pop.Hi[level-1] = -1
		}
	}
	// the panel itself takes the focus when the pointer opens the menu, so
	// that no item is highlighted and Down and Up then reach the first and
	// the last item; the keyboard opens it with the first item focused
	panel := c.BoxKeyed(ui.Focusable, ui.KeyN(okey, 1), nil)
	opened := os.Rect == [4]float32{}
	focusFirst := opened && ((level == 0 && m.keyed) || (level > 0 && c.FocusVisible()))

	n := len(items)
	var nv base.PopupNav
	var first, last ui.Key
	anyFocused := false
	active := !k.Inactive()
	for i := range items {
		it := &items[i]
		if it.Separator {
			c.NextSize(ui.Pct(1), ui.Px(sepH))
			sep := c.BoxKeyed(0, ui.KeyN(okey, uint64(1000+i)), nil)
			c.Push(sep)
			theme.At(c, rowPad, theme.Snap((sepH-r.Lines.Hairline)/2), rowW-2*rowPad, r.Lines.Hairline)
			c.NextBG(rc.Separator.RGBA(r))
			c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(okey, uint64(2000+i)), nil)
			c.Pop()
			continue
		}
		key := ui.KeyN(okey, uint64(i+2))
		disabled := it.Disabled
		subOpen := it.Sub != nil && pop.Hi[level] == int32(i)
		hi := !disabled && (c.FocusKey() == key || subOpen)
		fill, fg, fg2 := uint32(0), rc.Text.RGBA(r), rc.Shortcut.RGBA(r)
		switch {
		case disabled:
			fg, fg2 = rc.Disabled.RGBA(r), rc.Disabled.RGBA(r)
		case hi && active:
			fill, fg, fg2 = rc.Selected.RGBA(r), rc.OnSelected.RGBA(r), rc.OnSelected.RGBA(r)
		case it.Destructive:
			fg = rc.Destructive.RGBA(r)
		}
		flags := ui.DrawBackground | ui.Clickable | ui.Focusable
		if disabled {
			flags = ui.Disabled
		}
		role := ui.RoleMenuItem
		var state ui.SemState
		if it.Mark != ui.MarkNone {
			role = ui.RoleMenuItemCheckbox
			if it.Mark == ui.MarkOn {
				state = ui.SemChecked
			}
		}
		if subOpen {
			state |= ui.SemExpanded
		}
		c.NextBG(fill)
		c.NextRadius(pt(sc, rc.RowRadius))
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextPadding(rowPad, 0, rowPad, 0)
		c.NextSize(ui.Px(rowW), ui.Px(rowH))
		c.NextRole(role)
		c.NextSemName(it.Title)
		c.NextSemState(state)
		c.NextSemPos(i, n)
		row := c.BoxKeyed(flags, key, nil)
		c.Push(row)
		// the mark column
		switch it.Mark {
		case ui.MarkOn:
			c.NextSize(ui.Px(checkW), ui.Px(rowH))
			k.Icon(icon.Check, fg)
		case ui.MarkMixed:
			c.NextSize(ui.Px(checkW), ui.Px(rowH))
			c.NextFont(font)
			c.NextTextColor(fg)
			c.NextAlign(ui.AlignCenter)
			c.BoxKeyed(ui.DrawText, ui.KeyN(key, 1), base.StrBytes("–"))
		default:
			c.NextSize(ui.Px(checkW), ui.Px(rowH))
			c.BoxKeyed(0, ui.KeyN(key, 1), nil)
		}
		c.NextSize(ui.Px(gap), ui.Px(rowH))
		c.BoxKeyed(0, ui.KeyN(key, 5), nil)
		c.NextSize(ui.Pct(1), ui.Px(rowH))
		c.NextFont(font)
		c.NextTextColor(fg)
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(key, 2), base.StrBytes(it.Title))
		if _, off := mns.At(i); show && off >= 0 && !disabled {
			k.MnemonicUnderline(ui.KeyN(key, 7), font, it.Title, off, rowPad+checkW+gap, 0, rowH, fg)
		}
		if lab := shortcutLabel(buf[:], it); len(lab) > 0 {
			c.NextSize(ui.Px(sw+2*gap), ui.Px(rowH))
			c.NextFont(font)
			c.NextTextColor(fg2)
			c.NextAlign(ui.AlignRight)
			c.BoxKeyed(ui.DrawText, ui.KeyN(key, 3), lab)
		} else if sw > 0 {
			c.NextSize(ui.Px(sw+2*gap), ui.Px(rowH))
			c.BoxKeyed(0, ui.KeyN(key, 3), nil)
		}
		if it.Sub != nil {
			c.NextSize(ui.Px(gap), ui.Px(rowH))
			c.BoxKeyed(0, ui.KeyN(key, 6), nil)
			c.NextSize(ui.Px(chevW), ui.Px(chevH))
			k.Icon(icon.ChevronRight, fg)
		}
		c.Pop()

		if !disabled {
			if first == 0 {
				first = key
			}
			last = key
			nv.Arrive(c, key)
			if focusFirst {
				c.SetFocus(key)
				c.SetFocusVisible(true)
				focusFirst = false
			}
			s := c.Signal(row)
			if s.Hovering && k.MouseMoved() {
				c.SetFocus(key)
				c.SetFocusVisible(false)
				if it.Sub != nil {
					k.OpenSubmenu(level, i, key)
				} else if pop.Hi[level] >= 0 {
					pop.Hi[level] = -1
					c.RequestFrame()
				}
			}
			focused := c.Focused(row)
			anyFocused = anyFocused || focused
			switch {
			case s.Clicked || s.Activated:
				if it.Sub != nil {
					k.OpenSubmenu(level, i, key)
				} else if m.chosen < 0 {
					m.chosen, m.item = level, it.ID
				}
			case focused && it.Sub != nil && c.TakeKey(ui.KeyRight, 0):
				k.OpenSubmenu(level, i, key)
			case focused && level > 0 && c.TakeKey(ui.KeyLeft, 0):
				c.SetFocus(pop.Row[level-1])
				c.SetFocusVisible(true)
				pop.Hi[level-1] = -1
				c.RequestFrame()
			}
			nv.At(c, key, focused)
		}
		if subOpen {
			c.Push(row)
			m.panel(level+1, it.Sub, c.Rect(row), ui.PlaceRight, subKey(okey, i))
			c.Pop()
		}
	}
	nv.End(c)
	if !anyFocused && c.Focused(panel) {
		switch {
		case first != 0 && c.TakeKey(ui.KeyDown, 0):
			c.SetFocus(first)
			c.SetFocusVisible(true)
		case last != 0 && c.TakeKey(ui.KeyUp, 0):
			c.SetFocus(last)
			c.SetFocusVisible(true)
		}
	}
	c.EndOverlay()
	if level == 0 && m.chosen >= 0 {
		m.close()
	}
}

// letter chooses the item of the open menu (items at level, overlay key okey)
// whose mnemonic was typed this frame, with or without Alt: an item is
// chosen, a submenu opens; when several items have the letter, the focus
// moves to the next of them instead.
func (m *contextMenu) letter(level int, items []ui.MenuItem, mns *base.Mnemonics, okey ui.Key) {
	k := m.k
	c := k.Context()
	letter, at := base.TypedMnemonic(c, 0)
	if at < 0 {
		letter, at = base.TypedMnemonic(c, ui.ModAlt)
	}
	if at < 0 {
		return
	}
	from := -1
	for i := range items {
		if c.FocusKey() == ui.KeyN(okey, uint64(i+2)) {
			from = i
		}
	}
	i, n := mns.Find(letter, from)
	if i < 0 {
		return
	}
	base.ClaimMnemonic(c, at)
	it := &items[i]
	if it.Disabled {
		return
	}
	key := ui.KeyN(okey, uint64(i+2))
	c.SetFocus(key)
	c.SetFocusVisible(true)
	switch {
	case n > 1:
		c.RequestFrame()
	case it.Sub != nil:
		k.OpenSubmenu(level, i, key)
	case m.chosen < 0:
		m.chosen, m.item = level, it.ID
	}
}

// subKey is the overlay key of the submenu of item i of the menu okey.
func subKey(okey ui.Key, i int) ui.Key { return ui.KeyN(okey, uint64(10000+i)) }

// close closes the menu and gives the focus back to the box that had it
// when the menu opened.
func (m *contextMenu) close() {
	k := m.k
	c := k.Context()
	pop := k.Popup()
	if r := pop.Restore; r != 0 {
		c.SetFocus(r)
	}
	pop.Reset()
	c.RequestFrame()
}
