package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ButtonVariant selects a push button's look.
type ButtonVariant uint8

const (
	// ButtonDefault is a push button: the secondary fill, no border
	// (NSBezelStyleRounded; AppKit's push-regular).
	ButtonDefault ButtonVariant = iota
	// ButtonPrimary is the default button: the accent with a white label. In
	// an inactive window it loses the accent (push-default-inactive).
	ButtonPrimary
	// ButtonDestructive is a destructive action: a red label on red at 25 %,
	// as AppKit draws an alert's destructive button.
	ButtonDestructive
	// ButtonPlain is a borderless text button: no fill; the label darkens
	// while pressed.
	ButtonPlain
	// ButtonToolbar is a toolbar item: a glass capsule that reacts to hover.
	ButtonToolbar
	// ButtonGlass is a Liquid Glass push button (NSBezelStyleGlass, macOS
	// 26+), drawn with the toolbar capsule's resting colours until the native
	// material can host it.
	ButtonGlass

	// NumButtonVariants is the number of variants.
	NumButtonVariants
)

// ButtonOpts are a button's options, passed by value. The zero value is a
// regular-size default button.
type ButtonOpts struct {
	// Variant is the look.
	Variant ButtonVariant
	// Size is the control size (theme.SizeRegular: 24 pt, 13 pt type; large
	// and extra large are capsules).
	Size theme.Size
	// Recipe, when set, replaces the variant's recipe in this call: the
	// colours, rim, shadow, shape flags and hover policy. The size still sets
	// the height, padding and type. Keep the pointee (a ButtonRecipes entry must not be
	// modified): passing a pointer to a recipe kept by the caller allocates
	// nothing per frame.
	Recipe *ButtonRecipe
}

// ButtonRecipe is the look of one button variant: the Tints of each state.
// Fill is mixed toward FillHover by hover and toward FillPressed by the
// press (laid over Fill instead when Overlay is set); Label likewise toward
// LabelPressed.
type ButtonRecipe struct {
	// Fill and Label are the resting colours; FillInactive and LabelInactive
	// replace them in a window that is not key.
	Fill, Label, FillInactive, LabelInactive Tint
	// FillHover and FillPressed are the hovered and pressed fills;
	// LabelPressed the pressed label.
	FillHover, FillPressed, LabelPressed Tint
	// FillPressedInactive and LabelPressedInactive replace FillPressed and
	// LabelPressed in an inactive window (the zero Tint: the same): a
	// default button that is not emphasised presses as a push button does,
	// not toward the accent, even when the window loses key mid-press.
	FillPressedInactive, LabelPressedInactive Tint
	// FillDisabled and LabelDisabled are the disabled look: colours, not an
	// opacity.
	FillDisabled, LabelDisabled Tint
	// Rim is a one-pixel outline inside the edge (RimInactive in an inactive
	// window); Highlight a one-pixel line inside the bottom edge, and the top
	// one too with HighlightTop. Glass.
	Rim, RimInactive, Highlight Tint
	HighlightTop                bool
	// Overlay lays the hover and press fills over Fill rather than replacing
	// it (for an opaque glass fill).
	Overlay bool
	// Glass takes Fill and FillInactive from the toolbar material
	// (Resolved.Material(theme.MatToolbar)) rather than from the Tints, so a
	// theme whose capsule glass is translucent gets the material's opaque
	// Solid under Reduce Transparency.
	Glass bool
	// Capsule makes the button a capsule at every size; otherwise the size's
	// radius applies (capsules from large up).
	Capsule bool
	// Shadow is a drop shadow ShadowY points below the button with
	// ShadowBlur points of blur, while it is enabled (the zero Tint: none):
	// a live window's Liquid Glass button floats on one in light mode.
	Shadow              Tint
	ShadowY, ShadowBlur float32
	// Hover is the hover policy: AppKit's push buttons ignore the pointer.
	Hover base.Hover
}

// ButtonRecipes are the variants' recipes: Rex's push buttons are capsules at
// every size, as its tabs and its dialogs' buttons are, in the colours
// measured on macOS 27.2 (tools/appkitref, macbook-blue and macbook-system):
// the default button is secondarySystemFill (Fill2) under labelColor,
// #0000002B pressed (FillPressed) and #0000000A disabled with
// disabledControlTextColor; the primary button is the accent, AccentPressed
// while pressed. Change an entry to restyle a variant across the app (Capsule
// false gives AppKit's rounded rectangle), or copy this file to own it.
var ButtonRecipes = [NumButtonVariants]ButtonRecipe{
	ButtonDefault: {
		Fill: Of(theme.Fill2), Label: Of(theme.Label), FillInactive: Of(theme.Fill2), LabelInactive: Of(theme.Label),
		FillHover: None, FillPressed: Of(theme.FillPressed), LabelPressed: Of(theme.Label),
		FillDisabled: Of(theme.FillDisabled), LabelDisabled: Of(theme.TextDisabled),
		Rim: None, RimInactive: None, Highlight: None,
		Capsule: true,
	},
	ButtonPrimary: {
		Fill: Of(theme.Accent), Label: Of(theme.OnAccent), FillInactive: Of(theme.Fill2), LabelInactive: Of(theme.Label),
		FillHover: None, FillPressed: Of(theme.AccentPressed), LabelPressed: Of(theme.OnAccent),
		FillPressedInactive: Of(theme.FillPressed), LabelPressedInactive: Of(theme.Label),
		FillDisabled: Of(theme.FillDisabled), LabelDisabled: Of(theme.TextDisabled),
		Rim: None, RimInactive: None, Highlight: None,
		Capsule: true,
	},
	// An alert's destructive button: systemRed at 25 % (#FFCDCE over white,
	// #562728 over #1E1E1E) under a systemRed label.
	ButtonDestructive: {
		Fill: OfAlpha(theme.Red, 0.25, 0.25), Label: Of(theme.Red), FillInactive: OfAlpha(theme.Red, 0.25, 0.25), LabelInactive: Of(theme.Red),
		FillHover: None, FillPressed: OfAlpha(theme.Red, 0.4, 0.4), LabelPressed: Of(theme.Red),
		FillDisabled: Of(theme.FillDisabled), LabelDisabled: Of(theme.TextDisabled),
		Rim: None, RimInactive: None, Highlight: None,
		Capsule: true,
	},
	ButtonPlain: {
		Fill: None, Label: Of(theme.Label), FillInactive: None, LabelInactive: Of(theme.Label),
		FillHover: None, FillPressed: None, LabelPressed: Of(theme.Label2),
		FillDisabled: None, LabelDisabled: Of(theme.TextDisabled),
		Rim: None, RimInactive: None, Highlight: None,
		Capsule: true,
	},
	ButtonToolbar: {
		Fill: Of(theme.CapsuleFill), Label: Of(theme.Label), FillInactive: Of(theme.CapsuleFillInactive), LabelInactive: Of(theme.Label2),
		FillHover: Of(theme.Fill4), FillPressed: Of(theme.Fill2), LabelPressed: Of(theme.Label),
		FillDisabled: Of(theme.CapsuleFillInactive), LabelDisabled: Of(theme.TextDisabled),
		Rim: Of(theme.CapsuleRim), RimInactive: Of(theme.CapsuleRimInactive), Highlight: Of(theme.CapsuleHighlight),
		Overlay: true, Glass: true, Capsule: true, Hover: base.HoverOn,
	},
	ButtonGlass: {
		// measured on GnuiShot captures of AppKitRef -live: a rounded
		// rectangle of the size's radius, not a capsule; textColor; a rim
		// lighter than a toolbar capsule's in light (black at 15 %); lit at
		// the top and the bottom; a broad faint shadow in light
		Fill: Of(theme.CapsuleFill), Label: Of(theme.Text), FillInactive: Of(theme.CapsuleFillInactive), LabelInactive: Of(theme.Text),
		FillHover: None, FillPressed: Of(theme.Fill2), LabelPressed: Of(theme.Text),
		FillDisabled: Of(theme.CapsuleFillInactive), LabelDisabled: Of(theme.TextDisabled),
		Rim: OfAlpha(theme.CapsuleRim, 0.15, 1), RimInactive: Of(theme.CapsuleRimInactive), Highlight: Of(theme.CapsuleHighlight),
		HighlightTop: true, Overlay: true, Glass: true,
		Shadow: OfAlpha(theme.Shadow, 0.035, 0), ShadowY: 2, ShadowBlur: 6,
	},
}

// Button builds a push button with a text label and reports its signal
// (Clicked on a click, Enter or Space with the focus, or an accessibility
// press). The label follows ui's rules ("Name###id" shows Name). It is as
// wide as its label and the size's padding unless the kit's NextSize says
// otherwise. A row of buttons that wraps onto further lines is buttons built
// between k.Context().BeginFlow and EndFlow (ui.FlowOpts sets the gaps).
//
//	if rexui.Button(k, "Save", rexui.ButtonOpts{Variant: rexui.ButtonPrimary}).Clicked {
//		save()
//	}
func Button(k *base.Kit, label string, o ButtonOpts) base.Signal {
	return button(k, label, 0, false, o)
}

// ButtonIcon is Button with the icon ic before the label.
func ButtonIcon(k *base.Kit, ic icon.ID, label string, o ButtonOpts) base.Signal {
	return button(k, label, ic, true, o)
}

func button(k *base.Kit, label string, ic icon.ID, withIcon bool, o ButtonOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &ButtonRecipes[o.Variant%NumButtonVariants]
	if o.Recipe != nil {
		rc = o.Recipe
	}
	s := &r.Sizes[o.Size%theme.NumSizes]
	p := k.PressState(label, rc.Hover)
	fill, fg, rim := rc.Colors(r, p.Visual)

	h := theme.Snap(s.Height)
	radius := s.Radius
	if rc.Capsule {
		radius = h / 2
	}
	var flags ui.Flags
	if fill&0xFF != 0 {
		flags |= ui.DrawBackground
	}
	if rim&0xFF != 0 {
		c.NextBorder(rim, r.Lines.Hairline)
		flags |= ui.DrawBorder
	}
	if rc.Shadow != (Tint{}) && !p.Disabled {
		if sh := rc.Shadow.RGBA(r); sh&0xFF != 0 {
			c.NextShadow(ui.Shadow{OffY: k.Pt(rc.ShadowY), Sigma: k.Pt(rc.ShadowBlur), Color: sh})
			flags |= ui.DrawShadow
		}
	}
	c.NextBG(fill)
	c.NextRadius(radius)
	w := ui.TextSize(2 * s.PadX)
	var disp []byte
	if withIcon {
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextGap(r.Space.S4)
		c.NextPadding(s.PadX, 0, s.PadX, 0)
		w = ui.Children(1)
	} else {
		c.NextFont(s.Font.Font)
		c.NextTextColor(fg)
		c.NextAlign(k.TakeAlign(ui.AlignCenter))
		flags |= ui.DrawText
		disp = base.StrBytes(p.Name)
	}
	k.BeginPress(&p, ui.RoleButton, w, ui.Px(h), flags, disp)
	if withIcon { // the glyph is part of the button, not an image of its own
		if f, disp := k.Icons().Prepare(c, ic, fg); f != 0 {
			c.BoxKeyed(f, ui.KeyN(p.Key, 3), disp)
		}
		k.Text(1, p.Name, s.Font.Font, fg, ui.AlignLeft, h)
	}
	if hl := rc.Highlight.RGBA(r); hl&0xFF != 0 && !p.Inactive && !p.Disabled {
		sides := ui.SideBottom
		if rc.HighlightTop {
			sides |= ui.SideTop
		}
		innerRim(k, p.ID, hl, radius, sides)
	}
	k.FocusRing(p.ID, p.Visual, radius)
	return k.EndPress(&p)
}

// Colors are the fill, label and rim of the recipe in the visual state v.
func (rc *ButtonRecipe) Colors(r *theme.Resolved, v base.Visual) (fill, label, rim uint32) {
	switch {
	case v.Disabled:
		return rc.FillDisabled.RGBA(r), rc.LabelDisabled.RGBA(r), rc.RimInactive.RGBA(r)
	case v.Inactive:
		fill, label, rim = rc.FillInactive.RGBA(r), rc.LabelInactive.RGBA(r), rc.RimInactive.RGBA(r)
	default:
		fill, label, rim = rc.Fill.RGBA(r), rc.Label.RGBA(r), rc.Rim.RGBA(r)
	}
	if rc.Glass && !v.Disabled {
		m := r.Material(theme.MatToolbar)
		if v.Inactive {
			fill = r.Colors[m.FillInactive]
		} else {
			fill = r.Colors[m.Fill]
		}
	}
	fp, lp := rc.FillPressed, rc.LabelPressed
	if v.Inactive {
		if rc.FillPressedInactive != (Tint{}) {
			fp = rc.FillPressedInactive
		}
		if rc.LabelPressedInactive != (Tint{}) {
			lp = rc.LabelPressedInactive
		}
	}
	if rc.Overlay {
		fill = over(fill, rc.FillHover.RGBA(r), v.Hot)
		fill = over(fill, fp.RGBA(r), v.Down)
	} else {
		if h := rc.FillHover.RGBA(r); h != 0 {
			fill = mix(fill, h, v.Hot)
		}
		fill = mix(fill, fp.RGBA(r), v.Down)
	}
	label = mix(label, lp.RGBA(r), v.Down)
	return fill, label, rim
}

// innerRim builds a one-pixel line of colour col inside the given edges of
// the root id (the current parent), following its corners: a glass capsule's
// bright rims.
func innerRim(k *base.Kit, id ui.ID, col uint32, radius float32, sides ui.Sides) {
	c := k.Context()
	rc := c.Rect(id)
	w, h := rc[2]-rc[0], rc[3]-rc[1]
	if w <= 0 || h <= 0 {
		return
	}
	lw := k.Theme().Lines.Hairline
	theme.At(c, lw, lw, w-2*lw, h-2*lw)
	c.NextBorder(col, lw)
	c.NextBorderSides(sides)
	c.NextRadius(max(radius-lw, 0))
	c.BoxKeyed(ui.DrawBorder|base.Floating, ui.KeyN(c.KeyOf(id), 9030), nil)
}
