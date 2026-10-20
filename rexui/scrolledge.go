package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ScrollEdgeRecipe is the scroll edge (NSScrollPocket) over a scroll view
// that runs under a toolbar (base.Kit.NextScrollInset): over its top inset,
// while content is scrolled under it or the window is inactive
// (theme.ScrollEdgeVisible), and always above a table's header, a bar (opaque
// under Reduce Transparency: the scroll edge material's fill) of the window's colour that leaves a
// fifth of the content showing, and a hairline below it.
type ScrollEdgeRecipe struct {
	// Bar and BarInactive are the bar (the content under it shows through
	// its alpha). Measured on the Mac mini's 27.2 (26B5101f) over a solid
	// selection the whole pocket deep (tools/appkitscenes -selectAll):
	// flat, no gradient, from the top of the band to the pocket's end; of
	// the content 18% shows in light (17% of the accent, 15% of an
	// inactive selection), 22% in dark (21–22%), the same with legacy and
	// overlay scrollers. AppKit blurs the content too; this does not.
	Bar, BarInactive Tint
	// Under and UnderInactive are the bar while content is scrolled under
	// it. Without AppKit's blur, the content showing through Bar's alpha
	// stays legible (rows under the toolbar's title, behind a table's
	// header), so the scroll edge is then the colour AppKit's blurred
	// pocket averages to, opaque: measured on the Mac mini's 27.2 over
	// rows of text, #FCFCFC and #2B2B2B, in a key and an inactive window,
	// over the band and a header in the pocket alike (Finder, Settings,
	// Notes, legacy and overlay scrollers: #FAFAFA to #FEFEFE, #2A2A2A to
	// #2D2D2D; Finder's rows are striped). A list's selected rows add their
	// fill as AppKit's blur does, by their share of the inset and the share
	// of the content Bar lets through (1 - its alpha): Finder's two selected
	// rows tint its pocket #E9F3FE over the band and #D8EAFF over the header
	// (#242D39 and #213046 in dark). This is also the scroll
	// edge under Reduce Transparency and where no system blur exists.
	Under, UnderInactive Pair
	// Line is the hairline at the inset's end (none under a table's
	// header, whose own line ends it), theme.Window.ScrollEdge.Line high.
	Line Tint
}

// ScrollEdgeLook is the scroll edge's recipe.
var ScrollEdgeLook = ScrollEdgeRecipe{
	Bar:         OfAlpha(theme.ScrollEdgeBar, 0.82, 0.78),
	BarInactive: OfAlpha(theme.ScrollEdgeBarInactive, 0.85, 0.79),
	Under:       Solid(0xFCFCFC, 0x2B2B2B), UnderInactive: Solid(0xFCFCFC, 0x2B2B2B),
	Line: Of(theme.ScrollEdgeLine),
}

// scrollEdge draws the scroll edge over the top inset of the view whose
// outer box is the current parent, and its hairline if line; under is what a
// list's rows add to it while they are under it (rowLook.underRow; nil: none).
func scrollEdge(k *base.Kit, v *base.ScrollView, line bool, under *[3]float32) {
	if v.TopInset <= 0 {
		return
	}
	c, r := k.Context(), k.Theme()
	// a table's header in the pocket keeps the bar at rest, as Finder's
	// does (#262626 over its #1E1E1E rows in dark)
	_, sy := c.Scroll(v.Inner)
	if line && !theme.ScrollEdgeVisible(sy > 0.5, !k.Inactive()) {
		return
	}
	rc := &ScrollEdgeLook
	bar := rc.Bar.RGBA(r)
	if k.Inactive() {
		bar = rc.BarInactive.RGBA(r)
	}
	switch {
	case sy > 0.5: // content under it: the blurred pocket's colour, opaque
		bar = scrollEdgeUnder(k, v.TopInset, under)
	case r.Settings.ReduceTransparency: // the material's opaque fill
		m := r.Material(theme.MatScrollEdge)
		role := m.Fill
		if k.Inactive() {
			role = m.FillInactive
		}
		bar = r.Colors[role] | 0xFF
	}
	or := c.Rect(v.Outer)
	w := or[2] - or[0]
	key := c.KeyOf(v.Outer)
	theme.At(c, 0, 0, w, v.TopInset)
	c.NextBG(bar)
	c.NextAuditExempt(ui.AuditCovered.Mask()) // the content scrolls under it on purpose
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 6100), nil)
	if line {
		theme.At(c, 0, v.TopInset, w, r.Window.ScrollEdge.Line)
		c.NextBG(rc.Line.RGBA(r))
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 6101), nil)
	}
}

// scrollEdgeUnder is the scroll edge's colour while content is under it
// (ScrollEdgeRecipe.Under): the measured mean, with the rows' fills under an
// inset px high (under, nil: none) at the share Bar lets through.
func scrollEdgeUnder(k *base.Kit, inset float32, under *[3]float32) uint32 {
	r := k.Theme()
	rc := &ScrollEdgeLook
	fill, through := rc.Under, rc.Bar
	if k.Inactive() {
		fill, through = rc.UnderInactive, rc.BarInactive
	}
	col := fill.At(r.T) | 0xFF
	if under == nil || inset <= 0 {
		return col
	}
	a := theme.Lerp(through.Alpha[0], through.Alpha[1], r.T)
	for i := range under {
		sh := 24 - 8*uint(i)
		v := float32(col>>sh&0xFF) + (1-a)*under[i]/inset
		col = col&^(0xFF<<sh) | uint32(min(max(v+0.5, 0), 255))<<sh
	}
	return col
}
