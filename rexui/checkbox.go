package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// CheckboxOpts are a checkbox's options, passed by value.
type CheckboxOpts struct {
	// Size is the control size.
	Size theme.Size
}

// CheckboxRecipe is the look of checkboxes (and of radio buttons' circles).
// The box's side and corner radius are the theme's (theme.SizeSpec's Check
// and CheckRadius: 16, 14 and 18 pt with circular radii of 5.5, 4.5 and 6.25
// at regular, small and large, as macOS 27.2 draws them).
type CheckboxRecipe struct {
	// Off, OffPressed and OffDisabled fill an unchecked box: systemFill,
	// #00000030 measured pressed (FillPressed), tertiarySystemFill disabled.
	Off, OffPressed, OffDisabled Tint
	// On, OnPressed and OnDisabled fill a checked or mixed box: the accent,
	// darker pressed, and secondarySystemFill disabled.
	On, OnPressed, OnDisabled Tint
	// OnInactive and MarkInactive replace On and Mark in a window that is
	// not the key window: AppKit greys the checked box (labelColor at 14 %,
	// #DBDBDB over white and #3E3E3E over #1E1E1E measured) and draws the
	// check in labelColor over it. An unchecked box does not change.
	OnInactive, MarkInactive Tint
	// Mark and MarkDisabled colour the check and the dash.
	Mark, MarkDisabled Tint
	// Label and LabelDisabled colour the label.
	Label, LabelDisabled Tint
	// DashW and DashH are the mixed state's dash at the regular size, in
	// points (7 × 1.75 measured); other sizes scale it with the box.
	DashW, DashH float32
}

// CheckboxLook is the checkboxes' recipe.
var CheckboxLook = CheckboxRecipe{
	Off: Of(theme.Fill1), OffPressed: Of(theme.FillPressed), OffDisabled: Of(theme.Fill3),
	On: Of(theme.Accent), OnPressed: Of(theme.AccentPressed), OnDisabled: Of(theme.Fill2),
	Mark: Of(theme.OnAccent), MarkDisabled: Of(theme.Label3),
	OnInactive: OfAlpha(theme.Label, 0.141, 0.142), MarkInactive: Of(theme.Label),
	Label: Of(theme.ControlText), LabelDisabled: Of(theme.TextDisabled),
	DashW: 7, DashH: 1.75,
}

// Checkbox builds a checkbox that edits *v: a box and its label, which both
// toggle it; Space and Enter toggle it with the focus. Changed is set in the
// frame *v changed.
//
//	rexui.Checkbox(k, "Show hidden files", &v.hidden, rexui.CheckboxOpts{})
func Checkbox(k *base.Kit, label string, v *bool, o CheckboxOpts) base.Signal {
	st := base.Unchecked
	if *v {
		st = base.Checked
	}
	s := CheckboxTri(k, label, &st, o)
	*v = st == base.Checked
	return s
}

// CheckboxTri builds a tri-state checkbox that edits *st; clicking a mixed
// box checks it.
func CheckboxTri(k *base.Kit, label string, st *base.CheckState, o CheckboxOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &CheckboxLook
	size := o.Size % theme.NumSizes
	s := &r.Sizes[size]
	t := k.ToggleState(label, st, base.HoverOff)
	on := t.State != base.Unchecked
	box := theme.Snap(s.Check)
	radius := s.CheckRadius

	fill, mark, lc := rc.Off.RGBA(r), rc.Mark.RGBA(r), rc.Label.RGBA(r)
	switch {
	case t.Disabled && on:
		fill, mark, lc = rc.OnDisabled.RGBA(r), rc.MarkDisabled.RGBA(r), rc.LabelDisabled.RGBA(r)
	case t.Disabled:
		fill, lc = rc.OffDisabled.RGBA(r), rc.LabelDisabled.RGBA(r)
	case on && t.Inactive:
		fill, mark = rc.OnInactive.RGBA(r), rc.MarkInactive.RGBA(r)
	case on:
		fill = mix(rc.On.RGBA(r), rc.OnPressed.RGBA(r), t.Down)
	default:
		fill = mix(fill, rc.OffPressed.RGBA(r), t.Down)
	}

	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(s.CheckGap)
	k.BeginToggle(&t, ui.RoleCheckbox, ui.Children(1), ui.Px(box), 0)
	c.NextSize(ui.Px(box), ui.Px(box))
	c.NextBG(fill)
	c.NextRadius(radius)
	bx := c.BoxKeyed(ui.DrawBackground, ui.KeyN(t.Key, 1), nil)
	c.Push(bx)
	switch t.State {
	case base.Checked:
		if f, disp := k.Icons().Prepare(c, icon.CheckMark, mark); f != 0 {
			c.NextSize(ui.Px(box), ui.Px(box)) // DrawImage centres the check in the box
			c.BoxKeyed(f, ui.KeyN(t.Key, 2), disp)
		}
	case base.Mixed:
		f := box / theme.Snap(r.Sizes[theme.SizeRegular].Check)
		dw, dh := theme.Snap(k.Pt(rc.DashW)*f), theme.Snap(k.Pt(rc.DashH)*f)
		theme.At(c, theme.Snap((box-dw)/2), theme.Snap((box-dh)/2), dw, dh)
		c.NextBG(mark)
		c.NextRadius(dh / 2)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(t.Key, 3), nil)
	}
	c.Pop()
	k.Text(4, t.Name, s.Font.Font, lc, ui.AlignLeft, box)
	k.FocusRing(bx, t.Visual, radius)
	return k.EndToggle(&t)
}
