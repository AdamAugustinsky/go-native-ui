package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// IconButtonVariant selects an icon button's look.
type IconButtonVariant uint8

const (
	// IconBordered is a push button with an icon for its label: the
	// secondary fill in Rex's capsule (the size's radius, AppKit's
	// icon-rounded, under UseAppKitControls).
	IconBordered IconButtonVariant = iota
	// IconBorderless is a bare symbol in a quiet grey that darkens under the
	// pointer, on a chip that lights up behind it (icon-borderless; Rex's
	// pane-header buttons: IconButtonRecipes, IconButtonChip).
	IconBorderless
	// IconToolbar is a toolbar item: a glass capsule the toolbar's item
	// size (36 × 36 in a unified toolbar at the extra-large size, 28 × 24 in a
	// compact one otherwise), reacting to hover.
	IconToolbar
	// IconPrimary is IconBordered in ButtonPrimary's colours: the accent in a
	// key window.
	IconPrimary
	// IconDestructive is IconBordered in ButtonDestructive's colours.
	IconDestructive

	// NumIconButtonVariants is the number of variants.
	NumIconButtonVariants
)

// IconButtonOpts are an icon button's options, passed by value.
type IconButtonOpts struct {
	// Variant is the look.
	Variant IconButtonVariant
	// Size is the control size.
	Size theme.Size
	// Recipe, when set, replaces the variant's recipe in this call: the
	// colours and the hover policy (a call-site look, say a call button that
	// stays solid in an inactive window). The variant still sets the size,
	// geometry and glyph treatment. Keep the pointee (an IconButtonRecipes
	// entry must not be modified): passing a pointer to a recipe kept by the
	// caller allocates nothing per frame.
	Recipe *ButtonRecipe
}

// IconButtonRecipes are the variants' recipes (ButtonRecipe's fields; the
// label colours colour the icon).
var IconButtonRecipes = [NumIconButtonVariants]ButtonRecipe{
	IconBordered: ButtonRecipes[ButtonDefault],
	IconBorderless: {
		Fill: None, Label: Of(theme.PaneButton), FillInactive: None, LabelInactive: Of(theme.PaneButton),
		FillHover: None, FillPressed: None, LabelPressed: Of(theme.PaneButtonHover),
		FillDisabled: None, LabelDisabled: OfAlpha(theme.PaneButton, 0.4, 0.4),
		Rim: None, RimInactive: None, Highlight: None,
		Hover: base.HoverOn,
	},
	IconToolbar:     ButtonRecipes[ButtonToolbar],
	IconPrimary:     ButtonRecipes[ButtonPrimary],
	IconDestructive: ButtonRecipes[ButtonDestructive],
}

// IconButtonHoverLabel is the icon colour of a hovered borderless icon
// button, toward which Label is mixed by hover.
var IconButtonHoverLabel = Of(theme.PaneButtonHover)

// IconChip is the chip a borderless icon button lights up behind its symbol
// (Rex's pane-header buttons): the button's box, rounded, in Tint at the
// alpha Hover while the pointer is over it and Hover+Press while it is
// pressed. The zero value draws none.
type IconChip struct {
	// Tint is the chip's colour (its alpha is Hover and Press's).
	Tint Tint
	// Hover and Press are the chip's alpha under the pointer, and the alpha
	// that pressing adds.
	Hover, Press float32
	// Radius is the corner radius, in points.
	Radius float32
}

// IconButtonChip is the borderless icon button's chip, as Rex's pane headers
// have it: PaneButtonChip at 8 % under the pointer and 15 % pressed, on a
// box with 6-pt corners; a disabled button has none. The box is the button's
// own, so a button with a hit area larger than its symbol is given its size
// with Kit.NextSize. UseAppKitControls clears it.
var IconButtonChip = IconChip{Tint: Of(theme.PaneButtonChip), Hover: 0.08, Press: 0.07, Radius: 6}

// IconButton builds a button that shows only the icon ic. name is its
// accessible name (and what a tooltip should say); it follows ui's label
// rules.
//
//	if rexui.IconButton(k, icon.Plus, "Add", rexui.IconButtonOpts{}).Clicked {
//		add()
//	}
func IconButton(k *base.Kit, ic icon.ID, name string, o IconButtonOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	v := o.Variant % NumIconButtonVariants
	rc := &IconButtonRecipes[v]
	if o.Recipe != nil {
		rc = o.Recipe
	}
	s := &r.Sizes[o.Size%theme.NumSizes]
	p := k.PressState(name, rc.Hover)
	fill, fg, rim := rc.Colors(r, p.Visual)
	iw, ih := k.IconSize(ic)

	var w, h, radius float32
	switch v {
	case IconBorderless:
		w, h = max(theme.Snap(s.Icon), iw), max(theme.Snap(s.Icon), ih)
		fg = mix(fg, IconButtonHoverLabel.RGBA(r), p.Hot*(1-p.Down))
		if ch := &IconButtonChip; !p.Disabled && ch.Tint != (Tint{}) {
			if a := ch.Hover*p.Hot + ch.Press*p.Down; a > 0 {
				fill = theme.ColA(ch.Tint.RGBA(r)>>8, a)
				radius = k.Pt(ch.Radius)
			}
		}
	case IconToolbar:
		tb := &r.Window.Toolbars[theme.ToolbarCompact]
		if o.Size == theme.SizeXLarge {
			tb = &r.Window.Toolbars[theme.ToolbarUnified]
		}
		w, h = theme.Snap(tb.ItemW), theme.Snap(tb.CapsuleH)
		radius = h / 2
	default:
		h = theme.Snap(s.Height)
		w = theme.Snap(iw + 2*s.PadX)
		radius = s.Radius
		if rc.Capsule { // Rex's: round ends, a circle when the icon is square
			radius = h / 2
		}
	}
	var flags ui.Flags
	if fill&0xFF != 0 {
		flags |= ui.DrawBackground
	}
	if rim&0xFF != 0 {
		c.NextBorder(rim, r.Lines.Hairline)
		flags |= ui.DrawBorder
	}
	c.NextBG(fill)
	c.NextRadius(radius)
	k.BeginPress(&p, ui.RoleButton, ui.Px(w), ui.Px(h), flags, nil)
	// DrawImage centres the symbol in its box: a box as large as the root,
	// whatever size it took (NextWidth, NextHeight), centres it in the button
	if f, disp := k.Icons().Prepare(c, ic, fg); f != 0 {
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.BoxKeyed(f, ui.KeyN(p.Key, 1), disp)
	}
	if hl := rc.Highlight.RGBA(r); hl&0xFF != 0 && !p.Inactive && !p.Disabled {
		innerRim(k, p.ID, hl, radius, ui.SideBottom)
	}
	ringR := radius
	if v == IconBorderless { // AppKit rings the symbol; a rounded square of the size's radius stands in
		ringR = s.Radius
	}
	k.FocusRing(p.ID, p.Visual, ringR)
	return k.EndPress(&p)
}
