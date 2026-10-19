package widget

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/widget/base"
)

// Signal is what a widget reports about the frame, by value (base.Signal).
type Signal = base.Signal

// ButtonKind selects how a button looks.
type ButtonKind uint8

const (
	// ButtonDefault is a push button (rexui.ButtonDefault).
	ButtonDefault ButtonKind = iota
	// ButtonPrimary is the default button, filled with the accent: the main
	// action (rexui.ButtonPrimary).
	ButtonPrimary
	// ButtonPlain has no fill (rexui.ButtonPlain; for an icon button,
	// rexui.IconBorderless).
	ButtonPlain
	// ButtonDanger is a destructive action (rexui.ButtonDestructive).
	ButtonDanger
)

// buttonVariants are the rexui variants of the kinds.
var buttonVariants = [...]rexui.ButtonVariant{
	ButtonDefault: rexui.ButtonDefault,
	ButtonPrimary: rexui.ButtonPrimary,
	ButtonPlain:   rexui.ButtonPlain,
	ButtonDanger:  rexui.ButtonDestructive,
}

func (kind ButtonKind) variant() rexui.ButtonVariant {
	if int(kind) < len(buttonVariants) {
		return buttonVariants[kind]
	}
	return rexui.ButtonDefault
}

// Button builds a default button (rexui.Button). Label follows ui's label
// rules ("Name###id" shows Name and keeps its identity when the text changes).
func (k *Kit) Button(label string) Signal { return k.ButtonKind(label, ButtonDefault) }

// ButtonKind builds a button of the given kind.
func (k *Kit) ButtonKind(label string, kind ButtonKind) Signal {
	return rexui.Button(k.Kit, label, rexui.ButtonOpts{Variant: kind.variant()})
}

// ButtonIcon builds a button with an icon before its label.
func (k *Kit) ButtonIcon(ic icon.ID, label string, kind ButtonKind) Signal {
	return rexui.ButtonIcon(k.Kit, ic, label, rexui.ButtonOpts{Variant: kind.variant()})
}

// iconVariants are the rexui icon-button variants of the kinds.
var iconVariants = [...]rexui.IconButtonVariant{
	ButtonDefault: rexui.IconBordered,
	ButtonPrimary: rexui.IconPrimary,
	ButtonPlain:   rexui.IconBorderless,
	ButtonDanger:  rexui.IconDestructive,
}

// IconButton builds a button that shows only the icon (rexui.IconButton:
// bordered in the kind's colours, or borderless for ButtonPlain). name is its
// accessible name (and what a tooltip should say).
func (k *Kit) IconButton(ic icon.ID, name string, kind ButtonKind) Signal {
	v := rexui.IconBordered
	if int(kind) < len(iconVariants) {
		v = iconVariants[kind]
	}
	return rexui.IconButton(k.Kit, ic, name, rexui.IconButtonOpts{Variant: v})
}
