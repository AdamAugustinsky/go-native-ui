package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// Tabs builds a strip of tabs, one per title, that edits *sel (the index of the
// shown tab). The app builds the content of the selected tab below it. The
// selected tab is the strip's tab stop; the Left and Right arrow keys move the
// selection and the focus together, wrapping at the ends. Changed is set in the
// frame the selection changed.
func (k *Kit) Tabs(label string, sel *int, titles []string) Signal {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	_, ident := ui.SplitLabel(label)
	dim := k.dim()
	skey := ui.HashString(c.ParentKey(), ident)
	n := len(titles)
	stop := *sel
	if stop < 0 || stop >= n {
		stop = 0
	}
	h := theme.Snap(px.ControlH)
	var changed bool
	if !k.off() {
		changed = k.chooseByInput(skey, sel, n)
	}

	k.size(ui.Pct(1), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextBorderSides(ui.SideBottom)
	c.NextBorder(p.Line, px.BorderW)
	c.NextRole(ui.RoleTabList)
	c.NextSemName(label)
	strip := c.BoxKeyed(ui.DrawBorder, skey, nil)
	c.Push(strip)
	for i, title := range titles {
		key := ui.KeyN(skey, uint64(i))
		on := i == *sel
		v := k.vis(key)
		flags := k.flags() | ui.DrawText
		if i != stop && !k.off() {
			flags &^= ui.Focusable
		}
		font, col := k.r.Font.UI, p.TextMuted
		var state ui.SemState
		if on {
			font, col, state = k.r.Font.UISemi, p.Text, ui.SemSelected
		}
		w := k.textW(k.r.Font.UISemi, title) + 2*px.Pad
		c.NextSize(ui.Px(theme.Snap(w)), ui.Px(h))
		c.NextFont(font)
		c.NextTextColor(col)
		c.NextAlign(ui.AlignCenter)
		c.NextRole(ui.RoleTab)
		c.NextSemName(title)
		c.NextSemState(state)
		c.NextSemPos(i, n)
		id := c.BoxKeyed(flags, key, strBytes(title))
		c.Push(id)
		if on { // the underline of the shown tab
			lw := theme.Snap(px.FocusRingW)
			theme.At(c, px.Pad, h-lw, theme.Snap(w)-2*px.Pad, lw)
			c.NextBG(p.Accent)
			c.NextRadius(lw / 2)
			c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 9100), nil)
		}
		k.decorateBox(key, theme.Snap(w), h, v, px.RadiusSmall)
		c.Pop()
	}
	c.Pop()
	k.undim(dim)
	return Signal{ID: strip, Changed: changed}
}
