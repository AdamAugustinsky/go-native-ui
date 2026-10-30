package widget

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Select builds a pop-up button that edits *sel, the index of the chosen
// option (-1 for none): rexui.Select's closed look. Clicking it, or pressing
// Enter, Space or Down with the focus on it, opens the options in a popup
// below it, with the chosen one marked and focused; Up and Down move through
// them, Enter, Space or a click choose, and Escape or a click elsewhere close
// the popup. Changed is set in the frame the choice changed. With native
// menus (ui.Context.NativeMenus) the options are the system's pop-up menu
// (rexui.Select asks for it); otherwise the open list is drawn here.
func (k *Kit) Select(label string, sel *int, options []string) Signal {
	s := rexui.Select(k.Kit, label, sel, options, rexui.SelectOpts{})
	if s.Open {
		r := k.c.Rect(s.ID)
		s.Changed = k.selectPopup(s.ID, s.Key, sel, options, r[2]-r[0])
	}
	return s.Signal
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
	rowW := max(theme.Snap(2*px.Pad+iconW+ui.CeilText(lw)), minW-2*pad)
	okey := ui.KeyN(popBase, 5000)
	opening := !c.OverlayOpen(okey)

	k.popupSurface()
	c.NextPadding(pad, pad, pad, pad)
	c.NextSize(ui.Px(rowW+2*pad), ui.Children(1))
	c.NextRole(ui.RoleList)
	c.NextSemName("Options")
	os := c.BeginOverlay(okey, ui.OverlayPopup, c.Rect(id), ui.PlaceBelow)
	if os.Dismissed {
		k.pop.Reset()
	}
	var nv base.PopupNav
	for i, o := range options {
		rkey := ui.KeyN(okey, uint64(i+1))
		row := k.optionRow(optionRow{key: rkey, label: strBytes(o), checked: i == *sel, w: rowW,
			lw: rowW - 2*px.Pad - iconW, iconW: iconW, role: ui.RoleListItem, pos: i, n: len(options)})
		nv.Arrive(c, rkey)
		if opening && i == max(*sel, 0) {
			c.SetFocus(rkey)
			c.SetFocusVisible(false)
		}
		s := c.Signal(row)
		if s.Hovering && k.MouseMoved() {
			c.SetFocus(rkey)
			c.SetFocusVisible(false)
		}
		if s.Clicked || s.Activated {
			if *sel != i {
				*sel, changed = i, true
			}
			k.pop.Reset()
			c.SetFocus(key)
			c.RequestFrame()
		}
		nv.At(c, rkey, c.Focused(row))
	}
	nv.End(c)
	c.EndOverlay()
	return changed
}
