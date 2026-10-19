package base

import "gonativeui/ui"

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

// Toggle is an on/off component being built: a checkbox, a switch, a toggle
// button. ToggleState applies the previous frame's activation to the value
// and reads the visual state; BeginToggle builds the root; EndToggle closes
// it.
type Toggle struct {
	Press
	// State is the value after this frame's input: Checked, Unchecked or
	// Mixed.
	State CheckState
	// Changed: the value changed in this frame.
	Changed bool
}

// On reports whether the toggle is on (Checked).
func (t *Toggle) On() bool { return t.State == Checked }

// ToggleState reads the toggle with the label in the current parent and
// applies a click, Enter, Space or an assistive technology's press of the
// previous frame to *st: Checked becomes Unchecked, anything else Checked (so
// clicking a mixed box checks it). It consumes Force.
func (k *Kit) ToggleState(label string, st *CheckState, hover Hover) Toggle {
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(k.c.ParentKey(), ident)
	t := Toggle{Press: Press{Key: key, Name: display, Hover: hover}}
	if k.PressedBefore(key) {
		if *st == Checked {
			*st = Unchecked
		} else {
			*st = Checked
		}
		t.Changed = true
	}
	t.State = *st
	t.Visual = k.VisualOf(key, hover)
	return t
}

// ToggleBool is ToggleState for a two-state value.
func (k *Kit) ToggleBool(label string, on *bool, hover Hover) Toggle {
	st := Unchecked
	if *on {
		st = Checked
	}
	t := k.ToggleState(label, &st, hover)
	*on = st == Checked
	return t
}

// BeginToggle builds the root box of t and pushes it, as BeginPress does, with
// its state for the accessibility tree (checked or mixed); role is
// ui.RoleCheckbox or ui.RoleSwitch (a toggle button is a checkbox to the
// accessibility tree, as AppKit reports one).
func (k *Kit) BeginToggle(t *Toggle, role ui.Role, w, h ui.Size, flags ui.Flags) ui.ID {
	var state ui.SemState
	switch t.State {
	case Checked:
		state = ui.SemChecked
	case Mixed:
		state = ui.SemMixed
	}
	k.c.NextSemState(state)
	return k.BeginPress(&t.Press, role, w, h, flags, nil)
}

// EndToggle closes the root BeginToggle pushed and reports the frame's signal,
// with Changed.
func (k *Kit) EndToggle(t *Toggle) Signal {
	s := k.EndPress(&t.Press)
	s.Changed = t.Changed
	return s
}
