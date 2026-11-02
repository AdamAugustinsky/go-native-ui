package rexui

import (
	"gonativeui/platform"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// TabsTint is the tabs' capsule in an app's field colours (TintTabs): what an
// app that recolours Rex's field gives in place of Rex's lilacs. Colours are
// 0xRRGGBB, light first and dark second.
type TabsTint struct {
	// Capsule and Pill are the capsule and the chosen tab's pill in a key
	// window; CapsuleInactive and PillInactive in another (zero: the key
	// window's).
	Capsule, CapsuleInactive, Pill, PillInactive [2]uint32
	// Ink is the field's darkest tone in light: the pill's rim and shadow, a
	// tab's press and the close button's chip are drawn in it (in white and
	// black in dark, as Rex's are).
	Ink uint32
	// Name and Detail are a tab's title and detail while it is not chosen,
	// Chosen both while it is; Sep the separators between tabs (zero:
	// Detail).
	Name, Detail, Chosen, Sep [2]uint32
}

// TintTabs recolours rc (TabsRecipes[TabsRex], usually) to t at Rex's
// opacities, with a quiet hairline all round the chosen tab (Rex's rim on the
// end caps alone reads as a border that broke off once the field is not
// lilac). Call it once, before the first frame; set a field of rc after it
// for an opacity of the app's own.
//
//	rexui.TintTabs(&rexui.TabsRecipes[rexui.TabsRex], rexui.TabsTint{
//		Capsule: [2]uint32{0xDEE4E0, 0x282D2B}, Pill: [2]uint32{0xFCFDFC, 0x343A37}, Ink: 0x26302B, …})
func TintTabs(rc *TabsRecipe, t TabsTint) {
	or := func(v, def [2]uint32) [2]uint32 {
		if v == ([2]uint32{}) {
			return def
		}
		return v
	}
	ci, pi, sep := or(t.CapsuleInactive, t.Capsule), or(t.PillInactive, t.Pill), or(t.Sep, t.Detail)
	solid := func(v [2]uint32) Pair { return Solid(v[0], v[1]) }
	rc.Capsule = [2]Pair{solid(t.Capsule), solid(ci)}
	rc.CapsuleBackdrop = [2]Pair{PairA(t.Capsule[0], t.Capsule[1], 0.5, 0.55), PairA(ci[0], ci[1], 0.5, 0.55)}
	rc.Pill = [2]Pair{solid(t.Pill), solid(pi)}
	rc.PillRim, rc.PillRimAround = PairA(t.Ink, 0xFFFFFF, 0.12, 0.07), true
	rc.PillShadow = [2]Pair{PairA(t.Ink, 0x000000, 0.13, 0.40), PairA(t.Ink, 0x000000, 0.14, 0.45)}
	rc.Sep = PairA(sep[0], sep[1], 1, 0.6)
	rc.Press = PairA(t.Ink, 0xFFFFFF, 0.09, 0.08)
	rc.Name = [2]Pair{solid(t.Name), solid(t.Chosen)}
	rc.Detail = [2]Pair{solid(t.Detail), solid(t.Chosen)}
	rc.CloseFill = PairA(t.Ink, 0xFFFFFF, 0.08, 0.08)
}

// WindowOpts are BeginWindow's options.
type WindowOpts struct {
	// Cover is how much of the field's colour (theme.Field) is laid over
	// Rex's scrim while the system's material shows (Resolved.Backdrop), in
	// light and in dark: Rex's scrim alone lets a busy desktop wash out what
	// sits on the field. Zero lays none.
	Cover [2]float32
}

// BeginWindow lays the theme's field under everything the window builds and
// opens the box the window's content goes in, a column filling the window;
// EndWindow closes it. Over the system's material the field is the theme's
// scrim (and Cover of the field's colour over it); otherwise the field itself.
// Call it first in the window's root, after the kit's Begin and
// Theme.SetBackdrop:
//
//	rexui.BeginWindow(k, rexui.WindowOpts{Cover: [2]float32{0.72, 0.64}})
//	… the window's content …
//	rexui.EndWindow(k)
func BeginWindow(k *base.Kit, o WindowOpts) {
	c, r := k.Context(), k.Theme()
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.Push(c.Box(base.Floating, "window"))
	if r.Chrome != nil {
		r.Chrome.Background(c, r)
	}
	if r.Backdrop && o.Cover != ([2]float32{}) {
		w, h := c.Size()
		theme.At(c, 0, 0, float32(w), float32(h))
		c.NextBG(theme.WithAlpha(r.Colors[theme.Field], theme.Lerp(o.Cover[0], o.Cover[1], r.T)))
		c.Box(base.Floating|ui.DrawBackground, "field over glass")
	}
	c.Pop()
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.Push(c.Box(base.Floating, "content"))
}

// EndWindow closes the content BeginWindow opened.
func EndWindow(k *base.Kit) { k.Context().Pop() }

// HasMaterial reports whether w has the system's material behind it (a
// platform.Backdrop other than none): what Theme.SetBackdrop takes.
func HasMaterial(w *platform.Window) bool {
	return w != nil && w.Backdrop() != platform.BackdropNone
}
