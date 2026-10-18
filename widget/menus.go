package widget

import (
	"gonativeui/icon"
	"gonativeui/menu"
	"gonativeui/theme"
	"gonativeui/ui"
)

// Drawn menus (Windows and Linux; on macOS the same model becomes the native
// menu bar, phase 5B). A menu is a popup overlay: a press outside it and
// Escape close it, focus moves into it when it opens and back when it closes,
// the arrow keys move between the items, Right opens a submenu and Left closes
// it, Enter and Space choose. A choice is not acted on at once: it is handed to
// the model like a native menu item, so Kit.Command sees it in the next frame,
// and a command with an edit role acts on the text field that had the focus
// when the menu opened: a click on the bar leaves the focus where it is
// (ui.KeepFocus), and a choice gives it back at once, so that the next frame
// delivers the command as an edit action of that field. (A MenuButton is a
// button and takes the focus when clicked, like any button.)

const maxMenuDepth = 4

type popupKind uint8

const (
	popNone    popupKind = iota
	popMenu              // a menu of the model opened from a bar item or a menu button
	popContext           // a menu of the model opened at a point
	popSelect            // the options of a Select
)

// popup is the one popup chain a window has open: the menu or dropdown, and
// the submenu open at each level.
type popup struct {
	kind  popupKind
	owner ui.Key
	menu  int
	at    [4]float32
	hi    [maxMenuDepth]int32  // the submenu item open at each level, -1 none
	row   [maxMenuDepth]ui.Key // the row of that item
	// restore is the box that had the focus when the menu opened (the text
	// field an Edit menu acts on); a choice gives the focus back to it at once,
	// so that the command reaches it in the next frame.
	restore ui.Key
}

func (p *popup) reset() {
	*p = popup{}
	for i := range p.hi {
		p.hi[i] = -1
	}
}

func (p *popup) open() bool { return p.kind != popNone }

var popBase = ui.HashString(keyRoot, "popup")

// CloseMenus closes the open menu or dropdown, if any.
func (k *Kit) CloseMenus() { k.pop.reset() }

// MenuOpen reports whether a menu or dropdown is open.
func (k *Kit) MenuOpen() bool { return k.pop.open() }

// OpenContextMenu opens menu number menuIdx of the model (see menu.Model.Menus)
// as a context menu at the window position (x, y) in pixels, usually the
// ContextAt of a list that was right-clicked. It is built at the end of the
// frame (Kit.End) and in the frames after, until a choice or a click elsewhere
// closes it.
func (k *Kit) OpenContextMenu(menuIdx int, x, y float32) {
	if k.menu == nil || menuIdx < 0 || menuIdx >= len(k.menu.Menus()) {
		return
	}
	r := k.popRestore()
	k.pop.reset()
	k.pop.restore = r
	k.pop.kind, k.pop.menu, k.pop.at = popContext, menuIdx, [4]float32{x, y, x, y}
	k.c.RequestFrame()
}

// contextMenu builds the open context menu, at the end of the frame.
func (k *Kit) contextMenu() {
	if k.pop.kind == popContext && k.menu != nil {
		k.menuPanel(0, k.pop.menu, k.pop.at, ui.PlaceBelow)
	}
}

// popRestore is the focus to give back when the menu that is about to open
// closes: the one the open menu will give back (a menu replaces another in the
// bar), else the focus now.
func (k *Kit) popRestore() ui.Key {
	if k.pop.open() {
		return k.pop.restore
	}
	return k.c.FocusKey()
}

// choose hands a command the user picked to the model's queue for the next
// frame and closes the menus. The focus goes back at once to the box that had
// it when the menu opened (the menu rows have it now, and give it up only at
// the end of the next frame), so that the next frame's Begin sees the text
// field an edit command is meant for.
func (k *Kit) choose(id menu.ID) {
	if k.npend < len(k.pend) {
		k.pend[k.npend] = id
		k.npend++
	}
	if r := k.pop.restore; r != 0 {
		k.c.SetFocus(r)
	}
	k.pop.reset()
	k.c.RequestFrame()
}

// roleTitle is the title a drawn menu gives a command that has only a role.
func roleTitle(r menu.Role) string {
	switch r {
	case menu.RoleAbout:
		return "About"
	case menu.RoleQuit:
		return "Quit"
	case menu.RoleHide:
		return "Hide"
	case menu.RoleHideOthers:
		return "Hide Others"
	case menu.RoleShowAll:
		return "Show All"
	case menu.RoleUndo:
		return "Undo"
	case menu.RoleRedo:
		return "Redo"
	case menu.RoleCut:
		return "Cut"
	case menu.RoleCopy:
		return "Copy"
	case menu.RolePaste:
		return "Paste"
	case menu.RoleSelectAll:
		return "Select All"
	case menu.RoleDelete:
		return "Delete"
	case menu.RoleMinimize:
		return "Minimize"
	case menu.RoleZoom:
		return "Zoom"
	case menu.RoleCloseWindow:
		return "Close Window"
	}
	return ""
}

// itemTitle is the text of a menu item.
func (k *Kit) itemTitle(it menu.Item) (title string, cmd menu.Command) {
	m := k.menu
	switch it.Kind {
	case menu.ItemSubmenu:
		title = it.Title
		if title == "" {
			title = m.Menus()[it.Submenu].Title
		}
	case menu.ItemCommand:
		cmd, _ = m.Command(it.Command)
		title = cmd.Title
		if title == "" {
			title = roleTitle(cmd.Role)
		}
	}
	return title, cmd
}

// popupSurface sets the style of the next box to the raised surface of a
// popup.
func (k *Kit) popupSurface() {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	c.NextBG(p.SurfaceRaised)
	c.NextBorder(p.Line, px.BorderW)
	c.NextRadius(px.RadiusMedium)
	c.NextShadow(ui.Shadow{OffY: 2 * k.scale, Sigma: 8 * k.scale, Color: theme.WithAlpha(p.Shadow, 3)})
	c.NextFlags(ui.DrawBackground | ui.DrawBorder | ui.DrawShadow)
}

// nav moves the focus between the items of a popup list with Up and Down,
// wrapping at the ends. The items are built in order; the next one's key is
// not known when Down is pressed, so the move is done when it arrives.
type nav struct {
	prev, first, last  ui.Key
	pendNext, wrapBack bool
}

// arrive is called for each focusable item as it is built, before its keys.
func (n *nav) arrive(c *ui.Context, key ui.Key) {
	if n.pendNext {
		c.SetFocus(key)
		c.SetFocusVisible(true)
		n.pendNext = false
	}
}

// at takes Up and Down for the focusable item key.
func (n *nav) at(c *ui.Context, key ui.Key, focused bool) {
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

// end wraps a move past either end.
func (n *nav) end(c *ui.Context) {
	if n.pendNext && n.first != 0 {
		c.SetFocus(n.first)
		c.SetFocusVisible(true)
	}
	if n.wrapBack && n.last != 0 {
		c.SetFocus(n.last)
		c.SetFocusVisible(true)
	}
}

// optionRow describes one row of a popup list: a check mark column, the
// label, an optional shortcut and a submenu chevron, in fixed widths so that
// the rows of a menu line up.
type optionRow struct {
	key              ui.Key
	label, shortcut  []byte
	checked, sub     bool
	disabled         bool
	w, lw, sw, iconW float32
	role             ui.Role
	pos, n           int
}

func (k *Kit) optionRow(o optionRow) ui.ID {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	h := theme.Snap(px.RowH)
	flags := ui.Clickable | ui.Focusable | ui.HotAnimation | ui.FocusAnimation
	hi := max(c.HotTOf(o.key), c.FocusTOf(o.key))
	fg := p.Text
	if o.disabled {
		flags = ui.Disabled
		hi = 0
		fg = p.TextDisabled
	}
	var bg uint32
	if hi > 0.004 {
		flags |= ui.DrawBackground
		bg = theme.WithAlpha(p.Selection, hi)
	}
	var state ui.SemState
	if o.checked {
		state = ui.SemChecked
	}
	c.NextBG(bg)
	c.NextRadius(px.RadiusSmall)
	c.NextSize(ui.Px(o.w), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextPadding(px.Pad, 0, px.Pad, 0)
	c.NextRole(o.role)
	c.NextSemNameBytes(o.label)
	c.NextSemState(state)
	c.NextSemPos(o.pos, o.n)
	id := c.BoxKeyed(flags, o.key, nil)
	c.Push(id)
	// the check mark column
	c.NextSize(ui.Px(o.iconW), ui.Px(h))
	if o.checked {
		f, disp := k.icons.Prepare(c, icon.Check, fg)
		c.NextSize(ui.Px(o.iconW), ui.Px(h))
		c.BoxKeyed(f, ui.KeyN(o.key, 1), disp)
	} else {
		c.BoxKeyed(0, ui.KeyN(o.key, 1), nil)
	}
	c.NextSize(ui.Px(o.lw), ui.Px(h))
	c.NextFont(k.r.Font.UI)
	c.NextTextColor(fg)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(o.key, 2), o.label)
	if o.sw > 0 {
		c.NextSize(ui.Px(o.sw+px.Gap*2), ui.Px(h))
		c.NextFont(k.r.Font.UI)
		c.NextTextColor(p.TextMuted)
		c.NextAlign(ui.AlignRight)
		c.BoxKeyed(ui.DrawText, ui.KeyN(o.key, 3), o.shortcut)
	}
	if o.sub {
		f, disp := k.icons.Prepare(c, icon.ChevronRight, fg)
		_, ih := k.icons.Size(icon.ChevronRight)
		k.center(ih, h)
		c.BoxKeyed(f|ui.FloatingY, ui.KeyN(o.key, 4), disp)
	}
	c.Pop()
	return id
}

// menuPanel builds the popup of menu mi of the model at nesting level level,
// placed against anchor, and the submenu open from it. It reports nothing: a
// choice goes to the model, a dismissal updates the popup state.
func (k *Kit) menuPanel(level, mi int, anchor [4]float32, place ui.Placement) {
	c := k.c
	m := k.menu
	px := &k.r.Px
	mn := &m.Menus()[mi]
	if level >= maxMenuDepth {
		return
	}

	// the widths: the widest title and the widest shortcut
	iconW := theme.Snap(px.IconSize)
	var lw, sw float32
	hasSub := false
	for _, it := range mn.Items {
		t, cmd := k.itemTitle(it)
		lw = max(lw, k.textW(k.r.Font.UI, t))
		if it.Kind == menu.ItemCommand {
			k.scratch = cmd.Shortcut.AppendLabel(k.scratch[:0])
			sw = max(sw, k.textWB(k.r.Font.UI, k.scratch))
		}
		hasSub = hasSub || it.Kind == menu.ItemSubmenu
	}
	lw = theme.Snap(lw + labelSlack) // snapping must not leave the widest title narrower than its text
	sw = theme.Snap(sw)
	rowW := 2*px.Pad + iconW + lw
	if sw > 0 {
		rowW += sw + 2*px.Gap
	}
	if hasSub {
		rowW += iconW
	}
	rowW = theme.Snap(rowW)
	pad := theme.Snap(px.GapSmall)

	okey := ui.KeyN(popBase, uint64(level*64+mi+1))
	k.popupSurface()
	c.NextPadding(pad, pad, pad, pad)
	c.NextSize(ui.Px(rowW+2*pad), ui.Children(1))
	c.NextRole(ui.RoleMenu)
	c.NextSemName(mn.Title)
	os := c.BeginOverlay(okey, ui.OverlayPopup, anchor, place)
	if os.Dismissed {
		if level == 0 {
			k.pop.reset()
		} else {
			k.pop.hi[level-1] = -1
		}
	}
	n := len(mn.Items)
	var nv nav
	for i, it := range mn.Items {
		if it.Kind == menu.ItemSeparator {
			c.NextSize(ui.Pct(1), ui.Px(2*pad))
			sep := c.Box(0, "sep")
			c.Push(sep)
			theme.At(c, px.Pad, pad-px.BorderW/2, rowW-2*px.Pad, px.BorderW)
			c.NextBG(k.r.Pal.Line)
			c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(okey, uint64(1000+i)), nil)
			c.Pop()
			continue
		}
		title, cmd := k.itemTitle(it)
		key := ui.KeyN(okey, uint64(i+1))
		o := optionRow{key: key, label: strBytes(title), w: rowW, lw: lw, sw: sw, iconW: iconW,
			role: ui.RoleMenuItem, pos: i, n: n}
		if it.Kind == menu.ItemCommand {
			k.scratch = cmd.Shortcut.AppendLabel(k.scratch[:0])
			o.shortcut = k.scratch
			o.checked = m.Checked(cmd.ID)
			o.disabled = !m.Enabled(cmd.ID)
			if o.checked {
				o.role = ui.RoleMenuItemCheckbox
			}
		} else {
			o.sub = true
		}
		o.disabled = o.disabled || k.off()
		row := k.optionRow(o)
		if !o.disabled {
			nv.arrive(c, key)
			s := c.Signal(row)
			if s.Hovering && k.mouseMove {
				c.SetFocus(key)
				c.SetFocusVisible(false)
				if it.Kind == menu.ItemSubmenu {
					k.openSub(level, i, key)
				} else if k.pop.hi[level] >= 0 {
					k.pop.hi[level] = -1
				}
			}
			focused := c.Focused(row)
			switch {
			case s.Clicked || s.Activated:
				if it.Kind == menu.ItemSubmenu {
					k.openSub(level, i, key)
				} else {
					k.choose(cmd.ID)
				}
			case focused && it.Kind == menu.ItemSubmenu && c.TakeKey(ui.KeyRight, 0):
				k.openSub(level, i, key)
			case focused && level > 0 && c.TakeKey(ui.KeyLeft, 0):
				c.SetFocus(k.pop.row[level-1])
				c.SetFocusVisible(true)
				k.pop.hi[level-1] = -1
			case focused && level == 0 && k.pop.kind == popMenu && c.TakeKey(ui.KeyLeft, 0):
				k.barStep(-1)
			case focused && level == 0 && k.pop.kind == popMenu && it.Kind != menu.ItemSubmenu && c.TakeKey(ui.KeyRight, 0):
				k.barStep(1)
			}
			nv.at(c, key, focused)
		}
		if it.Kind == menu.ItemSubmenu && k.pop.hi[level] == int32(i) {
			c.Push(row)
			k.menuPanel(level+1, it.Submenu, c.Rect(row), ui.PlaceRight)
			c.Pop()
		}
	}
	nv.end(c)
	c.EndOverlay()
}

// openSub opens the submenu of item i at level.
func (k *Kit) openSub(level, i int, row ui.Key) {
	if k.pop.hi[level] != int32(i) {
		for l := level + 1; l < maxMenuDepth; l++ {
			k.pop.hi[l] = -1
		}
		k.pop.hi[level], k.pop.row[level] = int32(i), row
		k.c.RequestFrame()
	}
}

// MenuBar builds the menu bar of the model's Bar menus: a row of titles that
// open their menus below them. While one menu is open, moving over another
// title opens that one, and Left and Right move between menus. With the focus
// on a title, Left and Right move along the bar and Enter, Space and Down open
// the menu.
func (k *Kit) MenuBar() Signal {
	c := k.c
	m := k.menu
	if m == nil {
		return Signal{}
	}
	p, px := &k.r.Pal, &k.r.Px
	dim := k.dim()
	bar := m.Bar()
	h := theme.Snap(px.ControlH)
	bkey := ui.HashString(c.ParentKey(), "menubar")
	k.size(ui.Children(1), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextRole(ui.RoleMenuBar)
	c.NextSemName("Menu bar")
	id := c.BoxKeyed(0, bkey, nil)
	c.Push(id)
	want := -1
	for bi, mi := range bar {
		title := m.Menus()[mi].Title
		key := ui.KeyN(bkey, uint64(bi))
		open := k.pop.kind == popMenu && k.pop.owner == key
		w := theme.Snap(k.textW(k.r.Font.UI, title) + 2*px.Pad)
		hi := max(c.HotTOf(key), c.FocusTOf(key))
		flags := k.flags() | ui.DrawText
		if !k.off() {
			flags |= ui.KeepFocus // a click on the bar does not take the focus from the field it acts on
		}
		var bg uint32
		if open || hi > 0.004 {
			flags |= ui.DrawBackground
			bg = theme.WithAlpha(p.Selection, max(hi, b2f(open)))
		}
		var state ui.SemState
		if open {
			state = ui.SemExpanded
		}
		c.NextSize(ui.Px(w), ui.Px(h))
		c.NextBG(bg)
		c.NextRadius(px.RadiusSmall)
		c.NextFont(k.r.Font.UI)
		c.NextTextColor(p.Text)
		c.NextAlign(ui.AlignCenter)
		c.NextRole(ui.RoleMenuItem)
		c.NextSemName(title)
		c.NextSemState(state)
		c.NextSemActions(ui.ActPress | ui.ActFocus | ui.ActExpand | ui.ActCollapse)
		item := c.BoxKeyed(flags, key, strBytes(title))
		if !k.off() {
			s := c.Signal(item)
			switch {
			case s.Clicked || s.Activated || s.Action&(ui.ActExpand|ui.ActShowMenu) != 0:
				if open {
					k.pop.reset()
				} else {
					want = bi
				}
			case s.Hovering && k.mouseMove && k.pop.kind == popMenu && !open && k.isBarOwner(bkey):
				want = bi
			case c.Focused(item) && !open && c.TakeKey(ui.KeyDown, 0):
				want = bi
			case c.Focused(item) && !open && c.TakeKey(ui.KeyRight, 0):
				c.SetFocus(ui.KeyN(bkey, uint64((bi+1)%len(bar))))
				c.SetFocusVisible(true)
			case c.Focused(item) && !open && c.TakeKey(ui.KeyLeft, 0):
				c.SetFocus(ui.KeyN(bkey, uint64((bi+len(bar)-1)%len(bar))))
				c.SetFocusVisible(true)
			}
		}
		if open && !k.off() {
			c.Push(item)
			k.menuPanel(0, mi, c.Rect(item), ui.PlaceBelow)
			c.Pop()
		}
	}
	c.Pop()
	if want >= 0 {
		r := k.popRestore()
		k.pop.reset()
		k.pop.restore = r
		k.pop.kind, k.pop.owner, k.pop.menu = popMenu, ui.KeyN(bkey, uint64(want)), bar[want]
		k.pop.row[0] = k.pop.owner
		k.barKey = bkey
		c.RequestFrame()
	}
	k.undim(dim)
	return Signal{ID: id}
}

// isBarOwner reports whether the open menu belongs to the bar with key bkey.
func (k *Kit) isBarOwner(bkey ui.Key) bool { return k.barKey == bkey }

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// barStep opens the menu next to the open one in the bar, d = ±1.
func (k *Kit) barStep(d int) {
	bar := k.menu.Bar()
	if len(bar) == 0 {
		return
	}
	cur := -1
	for bi, mi := range bar {
		if mi == k.pop.menu {
			cur = bi
		}
	}
	if cur < 0 {
		return
	}
	n := (cur + d + len(bar)) % len(bar)
	k.pop.menu = bar[n]
	k.pop.owner = ui.KeyN(k.barKey, uint64(n))
	for l := range k.pop.hi {
		k.pop.hi[l] = -1
	}
	k.c.RequestFrame()
}

// MenuButton builds a button that opens menu number menuIdx of the model
// below itself.
func (k *Kit) MenuButton(label string, menuIdx int) Signal {
	c := k.c
	if k.menu == nil || menuIdx < 0 || menuIdx >= len(k.menu.Menus()) {
		return Signal{}
	}
	_, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	open := k.pop.kind == popMenu && k.pop.owner == key
	s := k.ButtonIcon(icon.ChevronDown, label, ButtonDefault)
	if s.Clicked {
		if open {
			k.pop.reset()
		} else {
			r := k.popRestore()
			k.pop.reset()
			k.pop.restore = r
			k.pop.kind, k.pop.owner, k.pop.menu = popMenu, key, menuIdx
			k.pop.row[0] = key
			c.RequestFrame()
		}
	}
	if open && !k.off() {
		c.Push(s.ID)
		k.menuPanel(0, menuIdx, c.Rect(s.ID), ui.PlaceBelow)
		c.Pop()
	}
	return s
}
