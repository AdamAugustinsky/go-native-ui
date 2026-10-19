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
	dim := k.dim()
	ch := k.ChoiceState(label, sel, len(titles))
	h := theme.Snap(px.ControlH)

	k.size(ui.Pct(1), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextBorderSides(ui.SideBottom)
	c.NextBorder(p.Line, px.BorderW)
	c.NextRole(ui.RoleTabList)
	c.NextSemName(label)
	strip := c.BoxKeyed(ui.DrawBorder, ch.Key, nil)
	c.Push(strip)
	for i, title := range titles {
		o := k.ChoiceItem(&ch, i, title, hoverOn)
		font, col := k.r.Font.UI, p.TextMuted
		if o.On {
			font, col = k.r.Font.UISemi, p.Text
		}
		w := theme.Snap(k.textW(k.r.Font.UISemi, title) + 2*px.Pad)
		c.NextFont(font)
		c.NextTextColor(col)
		c.NextAlign(ui.AlignCenter)
		k.BeginChoiceItem(&ch, &o, ui.RoleTab, ui.Px(w), ui.Px(h), ui.DrawText, strBytes(title))
		if o.On { // the underline of the shown tab
			lw := theme.Snap(px.FocusRingW)
			theme.At(c, px.Pad, h-lw, w-2*px.Pad, lw)
			c.NextBG(p.Accent)
			c.NextRadius(lw / 2)
			c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(o.Key, 9100), nil)
		}
		k.decorateBox(o.Key, w, h, o.Visual, px.RadiusSmall)
		c.Pop()
	}
	c.Pop()
	k.undim(dim)
	return Signal{ID: strip, Changed: ch.Changed}
}
