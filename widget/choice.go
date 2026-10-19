package widget

import (
	"gonativeui/rexui"
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

// Checkbox builds a checkbox that edits *v (rexui.Checkbox). Space and Enter
// toggle it when it has the focus. Changed is set in the frame *v changed.
func (k *Kit) Checkbox(label string, v *bool) Signal {
	return rexui.Checkbox(k.Kit, label, v, rexui.CheckboxOpts{})
}

// CheckboxTri builds a tri-state checkbox that edits *st. Clicking a mixed
// box checks it.
func (k *Kit) CheckboxTri(label string, st *CheckState) Signal {
	return rexui.CheckboxTri(k.Kit, label, st, rexui.CheckboxOpts{})
}

// Switch builds a switch that edits *on (rexui.Switch); the knob slides when
// it changes.
func (k *Kit) Switch(label string, on *bool) Signal {
	return rexui.Switch(k.Kit, label, on, rexui.SwitchOpts{})
}

// RadioGroup builds a column of radio buttons, one per option, that edits
// *sel (the index of the chosen option, -1 for none; rexui.RadioGroup). The
// chosen radio button (or the first, when none is) is the group's tab stop;
// the arrow keys move the choice and the focus together, wrapping at the
// ends. Changed is set in the frame the choice changed.
func (k *Kit) RadioGroup(label string, sel *int, options []string) Signal {
	return rexui.RadioGroup(k.Kit, label, sel, options, rexui.RadioOpts{})
}

// RadioRow is RadioGroup laid out left to right.
func (k *Kit) RadioRow(label string, sel *int, options []string) Signal {
	return rexui.RadioGroup(k.Kit, label, sel, options, rexui.RadioOpts{Horizontal: true})
}

// Slider builds a horizontal slider that edits *v between lo and hi in steps
// of step (0 for a continuous slider; rexui.Slider). The pointer sets the
// value; with the focus, the arrow keys move it by a step, Page Up and Page
// Down by ten steps (a tenth of the range when continuous), Home and End to
// the ends. The accessibility actions increment, decrement and set value
// work. The width fills the parent unless NextWidth says otherwise.
func (k *Kit) Slider(label string, v *float32, lo, hi, step float32) Signal {
	return rexui.Slider(k.Kit, label, v, lo, hi, step, rexui.SliderOpts{})
}
