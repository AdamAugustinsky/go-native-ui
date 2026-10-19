package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ScrollAxes selects the axes of a scroll view (base.ScrollAxes).
type ScrollAxes = base.ScrollAxes

// The axes a scroll view scrolls on.
const (
	// ScrollVertical scrolls up and down.
	ScrollVertical = base.ScrollVertical
	// ScrollHorizontal scrolls left and right.
	ScrollHorizontal = base.ScrollHorizontal
	// ScrollBoth scrolls both ways.
	ScrollBoth = base.ScrollBoth
)

// ScrollState is the little state a scroll view keeps between frames
// (base.ScrollState). The app owns it, next to the data the view shows.
type ScrollState = base.ScrollState

// ScrollView is an open scroll view (base.ScrollView), from BeginScroll, which
// is base's: a clipping box whose content scrolls with the wheel, the trackpad,
// the keyboard and scroll bars of the system's style: overlay bars show while
// the view scrolls, legacy ones always, beside the content. They can be
// dragged. Build the content, then EndScroll. The content lays out like a
// Column's.
type ScrollView = base.ScrollView

// EndScroll ends the scroll view and builds its bars.
func (k *Kit) EndScroll(v ScrollView) {
	k.CloseScrollContent(&v)
	k.scrollBars(&v)
	k.CloseScroll(&v)
}

// scrollBars builds the classic overlay bars of the view whose outer box is
// the current parent: a thin thumb that thickens under the pointer.
func (k *Kit) scrollBars(v *base.ScrollView) {
	c := k.c
	px := &k.r.Px
	bars := k.ScrollBars(v)
	for a := range bars {
		b := &bars[a]
		if !b.On {
			continue
		}
		bw := b.W
		if a == 0 {
			bw = b.H
		}
		k.BeginScrollBar(b)
		// the thumb: thin at rest, thicker under the pointer
		tw := theme.Snap(px.ScrollThumbW * (1 + b.Hot))
		var tx, ty, tww, thh float32
		if a == 1 {
			tx, ty, tww, thh = bw-tw-theme.Snap(px.BorderW*2), b.ThumbOff, tw, b.ThumbLen
		} else {
			tx, ty, tww, thh = b.ThumbOff, bw-tw-theme.Snap(px.BorderW*2), b.ThumbLen, tw
		}
		theme.At(c, tx, ty, tww, thh)
		c.NextBG(theme.WithAlpha(k.r.Pal.ScrollThumb, b.Show))
		c.NextRadius(tw / 2)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(b.Key, 1), nil)
		c.Pop()
	}
}
