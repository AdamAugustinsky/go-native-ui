package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// ToggleOpts are a toggle button's options, passed by value.
type ToggleOpts struct {
	// Size is the control size, as a push button's.
	Size theme.Size
}

// ToggleOnLook is the look of a toggle button that is on (an NSButton of type
// NSButtonTypePushOnPushOff on macOS 27.2, tools/appkitref toggle-on): a push
// button that takes the accent with a white label, as a default button does,
// and in a window that is not key a grey (black at 12.5 % over the window,
// #DFDFDF and #3A3A3A measured) under its label; disabled it is the accent at
// 70 % under a label that is white at 12 %. Off, a toggle button is a
// ButtonDefault push button (ButtonRecipes[ButtonDefault]).
var ToggleOnLook = ButtonRecipe{
	Fill: Of(theme.Accent), Label: Of(theme.OnAccent),
	FillInactive: OfAlpha(theme.Label, 0.125, 0.124), LabelInactive: Of(theme.Label),
	FillHover: None, FillPressed: Of(theme.AccentPressed), LabelPressed: Of(theme.OnAccent),
	FillPressedInactive: Of(theme.FillPressed), LabelPressedInactive: Of(theme.Label),
	FillDisabled: OfAlpha(theme.Accent, 0.7, 0.7), LabelDisabled: OfAlpha(theme.OnAccent, 0.12, 0.12),
	Rim: None, RimInactive: None, Highlight: None,
}

// ToggleButton builds a push button that is on or off and edits *on: a click,
// or Enter or Space with the focus, flips it. On, it is drawn in the accent
// (ToggleOnLook); off, as a push button. The label is its accessible name and
// the text it shows; it is a checkbox to the accessibility tree, as AppKit's
// are, reporting pressed. Changed is set in the frame *on changed.
//
//	rexui.ToggleButton(k, "Bold", &v.bold, rexui.ToggleOpts{})
func ToggleButton(k *base.Kit, label string, on *bool, o ToggleOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	s := &r.Sizes[o.Size%theme.NumSizes]
	tg := k.ToggleBool(label, on, base.HoverOff)
	rc := &ButtonRecipes[ButtonDefault]
	if *on {
		rc = &ToggleOnLook
	}
	fill, fg, _ := rc.Colors(r, tg.Visual)
	h := theme.Snap(s.Height)
	radius := s.Radius
	if rc.Capsule { // the recipe of the state shown: off or on
		radius = h / 2
	}
	var flags ui.Flags
	if fill&0xFF != 0 {
		flags |= ui.DrawBackground
	}
	c.NextBG(fill)
	c.NextRadius(radius)
	c.NextFont(s.Font.Font)
	c.NextTextColor(fg)
	c.NextAlign(k.TakeAlign(ui.AlignCenter))
	if *on { // what BeginToggle does: the state for the accessibility tree
		c.NextSemState(ui.SemChecked)
	}
	k.BeginPress(&tg.Press, ui.RoleCheckbox, ui.TextSize(2*s.PadX), ui.Px(h), flags|ui.DrawText, base.StrBytes(tg.Name))
	k.FocusRing(tg.ID, tg.Visual, radius)
	return k.EndToggle(&tg)
}
