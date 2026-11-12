package base

import "gonativeui/ui"

// Hover is a component's hover policy: whether it reacts to the pointer
// resting over it. The zero value, HoverAuto, shows the component's hover look
// (D103): a control answers the pointer as a web page's or Rex's do. A theme
// that asks for strict AppKit behaviour (theme.Theme.StrictAppKit) turns it
// off, as AppKit's push buttons, pop-up buttons, checkboxes, switches and
// table rows have none; HoverOn hovers there too (toolbar, borderless and
// recessed buttons, menu items and scroll bars do in AppKit), and HoverOff
// never hovers.
type Hover uint8

const (
	// HoverAuto: the component shows its hover look unless the theme is
	// strict AppKit; the default.
	HoverAuto Hover = iota
	// HoverOff: the component does not react to hover; Visual.Hot stays 0.
	HoverOff
	// HoverOn: Visual.Hot follows the pointer, whatever the theme.
	HoverOn
)

// Hovers reports whether a component with hover policy h reacts to hover
// with this kit's theme.
func (k *Kit) Hovers(h Hover) bool {
	switch h {
	case HoverOn:
		return true
	case HoverAuto:
		return !k.r.StrictAppKit
	}
	return false
}

// Visual is the visual state of a component, as of this frame's input (or
// forced with Kit.Force). A recipe draws from it and from
// nothing else about the interaction.
type Visual struct {
	// Hot is how hovered the component is, 0 to 1; 0 when it does not
	// hover (Kit.Hovers). It arrives at once and fades out (D103).
	Hot float32
	// Down is how pressed it is, 0 to 1: 1 from the frame of the press while
	// the pointer is on the component, fading out on a release or as the
	// pointer leaves it held down (a release there would not click).
	Down float32
	// Focus is how focused it is, 0 to 1, whether the focus came from the
	// keyboard or the pointer.
	Focus float32
	// Ring is how strongly its focus ring shows, 0 to 1: Focus while the focus
	// is visible (it came from the keyboard, ui.Context.FocusVisible), else 0.
	Ring float32
	// Disabled: the component is in a disabled region (Kit.BeginDisabled). The
	// other values are 0 then.
	Disabled bool
	// Inactive: the window is not the key window; recipes draw the
	// unemphasised look (a grey selection, a default button without the
	// accent).
	Inactive bool
}

// TextEntry returns v with the focus ring of a text-entry control: AppKit's
// NSTextField, NSComboBox, NSTokenField and NSSearchField show it whenever
// they have the focus, however they got it (a click included), where a button
// or another control shows it for keyboard focus only (Visual.Ring as
// VisualOf sets it). No ring shows in a window that is not the key window.
// The four text-entry components draw their ring from it, so they cannot
// disagree.
func (v Visual) TextEntry() Visual {
	v.Ring = 0
	if !v.Inactive {
		v.Ring = v.Focus
	}
	return v
}

// VisualOf reads the visual state of the box with the given key, with the
// component's hover policy, and consumes the look forced with Force.
func (k *Kit) VisualOf(key ui.Key, hover Hover) Visual {
	c := k.c
	v := Visual{Inactive: k.Inactive()}
	f := k.TakeForce()
	if f&LookInactive != 0 {
		v.Inactive = true
	}
	switch {
	case k.Disabled():
		v.Disabled = true
		return v
	case f&^LookInactive != 0:
		if f&LookHover != 0 && k.Hovers(hover) {
			v.Hot = 1
		}
		if f&LookPressed != 0 {
			v.Down = 1
		}
		if f&LookFocused != 0 {
			v.Focus, v.Ring = 1, 1
		}
		return v
	}
	if k.Hovers(hover) {
		v.Hot = c.HotTOf(key)
	}
	v.Down, v.Focus = c.ActiveTOf(key), c.FocusTOf(key)
	if c.FocusVisible() {
		v.Ring = v.Focus
	}
	return v
}

// InteractFlags are the interaction flags of a component's root: clickable, a
// tab stop, animating press and focus (and hover when it hovers), and inert
// (ui.Disabled) inside a disabled region.
func (k *Kit) InteractFlags(hover Hover) ui.Flags {
	if k.Disabled() {
		return ui.Disabled
	}
	f := ui.Clickable | ui.Focusable | ui.ActiveAnimation | ui.FocusAnimation
	if k.Hovers(hover) {
		f |= ui.HotAnimation
	}
	return f
}

// HeldDown is the press value of a component whose press is a drag (a
// slider's knob, a scroll bar, a splitter): 1 while the left button is held
// on it, wherever the pointer goes, else its ActiveT as a release fades. A
// button's press follows the pointer instead (Visual.Down).
func (k *Kit) HeldDown(key ui.Key) float32 {
	if k.c.ActiveKey() == key {
		return 1
	}
	return k.c.ActiveTOf(key)
}

// Signal is what a component reports about the frame, by value.
type Signal struct {
	// ID is the component's root box (for ui.Context.Rect, Tooltip and the
	// like).
	ID ui.ID
	// Hovering: the pointer is over the component.
	Hovering bool
	// Held: the left button went down on the component and is still down.
	Held bool
	// Focused: the component has the keyboard focus.
	Focused bool
	// Clicked: the component was clicked, or activated with Enter or Space,
	// or by an assistive technology's press request. For a choice component
	// the value changed with it (Changed).
	Clicked bool
	// Changed: the value the component edits changed (a checkbox, a slider, a
	// selection, a text field).
	Changed bool
	// RightClicked: the right button was clicked on the component.
	RightClicked bool
	// DoubleClicked: the component was clicked twice quickly.
	DoubleClicked bool
}

// SignalOf reads the pointer and keyboard signal of box id.
func (k *Kit) SignalOf(id ui.ID) Signal {
	s := k.c.Signal(id)
	return Signal{
		ID: id, Hovering: s.Hovering, Held: s.Dragging, Focused: k.c.Focused(id) && !k.Disabled(),
		Clicked: s.Clicked || s.Activated, RightClicked: s.RightClicked, DoubleClicked: s.DoubleClicked,
	}
}
