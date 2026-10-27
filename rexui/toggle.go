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

// ToggleOnLook is the look of a toggle button that is on: Rex's capsule in a
// quiet tint of the accent (14 % in light, 24 % in dark) under the label's
// own colour, as a chosen chip or segment is, rather than a block of the
// accent; in a window that is not key a grey (black at 12.5 % over the
// window, as AppKit's NSButtonTypePushOnPushOff, tools/appkitref toggle-on)
// under its label. Off, a toggle button is a ButtonDefault push button
// (ButtonRecipes[ButtonDefault]), a capsule too.
var ToggleOnLook = ButtonRecipe{
	Fill: OfAlpha(theme.Accent, quietTint, quietTintDark), Label: Of(theme.Label),
	FillInactive: OfAlpha(theme.Label, 0.125, 0.124), LabelInactive: Of(theme.Label),
	FillHover: None, FillPressed: OfAlpha(theme.Accent, quietTint*1.6, quietTintDark*1.4), LabelPressed: Of(theme.Label),
	FillPressedInactive: Of(theme.FillPressed), LabelPressedInactive: Of(theme.Label),
	FillDisabled: OfAlpha(theme.Accent, quietTint/2, quietTintDark/2), LabelDisabled: Of(theme.TextDisabled),
	Rim: None, RimInactive: None, Highlight: None,
	Capsule: true,
}

// How much of the accent tints a chosen toggle or segment, in light and in
// dark (where the same share reads fainter over the darker panes).
const quietTint, quietTintDark = 0.14, 0.24

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
