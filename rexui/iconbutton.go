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
	// secondary fill and the size's radius (AppKit's icon-rounded).
	IconBordered IconButtonVariant = iota
	// IconBorderless is a bare symbol in secondary label colour that
	// darkens under the pointer (icon-borderless; rexlook's pane buttons).
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
}

// IconButtonRecipes are the variants' recipes (ButtonRecipe's fields; the
// label colours colour the icon).
var IconButtonRecipes = [NumIconButtonVariants]ButtonRecipe{
	IconBordered: ButtonRecipes[ButtonDefault],
	IconBorderless: {
		Fill: None, Label: Of(theme.Label2), FillInactive: None, LabelInactive: Of(theme.Label3),
		FillHover: None, FillPressed: None, LabelPressed: Of(theme.Label),
		FillDisabled: None, LabelDisabled: Of(theme.Label4),
		Rim: None, RimInactive: None, Highlight: None,
		Hover: base.HoverOn,
	},
	IconToolbar:     ButtonRecipes[ButtonToolbar],
	IconPrimary:     ButtonRecipes[ButtonPrimary],
	IconDestructive: ButtonRecipes[ButtonDestructive],
}

// IconButtonHoverLabel is the icon colour of a hovered borderless icon
// button, toward which Label is mixed by hover.
var IconButtonHoverLabel = Of(theme.Label)

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
	s := &r.Sizes[o.Size%theme.NumSizes]
	p := k.PressState(name, rc.Hover)
	fill, fg, rim := rc.Colors(r, p.Visual)
	iw, ih := k.IconSize(ic)

	var w, h, radius float32
	switch v {
	case IconBorderless:
		w, h = max(theme.Snap(s.Icon), iw), max(theme.Snap(s.Icon), ih)
		fg = mix(fg, IconButtonHoverLabel.RGBA(r), p.Hot*(1-p.Down))
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
