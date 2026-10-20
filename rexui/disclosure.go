package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// DisclosureVariant selects a disclosure control's look.
type DisclosureVariant uint8

const (
	// DisclosureTriangle is the disclosure triangle of outline rows and
	// groups (NSButtonBezelStyleDisclosure): a 13 pt box holding a chevron
	// that points right when closed and down when open. On macOS 27.2 the
	// "triangle" is a chevron.
	DisclosureTriangle DisclosureVariant = iota
	// DisclosurePush is the push disclosure button
	// (NSButtonBezelStylePushDisclosure): a 24 pt rounded square of a push
	// button's fill holding a chevron that points down when closed and up
	// when open.
	DisclosurePush

	// NumDisclosureVariants is the number of variants.
	NumDisclosureVariants
)

// DisclosureOpts are a disclosure control's options, passed by value.
type DisclosureOpts struct {
	// Variant is the look.
	Variant DisclosureVariant
	// Backdrop is the colour the triangle's translucent chevron lies on (the
	// zero Tint is the window background): the chevron is drawn in the
	// opaque colour it has over it. DisclosurePush draws on its own body.
	Backdrop Tint
}

// DisclosureRecipe is the look of disclosure controls (NSButton on macOS
// 27.2, tools/appkitref button:disclosure-closed/-open and push-disclosure).
type DisclosureRecipe struct {
	// Box is the triangle's hit box, and PushBox the push button's side, in
	// points (13 and 24, measured).
	Box, PushBox float32
	// PushRadius is the push button's corner radius, in points (5.9, measured).
	PushRadius float32
	// Closed and PushClosed are the chevrons' centrelines at rest, in points
	// from the top left of the box, and Width their stroke's width. Opening
	// turns them: the triangle's a quarter turn clockwise (right to down),
	// the push button's a half turn (down to up).
	Closed, PushClosed Chevron
	Width, PushWidth   float32
	// Open is the turn of the open chevron in degrees: a quarter turn for
	// the triangle, a half for the push button.
	Open, PushOpen float32
	// Rest, Pressed and Disabled colour the triangle's chevron: black at
	// 23.5 % in light and white at 41 % in dark measured over the window
	// (#C3C3C3 and #7A7A7A over white and #1E1E1E; no role names them).
	// Pressed and Disabled were not measured.
	Rest, Pressed, Disabled Tint
}

// DisclosureLook is the disclosure controls' recipe. The chevrons were fitted
// to AppKitRef's renders: the arms' centrelines are 45° ± 6° from the
// vertical, the triangle's 1.3 pt and the push button's 1.2 pt wide (the width that gives AppKit's amount of ink).
var DisclosureLook = DisclosureRecipe{
	Box: 13, PushBox: 24, PushRadius: 5.9,
	Closed:     Chevron{A: [2]float32{5.75, 2.35}, Apex: [2]float32{8.95, 6.25}, B: [2]float32{5.75, 10.15}, Centre: [2]float32{6.6, 6.2}},
	PushClosed: Chevron{A: [2]float32{6.5, 9.8}, Apex: [2]float32{11.55, 15.15}, B: [2]float32{16.6, 9.8}, Centre: [2]float32{11.55, 12.5}},
	Width:      1.3, PushWidth: 1.2,
	Open: 90, PushOpen: 180,
	Rest: OfAlpha(theme.Label, 0.235, 0.41), Pressed: Of(theme.Label2), Disabled: OfAlpha(theme.Label, 0.1, 0.15),
}

// Disclosure builds a disclosure triangle or push disclosure button that
// edits *open. A click, or Enter or Space with the focus, toggles it; the
// chevron turns over the theme's Standard duration (at once under Reduce
// Motion). The label is the accessible name; the control reports expanded to
// assistive technologies. Changed is set in the frame *open changed.
//
//	if rexui.Disclosure(k, "Advanced", &v.advanced, rexui.DisclosureOpts{}).Changed { … }
func Disclosure(k *base.Kit, label string, open *bool, o DisclosureOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &DisclosureLook
	push := o.Variant%NumDisclosureVariants == DisclosurePush
	tg := k.ToggleBool(label, open, base.HoverOff)
	target := float32(0)
	if *open {
		target = 1
	}
	rate := RateFor(r.Motion.Standard)
	if reduced(k) {
		rate = theme.Instant
	}
	t := c.AnimateValue(ui.KeyN(tg.Key, 5), target, rate)

	box, radius, ch, deg, w := rc.Box, float32(0), &rc.Closed, rc.Open, rc.Width
	var fill, fg uint32
	var flags ui.Flags
	under := o.Backdrop.RGBA(r)
	if under == 0 { // the zero Tint: the window
		under = r.Colors[theme.Window]
	}
	if push {
		box, radius, ch, deg, w = rc.PushBox, rc.PushRadius*k.Scale(), &rc.PushClosed, rc.PushOpen, rc.PushWidth
		fill, fg, _ = ButtonRecipes[ButtonDefault].Colors(r, tg.Visual)
		under = ink(fill, under)
		flags = ui.DrawBackground
	} else {
		fg = rc.Rest.RGBA(r)
		switch {
		case tg.Disabled:
			fg = rc.Disabled.RGBA(r)
		case tg.Down > 0:
			fg = mix(fg, rc.Pressed.RGBA(r), tg.Down)
		}
	}
	px := theme.Snap(box * k.Scale())
	var state ui.SemState
	if *open {
		state = ui.SemExpanded
	}
	c.NextSemState(state)
	c.NextBG(fill)
	c.NextRadius(radius)
	k.BeginPress(&tg.Press, ui.RoleButton, ui.Px(px), ui.Px(px), flags, nil)
	chevron(c, tg.Key, 100, ch, t*deg, w, k.Scale(), ink(fg, under))
	k.FocusRing(tg.ID, tg.Visual, max(radius, 2))
	sig := k.EndPress(&tg.Press)
	sig.Changed = tg.Changed
	return sig
}
