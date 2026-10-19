package widget

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// CheckState is the state of a tri-state checkbox (base.CheckState).
type CheckState = base.CheckState

// The states of a tri-state checkbox.
const (
	// Unchecked is an empty box.
	Unchecked = base.Unchecked
	// Checked is a box with a check mark.
	Checked = base.Checked
	// Mixed is a box with a dash: some of what it stands for is checked.
	Mixed = base.Mixed
)

// rowH is the height of a checkbox, switch or radio row in pixels.
func (k *Kit) rowH() float32 { return theme.Snap(k.r.Px.RowH) }

// labelBox builds the text of a choice widget, vertically centred in a row of
// height h.
func (k *Kit) labelBox(key ui.Key, display string, h float32) {
	if display == "" {
		return
	}
	c := k.c
	c.NextSize(ui.TextSize(0), ui.Px(h))
	c.NextFont(k.r.Font.UI)
	c.NextTextColor(k.r.Pal.Text)
	c.BoxKeyed(ui.DrawText, ui.KeyN(key, 2), strBytes(display))
}

// Checkbox builds a checkbox that edits *v. Space and Enter toggle it when it
// has the focus. Changed is set in the frame *v changed.
func (k *Kit) Checkbox(label string, v *bool) Signal {
	st := Unchecked
	if *v {
		st = Checked
	}
	s := k.check(label, &st)
	*v = st == Checked
	return s
}

// CheckboxTri builds a tri-state checkbox that edits *st. Clicking a mixed
// box checks it.
func (k *Kit) CheckboxTri(label string, st *CheckState) Signal { return k.check(label, st) }

// check is the classic look over base's Toggle primitive.
func (k *Kit) check(label string, st *CheckState) Signal {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	dim := k.dim()
	t := k.ToggleState(label, st, hoverOn)

	h := k.rowH()
	box := theme.Snap(px.IconSize)
	c.NextAxis(ui.AxisX)
	c.NextGap(px.Pad)
	k.BeginToggle(&t, ui.RoleCheckbox, ui.Children(1), ui.Px(h), 0)
	k.center(box, h)
	k.indicator(t.Key, box, px.RadiusSmall, t.State != Unchecked, t.Visual)
	if t.State != Unchecked {
		k.indicatorMark(t.State, box, p.OnAccent)
	}
	c.Pop()
	k.labelBox(t.Key, t.Name, h)
	sig := k.EndToggle(&t)
	k.undim(dim)
	return sig
}

// indicator builds the box of a checkbox or radio button in the current parent
// (the control's root) and leaves it as the current parent: the caller adds
// the mark and pops. on fills it with the accent colour.
func (k *Kit) indicator(key ui.Key, box, radius float32, on bool, v base.Visual) ui.ID {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	bg, border := p.ControlFill, p.ControlBorder
	if on {
		bg, border = p.Accent, p.Accent
	}
	c.NextSize(ui.Px(box), ui.Px(box))
	c.NextBG(bg)
	c.NextBorder(border, px.BorderW)
	c.NextRadius(radius)
	id := c.BoxKeyed(ui.DrawBackground|ui.DrawBorder|ui.FloatingY, ui.KeyN(key, 1), nil)
	c.Push(id)
	k.decorateBox(key, box, box, v, radius)
	return id
}

// indicatorMark builds the check or dash inside the indicator, which is the
// current parent.
func (k *Kit) indicatorMark(st CheckState, box float32, col uint32) {
	c := k.c
	ic := icon.Check
	if st == Mixed {
		ic = icon.Minus
	}
	flags, disp := k.icons.Prepare(c, ic, col)
	c.NextPos(0, 0)
	c.NextSize(ui.Px(box), ui.Px(box))
	if flags&ui.DrawImage != 0 || flags&ui.DrawText != 0 {
		c.BoxKeyed(flags|fl, ui.KeyN(c.ParentKey(), 9100), disp)
	}
}

// Switch builds a switch that edits *on; the knob slides when it changes.
func (k *Kit) Switch(label string, on *bool) Signal {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	dim := k.dim()
	tg := k.ToggleBool(label, on, hoverOn)
	key := tg.Key
	target := float32(0)
	if *on {
		target = 1
	}
	t := c.AnimateValue(ui.KeyN(key, 5), target, 40)

	h := k.rowH()
	tw, th := theme.Snap(2*px.IconSize*1.1), theme.Snap(px.IconSize*1.2)
	c.NextAxis(ui.AxisX)
	c.NextGap(px.Pad)
	k.BeginToggle(&tg, ui.RoleSwitch, ui.Children(1), ui.Px(h), 0)
	k.center(th, h)
	c.NextSize(ui.Px(tw), ui.Px(th))
	c.NextBG(theme.MixRGBA(p.TrackFill, p.Accent, t))
	c.NextRadius(th / 2)
	tr := c.BoxKeyed(ui.DrawBackground|ui.FloatingY, ui.KeyN(key, 1), nil)
	c.Push(tr)
	pad := theme.Snap(px.BorderW * 3)
	d := th - 2*pad
	theme.At(c, pad+t*(tw-th), pad, d, d)
	c.NextBG(p.ThumbFill)
	c.NextRadius(d / 2)
	c.NextShadow(ui.Shadow{OffY: px.BorderW, Sigma: px.BorderW * 1.5, Color: theme.ColA(p.Shadow>>8, 0.35)})
	c.BoxKeyed(ui.DrawBackground|ui.DrawShadow|fl, ui.KeyN(key, 3), nil)
	k.decorateBox(key, tw, th, tg.Visual, th/2)
	c.Pop()
	k.labelBox(key, tg.Name, h)
	sig := k.EndToggle(&tg)
	k.undim(dim)
	return sig
}

// RadioGroup builds a column of radio buttons, one per option, that edits
// *sel (the index of the chosen option, -1 for none). The chosen radio button
// (or the first, when none is) is the group's tab stop; the arrow keys move
// the choice and the focus together, wrapping at the ends. Changed is set in
// the frame the choice changed.
func (k *Kit) RadioGroup(label string, sel *int, options []string) Signal {
	return k.radioGroup(label, sel, options, ui.AxisY)
}

// RadioRow is RadioGroup laid out left to right.
func (k *Kit) RadioRow(label string, sel *int, options []string) Signal {
	return k.radioGroup(label, sel, options, ui.AxisX)
}

// radioGroup is the classic look over base's Choice primitive.
func (k *Kit) radioGroup(label string, sel *int, options []string, axis ui.Axis) Signal {
	c := k.c
	px := &k.r.Px
	dim := k.dim()
	n := len(options)
	ch := k.ChoiceState(label, sel, n)

	c.NextRole(ui.RoleRadioGroup)
	c.NextSemName(ch.Name)
	gap := float32(0)
	if axis == ui.AxisX {
		gap = px.Gap * 2
	}
	c.NextGap(gap)
	k.size(ui.Children(1), ui.Children(1))
	c.NextAxis(axis)
	gid := c.BoxKeyed(0, ch.Key, nil)
	c.Push(gid)
	h := k.rowH()
	box := theme.Snap(px.IconSize)
	for i, opt := range options {
		o := k.ChoiceItem(&ch, i, opt, hoverOn)
		c.NextAxis(ui.AxisX)
		c.NextGap(px.Pad)
		k.BeginChoiceItem(&ch, &o, ui.RoleRadio, ui.Children(1), ui.Px(h), 0, nil)
		k.center(box, h)
		k.indicator(o.Key, box, box/2, o.On, o.Visual)
		if o.On {
			d := theme.Snap(box / 2)
			theme.At(c, (box-d)/2, (box-d)/2, d, d)
			c.NextBG(k.r.Pal.OnAccent)
			c.NextRadius(d / 2)
			c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(o.Key, 9100), nil)
		}
		c.Pop()
		k.labelBox(o.Key, opt, h)
		c.Pop()
	}
	c.Pop()
	k.undim(dim)
	return Signal{ID: gid, Changed: ch.Changed, Focused: n > 0 && c.Focused(gid)}
}

// Slider builds a horizontal slider that edits *v between lo and hi in steps
// of step (0 for a continuous slider). The pointer sets the value; with the
// focus, the arrow keys move it by a step, Page Up and Page Down by ten steps
// (a tenth of the range when continuous), Home and End to the ends. The
// accessibility actions increment, decrement and set value work. The width
// fills the parent unless NextWidth says otherwise.
func (k *Kit) Slider(label string, v *float32, lo, hi, step float32) Signal {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	dim := k.dim()
	h := theme.Snap(px.ControlH)
	thumb := theme.Snap(px.IconSize)
	rg := k.RangeState(label, v, lo, hi, step, thumb/2, hoverOn)
	key := rg.Key

	id := k.BeginRange(&rg, ui.Pct(1), ui.Px(h), 0)
	r := c.Rect(id)
	w := r[2] - r[0]
	if w <= 0 {
		w = thumb * 8
	}
	th := theme.Snap(px.ScrollThumbW)
	x0, x1 := thumb/2, w-thumb/2
	cx := x0 + rg.Frac*(x1-x0)
	// the track and its filled part
	theme.At(c, x0, theme.Snap((h-th)/2), x1-x0, th)
	c.NextBG(p.TrackFill)
	c.NextRadius(th / 2)
	c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 1), nil)
	theme.At(c, x0, theme.Snap((h-th)/2), max(cx-x0, 0), th)
	c.NextBG(p.Accent)
	c.NextRadius(th / 2)
	c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 2), nil)
	// the thumb
	tx, ty := theme.Snap(cx-thumb/2), theme.Snap((h-thumb)/2)
	theme.At(c, tx, ty, thumb, thumb)
	c.NextBG(p.ThumbFill)
	c.NextBorder(p.ControlBorder, px.BorderW)
	c.NextRadius(thumb / 2)
	c.NextShadow(ui.Shadow{OffY: px.BorderW, Sigma: px.BorderW * 1.5, Color: theme.ColA(p.Shadow>>8, 0.35)})
	tb := c.BoxKeyed(ui.DrawBackground|ui.DrawBorder|ui.DrawShadow|fl, ui.KeyN(key, 3), nil)
	c.Push(tb)
	k.decorateBox(key, thumb, thumb, rg.Visual, thumb/2)
	c.Pop()
	sig := k.EndRange(&rg)
	k.undim(dim)
	return sig
}
