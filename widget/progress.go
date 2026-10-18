package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
)

// indeterminateNs is how long the bar of an indeterminate progress bar takes
// to cross.
const indeterminateNs = 1400e6

// ProgressBar builds a progress bar showing frac, from 0 to 1. A negative frac
// is an indeterminate bar: a segment sweeps along the track for as long as the
// bar is visible, and only then does it ask for frames; a window whose bar is
// scrolled or covered out of view goes back to idle. The width fills the
// parent unless NextWidth says otherwise. The label is the accessible name.
func (k *Kit) ProgressBar(label string, frac float32) ui.ID {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	display, ident := ui.SplitLabel(label)
	dim := k.dim()
	key := ui.HashString(c.ParentKey(), ident)
	h := theme.Snap(px.ScrollThumbW * 1.5)
	indeterminate := frac < 0
	frac = min(frac, 1)

	k.size(ui.Pct(1), ui.Px(h))
	c.NextBG(p.TrackFill)
	c.NextRadius(h / 2)
	c.NextRole(ui.RoleProgressBar)
	c.NextSemName(display)
	if !indeterminate {
		c.NextSemRange(0, 1, float64(frac), 0)
	}
	id := c.BoxKeyed(ui.DrawBackground|ui.Clip, key, nil)
	c.Push(id)
	r := c.Rect(id)
	w := r[2] - r[0]
	if indeterminate {
		if k.visible(r) {
			c.RequestFrame()
		}
		t := float32(0)
		if now := c.Input().Time; now != 0 {
			t = float32(now%int64(indeterminateNs)) / indeterminateNs
		}
		seg := w * 0.4
		theme.At(c, -seg+t*(w+seg), 0, seg, h)
	} else {
		theme.At(c, 0, 0, max(frac, 0)*w, h)
	}
	c.NextBG(p.Accent)
	c.NextRadius(h / 2)
	c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 1), nil)
	c.Pop()
	k.undim(dim)
	return id
}
