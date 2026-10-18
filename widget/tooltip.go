package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// DefaultTooltipDelayNs is how long the pointer rests on a widget before its
// tooltip shows.
const DefaultTooltipDelayNs = 500e6

// tipState is the hover timer of the tooltip: the widget the pointer rests on
// and since when.
type tipState struct {
	key   ui.Key
	since int64
}

// tipKeyBase keys the tooltip overlays.
var tipKeyBase = ui.HashString(keyRoot, "tooltip")

// Tooltip shows text beside the widget id, which must have been built in this
// frame and take pointer input (a button, a field, a list; a plain label does
// not). The tooltip appears once the pointer has rested on the widget for
// TooltipDelayNs, fades in, and goes when the pointer leaves it or a button is
// pressed. It is an overlay: it is drawn above everything, takes no input and
// never takes the focus. Call it right after the widget.
func (k *Kit) Tooltip(id ui.ID, text string) {
	c := k.c
	if id == 0 || text == "" || k.off() {
		return
	}
	key := c.KeyOf(id)
	now := c.Input().Time
	if !c.Hot(id) {
		if k.tip.key == key {
			k.tip.key = 0
		}
		return
	}
	if c.ActiveKey() != 0 || k.pop.open() { // pressed, or a menu is open: start over
		k.tip.key, k.tip.since = key, now
		return
	}
	if k.tip.key != key {
		k.tip.key, k.tip.since = key, now
	}
	delay := k.TooltipDelayNs
	if delay == 0 {
		delay = DefaultTooltipDelayNs
	}
	if now == 0 {
		return // without a clock a delay cannot pass
	}
	if el := now - k.tip.since; el < delay {
		c.WakeIn(delay - el)
		return
	}
	p, px := &k.r.Pal, &k.r.Px
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
