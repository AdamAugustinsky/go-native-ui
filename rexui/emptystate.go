package rexui

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// EmptyStateOpts are an empty state's options, passed by value.
type EmptyStateOpts struct {
	// Icon is the glyph above the title, drawn in the tertiary label colour,
	// when HasIcon (icon.ID 0 is a real icon). Add a large one to the kit's
	// atlas (base.Kit.AddIcon with icon.Spec.Pt of 36 to 48).
	Icon    icon.ID
	HasIcon bool
	// Message is the explanation below the title, in the secondary label
	// colour, wrapped to EmptyStateLook.MessageW.
	Message string
	// Action is the label of the button below, if any.
	Action string
}

// EmptyStateRecipe is the look of empty states (Finder's and Mail's empty
// views: a quiet glyph, a title and a line of explanation). Designed, not
// measured.
type EmptyStateRecipe struct {
	// MessageW is the widest the message gets, in points; Gap the space
	// between the parts and ActionGap the one above the button.
	MessageW, Gap, ActionGap float32
	// Glyph, Title and Message are the colours.
	Glyph, Title, Message Tint
}

// EmptyStateLook is the empty states' recipe.
var EmptyStateLook = EmptyStateRecipe{
	MessageW: 280, Gap: 6, ActionGap: 12,
	Glyph: Of(theme.Label3), Title: Of(theme.Label), Message: Of(theme.Label2),
}

// EmptyStateSignal is what an empty state reports.
type EmptyStateSignal struct {
	// ID is the state's root.
	ID ui.ID
	// Action: the action button was clicked.
	Action bool
}

// EmptyState builds what a list shows when it has nothing: a glyph, the title
// (Title 3) and a message, centred in the parent, and an action button. It
// fills the parent (the kit's NextSize says otherwise). The title is the
// accessible name of the group.
//
//	if rexui.EmptyState(k, "No messages", rexui.EmptyStateOpts{Message: "Start a conversation.", Action: "New Message"}).Action {
//		v.compose()
//	}
func EmptyState(k *base.Kit, title string, o EmptyStateOpts) EmptyStateSignal {
	c, r := k.Context(), k.Theme()
	rc := &EmptyStateLook
	sc := k.Scale()
	display, ident := ui.SplitLabel(title)
	key := ui.HashString(c.ParentKey(), ident)
	k.ApplySize(ui.Pct(1), ui.Pct(1))
	c.NextAxis(ui.AxisY)
	c.NextCross(ui.CrossCenter)
	c.NextJustify(ui.JustifyCenter)
	c.NextGap(pt(sc, rc.Gap))
	c.NextRole(ui.RoleGroup)
	c.NextSemName(display)
	if o.Message != "" {
		c.NextSemValueBytes(base.StrBytes(o.Message)) // the explanation is the group's value
	}
	root := c.BoxKeyed(disabledFlag(k), key, nil)
	c.Push(root)
	sig := EmptyStateSignal{ID: root}
	if o.HasIcon {
		k.Icon(o.Icon, rc.Glyph.RGBA(r))
	}
	st := &r.Styles[theme.StyleTitle3]
	c.NextSize(ui.TextSize(0), ui.Px(st.Line))
	c.NextFont(st.Font)
	c.NextTextColor(rc.Title.RGBA(r))
	c.NextAlign(ui.AlignCenter)
	c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.KeyN(key, 1), base.StrBytes(display))
	if o.Message != "" {
		ms := &r.Styles[theme.StyleBody]
		c.NextSize(ui.Px(pt(sc, rc.MessageW)), ui.TextSize(0))
		c.NextFont(ms.Font)
		c.NextTextColor(rc.Message.RGBA(r))
		c.NextAlign(ui.AlignCenter)
		c.BoxKeyed(ui.DrawText|ui.WrapText, ui.KeyN(key, 2), base.StrBytes(o.Message))
	}
	if o.Action != "" {
		c.NextPadding(0, pt(sc, rc.ActionGap-rc.Gap), 0, 0)
		wrap := c.BoxKeyed(0, ui.KeyN(key, 3), nil)
		c.Push(wrap)
		sig.Action = Button(k, o.Action, ButtonOpts{}).Clicked
		c.Pop()
	}
	c.Pop()
	return sig
}
