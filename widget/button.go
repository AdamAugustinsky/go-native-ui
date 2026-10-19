package widget

import (
	"gonativeui/icon"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Signal is what a widget reports about the frame, by value (base.Signal).
type Signal = base.Signal

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

// button is the classic look over base's Press primitive.
func (k *Kit) button(label string, ic icon.ID, show int, kind ButtonKind) Signal {
	c := k.c
	p := &k.r.Pal
	px := &k.r.Px

	dim := k.dim()
	b := k.PressState(label, hoverOn)

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
	var flags ui.Flags
	if bg != 0 || border != 0 {
		flags |= ui.DrawBackground | ui.DrawBorder
	}
	c.NextBG(bg)
	c.NextBorder(border, px.BorderW)
	c.NextRadius(px.RadiusMedium)

	var disp []byte
	w, hs := ui.TextSize(2*px.Pad), ui.Px(h)
	switch show {
	case btnIconOnly:
		var f ui.Flags
		f, disp = k.icons.Prepare(c, ic, fg)
		flags |= f
		w = ui.Px(h)
	case btnIconText:
		w = ui.Children(1)
		c.NextAxis(ui.AxisX)
		c.NextGap(px.GapSmall)
		c.NextPadding(px.Pad, 0, px.Pad, 0)
	default:
		c.NextFont(font)
		c.NextTextColor(fg)
		c.NextAlign(k.align(ui.AlignCenter))
		flags |= ui.DrawText
		disp = strBytes(b.Name)
	}
	id := k.BeginPress(&b, ui.RoleButton, w, hs, flags, disp)
	if show == btnIconText {
		k.iconLabel(b.Key, ic, b.Name, font, fg, h)
	}
	k.decorate(id, b.Visual, px.RadiusMedium)
	sig := k.EndPress(&b)
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
