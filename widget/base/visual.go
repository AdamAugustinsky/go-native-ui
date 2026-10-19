package base

import "gonativeui/ui"

// Hover is a component's hover policy: whether it reacts to the pointer
// resting over it. AppKit's push buttons, pop-up buttons, checkboxes, switches
// and table rows do not; toolbar, borderless and recessed buttons, menu items
// and scroll bars do. The zero value is HoverOff, the macOS default.
type Hover uint8

const (
	// HoverOff: the component does not react to hover; Visual.Hot stays 0.
	HoverOff Hover = iota
	// HoverOn: Visual.Hot follows the pointer, animated.
	HoverOn
)

// Visual is the visual state of a component, as of the previous frame's
// animations (or forced with Kit.Force). A recipe draws from it and from
// nothing else about the interaction.
type Visual struct {
	// Hot is how hovered the component is, 0 to 1; 0 with HoverOff.
	Hot float32
	// Down is how pressed it is, 0 to 1.
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
		if f&LookHover != 0 && hover == HoverOn {
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
	if hover == HoverOn {
		v.Hot = c.HotTOf(key)
	}
	v.Down, v.Focus = c.ActiveTOf(key), c.FocusTOf(key)
	if c.FocusVisible() {
		v.Ring = v.Focus
	}
	return v
}

// InteractFlags are the interaction flags of a component's root: clickable, a
// tab stop, animating press and focus (and hover with HoverOn), and inert
// (ui.Disabled) inside a disabled region.
func (k *Kit) InteractFlags(hover Hover) ui.Flags {
	if k.Disabled() {
		return ui.Disabled
	}
	f := ui.Clickable | ui.Focusable | ui.ActiveAnimation | ui.FocusAnimation
	if hover == HoverOn {
		f |= ui.HotAnimation
	}
	return f
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
