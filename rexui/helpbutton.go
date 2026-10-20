package rexui

import (
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// HelpButtonOpts are a help button's options, passed by value.
type HelpButtonOpts struct {
	// Size is the control size: the circle's diameter is the size's control
	// height (24 pt at regular size, measured; the others are that scaled,
	// not measured).
	Size theme.Size
}

// HelpButtonRecipe is the look of the round help button (NSButton with
// NSBezelStyleHelpButton on macOS 27.2: a 24 pt circle of a push button's
// fill holding a bold question mark).
type HelpButtonRecipe struct {
	// Glyph is the question mark's point size at the regular size (16.5 pt regular: its ink is 7 × 12 pt) and DropY how far its ink centre lies
	// below the circle's centre, in points (1, measured).
	Glyph, DropY float32
	// Weight is the question mark's font weight.
	Weight int
}

// HelpButtonLook is the help button's recipe; its fills and label colour are
// ButtonDefault's.
var HelpButtonLook = HelpButtonRecipe{Glyph: 16.5, DropY: 1, Weight: theme.WeightRegular}

// HelpButton builds the round "?" button that opens help for what it sits
// beside, and reports its signal (Clicked on a click, Enter or Space with the
// focus, or an accessibility press). The label is its accessible name ("Help").
//
//	if rexui.HelpButton(k, "Help", rexui.HelpButtonOpts{}).Clicked { openHelp() }
func HelpButton(k *base.Kit, label string, o HelpButtonOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &HelpButtonLook
	s := &r.Sizes[o.Size%theme.NumSizes]
	p := k.PressState(label, base.HoverOff)
	fill, fg, _ := ButtonRecipes[ButtonDefault].Colors(r, p.Visual)
	d := theme.Snap(s.Height)
	f := k.TextSystem().Font(text.FontDesc{SizePt: rc.Glyph * s.Height / (24 * k.Scale()), Weight: rc.Weight, Scale: k.Scale()})
	c.NextBG(fill)
	c.NextRadius(d / 2)
	c.NextFont(f)
	c.NextTextColor(fg)
	c.NextAlign(ui.AlignCenter)
	c.NextPadding(0, theme.Snap(2*rc.DropY*k.Scale()*s.Height/(24*k.Scale())), 0, 0)
	k.BeginPress(&p, ui.RoleButton, ui.Px(d), ui.Px(d), ui.DrawBackground|ui.DrawText, base.StrBytes("?"))
	k.FocusRing(p.ID, p.Visual, d/2)
	return k.EndPress(&p)
}
