package base

import "gonativeui/ui"

// FieldSignal is what a text field reports.
type FieldSignal struct {
	Signal
	// Submitted: Enter went down in the field.
	Submitted bool
	// Cancelled: Escape went down in the field (a search field clears itself
	// then).
	Cancelled bool
	// Pressed: the left button went down on the field in this frame.
	Pressed bool
	// Key is the key of the box that takes the keyboard focus, which lasts
	// across frames where Signal.ID does not: an app that wants to move the
	// focus into the field (Kit.Focus) keeps it.
	Key ui.Key
}

// FieldKey is the key of the box that takes the keyboard focus in the text
// field built with label inside the box whose key is parent. An app moves the
// focus there with ui.Context.SetFocus before the field has been built.
func FieldKey(parent ui.Key, label string) ui.Key {
	_, ident := ui.SplitLabel(label)
	return ui.HashString(ui.HashString(parent, ident), ident)
}

// TextInput is a single-line text field being built: the box that holds it
// (Wrap, the size the app gave) and the editing box inside it, which is
// ui.Context.TextField (the caret, selection, mouse, copy, cut, paste, undo and
// input method are documented there). BeginTextInput builds the holder and
// reads the visual state; the recipe styles the editing box with ui's Next
// calls and builds it with TextInput; EndTextInput closes the holder.
type TextInput struct {
	// Wrap is the holder box; WrapKey its key.
	Wrap    ui.ID
	WrapKey ui.Key
	// Key is the key of the editing box, the one that takes the focus
	// (FieldKey).
	Key ui.Key
	// Name is the shown part of the label, the field's accessible name.
	Name string
	// H is the field's height in pixels: the default BeginTextInput was given,
	// or a fixed NextHeight.
	H float32
	// Mono: NextMono asked for the monospace font.
	Mono bool
	// Hover is the hover policy BeginTextInput was given; with HoverOff the
	// editing box does not animate hover.
	Hover Hover
	// Visual is the editing box's state: Focus follows the keyboard focus,
	// Hot the pointer (with HoverOn).
	Visual

	ident string
}

// BeginTextInput starts the text field with the label in the current parent:
// it builds the holder box (as wide as the parent and h pixels high unless
// NextSize says otherwise; a fixed NextHeight also becomes H), pushes it, and
// reads the visual state of the editing box (as VisualOf does: nothing but
// Disabled and Inactive in a disabled region). It consumes NextMono and Force.
func (k *Kit) BeginTextInput(label string, h float32, hover Hover) TextInput {
	c := k.c
	display, ident := ui.SplitLabel(label)
	if k.hasH && k.nh.Kind == ui.SizePixels { // NextHeight with a fixed height
		h = k.nh.Value
	}
	ti := TextInput{Name: display, H: h, Mono: k.TakeMono(), Hover: hover, ident: ident}
	ti.WrapKey = ui.HashString(c.ParentKey(), ident)
	ti.Key = ui.HashString(ti.WrapKey, ident)
	k.ApplySize(ui.Pct(1), ui.Px(h))
	ti.Wrap = c.BoxKeyed(0, ti.WrapKey, nil)
	c.Push(ti.Wrap)

	ti.Visual = k.VisualOf(ti.Key, hover)
	return ti
}

// TextInput builds the editing box of ti, editing buf, in the style the recipe
// set with ui's Next calls (size, fill, border, padding, font, text colour):
// with role (ui.RoleTextField or ui.RoleSearchField) and the name, inert in a
// disabled region, and with escapeClears Escape clears the field rather than
// closing a popup or a dialog around it (a search field).
func (k *Kit) TextInput(ti *TextInput, buf *ui.TextBuffer, role ui.Role, escapeClears bool) FieldSignal {
	c := k.c
	if k.Disabled() {
		c.NextFlags(ui.Disabled)
	}
	c.NextRole(role)
	c.NextSemName(ti.Name)
	if ti.Hover == HoverOff {
		c.NextFieldNoHover()
	}
	if escapeClears {
		c.NextEscapeClears()
	}
	fs := c.TextField(ti.ident, buf)
	return FieldSignal{
		Signal:    Signal{ID: fs.ID, Hovering: fs.Hovering, Focused: fs.Focused, Changed: fs.Changed, Clicked: fs.Clicked},
		Submitted: fs.Submitted, Cancelled: fs.Cancelled, Pressed: fs.Pressed, Key: ti.Key,
	}
}

// Composing reports whether the field that reported fs shows an input-method
// composition, which takes the place of a placeholder.
func (k *Kit) Composing(fs FieldSignal) bool {
	return fs.Focused && k.c.Input().NPreedit > 0
}

// ClearField empties buf, the text of the field that reported fs, as a user
// edit (a search field's clear button, Escape): the field drops its undo
// state for it and the signal reports the change.
func (k *Kit) ClearField(buf *ui.TextBuffer, fs *FieldSignal) {
	buf.Reset()
	k.c.FieldEmptied(fs.ID)
	fs.Changed = true
}

// FieldWidth is the width in pixels of ti's holder in the previous frame.
func (k *Kit) FieldWidth(ti *TextInput) float32 {
	r := k.c.RectOf(ti.WrapKey)
	return r[2] - r[0]
}

// EndTextInput closes the holder BeginTextInput pushed.
func (k *Kit) EndTextInput(ti *TextInput) { k.c.Pop() }
