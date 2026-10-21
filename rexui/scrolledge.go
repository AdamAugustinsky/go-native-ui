package rexui

import (
	"gonativeui/gpu"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ScrollEdgeRecipe is the scroll edge (NSScrollPocket) over a scroll view
// that runs under a toolbar (base.Kit.NextScrollInset): over its top inset,
// while content is scrolled under it or the window is inactive
// (theme.ScrollEdgeVisible), and always above a table's header. At rest it
// is a bar of the window's colour (opaque under Reduce Transparency: the
// scroll edge material's fill); while content is under it, AppKit's blur of
// that content (D54); and a hairline below it.
type ScrollEdgeRecipe struct {
	// Bar and BarInactive are the bar at rest (the content under it shows
	// through its alpha). Measured on the Mac mini's 27.2 (26B5101f) over a
	// solid selection the whole pocket deep (tools/appkitscenes
	// -selectAll): flat, no gradient, from the top of the band to the
	// pocket's end; of the content 18% shows in light (17% of the accent,
	// 15% of an inactive selection), 22% in dark (21–22%), the same with
	// legacy and overlay scrollers.
	Bar, BarInactive Tint
	// Hard and Thin are AppKit's blur of the content scrolled under the
	// edge, read from its layers on the Mac mini's 27.2 (26B5101f) and
	// matched over GlassProbe's rows (docs/research/glass-over-metal.md).
	// Hard is a table's, whose header is in the pocket, and the toolbar's
	// own band's (HardPocketContentBlur, then HardPocketBackgroundReplay):
	// the content at a quarter of the backing scale under a 6-pt Gaussian,
	// plus 0.03, and over it the content's colour plus 0.03 (a colour:
	// white stays white) at 82.45 %; drawn, within ΔE2000 0.07 (light) and
	// 0.2 (dark) of AppKit's on average over rows of text, 0.3 and 0.6 over
	// a blue selection. Thin is a plain scroll view's (ThinFilm): the
	// content's colour under the blur at 50 % / 60 %, half scale,
	// saturation 1.25, plus 0.03; AppKit's blur there falls off toward the
	// hairline, which this does not model: a constant 6 pt is within 0.36
	// and 0.95. HardInactive and ThinInactive are an inactive window's:
	// AppKit's recipe is the same there (its bands measured ΔE 0.00 apart).
	Hard, HardInactive, Thin, ThinInactive BackdropRecipe
	// Under and UnderInactive are the edge while content is under it where
	// there is no blur: under Reduce Transparency, and where the renderer
	// has none (gpu.Renderer.Backdrops), the blur's fallback. They are the
	// colour AppKit's blurred pocket averages to, opaque: measured on the
	// Mac mini's 27.2 over rows of text, #FCFCFC and #2B2B2B, in a key and
	// an inactive window, over the band and a header in the pocket alike
	// (Finder, Settings, Notes, legacy and overlay scrollers: #FAFAFA to
	// #FEFEFE, #2A2A2A to #2D2D2D; Finder's rows are striped). A list's
	// selected rows add their fill as AppKit's blur does, by their share of
	// the inset and the share of the content Bar lets through (1 - its
	// alpha): Finder's two selected rows tint its pocket #E9F3FE over the
	// band and #D8EAFF over the header (#242D39 and #213046 in dark).
	Under, UnderInactive Pair
	// Line is the hairline at the inset's end (none under a table's
	// header, whose own line ends it), theme.Window.ScrollEdge.Line high.
	Line Tint
}

// BackdropRecipe is a blur of what is drawn under a box
// (ui.Context.NextBackdrop) as a recipe names it: its lengths in points, its
// colour a role. See gpu.Backdrop.
type BackdropRecipe struct {
	// Sigma is the Gaussian's standard deviation in points, and Down the
	// factor the content is scaled down by first, of the backing scale
	// (AppKit's quarter: 4).
	Sigma, Down float32
	// Tint is mixed over the blur by Mix, in light ([0]) and dark ([1]).
	Tint Tint
	Mix  [2]float32
	// Saturation (light, dark), Add and TintUnder are gpu.Backdrop's.
	Saturation [2]float32
	Add        float32
	TintUnder  bool
}

// Backdrop is the recipe for k's theme, appearance and scale.
func (b *BackdropRecipe) Backdrop(k *base.Kit) gpu.Backdrop {
	r := k.Theme()
	return gpu.Backdrop{
		Sigma: b.Sigma * k.Scale(), Down: b.Down, Tint: b.Tint.RGBA(r),
		Mix:        theme.Lerp(b.Mix[0], b.Mix[1], r.T),
		Saturation: theme.Lerp(b.Saturation[0], b.Saturation[1], r.T), Add: b.Add, TintUnder: b.TintUnder,
	}
}

// hardPocket and thinFilm are AppKit's two scroll-edge blurs (macOS 27.2).
var (
	hardPocket = BackdropRecipe{Sigma: 6, Down: 4, Tint: Of(theme.ControlBackground), Mix: [2]float32{0.8245, 0.8245},
		Saturation: [2]float32{1, 1}, Add: 0.03}
	thinFilm = BackdropRecipe{Sigma: 6, Down: 2, Tint: Of(theme.ControlBackground), Mix: [2]float32{0.5, 0.6},
		Saturation: [2]float32{1.25, 1.25}, Add: 0.03, TintUnder: true}
)

// ScrollEdgeLook is the scroll edge's recipe.
var ScrollEdgeLook = ScrollEdgeRecipe{
	Bar:         OfAlpha(theme.ScrollEdgeBar, 0.82, 0.78),
	BarInactive: OfAlpha(theme.ScrollEdgeBarInactive, 0.85, 0.79),
	Hard:        hardPocket, HardInactive: hardPocket, Thin: thinFilm, ThinInactive: thinFilm,
	Under: Solid(0xFCFCFC, 0x2B2B2B), UnderInactive: Solid(0xFCFCFC, 0x2B2B2B),
	Line: Of(theme.ScrollEdgeLine),
}

// scrollEdge draws the scroll edge over the top inset of the view whose
// outer box is the current parent, and its hairline if line (a plain scroll
// view's: a table's header ends its own); under is what a list's rows add
// to it while they are under it (rowLook.underRow; nil: none).
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
	case sy > 0.5: // content under it: the blur, or the colour it averages to, opaque
		bar = scrollEdgeUnder(k, v.TopInset, under)
		if !r.Settings.ReduceTransparency {
			c.NextBackdrop(scrollEdgeBlur(k, !line))
		}
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

// scrollEdgeBlur is the scroll edge's blur in k's window: Hard (a table's,
// hard) or Thin (a plain scroll view's), key or inactive.
func scrollEdgeBlur(k *base.Kit, hard bool) gpu.Backdrop {
	rc := &ScrollEdgeLook
	b := &rc.Thin
	switch {
	case hard && k.Inactive():
		b = &rc.HardInactive
	case hard:
		b = &rc.Hard
	case k.Inactive():
		b = &rc.ThinInactive
	}
	return b.Backdrop(k)
}

// scrollEdgeUnder is the scroll edge's colour while content is under it
// where there is no blur (ScrollEdgeRecipe.Under): the measured mean, with
// the rows' fills under an inset px high (under, nil: none) at the share
// Bar lets through.
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
