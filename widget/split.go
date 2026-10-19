package widget

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// MaxPanes is the most panes a splitter has.
const MaxPanes = base.MaxPanes

// SplitState is the app-owned state of a splitter: how the room is shared
// between the panes (base.SplitState). Make it with NewSplitState.
type SplitState = base.SplitState

// NewSplitState makes the state of a splitter with panes sharing the room in
// the proportions of ratios (normalised to add up to 1); at most MaxPanes.
func NewSplitState(ratios ...float32) SplitState { return base.NewSplitState(ratios...) }

// Splitter is an open splitter (base.Splitter), from BeginSplitter, which is
// base's: it divides its box into the panes of a SplitState, side by side
// (ui.AxisX) or one above the other (ui.AxisY), with the theme's gutter and
// divider between them. The user drags a divider, or focuses it and uses the
// arrow keys (16 pt a step, 64 pt with Shift) and Home and End, to move it; no
// pane goes below its minimum.
//
//	sp := k.BeginSplitter("main", &v.split, ui.AxisX)
//	sp.Pane(0)
//	…
//	sp.EndPane()
//	sp.Pane(1)
//	…
//	sp.EndPane()
//	k.EndSplitter(&sp)
type Splitter = base.Splitter

// EndSplitter builds the dividers, in the theme's chrome, and ends the
// splitter.
func (k *Kit) EndSplitter(s *Splitter) {
	c := k.c
	p := &k.r.Pal
	s.EndPane()
	for i := 0; i < s.Dividers(); i++ {
		d := s.Divider(i)
		gut := d.Gutter
		dim := k.dim()
		k.NextDivider(s, &d)
		k.r.Chrome.Divider(c, k.r, d.Key, d.Axis, d.Bounds, gut)
		if ft := d.Ring; ft > 0.004 {
			w, h := d.Bounds[2], d.Bounds[3]
			lw := theme.Snap(k.r.Px.FocusRingW)
			if d.Axis == ui.AxisX {
				theme.At(c, theme.Snap((gut[0]+gut[2])/2-lw/2), 0, lw, h)
			} else {
				theme.At(c, 0, theme.Snap((gut[1]+gut[3])/2-lw/2), w, lw)
			}
			c.NextBG(theme.WithAlpha(p.FocusRing, ft))
			c.NextRadius(lw / 2)
			c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(d.Key, 9100), nil)
		}
		k.undim(dim)
	}
	k.CloseSplitter(s)
}
