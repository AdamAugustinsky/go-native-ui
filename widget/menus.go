package widget

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Drawn menus (Windows and Linux, and benches and offscreen renders; on macOS
// the app shows the same model natively, ui.Context.NativeMenus, and the
// builders below ask for native menus instead). A menu is a popup overlay: a press outside it and
// Escape close it, focus moves into it when it opens and back when it closes,
// the arrow keys move between the items, Right opens a submenu and Left closes
// it, Enter and Space choose. A choice is not acted on at once: it is handed to
// the model like a native menu item, so Kit.Command sees it in the next frame,
// and a command with an edit role acts on the text field that had the focus
// when the menu opened: a click on the bar leaves the focus where it is
// (ui.KeepFocus), and a choice gives it back at once, so that the next frame
// delivers the command as an edit action of that field. (A MenuButton is a
// button and takes the focus when clicked, like any button.)

var popBase = base.PopupKeyBase

// contextMenu builds the open context menu, at the end of the frame.
func (k *Kit) contextMenu() {
	if k.pop.Kind == base.PopupContext && k.Menu() != nil {
		k.menuPanel(0, k.pop.Menu, k.pop.At, ui.PlaceBelow)
	}
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

// menuPanel shares Rex's drawn-menu implementation.
func (k *Kit) menuPanel(level, mi int, anchor [4]float32, place ui.Placement) {
	rexui.DrawMenu(k.Kit, mi, anchor, place)
}

// MenuBar builds the menu bar of the model's Bar menus: a row of titles that
// open their menus below them. While one menu is open, moving over another
// title opens that one, and Left and Right move between menus. With the focus
// on a title, Left and Right move along the bar and Enter, Space and Down open
// the menu. With native menus (macOS, ui.Context.NativeMenus) the bar is the
// system's and MenuBar builds nothing.
func (k *Kit) MenuBar() Signal {
	c := k.c
	m := k.Menu()
	if m == nil || c.NativeMenus() {
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
		open := k.pop.Kind == base.PopupMenu && k.pop.Owner == key
		w := theme.Snap(k.textW(k.r.Font.UI, title) + 2*px.Pad)
		hi := max(c.HotTOf(key), c.FocusTOf(key))
		flags := k.InteractFlags(hoverOn) | ui.DrawText
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
					k.pop.Reset()
				} else {
					want = bi
				}
			case s.Hovering && k.MouseMoved() && k.pop.Kind == base.PopupMenu && !open && k.pop.Bar == bkey:
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
		k.OpenPopup(base.PopupMenu, ui.KeyN(bkey, uint64(want)), bar[want], [4]float32{})
		k.pop.Bar = bkey
	}
	k.undim(dim)
	return Signal{ID: id}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// MenuButton builds a button that opens menu number menuIdx of the model
// below itself: a pull-down, native with native menus (ui.Context.NativeMenus).
func (k *Kit) MenuButton(label string, menuIdx int) Signal {
	c := k.c
	if m := k.Menu(); m == nil || menuIdx < 0 || menuIdx >= len(m.Menus()) {
		return Signal{}
	}
	_, ident := ui.SplitLabel(label)
	key := ui.HashString(c.ParentKey(), ident)
	open := k.pop.Kind == base.PopupMenu && k.pop.Owner == key
	s := k.ButtonIcon(icon.ChevronDown, label, ButtonDefault)
	if c.NativeMenus() {
		if s.Clicked {
			c.RequestMenu(ui.MenuRequest{Kind: ui.MenuPullDown, Owner: key, Menu: menuIdx, Rect: c.Rect(s.ID)})
		}
		return s
	}
	if s.Clicked {
		if open {
			k.pop.Reset()
		} else {
			k.OpenPopup(base.PopupMenu, key, menuIdx, [4]float32{})
		}
	}
	if open && !k.off() {
		c.Push(s.ID)
		k.menuPanel(0, menuIdx, c.Rect(s.ID), ui.PlaceBelow)
		c.Pop()
	}
	return s
}
