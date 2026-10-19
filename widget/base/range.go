package base

import (
	"math"

	"gonativeui/ui"
)

// Range is a component that edits a number between two ends being built: a
// slider. RangeState applies the previous frame's input to the value;
// BeginRange builds the root; EndRange closes it.
type Range struct {
	Press
	// Lo, Hi and Step are the ends and the step (0: continuous).
	Lo, Hi, Step float32
	// Value is the value after this frame's input, clamped to Lo..Hi.
	Value float32
	// Frac is where Value is between Lo and Hi, 0 to 1.
	Frac float32
	// Changed: the value changed in this frame.
	Changed bool
}

// RangeState reads the range component with the label in the current parent
// and applies the previous frame's input to *v: while the left button is down
// on it the value follows the pointer across the root's width less inset
// pixels at each end (half the thumb of a slider); with the focus the arrow
// keys move it by a step, Page Up and Page Down by ten steps (a tenth of the
// range when continuous), Home and End to the ends; the accessibility actions
// increment, decrement and set value work. It consumes Force.
func (k *Kit) RangeState(label string, v *float32, lo, hi, step, inset float32, hover Hover) Range {
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(k.c.ParentKey(), ident)
	r := Range{Press: Press{Key: key, Name: display, Hover: hover}, Lo: lo, Hi: hi, Step: step}
	old := *v
	if !k.Disabled() {
		*v = k.rangeInput(key, *v, lo, hi, step, inset)
	}
	r.Changed = *v != old
	r.Value = min(max(*v, lo), hi)
	if hi > lo {
		r.Frac = (r.Value - lo) / (hi - lo)
	}
	r.Visual = k.VisualOf(key, hover)
	return r
}

// BeginRange builds the root box of r and pushes it, as BeginPress does, as a
// ui.RoleSlider with its range and value for the accessibility tree.
func (k *Kit) BeginRange(r *Range, w, h ui.Size, flags ui.Flags) ui.ID {
	c := k.c
	c.NextCursor(ui.CursorArrow)
	c.NextSemRange(float64(r.Lo), float64(r.Hi), float64(r.Value), float64(r.Step))
	return k.BeginPress(&r.Press, ui.RoleSlider, w, h, flags, nil)
}

// EndRange closes the root BeginRange pushed and reports the frame's signal,
// with Changed.
func (k *Kit) EndRange(r *Range) Signal {
	s := k.EndPress(&r.Press)
	s.Changed = r.Changed
	return s
}

// rangeInput applies the pointer, keys and accessibility actions of the
// previous frame's root box to the value.
func (k *Kit) rangeInput(key ui.Key, val, lo, hi, step, inset float32) float32 {
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
		if w := r[2] - r[0] - 2*inset; w > 0 {
			f := (c.Input().MouseX - r[0] - inset) / w
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
