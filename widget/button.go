package widget

import (
	"gonativeui/icon"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
)

// Signal is what a widget reports about the frame, by value.
type Signal struct {
	// ID is the widget's root box (for ui.Context.Rect, Tooltip and the like).
	ID ui.ID
	// Hovering: the pointer is over the widget.
	Hovering bool
	// Held: the left button went down on the widget and is still down.
	Held bool
	// Focused: the widget has the keyboard focus.
	Focused bool
	// Clicked: the widget was clicked, or activated with Enter or Space, or by
	// an assistive technology's press request. For a choice widget the value
	// changed with it (Changed).
	Clicked bool
	// Changed: the value the widget edits changed (a checkbox, a slider, a
	// selection, a text field).
	Changed bool
	// RightClicked: the right button was clicked on the widget.
	RightClicked bool
	// DoubleClicked: the widget was clicked twice quickly.
	DoubleClicked bool
}

// signal reads the pointer and keyboard signal of box id.
func (k *Kit) signal(id ui.ID) Signal {
	s := k.c.Signal(id)
	return Signal{
		ID: id, Hovering: s.Hovering, Held: s.Dragging, Focused: k.c.Focused(id) && !k.off(),
		Clicked: s.Clicked || s.Activated, RightClicked: s.RightClicked, DoubleClicked: s.DoubleClicked,
	}
}

// ButtonKind selects how a button looks.
type ButtonKind uint8

const (
	// ButtonDefault is a button on the theme's control fill with an outline.
	ButtonDefault ButtonKind = iota
	// ButtonPrimary is filled with the accent colour: the main action.
	ButtonPrimary
	// ButtonPlain has no fill or outline until the pointer is over it.
	ButtonPlain
	// ButtonDanger is a default button whose text is in the danger colour.
	ButtonDanger
)

// What a button shows.
const (
	btnText = iota
	btnIconText
	btnIconOnly
)

// Button builds a default button. Label follows ui's label rules ("Name###id"
// shows Name and keeps its identity when the text changes).
func (k *Kit) Button(label string) Signal { return k.button(label, 0, btnText, ButtonDefault) }

// ButtonKind builds a button of the given kind.
func (k *Kit) ButtonKind(label string, kind ButtonKind) Signal {
	return k.button(label, 0, btnText, kind)
}

// ButtonIcon builds a button with an icon before its label.
func (k *Kit) ButtonIcon(ic icon.ID, label string, kind ButtonKind) Signal {
	return k.button(label, ic, btnIconText, kind)
}

// IconButton builds a square button that shows only the icon. name is its
// accessible name (and what a tooltip should say).
func (k *Kit) IconButton(ic icon.ID, name string, kind ButtonKind) Signal {
	return k.button(name, ic, btnIconOnly, kind)
}

func (k *Kit) button(label string, ic icon.ID, show int, kind ButtonKind) Signal {
	c := k.c
	p := &k.r.Pal
	px := &k.r.Px
	display, ident := ui.SplitLabel(label)

	dim := k.dim()
	key := ui.HashString(c.ParentKey(), ident)
	v := k.vis(key)

	h := theme.Snap(px.ControlH)
	var bg, fg, border uint32
	switch kind {
	case ButtonPrimary:
		bg, fg = p.Accent, p.OnAccent
	case ButtonPlain:
		fg = p.Text
	case ButtonDanger:
		bg, border, fg = p.ControlFill, p.ControlBorder, p.Danger
	default:
		bg, border, fg = p.ControlFill, p.ControlBorder, p.Text
	}
	font := k.r.Font.UI
	if kind == ButtonPrimary {
		font = k.r.Font.UISemi
	}
	flags := k.flags()
	if bg != 0 || border != 0 {
		flags |= ui.DrawBackground | ui.DrawBorder
	}
	c.NextBG(bg)
	c.NextBorder(border, px.BorderW)
	c.NextRadius(px.RadiusMedium)
	c.NextRole(ui.RoleButton)
	c.NextSemName(display)

	var disp []byte
	switch show {
	case btnIconOnly:
		var f ui.Flags
		f, disp = k.icons.Prepare(c, ic, fg)
		flags |= f
		k.size(ui.Px(h), ui.Px(h))
	case btnIconText:
		k.size(ui.Children(1), ui.Px(h))
		c.NextAxis(ui.AxisX)
		c.NextGap(px.GapSmall)
		c.NextPadding(px.Pad, 0, px.Pad, 0)
	default:
		k.size(ui.TextSize(2*px.Pad), ui.Px(h))
		c.NextFont(font)
		c.NextTextColor(fg)
		c.NextAlign(k.align(ui.AlignCenter))
		flags |= ui.DrawText
		disp = strBytes(display)
	}
	id := c.BoxKeyed(flags, key, disp)
	c.Push(id)
	if show == btnIconText {
		k.iconLabel(key, ic, display, font, fg, h)
	}
	k.decorate(id, v, px.RadiusMedium)
	c.Pop()
	sig := k.signal(id)
	k.undim(dim)
	return sig
}

// iconLabel builds the icon and the text of a button that has both, in the
// current parent (the button).
func (k *Kit) iconLabel(key ui.Key, ic icon.ID, label string, font text.FontKey, fg uint32, h float32) {
	c := k.c
	flags, disp := k.icons.Prepare(c, ic, fg)
	_, ih := k.icons.Size(ic)
	k.center(ih, h)
	c.BoxKeyed(flags|ui.FloatingY, ui.KeyN(key, 1), disp)
	if label != "" {
		c.NextSize(ui.TextSize(0), ui.Px(h))
		c.NextFont(font)
		c.NextTextColor(fg)
		c.BoxKeyed(ui.DrawText, ui.KeyN(key, 2), strBytes(label))
	}
}
