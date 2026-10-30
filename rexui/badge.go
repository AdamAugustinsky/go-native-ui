package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// BadgeVariant selects a badge's look.
type BadgeVariant uint8

const (
	// BadgeCount is a count: white Caption 2 on a red capsule (the badge of a
	// toolbar item or a sidebar row), a circle for one digit.
	BadgeCount BadgeVariant = iota
	// BadgeStatus is a dot: a presence, a connection, a build.
	BadgeStatus
	// BadgeTag is a label: Caption 2 in the secondary label colour on a
	// secondary fill capsule.
	BadgeTag

	// NumBadgeVariants is the number of variants.
	NumBadgeVariants
)

// BadgeOpts are a badge's options, passed by value.
type BadgeOpts struct {
	// Variant is the look.
	Variant BadgeVariant
	// Color replaces the variant's colour: the fill of a count or a status
	// dot, the text of a tag. The zero Tint keeps the default (red for a
	// count, green for a status, the secondary label for a tag).
	Color Tint
	// TextColor replaces the text colour of a count (a count on a coloured
	// row whose fill is Color needs a text that contrasts with it). The zero
	// Tint keeps the variant's.
	TextColor Tint
	// Max is the largest count shown; above it the badge reads "Max+" (0:
	// 99). BadgeCountN only.
	Max int
	// Name is the accessible name; empty takes the text (a status dot, which
	// shows none, must be given one).
	Name string
}

// BadgeRecipe is the look of badges. They are designed, not measured:
// AppKit draws no badge offscreen (NSToolbarItem.badge and the sidebar's
// counts are window-server layers), so the sizes follow the live toolbar
// badge (a 16 pt capsule) and the HIG's 8 pt status dot.
type BadgeRecipe struct {
	// H is the badge's height per variant and PadX its horizontal padding,
	// in points; a count is at least as wide as it is high.
	H, PadX [NumBadgeVariants]float32
	// Fill and Text are the colours per variant (a status has no text); Off
	// and OffText are a disabled badge's, a colour and not an opacity.
	Fill, Text, Off, OffText [NumBadgeVariants]Tint
}

// BadgeLook is the badges' recipe.
var BadgeLook = BadgeRecipe{
	H:       [NumBadgeVariants]float32{16, 8, 18},
	PadX:    [NumBadgeVariants]float32{5, 0, 8},
	Fill:    [NumBadgeVariants]Tint{Of(theme.Red), Of(theme.Green), Of(theme.Fill2)},
	Text:    [NumBadgeVariants]Tint{Of(theme.OnAccent), None, Of(theme.Label2)},
	Off:     [NumBadgeVariants]Tint{Of(theme.Fill1), Of(theme.Fill1), Of(theme.Fill3)},
	OffText: [NumBadgeVariants]Tint{Of(theme.TextDisabled), None, Of(theme.TextDisabled)},
}

// Badge builds a badge showing label: the text of a tag or a count that is
// already text. A status dot shows no text; o.Name names it. The badge is
// static (a label for assistive technologies, no pointer input) and takes the
// size its text needs.
//
//	rexui.Badge(k, "Beta", rexui.BadgeOpts{Variant: rexui.BadgeTag})
//	rexui.Badge(k, "", rexui.BadgeOpts{Variant: rexui.BadgeStatus, Name: "Online"})
func Badge(k *base.Kit, label string, o BadgeOpts) ui.ID {
	return badge(k, base.StrBytes(label), o)
}

// BadgeCountN builds a count badge for n (nothing for n ≤ 0; o.Max caps it,
// "99+" by default) without allocating.
//
//	rexui.BadgeCountN(k, row.unread, rexui.BadgeOpts{})
func BadgeCountN(k *base.Kit, n int, o BadgeOpts) ui.ID {
	if n <= 0 {
		return 0
	}
	max := o.Max
	if max <= 0 {
		max = 99
	}
	f := k.Context().Fmt()
	if n > max {
		f.Int(max).Byte('+')
	} else {
		f.Int(n)
	}
	o.Variant = BadgeCount
	return badge(k, f.Bytes(), o)
}

func badge(k *base.Kit, txt []byte, o BadgeOpts) ui.ID {
	c, r := k.Context(), k.Theme()
	rc := &BadgeLook
	v := o.Variant % NumBadgeVariants
	sc := k.Scale()
	h := pt(sc, rc.H[v])
	fill, fg := rc.Fill[v], rc.Text[v]
	if k.Disabled() {
		fill, fg = rc.Off[v], rc.OffText[v]
	}
	fillC := fill.RGBA(r)
	if o.Color != (Tint{}) && !k.Disabled() {
		if v == BadgeTag {
			fg = o.Color
		} else {
			fillC = o.Color.RGBA(r)
		}
	}
	if o.TextColor != (Tint{}) && !k.Disabled() && v != BadgeStatus && v != BadgeTag {
		fg = o.TextColor
	}
	st := &r.Styles[theme.StyleCaption2]
	w := h
	if v != BadgeStatus {
		w = max(h, ui.CeilText(k.TextWidthBytes(st.Font, txt)+2*pt(sc, rc.PadX[v])))
	}
	key := ui.HashString(c.ParentKey(), "badge") // badges of one parent are told apart by their order
	k.ApplySize(ui.Px(w), ui.Px(h))
	c.NextBG(fillC)
	c.NextRadius(h / 2)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextRole(ui.RoleLabel)
	if o.Name != "" {
		c.NextSemName(o.Name)
	} else {
		c.NextSemNameBytes(txt)
	}
	id := c.BoxKeyed(ui.DrawBackground|disabledFlag(k), key, nil)
	if v == BadgeStatus || len(txt) == 0 {
		return id
	}
	c.Push(id)
	// The text box is the whole capsule with the text centred in it, on both
	// axes by the text's own rule. A box of the text's width and the font's line
	// height, centred, leaves the text up to a pixel off: a snapped box a half
	// pixel low.
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextAlign(ui.AlignCenter)
	c.NextFont(st.Font)
	c.NextTextColor(fg.RGBA(r))
	c.BoxKeyed(ui.DrawText, ui.KeyN(key, 1), txt)
	c.Pop()
	return id
}
