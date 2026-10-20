package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// RadioOpts are a radio group's options, passed by value.
type RadioOpts struct {
	// Size is the control size.
	Size theme.Size
	// Horizontal lays the buttons out left to right instead of top to bottom.
	Horizontal bool
}

// RadioRecipe is the look of radio buttons. The circle takes
// CheckboxLook's box size and fills.
type RadioRecipe struct {
	// Dot is the diameter of the chosen button's dot per size, in points (5
	// measured at regular size, in a 16 pt circle).
	Dot [theme.NumSizes]float32
	// RowGap is the space between the buttons of a vertical group, and
	// ColumnGap between those of a horizontal one, in points (AppKit stacks
	// 16 pt radio buttons 20 pt apart).
	RowGap, ColumnGap float32
}

// RadioLook is the radio buttons' recipe.
var RadioLook = RadioRecipe{
	Dot:    [theme.NumSizes]float32{theme.SizeRegular: 5, theme.SizeMini: 4, theme.SizeSmall: 4.5, theme.SizeLarge: 6, theme.SizeXLarge: 6},
	RowGap: 4, ColumnGap: 20,
}

// RadioGroup builds a group of radio buttons, one per option, that edits
// *sel (the chosen option, -1 for none). The chosen button (or the first) is
// the group's tab stop; the arrow keys move the choice and the focus
// together. Changed is set in the frame the choice changed.
//
//	rexui.RadioGroup(k, "Sort by", &v.sort, []string{"Name", "Date"}, rexui.RadioOpts{})
func RadioGroup(k *base.Kit, label string, sel *int, options []string, o RadioOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc, cb := &RadioLook, &CheckboxLook
	size := o.Size % theme.NumSizes
	s := &r.Sizes[size]
	n := len(options)
	ch := k.ChoiceState(label, sel, n)
	box := theme.Snap(s.Check)
	dot := pt(k.Scale(), rc.Dot[size])

	axis, gap := ui.AxisY, k.Pt(rc.RowGap)
	if o.Horizontal {
		axis, gap = ui.AxisX, k.Pt(rc.ColumnGap)
	}
	c.NextRole(ui.RoleRadioGroup)
	c.NextSemName(ch.Name)
	c.NextGap(gap)
	c.NextAxis(axis)
	k.ApplySize(ui.Children(1), ui.Children(1))
	gid := c.BoxKeyed(0, ch.Key, nil)
	c.Push(gid)
	focused := false
	for i, opt := range options {
		it := k.ChoiceItem(&ch, i, opt, base.HoverOff)
		fill, mark, lc := cb.Off.RGBA(r), cb.Mark.RGBA(r), cb.Label.RGBA(r)
		switch {
		case it.Disabled && it.On:
			fill, mark, lc = cb.OnDisabled.RGBA(r), cb.MarkDisabled.RGBA(r), cb.LabelDisabled.RGBA(r)
		case it.Disabled:
			fill, lc = cb.OffDisabled.RGBA(r), cb.LabelDisabled.RGBA(r)
		case it.On:
			fill = mix(cb.On.RGBA(r), cb.OnPressed.RGBA(r), it.Down)
		default:
			fill = mix(fill, cb.OffPressed.RGBA(r), it.Down)
		}
		c.NextAxis(ui.AxisX)
		c.NextCross(ui.CrossCenter)
		c.NextGap(s.CheckGap)
		k.BeginChoiceItem(&ch, &it, ui.RoleRadio, ui.Children(1), ui.Px(box), 0, nil)
		c.NextSize(ui.Px(box), ui.Px(box))
		c.NextBG(fill)
		c.NextRadius(box / 2)
		cid := c.BoxKeyed(ui.DrawBackground, ui.KeyN(it.Key, 1), nil)
		if it.On {
			c.Push(cid)
			theme.At(c, theme.Snap((box-dot)/2), theme.Snap((box-dot)/2), dot, dot)
			c.NextBG(mark)
			c.NextRadius(dot / 2)
			c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(it.Key, 2), nil)
			c.Pop()
		}
		k.Text(3, opt, s.Font.Font, lc, ui.AlignLeft, box)
		k.FocusRing(cid, it.Visual, box/2)
		focused = focused || c.Focused(it.ID)
		k.EndPress(&it.Press)
	}
	c.Pop()
	// the focus is on a button, never on the group
	return base.Signal{ID: gid, Changed: ch.Changed, Focused: focused}
}
