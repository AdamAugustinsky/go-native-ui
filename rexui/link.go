package rexui

import (
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// LinkOpts are a link's options, passed by value. The zero value is body text
// that is always underlined.
type LinkOpts struct {
	// Style is the text style (TextBody: 13 pt).
	Style TextStyle
	// HoverUnderline underlines the link only while the pointer is over it
	// or it has the keyboard focus, as navigation links in a sidebar or a
	// footer are; otherwise it is always underlined, as AppKit's are.
	HoverUnderline bool
}

// LinkRecipe is the look of text links (an NSTextField label whose text has
// NSLinkAttributeName on macOS 27.2: linkColor, underlined, the pointing-hand
// cursor over it).
type LinkRecipe struct {
	// Color is the link's text (linkColor: #0068DA in light, #419CFF in
	// dark). Pressed is the colour it moves toward while pressed, by
	// PressMix at most, and Disabled the colour of a disabled link (the press
	// and the disabled look were not measured).
	Color, Pressed, Disabled Tint
	PressMix                 float32
	// UnderlineY is the underline's top below the baseline and UnderlineH its
	// thickness, in points (1.5 and 1, measured); the underline is as wide as
	// the text.
	UnderlineY, UnderlineH float32
	// RingRadius is the focus ring's corner radius round the text, in points.
	RingRadius float32
}

// LinkLook is the links' recipe.
var LinkLook = LinkRecipe{
	Color: Of(theme.Link), Pressed: Of(theme.Label), Disabled: Of(theme.TextDisabled),
	PressMix:   0.3,
	UnderlineY: 1.5, UnderlineH: 1,
	RingRadius: 3,
}

// Link builds a text link and reports its signal: Clicked when it was clicked,
// or activated with Enter or Space with the focus, or by an assistive
// technology's press. It only reports: the app decides what the link does
// (opening an address, showing a page); nothing here opens a URL. The pointer
// over it is the pointing hand, its focus ring shows for keyboard focus, and
// it is a link to the accessibility tree, named by its text.
//
//	if rexui.Link(k, "Learn more", rexui.LinkOpts{}).Clicked {
//		open("https://example.com/learn")
//	}
func Link(k *base.Kit, label string, o LinkOpts) base.Signal {
	c, r := k.Context(), k.Theme()
	rc := &LinkLook
	st := &r.Styles[TextStyles[o.Style%NumTextStyles]]
	p := k.PressState(label, base.HoverOn)
	col := rc.Color.RGBA(r)
	switch {
	case p.Disabled:
		col = rc.Disabled.RGBA(r)
	case p.Down > 0:
		col = mix(col, rc.Pressed.RGBA(r), rc.PressMix*p.Down)
	}
	c.NextFont(st.Font)
	c.NextTextColor(col)
	c.NextAlign(ui.AlignLeft)
	if !p.Disabled {
		c.NextCursor(ui.CursorPointer)
	}
	c.NextSemActions(ui.ActPress | ui.ActFocus)
	k.BeginPress(&p, ui.RoleLink, ui.TextSize(base.LabelSlack), ui.Px(st.Line), ui.DrawText, base.StrBytes(p.Name))
	if !o.HoverUnderline || p.Hot > 0.5 || p.Ring > 0.5 {
		sc := k.Scale()
		w := k.TextWidth(st.Font, p.Name)
		h := theme.Snap(rc.UnderlineH * sc)
		theme.At(c, 0, theme.Snap(st.Baseline+rc.UnderlineY*sc), w, h)
		c.NextBG(col)
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(p.Key, 1), nil)
	}
	k.FocusRing(p.ID, p.Visual, k.Pt(rc.RingRadius))
	return k.EndPress(&p)
}
