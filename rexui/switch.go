package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// SwitchOpts are a switch's options, passed by value.
type SwitchOpts struct {
	// Size is the control size (theme.SizeSpec's SwitchW × SwitchH: 54 × 24
	// at regular size).
	Size theme.Size
}

// SwitchRecipe is the look of switches (NSSwitch on macOS 27.2: a capsule
// track and a white pill-shaped knob).
type SwitchRecipe struct {
	// KnobW is the knob's width per size and Inset its distance from the
	// track's edges, in points; the knob is the track's height less two
	// insets tall (measured: 32 × 20 in 54 × 24, 26 × 16 in 44 × 20, 38 × 24
	// in 64 × 28, 21 × 13 in 36 × 16).
	KnobW, Inset [theme.NumSizes]float32
	// OnInactive replaces On in a window that is not the key window (as
	// CheckboxRecipe's OnInactive: labelColor at 14 % measured); the off
	// track and the knob do not change.
	OnInactive Tint
	// Off and On fill the track; OffPressed and OnPressed while pressed;
	// OffDisabled and OnDisabled when disabled (the accent at 70 %, measured
	// #ED4D98B4 for the pink accent).
	Off, OffPressed, OffDisabled, On, OnPressed, OnDisabled Tint
	// Knob is the knob's colour; KnobShadow the colour of its shadow, which
	// lies KnobShadowY points below it with KnobShadowBlur points of blur,
	// and KnobAmbient that of a second, wide one (KnobAmbientY,
	// KnobAmbientBlur): a live window's Liquid Glass knob floats in a faint
	// halo some 14 pt wide in light, none in dark (GnuiShot captures of
	// AppKitRef -live; offscreen renders do not show it).
	Knob, KnobShadow, KnobAmbient Tint
	KnobShadowY, KnobShadowBlur   float32
	KnobAmbientY, KnobAmbientBlur float32
	// Label and LabelDisabled colour the label, which follows the switch at
	// the theme's ControlGap.
	Label, LabelDisabled Tint
}

// SwitchLook is the switches' recipe.
var SwitchLook = SwitchRecipe{
	KnobW: [theme.NumSizes]float32{theme.SizeRegular: 32, theme.SizeMini: 21, theme.SizeSmall: 26, theme.SizeLarge: 38, theme.SizeXLarge: 38},
	Inset: [theme.NumSizes]float32{theme.SizeRegular: 2, theme.SizeMini: 1.5, theme.SizeSmall: 2, theme.SizeLarge: 2, theme.SizeXLarge: 2},
	Off:   Of(theme.Fill1), OffPressed: Of(theme.FillPressed), OffDisabled: Of(theme.Fill3),
	OnInactive: OfAlpha(theme.Label, 0.141, 0.142),
	On:         Of(theme.Accent), OnPressed: Of(theme.AccentPressed), OnDisabled: OfAlpha(theme.Accent, 0.7, 0.7),
	Knob: Of(theme.Knob), KnobShadow: OfAlpha(theme.Shadow, 0.15, 0),
	KnobShadowY: 1, KnobShadowBlur: 1,
	KnobAmbient: OfAlpha(theme.Shadow, 0.13, 0), KnobAmbientY: 2, KnobAmbientBlur: 10,
	Label: Of(theme.ControlText), LabelDisabled: Of(theme.TextDisabled),
}

// Switch builds a switch that edits *on; the knob slides over the theme's
// Standard duration (at once under Reduce Motion). The label, if any, follows
// it and is its accessible name.
//
//	rexui.Switch(k, "Wi-Fi", &v.wifi, rexui.SwitchOpts{})
func Switch(k *base.Kit, label string, on *bool, o SwitchOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &SwitchLook
	size := o.Size % theme.NumSizes
	s := &r.Sizes[size]
	tg := k.ToggleBool(label, on, base.HoverOff)
	target := float32(0)
	if *on {
		target = 1
	}
	t := c.AnimateValue(ui.KeyN(tg.Key, 5), target, RateFor(r.Motion.Standard))

	tw, th := theme.Snap(s.SwitchW), theme.Snap(s.SwitchH)
	off, onc := rc.Off.RGBA(r), rc.On.RGBA(r)
	lc := rc.Label.RGBA(r)
	switch {
	case tg.Disabled:
		off, onc, lc = rc.OffDisabled.RGBA(r), rc.OnDisabled.RGBA(r), rc.LabelDisabled.RGBA(r)
	case tg.Inactive:
		onc = rc.OnInactive.RGBA(r)
	case tg.Down > 0:
		off, onc = mix(off, rc.OffPressed.RGBA(r), tg.Down), mix(onc, rc.OnPressed.RGBA(r), tg.Down)
	}
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(r.Space.ControlGap)
	k.BeginToggle(&tg, ui.RoleSwitch, ui.Children(1), ui.Px(th), 0)
	c.NextSize(ui.Px(tw), ui.Px(th))
	c.NextBG(theme.MixRGBA(off, onc, t))
	c.NextRadius(th / 2)
	tr := c.BoxKeyed(ui.DrawBackground, ui.KeyN(tg.Key, 1), nil)
	c.Push(tr)
	in := pt(k.Scale(), rc.Inset[size])
	kw, kh := pt(k.Scale(), rc.KnobW[size]), th-2*in
	theme.At(c, theme.Snap(in+t*(tw-2*in-kw)), in, kw, kh)
	c.NextBG(rc.Knob.RGBA(r))
	c.NextRadius(kh / 2)
	flags := ui.DrawBackground | base.Floating
	if sh := rc.KnobShadow.RGBA(r); sh&0xFF != 0 { // disabled too, as AppKit
		near := ui.Shadow{OffY: k.Pt(rc.KnobShadowY), Sigma: k.Pt(rc.KnobShadowBlur), Color: sh}
		if am := rc.KnobAmbient.RGBA(r); am&0xFF != 0 {
			c.NextShadows(ui.Shadow{OffY: k.Pt(rc.KnobAmbientY), Sigma: k.Pt(rc.KnobAmbientBlur), Color: am}, near)
		} else {
			c.NextShadow(near)
		}
		flags |= ui.DrawShadow
	}
	c.BoxKeyed(flags, ui.KeyN(tg.Key, 3), nil)
	c.Pop()
	k.Text(4, tg.Name, s.Font.Font, lc, ui.AlignLeft, th)
	k.FocusRing(tr, tg.Visual, th/2)
	return k.EndToggle(&tg)
}
