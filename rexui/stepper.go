package rexui

import (
	"strconv"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// StepperOpts are a stepper's options, passed by value.
type StepperOpts struct {
	// Size is the control size. The regular size (20 × 24 pt) was measured;
	// the others are it scaled by their control height.
	Size theme.Size
	// Wrap makes a step past an end go to the other end (NSStepper's
	// valueWraps) instead of stopping there.
	Wrap bool
	// Backdrop is the colour under the stepper's translucent body (the zero
	// Tint is the window background): its arrows are drawn in the opaque
	// colour they have over the body over it.
	Backdrop Tint
}

// StepperRecipe is the look of steppers (NSStepper on macOS 27.2: a rounded
// body of a push button's fill with a chevron in each half and a hairline
// between them).
type StepperRecipe struct {
	// W, H and Radius are the regular stepper's size and corner radius, in
	// points (20, 24 and 6, measured: 5.8 of a body 19.6 × 23.6).
	W, H, Radius float32
	// Fill is the body, FillPressed what a pressed half is darkened with
	// and FillDisabled a disabled body: ButtonDefault's.
	Fill, FillPressed, FillDisabled Tint
	// Divider is the line between the halves, DividerW × DividerH points at
	// DividerX, DividerY (14 × 1 at 3, 12; black at 13.6 % or white at
	// 13.5 %: #CBCBCB over #EBEBEB and #4C4C4C over #303030 measured).
	Divider                                Tint
	DividerX, DividerY, DividerW, DividerH float32
	// Up and Down are the arrows' centrelines in points from the top left,
	// Width their stroke's (1.45, fitted to the renders' ink); Ink colours them
	// (labelColor, measured) and InkDisabled disables them.
	Up, Down         Chevron
	Width            float32
	Ink, InkDisabled Tint
	// RepeatDelay and RepeatInterval are the autorepeat of a held half, in
	// seconds: the delay before the first repeat and the time between
	// repeats. They are the system's key repeat as AppKitRef reads it
	// (metrics.json behaviour keyRepeatDelay and keyRepeatInterval: 0.25 and
	// 0.02 on the reference MacBook).
	RepeatDelay, RepeatInterval float32
	// FieldGap and FieldWidth are the space between a StepperField's text
	// field and its stepper and the field's width, in points (designed).
	FieldGap, FieldWidth float32
}

// StepperLook is the steppers' recipe.
var StepperLook = StepperRecipe{
	W: 20, H: 24, Radius: 6,
	Fill: Of(theme.Fill2), FillPressed: Of(theme.FillPressed), FillDisabled: Of(theme.FillDisabled),
	Divider:  OfAlpha(theme.Label, 0.136, 0.135),
	DividerX: 3, DividerY: 12, DividerW: 14, DividerH: 1,
	Up:    Chevron{A: [2]float32{4.7, 9.1}, Apex: [2]float32{9.7, 3.7}, B: [2]float32{14.7, 9.1}},
	Down:  Chevron{A: [2]float32{4.7, 15.9}, Apex: [2]float32{9.7, 21.3}, B: [2]float32{14.7, 15.9}},
	Width: 1.45, Ink: Of(theme.Label), InkDisabled: Of(theme.TextDisabled),
	RepeatDelay: 0.25, RepeatInterval: 0.02,
	FieldGap: 4, FieldWidth: 60,
}

// Stepper builds a stepper that edits *v between lo and hi in steps of step
// (1 when it is not above 0). A click on its upper half steps up and on its
// lower half down; a held half repeats after the recipe's delay at its
// interval, and only while the pointer is over it. With the focus the Up and
// Down arrow keys step (the system's key repeat applies); the accessibility
// actions increment, decrement and set value work. It reports Changed in the
// frame *v changed.
//
//	rexui.Stepper(k, "Copies", &v.copies, 1, 99, 1, rexui.StepperOpts{})
//
// StepperField pairs it with a text field.
func Stepper(k *base.Kit, label string, v *float32, lo, hi, step float32, o StepperOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &StepperLook
	s := &r.Sizes[o.Size%theme.NumSizes]
	p := k.PressState(label, base.HoverOff)
	f := s.Height / (rc.H * k.Scale())
	w, h := theme.Snap(rc.W*k.Scale()*f), theme.Snap(s.Height)
	radius := rc.Radius * k.Scale() * f
	if step <= 0 {
		step = 1
	}
	upKey, downKey := ui.KeyN(p.Key, 11), ui.KeyN(p.Key, 12)

	old := *v
	if !k.Disabled() {
		n := 0
		su, sd := c.PeekSignal(upKey), c.PeekSignal(downKey)
		switch {
		case su.Pressed || su.Dragging && su.Hovering:
			n = repeats(c, rc, ui.KeyN(p.Key, 15), holdMs(c, ui.KeyN(p.Key, 13)))
		case sd.Pressed || sd.Dragging && sd.Hovering:
			n = -repeats(c, rc, ui.KeyN(p.Key, 15), holdMs(c, ui.KeyN(p.Key, 13)))
		}
		if c.FocusKey() == p.Key {
			for c.TakeKey(ui.KeyUp, 0) {
				n++
			}
			for c.TakeKey(ui.KeyDown, 0) {
				n--
			}
		}
		if a := c.PeekSignal(p.Key); a.Action != 0 {
			if a.Action&ui.ActIncrement != 0 {
				n++
			}
			if a.Action&ui.ActDecrement != 0 {
				n--
			}
			if a.Action&ui.ActSetValue != 0 {
				*v = float32(a.ActionValue)
			}
		}
		*v = stepBy(*v, lo, hi, step, n, o.Wrap)
	}
	val := min(max(*v, lo), hi)

	fill, ink0 := rc.Fill.RGBA(r), rc.Ink.RGBA(r)
	if p.Disabled {
		fill, ink0 = rc.FillDisabled.RGBA(r), rc.InkDisabled.RGBA(r)
	}
	under := o.Backdrop.RGBA(r)
	if under == 0 {
		under = r.Colors[theme.Window]
	}
	under = ink(fill, under)

	c.NextBG(fill)
	c.NextRadius(radius)
	c.NextSemRange(float64(lo), float64(hi), float64(val), float64(step))
	c.NextAxis(ui.AxisY)
	k.BeginPress(&p, ui.RoleSlider, ui.Px(w), ui.Px(h), ui.DrawBackground, nil)
	// the halves: what is clicked, and what darkens while it is pressed
	var halfFlags ui.Flags
	if !p.Disabled {
		halfFlags = ui.Clickable | ui.ActiveAnimation
	}
	pressed := rc.FillPressed.RGBA(r)
	for i, key := range [2]ui.Key{upKey, downKey} {
		a := c.ActiveTOf(key)
		if p.Disabled {
			a = 0
		}
		c.NextSize(ui.Pct(1), ui.Px(h/2))
		c.NextBG(theme.WithAlpha(pressed, a))
		if i == 0 {
			c.NextRadii(radius, radius, 0, 0)
		} else {
			c.NextRadii(0, 0, radius, radius)
		}
		c.BoxKeyed(halfFlags|ui.DrawBackground, key, nil)
	}
	sc := k.Scale() * f
	theme.At(c, theme.Snap(rc.DividerX*sc), theme.Snap(rc.DividerY*sc), theme.Snap(rc.DividerW*sc), max(theme.Snap(rc.DividerH*sc), 1))
	c.NextBG(rc.Divider.RGBA(r))
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 14), nil)
	col := ink(ink0, under)
	chevron(c, p.Key, 100, &rc.Up, 0, rc.Width*f, k.Scale(), col)
	chevron(c, p.Key, 200, &rc.Down, 0, rc.Width*f, k.Scale(), col)
	k.FocusRing(p.ID, p.Visual, radius)
	sig := k.EndPress(&p)
	sig.Changed = *v != old
	return sig
}

// clockMod is the period of holdMs's clock in milliseconds: the largest that a
// float32 holds exactly.
const clockMod = 1 << 24

// holdMs is how long, in milliseconds, the button that this is called for each
// frame of has been held, 0 in the frame it went down. It keeps the time of the
// first call in ui's animated values: a value that is asked for and never
// moves (rate 0) stays where it started, and is dropped in the first frame it
// is not asked for, so that the next hold starts afresh. Asking also keeps
// frames coming while it is held, which the repeats need.
func holdMs(c *ui.Context, key ui.Key) float32 {
	now := c.Input().Time / 1e6 & (clockMod - 1)
	t0 := c.AnimateValueFrom(key, float32(now), float32(now)+1, 0)
	return float32((now - int64(t0)) & (clockMod - 1))
}

// repeats is how many steps a half held for e milliseconds is due in this
// frame: one when it went down, one more after the delay and then one per
// interval, less those already made in this hold, which it keeps under key
// (ui.Context.KeepValue). Counting what was made, rather than what was due a
// frame's dt earlier, counts each step once whatever the frame times and
// the clock's rounding to milliseconds.
func repeats(c *ui.Context, rc *StepperRecipe, key ui.Key, e float32) int {
	delay, every := rc.RepeatDelay*1000, max(rc.RepeatInterval*1000, 1)
	n := 1
	if e >= delay {
		n += 1 + int((e-delay)/every)
	}
	made := int(c.KeepValue(key, 0, float32(n)))
	return max(n-made, 0)
}

// stepBy moves v by n steps of step within lo..hi, onto the grid of steps from
// lo; past an end it stops there, or with wrap goes to the other end.
func stepBy(v, lo, hi, step float32, n int, wrap bool) float32 {
	if hi > lo {
		v = lo + float32(int((v-lo)/step+0.5))*step
	}
	for ; n > 0; n-- {
		switch {
		case v+step <= hi:
			v += step
		case wrap && v < hi+step/2 && v+step > hi:
			v = lo
		case !wrap:
			v = hi
		}
	}
	for ; n < 0; n++ {
		switch {
		case v-step >= lo:
			v -= step
		case wrap && v > lo-step/2 && v-step < lo:
			v = hi
		case !wrap:
			v = lo
		}
	}
	return min(max(v, lo), hi)
}

// StepperFieldOpts are a stepper field's options, passed by value.
type StepperFieldOpts struct {
	// Stepper and Field are the options of its parts.
	Stepper StepperOpts
	Field   FieldOpts
	// Decimals is how many digits follow the point in the text (0: whole
	// numbers).
	Decimals int
}

// StepperField builds a text field and a stepper side by side, as AppKit
// pairs them, that edit *v between lo and hi in steps of step: the stepper
// steps the value and the field shows it; typing a number and pressing Enter
// sets it (clamped); the field shows the value again when it loses the focus.
// buf holds the field's text. The field is the recipe's FieldWidth wide
// unless the kit's NextWidth says otherwise. Changed is set in the frame *v
// changed.
//
//	rexui.StepperField(k, "Copies", &v.copiesText, &v.copies, 1, 99, 1, rexui.StepperFieldOpts{})
func StepperField(k *base.Kit, label string, buf *ui.TextBuffer, v *float32, lo, hi, step float32, o StepperFieldOpts) base.Signal {
	c := k.Context()
	rc := &StepperLook
	_, ident := ui.SplitLabel(label)
	parent := c.ParentKey()
	rowKey := ui.HashString(ui.HashString(parent, ident), "row")
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(k.Pt(rc.FieldGap))
	k.ApplySize(ui.Children(1), ui.Children(1))
	row := c.BoxKeyed(0, rowKey, nil)
	c.Push(row)
	old := *v
	if c.FocusKey() != base.FieldKey(rowKey, label) {
		showValue(buf, *v, o.Decimals)
	}
	k.NextWidth(k.Fixed(rc.FieldWidth))
	fs := TextField(k, label, buf, "", o.Field)
	if fs.Submitted {
		if x, err := strconv.ParseFloat(buf.String(), 32); err == nil {
			*v = min(max(float32(x), lo), hi)
		}
		showValue(buf, *v, o.Decimals)
	}
	// the stepper in a box of its own, so that its key is not the field's
	c.NextSize(ui.Children(1), ui.Children(1))
	grp := c.BoxKeyed(0, ui.KeyN(rowKey, 2), nil)
	c.Push(grp)
	st := Stepper(k, label, v, lo, hi, step, o.Stepper)
	c.Pop()
	c.Pop()
	st.ID = row
	st.Changed = *v != old
	return st
}

// showValue sets buf to v with the given digits unless it shows it already.
func showValue(buf *ui.TextBuffer, v float32, decimals int) {
	var tmp [32]byte
	b := strconv.AppendFloat(tmp[:0], float64(v), 'f', decimals, 32)
	if string(buf.Bytes()) != string(b) {
		buf.SetBytes(b)
	}
}
