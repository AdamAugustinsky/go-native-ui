package widget

import (
	"math"

	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
)

// CheckState is the state of a tri-state checkbox.
type CheckState int8

const (
	// Unchecked is an empty box.
	Unchecked CheckState = iota
	// Checked is a box with a check mark.
	Checked
	// Mixed is a box with a dash: some of what it stands for is checked.
	Mixed
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
	s := k.check(label, &st, false)
	*v = st == Checked
	return s
}

// CheckboxTri builds a tri-state checkbox that edits *st. Clicking a mixed
// box checks it.
func (k *Kit) CheckboxTri(label string, st *CheckState) Signal { return k.check(label, st, true) }

func (k *Kit) check(label string, st *CheckState, tri bool) Signal {
	c := k.c
	p, px := &k.r.Pal, &k.r.Px
	display, ident := ui.SplitLabel(label)
	dim := k.dim()
	key := ui.HashString(c.ParentKey(), ident)
	var changed bool
	if !k.off() {
		if s := c.PeekSignal(key); s.Clicked || s.Activated || s.Action&ui.ActPress != 0 {
			*st = nextCheck(*st)
			changed = true
		}
	}
	v := k.vis(key)

	h := k.rowH()
	box := theme.Snap(px.IconSize)
	var state ui.SemState
	switch *st {
	case Checked:
		state = ui.SemChecked
	case Mixed:
		state = ui.SemMixed
	}
	k.size(ui.Children(1), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextGap(px.Pad)
	c.NextRole(ui.RoleCheckbox)
	c.NextSemName(display)
	c.NextSemState(state)
	id := c.BoxKeyed(k.flags(), key, nil)
	c.Push(id)
	k.center(box, h)
	k.indicator(key, box, px.RadiusSmall, *st != Unchecked, v)
	if *st != Unchecked {
		k.indicatorMark(*st, box, p.OnAccent)
	}
	c.Pop()
	k.labelBox(key, display, h)
	c.Pop()
	sig := k.signal(id)
	sig.Changed = changed
	k.undim(dim)
	return sig
}

func nextCheck(s CheckState) CheckState {
	if s == Checked {
		return Unchecked
	}
	return Checked
}

// indicator builds the box of a checkbox or radio button in the current parent
// (the control's root) and leaves it as the current parent: the caller adds
// the mark and pops. on fills it with the accent colour.
func (k *Kit) indicator(key ui.Key, box, radius float32, on bool, v vis) ui.ID {
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
	display, ident := ui.SplitLabel(label)
	dim := k.dim()
	key := ui.HashString(c.ParentKey(), ident)
	var changed bool
	if !k.off() {
		if s := c.PeekSignal(key); s.Clicked || s.Activated || s.Action&ui.ActPress != 0 {
			*on = !*on
			changed = true
		}
	}
	v := k.vis(key)
	target := float32(0)
	if *on {
		target = 1
	}
	t := c.AnimateValue(ui.KeyN(key, 5), target, 40)

	h := k.rowH()
	tw, th := theme.Snap(2*px.IconSize*1.1), theme.Snap(px.IconSize*1.2)
	var state ui.SemState
	if *on {
		state = ui.SemChecked
	}
	k.size(ui.Children(1), ui.Px(h))
	c.NextAxis(ui.AxisX)
	c.NextGap(px.Pad)
	c.NextRole(ui.RoleSwitch)
	c.NextSemName(display)
	c.NextSemState(state)
	id := c.BoxKeyed(k.flags(), key, nil)
	c.Push(id)
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
	k.decorateBox(key, tw, th, v, th/2)
	c.Pop()
	k.labelBox(key, display, h)
	c.Pop()
	sig := k.signal(id)
	sig.Changed = changed
	k.undim(dim)
	return sig
}

// decorateBox builds the hover and press tints and the focus ring of a control whose
// visible box is the current parent, w×h pixels.
func (k *Kit) decorateBox(key ui.Key, w, h float32, v vis, radius float32) {
	c := k.c
	if v.hot > 0.004 {
		theme.At(c, 0, 0, w, h)
		c.NextBG(theme.WithAlpha(k.r.Pal.HoverOverlay, v.hot))
		c.NextRadius(radius)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 9001), nil)
	}
	if v.press > 0.004 {
		theme.At(c, 0, 0, w, h)
		c.NextBG(theme.WithAlpha(k.r.Pal.PressOverlay, v.press))
		c.NextRadius(radius)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 9002), nil)
	}
	if v.ring > 0.004 {
		k.ringBox(key, w, h, radius, v.ring)
	}
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

func (k *Kit) radioGroup(label string, sel *int, options []string, axis ui.Axis) Signal {
	c := k.c
	px := &k.r.Px
	display, ident := ui.SplitLabel(label)
	dim := k.dim()
	gkey := ui.HashString(c.ParentKey(), ident)
	n := len(options)
	stop := *sel
	if stop < 0 || stop >= n {
		stop = 0
	}
	var changed bool
	if !k.off() {
		changed = k.chooseByInput(gkey, sel, n)
	}

	c.NextRole(ui.RoleRadioGroup)
	c.NextSemName(display)
	gap := float32(0)
	if axis == ui.AxisX {
		gap = px.Gap * 2
	}
	c.NextGap(gap)
	k.size(ui.Children(1), ui.Children(1))
	c.NextAxis(axis)
	gid := c.BoxKeyed(0, gkey, nil)
	c.Push(gid)
	h := k.rowH()
	box := theme.Snap(px.IconSize)
	for i, opt := range options {
		key := ui.KeyN(gkey, uint64(i))
		on := i == *sel
		v := k.vis(key)
		flags := k.flags()
		if i != stop && !k.off() {
			flags &^= ui.Focusable
		}
		var state ui.SemState
		if on {
			state = ui.SemChecked
		}
		k.size(ui.Children(1), ui.Px(h))
		c.NextAxis(ui.AxisX)
		c.NextGap(px.Pad)
		c.NextRole(ui.RoleRadio)
		c.NextSemName(opt)
		c.NextSemState(state)
		c.NextSemPos(i, n)
		id := c.BoxKeyed(flags, key, nil)
		c.Push(id)
		k.center(box, h)
		k.indicator(key, box, box/2, on, v)
		if on {
			d := theme.Snap(box / 2)
			theme.At(c, (box-d)/2, (box-d)/2, d, d)
			c.NextBG(k.r.Pal.OnAccent)
			c.NextRadius(d / 2)
			c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 9100), nil)
		}
		c.Pop()
		k.labelBox(key, opt, h)
		c.Pop()
	}
	c.Pop()
	k.undim(dim)
	return Signal{ID: gid, Changed: changed, Focused: n > 0 && c.Focused(gid)}
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
	display, ident := ui.SplitLabel(label)
	dim := k.dim()
	key := ui.HashString(c.ParentKey(), ident)
	h := theme.Snap(px.ControlH)
	thumb := theme.Snap(px.IconSize)
	old := *v
	if !k.off() {
		*v = k.sliderInput(key, *v, lo, hi, step, thumb)
	}
	val := min(max(*v, lo), hi)
	vz := k.vis(key)

	k.size(ui.Pct(1), ui.Px(h))
	c.NextCursor(ui.CursorArrow)
	c.NextRole(ui.RoleSlider)
	c.NextSemName(display)
	c.NextSemRange(float64(lo), float64(hi), float64(val), float64(step))
	id := c.BoxKeyed(k.flags(), key, nil)
	r := c.Rect(id)
	w := r[2] - r[0]
	if w <= 0 {
		w = thumb * 8
	}
	frac := float32(0)
	if hi > lo {
		frac = (val - lo) / (hi - lo)
	}
	c.Push(id)
	th := theme.Snap(px.ScrollThumbW)
	x0, x1 := thumb/2, w-thumb/2
	cx := x0 + frac*(x1-x0)
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
	k.decorateBox(key, thumb, thumb, vz, thumb/2)
	c.Pop()
	c.Pop()
	sig := k.signal(id)
	sig.Changed = *v != old
	k.undim(dim)
	return sig
}

// sliderInput applies the pointer, keys and accessibility actions of the
// previous frame's slider box to the value.
func (k *Kit) sliderInput(key ui.Key, val, lo, hi, step, thumb float32) float32 {
	c := k.c
	s := c.PeekSignal(key)
	span := hi - lo
	if span <= 0 {
		return val
	}
	big := step * 10
	if step <= 0 {
		step, big = span/100, span/10
	}
	if s.Dragging || s.Pressed { // while the button is down the value follows the pointer
		r := c.RectOf(key)
		if w := r[2] - r[0] - thumb; w > 0 {
			f := (c.Input().MouseX - r[0] - thumb/2) / w
			val = lo + min(max(f, 0), 1)*span
		}
	}
	if s.Action&ui.ActIncrement != 0 {
		val += step
	}
	if s.Action&ui.ActDecrement != 0 {
		val -= step
	}
	if s.Action&ui.ActSetValue != 0 {
		val = float32(s.ActionValue)
	}
	if c.FocusKey() == key {
		switch {
		case c.TakeKey(ui.KeyLeft, 0) || c.TakeKey(ui.KeyDown, 0):
			val -= step
		case c.TakeKey(ui.KeyRight, 0) || c.TakeKey(ui.KeyUp, 0):
			val += step
		case c.TakeKey(ui.KeyPageDown, 0):
			val -= big
		case c.TakeKey(ui.KeyPageUp, 0):
			val += big
		case c.TakeKey(ui.KeyHome, 0):
			val = lo
		case c.TakeKey(ui.KeyEnd, 0):
			val = hi
		}
	}
	if step > 0 {
		val = lo + float32(math.Round(float64((val-lo)/step)))*step
	}
	return min(max(val, lo), hi)
}

// chooseByInput applies the clicks, presses and arrow keys of the previous
// frame to the choice *sel among the n items keyed KeyN(group, i), before the
// items are built, so that they all show the new choice. Arrow keys move the
// choice and the focus together, wrapping at the ends. It reports whether the
// choice changed.
func (k *Kit) chooseByInput(group ui.Key, sel *int, n int) bool {
	c := k.c
	want := -1
	for i := 0; i < n; i++ {
		key := ui.KeyN(group, uint64(i))
		if s := c.PeekSignal(key); s.Clicked || s.Action&ui.ActPress != 0 {
			want = i
			c.SetFocus(key)
			c.SetFocusVisible(false)
		}
		if c.FocusKey() == key && n > 1 {
			switch {
			case c.TakeKey(ui.KeyDown, 0) || c.TakeKey(ui.KeyRight, 0):
				want = (i + 1) % n
			case c.TakeKey(ui.KeyUp, 0) || c.TakeKey(ui.KeyLeft, 0):
				want = (i + n - 1) % n
			}
			if want >= 0 && want != i {
				c.SetFocus(ui.KeyN(group, uint64(want)))
				c.SetFocusVisible(true)
			}
		}
	}
	if want >= 0 && want != *sel {
		*sel = want
		return true
	}
	return false
}
