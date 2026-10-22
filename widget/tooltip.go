package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// DefaultTooltipDelayNs is how long the pointer rests on a widget before its
// tooltip shows.
const DefaultTooltipDelayNs = base.DefaultTooltipDelayNs

// Tooltip shows text beside the widget id, which must have been built in this
// frame and take pointer input (a button, a field, a list; a plain label does
// not). The tooltip appears once the pointer has rested on the widget for
// TooltipDelayNs, fades in, and goes when the pointer leaves it or a button is
// pressed. It is an overlay: it is drawn above everything, takes no input and
// never takes the focus. Call it right after the widget.
//
// Where the window system shows tooltips itself (macOS: ui.Context.NativeTooltips)
// nothing is drawn: the text is declared for the frame, and the system owns the
// delay, the look and the placement. Disabled widgets show tooltips too,
// without becoming hot or taking input.
func (k *Kit) Tooltip(id ui.ID, text string) {
	c := k.c
	if text == "" {
		return
	}
	if c.NativeTooltips() {
		if id != 0 {
			c.NativeTooltip(id, text)
		}
		return
	}
	key, due := k.TooltipDue(id)
	if !due {
		return
	}
	p, px := &k.r.Pal, &k.r.Px
	tipKeyBase := base.TooltipKeyBase
	r := c.Rect(id)
	anchor := [4]float32{r[0], r[1], r[2], r[3] + px.GapSmall}
	t := c.AnimateValueFrom(ui.KeyN(tipKeyBase, uint64(key)), 0, 1, k.r.Motion.Fade)
	c.PushOpacity(t)
	c.NextBG(p.SurfaceRaised)
	c.NextBorder(p.Line, px.BorderW)
	c.NextRadius(px.RadiusMedium)
	c.NextPadding(px.Pad, px.GapSmall, px.Pad, px.GapSmall)
	c.NextShadow(ui.Shadow{OffY: 2 * k.scale, Sigma: 6 * k.scale, Color: theme.WithAlpha(p.Shadow, 3)})
	c.NextFlags(ui.DrawBackground | ui.DrawBorder | ui.DrawShadow)
	c.NextRole(ui.RoleTooltip)
	c.NextSemName(text)
	c.NextSize(ui.Children(1), ui.Children(1))
	c.BeginOverlay(ui.KeyN(tipKeyBase, uint64(key)), ui.OverlayTooltip, anchor, ui.PlaceBelow)
	c.NextFont(k.r.Font.Small)
	c.NextTextColor(p.Text)
	c.NextSize(ui.TextSize(0), ui.TextSize(0))
	c.BoxKeyed(ui.DrawText, ui.KeyN(tipKeyBase, uint64(key)+1), strBytes(text))
	c.EndOverlay()
	c.PopOpacity()
}
