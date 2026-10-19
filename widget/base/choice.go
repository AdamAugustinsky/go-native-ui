package base

import "gonativeui/ui"

// Choice is a one-of-n component being built: a radio group, a tab strip, a
// segmented control. ChoiceState applies the previous frame's input to the
// choice; the recipe builds the group's box and, for each option, reads
// ChoiceItem and builds it with BeginChoiceItem.
//
// The chosen option (or the first, when none is) is the group's tab stop; the
// arrow keys move the choice and the focus together, wrapping at the ends.
type Choice struct {
	// Key is the group's key; option i is keyed ui.KeyN(Key, i).
	Key ui.Key
	// Name is the shown part of the group's label.
	Name string
	// N is the number of options.
	N int
	// Sel is the chosen option after this frame's input (-1 for none).
	Sel int
	// Stop is the option that is the tab stop.
	Stop int
	// Changed: the choice changed in this frame.
	Changed bool
}

// ChoiceState reads the choice group with the label in the current parent and
// applies the clicks, presses and arrow keys of the previous frame to *sel,
// the chosen option among n (-1 for none).
func (k *Kit) ChoiceState(label string, sel *int, n int) Choice {
	display, ident := ui.SplitLabel(label)
	ch := Choice{Key: ui.HashString(k.c.ParentKey(), ident), Name: display, N: n}
	if !k.Disabled() {
		ch.Changed = k.chooseByInput(ch.Key, sel, n)
	}
	ch.Sel = *sel
	ch.Stop = *sel
	if ch.Stop < 0 || ch.Stop >= n {
		ch.Stop = 0
	}
	return ch
}

// ChoiceItem reads option i of the group: its key, name and visual state, and
// whether it is chosen. It consumes Force.
func (k *Kit) ChoiceItem(ch *Choice, i int, name string, hover Hover) ChoiceOption {
	key := ui.KeyN(ch.Key, uint64(i))
	return ChoiceOption{
		Press: Press{Key: key, Name: name, Hover: hover, Visual: k.VisualOf(key, hover)},
		Index: i, On: i == ch.Sel, stop: i == ch.Stop,
	}
}

// ChoiceOption is one option of a Choice being built.
type ChoiceOption struct {
	Press
	// Index is the option's place in the group.
	Index int
	// On: the option is the chosen one.
	On   bool
	stop bool
}

// BeginChoiceItem builds the root box of option o of ch and pushes it, as
// BeginPress does: a tab stop only when it is the group's, with its role
// (ui.RoleRadio, ui.RoleTab), its state (checked for a radio button, selected
// otherwise) and its position in the group. disp is its text, if it draws one.
// Close it with EndPress(&o.Press).
func (k *Kit) BeginChoiceItem(ch *Choice, o *ChoiceOption, role ui.Role, w, h ui.Size, flags ui.Flags, disp []byte) ui.ID {
	c := k.c
	f := k.InteractFlags(o.Hover)
	if !o.stop && !k.Disabled() {
		f &^= ui.Focusable
	}
	var state ui.SemState
	if o.On {
		state = ui.SemSelected
		if role == ui.RoleRadio {
			state = ui.SemChecked
		}
	}
	k.ApplySize(w, h)
	c.NextRole(role)
	c.NextSemName(o.Name)
	c.NextSemState(state)
	c.NextSemPos(o.Index, ch.N)
	o.ID = c.BoxKeyed(f|flags, o.Key, disp)
	c.Push(o.ID)
	return o.ID
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
