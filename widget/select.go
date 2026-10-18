package widget

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
)

// selectDefaultPt is the width of a Select that sets none, in points.
const selectDefaultPt = 160

// Select builds a drop-down that edits *sel, the index of the chosen option
// (-1 for none). Clicking it, or pressing Enter, Space or Down with the focus
// on it, opens the options in a popup below it, with the chosen one marked and
// focused; Up and Down move through them, Enter, Space or a click choose, and
// Escape or a click elsewhere close the popup. Changed is set in the frame the
// choice changed.
func (k *Kit) Select(label string, sel *int, options []string) Signal {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	display, ident := ui.SplitLabel(label)
	dim := k.dim()
	key := ui.HashString(c.ParentKey(), ident)
	open := k.pop.kind == popSelect && k.pop.owner == key
	h := theme.Snap(px.ControlH)
	v := k.vis(key)
	cur := ""
	if *sel >= 0 && *sel < len(options) {
		cur = options[*sel]
	}
	fg := p.Text
	var state ui.SemState
	if open {
		state = ui.SemExpanded
	}
	k.size(k.Fixed(selectDefaultPt), ui.Px(h))
	c.NextBG(p.ControlFill)
	c.NextBorder(p.ControlBorder, px.BorderW)
	c.NextRadius(px.RadiusMedium)
	c.NextAxis(ui.AxisX)
	c.NextPadding(px.Pad, 0, px.Pad, 0)
	c.NextRole(ui.RoleComboBox)
	c.NextSemName(display)
	c.NextSemValueBytes(strBytes(cur))
	c.NextSemState(state)
	id := c.BoxKeyed(k.flags()|ui.DrawBackground|ui.DrawBorder|ui.Clip, key, nil)
	c.Push(id)
	r := c.Rect(id)
	w := r[2] - r[0]
	chev := theme.Snap(px.IconSize)
	c.NextSize(ui.Px(max(w-2*px.Pad-chev, 0)), ui.Px(h))
	c.NextFont(k.r.Font.UI)
	c.NextTextColor(fg)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(key, 1), strBytes(cur))
	f, disp := k.icons.Prepare(c, icon.ChevronDown, p.TextMuted)
	_, ih := k.icons.Size(icon.ChevronDown)
	k.center(ih, h)
	c.BoxKeyed(f|ui.FloatingY, ui.KeyN(key, 2), disp)
	k.decorate(id, v, px.RadiusMedium)
	c.Pop()

	var changed bool
	if !k.off() {
		s := c.Signal(id)
		switch {
		case s.Clicked || s.Activated || s.Action&(ui.ActExpand|ui.ActShowMenu) != 0:
			if open {
				k.pop.reset()
			} else {
				k.pop.reset()
				k.pop.kind, k.pop.owner = popSelect, key
				open = true
				c.RequestFrame()
			}
		case c.Focused(id) && !open && c.TakeKey(ui.KeyDown, 0):
			k.pop.reset()
			k.pop.kind, k.pop.owner = popSelect, key
			open = true
			c.RequestFrame()
		}
	}
	if open && !k.off() {
		changed = k.selectPopup(id, key, sel, options, w)
	}
	sig := k.signal(id)
	sig.Changed = changed
	k.undim(dim)
	return sig
}

// selectPopup builds the options of the open Select below its box id.
func (k *Kit) selectPopup(id ui.ID, key ui.Key, sel *int, options []string, minW float32) (changed bool) {
	c := k.c
	px := &k.r.Px
	iconW := theme.Snap(px.IconSize)
	var lw float32
	for _, o := range options {
		lw = max(lw, k.textW(k.r.Font.UI, o))
	}
	pad := theme.Snap(px.GapSmall)
	rowW := max(theme.Snap(2*px.Pad+iconW+lw), minW-2*pad)
	okey := ui.KeyN(popBase, 5000)
	opening := !c.OverlayOpen(okey)

	k.popupSurface()
	c.NextPadding(pad, pad, pad, pad)
	c.NextSize(ui.Px(rowW+2*pad), ui.Children(1))
	c.NextRole(ui.RoleList)
	c.NextSemName("Options")
	os := c.BeginOverlay(okey, ui.OverlayPopup, c.Rect(id), ui.PlaceBelow)
	if os.Dismissed {
		k.pop.reset()
	}
	var nv nav
	for i, o := range options {
		rkey := ui.KeyN(okey, uint64(i+1))
		row := k.optionRow(optionRow{key: rkey, label: strBytes(o), checked: i == *sel, w: rowW,
			lw: rowW - 2*px.Pad - iconW, iconW: iconW, role: ui.RoleListItem, pos: i, n: len(options)})
		nv.arrive(c, rkey)
		if opening && i == max(*sel, 0) {
			c.SetFocus(rkey)
			c.SetFocusVisible(false)
		}
		s := c.Signal(row)
		if s.Hovering && k.mouseMove {
			c.SetFocus(rkey)
			c.SetFocusVisible(false)
		}
		if s.Clicked || s.Activated {
			if *sel != i {
				*sel, changed = i, true
			}
			k.pop.reset()
			c.SetFocus(key)
			c.RequestFrame()
		}
		nv.at(c, rkey, c.Focused(row))
	}
	nv.end(c)
	c.EndOverlay()
	return changed
}
