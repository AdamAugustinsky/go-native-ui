package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// SliderOpts are a slider's options, passed by value.
type SliderOpts struct {
	// Size is the control size (regular and small; the others take
	// regular's geometry).
	Size theme.Size
}

// SliderRecipe is the look of horizontal sliders (NSSlider on macOS 27.2: a
// 6 pt track, filled with the accent up to a white 20 × 16 pt knob).
type SliderRecipe struct {
	// Height is the slider's height per size, in points (16 regular, 14
	// small); Track the track's thickness (6 and 4); KnobW and KnobH the
	// knob's size.
	Height, Track, KnobW, KnobH [theme.NumSizes]float32
	// Rest is the empty track, Filled its part before the knob,
	// FilledDisabled that part when disabled (black or white at 15 %,
	// measured #00000025).
	Rest, Filled, FilledDisabled Tint
	// Knob is the knob's colour and KnobShadow its shadow's, KnobShadowY
	// points down with KnobShadowBlur points of blur: AppKit's knob floats
	// in a soft shadow in light and has none in dark.
	Knob, KnobShadow            Tint
	KnobShadowY, KnobShadowBlur float32
}

// SliderLook is the sliders' recipe.
var SliderLook = SliderRecipe{
	Height: [theme.NumSizes]float32{theme.SizeRegular: 16, theme.SizeMini: 12, theme.SizeSmall: 14, theme.SizeLarge: 16, theme.SizeXLarge: 16},
	Track:  [theme.NumSizes]float32{theme.SizeRegular: 6, theme.SizeMini: 4, theme.SizeSmall: 4, theme.SizeLarge: 6, theme.SizeXLarge: 6},
	KnobW:  [theme.NumSizes]float32{theme.SizeRegular: 20, theme.SizeMini: 15, theme.SizeSmall: 17.5, theme.SizeLarge: 20, theme.SizeXLarge: 20},
	KnobH:  [theme.NumSizes]float32{theme.SizeRegular: 16, theme.SizeMini: 12, theme.SizeSmall: 14, theme.SizeLarge: 16, theme.SizeXLarge: 16},
	Rest:   Of(theme.Fill1), Filled: Of(theme.Accent), FilledDisabled: OfAlpha(theme.Label, 0.15, 0.15),
	Knob: Of(theme.OnAccent), KnobShadow: OfAlpha(theme.Shadow, 0.25, 0),
	KnobShadowY: 0.5, KnobShadowBlur: 2,
}

// Slider builds a horizontal slider that edits *v between lo and hi in steps
// of step (0: continuous). The pointer sets the value; with the focus the
// arrow keys move it by a step, Page Up and Page Down by ten, Home and End to
// the ends; the accessibility actions work. It fills the parent's width unless
// the kit's NextWidth says otherwise.
//
//	rexui.Slider(k, "Volume", &v.volume, 0, 100, 1, rexui.SliderOpts{})
func Slider(k *base.Kit, label string, v *float32, lo, hi, step float32, o SliderOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &SliderLook
	size := o.Size % theme.NumSizes
	sc := k.Scale()
	h, th := pt(sc, rc.Height[size]), pt(sc, rc.Track[size])
	kw, kh := pt(sc, rc.KnobW[size]), pt(sc, rc.KnobH[size])
	rg := k.RangeState(label, v, lo, hi, step, kw/2, base.HoverOff)

	id := k.BeginRange(&rg, ui.Pct(1), ui.Px(h), 0)
	rr := c.Rect(id)
	w := rr[2] - rr[0]
	if w <= 0 {
		w = 8 * kw
	}
	filled := rc.Filled.RGBA(r)
	if rg.Disabled {
		filled = rc.FilledDisabled.RGBA(r)
	}
	ty := theme.Snap((h - th) / 2)
	kx := theme.Snap(rg.Frac * (w - kw))
	// The empty track starts under the knob, so a translucent fill before
	// it is not laid over it.
	theme.At(c, kx, ty, w-kx, th)
	c.NextBG(rc.Rest.RGBA(r))
	c.NextRadius(th / 2)
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(rg.Key, 1), nil)
	if fw := kx + kw/2; fw > 0 {
		theme.At(c, 0, ty, fw, th)
		c.NextBG(filled)
		c.NextRadius(th / 2)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(rg.Key, 2), nil)
	}
	theme.At(c, kx, theme.Snap((h-kh)/2), kw, kh)
	c.NextBG(rc.Knob.RGBA(r))
	c.NextRadius(kh / 2)
	flags := ui.DrawBackground | base.Floating
	if sh := rc.KnobShadow.RGBA(r); sh&0xFF != 0 {
		c.NextShadow(ui.Shadow{OffY: k.Pt(rc.KnobShadowY), Sigma: k.Pt(rc.KnobShadowBlur), Color: sh})
		flags |= ui.DrawShadow
	}
	kb := c.BoxKeyed(flags, ui.KeyN(rg.Key, 3), nil)
	k.FocusRing(kb, rg.Visual, kh/2)
	return k.EndRange(&rg)
}
