package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// SeparatorOpts are a separator's options, passed by value.
type SeparatorOpts struct {
	// Inset is the space left bare at each end, in points (an inset
	// separator between the rows of a form).
	Inset float32
	// Hairline draws one device pixel (theme.Lines.Hairline) instead of
	// NSBox's one point: the dividers inside lists, forms and toolbars.
	Hairline bool
}

// SeparatorRecipe is the look of separators.
type SeparatorRecipe struct {
	// Color is the line's colour (separatorColor).
	Color Tint
	// Width is its thickness in points (an NSBox separator is 1 pt: two
	// device pixels of #E6E6E6 over white measured).
	Width float32
}

// SeparatorLook is the separators' recipe.
var SeparatorLook = SeparatorRecipe{Color: Of(theme.Separator), Width: 1}

// Separator builds a line across its parent: horizontal in a column,
// vertical in a row.
//
//	rexui.Separator(k, rexui.SeparatorOpts{})
func Separator(k *base.Kit, o SeparatorOpts) ui.ID {
	c, r := k.Context(), k.Theme()
	lw := pt(k.Scale(), SeparatorLook.Width)
	if o.Hairline {
		lw = r.Lines.Hairline
	}
	in := k.Pt(o.Inset)
	row := c.Parent() != 0 && k.InRow()
	if row {
		c.NextPadding(0, in, 0, in)
		k.ApplySize(ui.Px(lw), ui.Pct(1))
	} else {
		c.NextPadding(in, 0, in, 0)
		k.ApplySize(ui.Pct(1), ui.Px(lw))
	}
	id := c.Box(0, "separator")
	c.Push(id)
	c.NextBG(SeparatorLook.Color.RGBA(r))
	if row {
		c.NextSize(ui.Px(lw), ui.Pct(1))
	} else {
		c.NextSize(ui.Pct(1), ui.Px(lw))
	}
	c.Box(ui.DrawBackground, "line")
	c.Pop()
	return id
}
