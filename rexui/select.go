package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// SelectVariant selects a pop-up button's look.
type SelectVariant uint8

const (
	// SelectDefault is a pop-up button: the secondary fill, the chosen
	// option and the up-down chevron (NSPopUpButton).
	SelectDefault SelectVariant = iota
	// SelectBorderless has no fill (popup-borderless).
	SelectBorderless
	// SelectPullDown is a pull-down button: it shows its label, not the
	// chosen option, and a down chevron (pulldown).
	SelectPullDown

	// NumSelectVariants is the number of variants.
	NumSelectVariants
)

// SelectOpts are a pop-up button's options, passed by value.
type SelectOpts struct {
	// Variant is the look.
	Variant SelectVariant
	// Size is the control size.
	Size theme.Size
	// Lead, with HasLead, is a symbol before the value (a web app's select:
	// a folder before a project, a branch before a branch's name), in the
	// value's colour.
	Lead    icon.ID
	HasLead bool
}

// SelectRecipe is the look of pop-up buttons; their fills are ButtonDefault's.
type SelectRecipe struct {
	_ [0]func() // not comparable: nothing compares recipes, and their generated == is linked into every app (D101)
	// Width is the width of a pop-up button that NextWidth does not size, in
	// points.
	Width float32
	// ChevronRight is the chevron's centre from the end per variant, in
	// points (14 for the up-down chevron, 10 borderless, 13.25 for a
	// pull-down's, measured at regular size).
	ChevronRight [NumSelectVariants]float32
	// PadX is the label's distance from the start per variant, in points;
	// 0 takes the size's (SizeSpec.PadX). A borderless pop-up button's is 8.
	PadX [NumSelectVariants]float32
	// Down draws a pop-up button's chevron as one chevron pointing down, a
	// web app's select's, rather than AppKit's double one; FitWidth makes a
	// borderless pop-up button as wide as its lead, value and chevron, a
	// web app's toolbar pill, unless NextWidth says otherwise.
	Down, FitWidth bool
	// Text, when not the zero Tint, colours a borderless pop-up button's
	// value, lead and chevron (a web toolbar pill's quieter text).
	Text Tint
	// Chevron, when not the zero Tint, colours a web pill's chevron
	// (FitWidth), quieter than its text as a web select's (--fg-35).
	Chevron Tint
}

// SelectLook is the pop-up buttons' recipe.
var SelectLook = SelectRecipe{
	Width:        160,
	ChevronRight: [NumSelectVariants]float32{14, 10, 13.25},
	PadX:         [NumSelectVariants]float32{0, 8, 0},
}

// SelectSignal is what a pop-up button reports.
type SelectSignal struct {
	base.Signal
	// Open: the drawn popup of its options is showing (never with native
	// menus, where the platform's menu shows them); widget.Kit.Select builds
	// it, and base.Popup records which component it belongs to.
	Open bool
	// Key is the button's key, the popup's owner.
	Key ui.Key
}

// Select builds a pop-up button showing options[*sel] (or, for a pull-down,
// its label). A click, or Enter, Space or Down with the focus, opens its
// options. With native menus (ui.Context.NativeMenus) they are the system's
// pop-up menu (ui.Context.RequestMenu); the choice comes back through
// ui.Context.MenuChoice, sets *sel and reports Changed, and Open stays false.
// Otherwise they are the drawn popup (base.PopupSelect), which a second click
// closes; Open reports it and the caller builds it. The width is
// SelectLook.Width unless the kit's NextWidth says otherwise.
//
//	s := rexui.Select(k, "Sort", &v.sort, sorts, rexui.SelectOpts{})
//	if s.Open { … build the options below s.ID … }
func Select(k *base.Kit, label string, sel *int, options []string, o SelectOpts) SelectSignal {
	c, r := k.Context(), k.Theme()
	v := o.Variant % NumSelectVariants
	rc := &ButtonRecipes[ButtonDefault]
	s := &r.Sizes[o.Size%theme.NumSizes]
	pill := SelectLook.FitWidth && v == SelectBorderless
	hover := base.HoverOff
	if pill {
		hover = base.HoverOn // a web pill fills under the pointer
	}
	p := k.PressState(label, hover)
	pop := k.Popup()
	open := pop.OpenBy(base.PopupSelect, p.Key)

	shown := p.Name
	if v != SelectPullDown {
		shown = ""
		if *sel >= 0 && *sel < len(options) {
			shown = options[*sel]
		}
	}
	fill, fg, _ := rc.Colors(r, p.Visual)
	padX := s.PadX
	if x := SelectLook.PadX[v]; x > 0 {
		padX = k.Pt(x)
	}
	if v == SelectBorderless {
		fill = 0
		if pill {
			fill = theme.WithAlpha(r.Colors[theme.RowHover], max(p.Visual.Hot, p.Visual.Down))
			if open {
				fill = r.Colors[theme.RowHover]
			}
		}
		if SelectLook.Text != (Tint{}) && !p.Visual.Disabled {
			fg = SelectLook.Text.RGBA(r)
		}
	}
	h := theme.Snap(s.Height)
	radius := s.Radius
	if rc.Capsule && !pill { // the push buttons' shape: Rex's capsule; a web pill keeps the size's radius
		radius = h / 2
	}
	var state ui.SemState
	if open {
		state = ui.SemExpanded
	}
	c.NextBG(fill)
	c.NextRadius(radius)
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextPadding(padX, 0, 0, 0)
	c.NextSemValueBytes(base.StrBytes(shown))
	c.NextSemState(state)
	role := ui.RoleComboBox
	if v == SelectPullDown {
		role = ui.RoleButton
	}
	chev := icon.ChevronUpDown
	if SelectLook.Down {
		chev = icon.ChevronDown
	}
	if v == SelectPullDown {
		chev = icon.PullDown
	}
	cw, _ := k.IconSize(chev)
	if pill {
		// the pill's own width: lead, value and chevron, padded as a web
		// select (8 at each end, 6 between)
		gap := k.Pt(6)
		c.NextGap(gap)
		c.NextPadding(padX, 0, padX, 0)
		id := k.BeginPress(&p, role, ui.Children(1), ui.Px(h), ui.DrawBackground, nil)
		if o.HasLead {
			if f, disp := k.Icons().Prepare(c, o.Lead, fg); f != 0 {
				lw, lh := k.IconSize(o.Lead)
				c.NextSize(ui.Px(lw), ui.Px(lh))
				c.BoxKeyed(f, ui.KeyN(p.Key, 3), disp)
			}
		}
		c.NextSize(ui.TextSize(0), ui.Px(h))
		c.NextMaxSize(k.Pt(SelectLook.Width), 0)
		c.NextFont(s.Font.Font)
		c.NextTextColor(fg)
		c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(p.Key, 1), base.StrBytes(shown))
		cf := fg
		if SelectLook.Chevron != (Tint{}) && !p.Visual.Disabled {
			cf = SelectLook.Chevron.RGBA(r)
		}
		if f, disp := k.Icons().Prepare(c, chev, cf); f != 0 {
			_, ch := k.IconSize(chev)
			c.NextSize(ui.Px(cw), ui.Px(ch))
			c.BoxKeyed(f, ui.KeyN(p.Key, 2), disp)
		}
		c.Pop()
		return selectEnd(k, id, &p, sel, options, v, open, radius)
	}
	id := k.BeginPress(&p, role, k.Fixed(SelectLook.Width), ui.Px(h), ui.DrawBackground|ui.Clip, nil)
	rr := c.Rect(id)
	w := rr[2] - rr[0]
	cx := k.Pt(SelectLook.ChevronRight[v])
	c.NextSize(ui.Px(max(w-padX-cx-cw/2, 0)), ui.Px(h))
	c.NextFont(s.Font.Font)
	c.NextTextColor(fg)
	c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(p.Key, 1), base.StrBytes(shown))
	if f, disp := k.Icons().Prepare(c, chev, fg); f != 0 {
		_, ch := k.IconSize(chev)
		theme.At(c, theme.Snap(w-cx-cw/2), theme.Snap((h-ch)/2), cw, ch)
		c.BoxKeyed(f|base.Floating, ui.KeyN(p.Key, 2), disp)
	}
	c.Pop()
	return selectEnd(k, id, &p, sel, options, v, open, radius)
}

// selectEnd is Select's behaviour once its button is built: the focus ring,
// the options' menu or popup, and the signal.
func selectEnd(k *base.Kit, id ui.ID, p *base.Press, sel *int, options []string, v SelectVariant, open bool, radius float32) SelectSignal {
	c := k.Context()
	pop := k.Popup()
	rr := c.Rect(id)
	k.FocusRing(id, p.Visual, radius)

	if c.NativeMenus() { // the platform's pop-up menu (N1), never the drawn popup
		var changed bool
		if !k.Disabled() {
			sig := c.Signal(id)
			if sig.Clicked || sig.Activated || sig.Action&(ui.ActExpand|ui.ActShowMenu) != 0 || (c.Focused(id) && c.TakeKey(ui.KeyDown, 0)) {
				chosen := *sel
				if v == SelectPullDown {
					chosen = -1
				}
				c.RequestMenu(ui.MenuRequest{Kind: ui.MenuList, Owner: p.Key, Options: options, Selected: chosen, Rect: rr})
			}
			if i, ok := c.MenuChoice(id, options); ok && i != *sel {
				// the box was built with the old value: show the new one in
				// a frame of its own (a keyboard choice leaves no events)
				*sel, changed = i, true
				c.RequestFrame()
			}
		}
		res := SelectSignal{Signal: k.SignalOf(id), Key: p.Key}
		res.Changed = changed
		return res
	}
	if !k.Disabled() {
		sig := c.Signal(id)
		switch {
		case sig.Clicked || sig.Activated || sig.Action&(ui.ActExpand|ui.ActShowMenu) != 0:
			if open {
				pop.Reset()
				open = false
			} else {
				k.OpenPopup(base.PopupSelect, p.Key, 0, rr)
				open = true
			}
		case c.Focused(id) && !open && c.TakeKey(ui.KeyDown, 0):
			k.OpenPopup(base.PopupSelect, p.Key, 0, rr)
			open = true
		}
	}
	return SelectSignal{Signal: k.SignalOf(id), Open: open && !k.Disabled(), Key: p.Key}
}
