package base

import "gonativeui/ui"

// Press is a clickable, focusable component being built: a button, an icon
// button, a pop-up button, a toolbar item. PressState reads it before the root
// box exists; BeginPress builds the root, EndPress closes it.
type Press struct {
	// ID is the root box, from BeginPress on.
	ID ui.ID
	// Key is the root's key, stable across frames.
	Key ui.Key
	// Name is the shown part of the label (ui.SplitLabel): the accessible
	// name, and the text a recipe draws.
	Name string
	// Hover is the hover policy PressState was given.
	Hover Hover
	Visual
}

// PressState reads the identity and visual state of the press component with
// the label (ui's label rules: "Name###id" shows Name and keeps its identity
// when the text changes), in the current parent. It consumes Force.
func (k *Kit) PressState(label string, hover Hover) Press {
	display, ident := ui.SplitLabel(label)
	key := ui.HashString(k.c.ParentKey(), ident)
	return Press{Key: key, Name: display, Hover: hover, Visual: k.VisualOf(key, hover)}
}

// BeginPress builds the root box of p and pushes it: of size w×h (NextSize
// replaces either), with the role and p's name for the accessibility tree, the
// interaction flags of p's hover policy (inert when disabled) and the drawing
// flags the recipe passes, with whatever style it set with ui's Next calls,
// and disp as its text or image (nil for none). Build the content inside,
// then EndPress.
func (k *Kit) BeginPress(p *Press, role ui.Role, w, h ui.Size, flags ui.Flags, disp []byte) ui.ID {
	c := k.c
	k.ApplySize(w, h)
	c.NextRole(role)
	c.NextSemName(p.Name)
	p.ID = c.BoxKeyed(k.InteractFlags(p.Hover)|flags, p.Key, disp)
	c.Push(p.ID)
	return p.ID
}

// EndPress closes the root BeginPress pushed and reports the frame's signal.
func (k *Kit) EndPress(p *Press) Signal {
	k.c.Pop()
	return k.SignalOf(p.ID)
}

// PressedBefore reports whether the box with key was clicked, activated (Enter,
// Space) or pressed by an assistive technology in the previous frame. Toggles
// and choices apply it before they build, so that the frame shows the new
// value. It is false in a disabled region.
func (k *Kit) PressedBefore(key ui.Key) bool {
	if k.Disabled() {
		return false
	}
	s := k.c.PeekSignal(key)
	return s.Clicked || s.Activated || s.Action&ui.ActPress != 0
}
