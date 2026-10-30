package rexui

import (
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// TextStyle is a label's text style: the macOS text styles a label uses,
// in an order whose zero value is body text.
type TextStyle uint8

const (
	// TextBody is 13/16 regular (theme.StyleBody).
	TextBody TextStyle = iota
	// TextHeadline is 13/16 bold.
	TextHeadline
	// TextSemibold is 13/16 semibold.
	TextSemibold
	// TextCallout is 12/15: help text.
	TextCallout
	// TextSubheadline is 11/14: small controls, table headers.
	TextSubheadline
	// TextFootnote is 10/13: footers.
	TextFootnote
	// TextCaption is 10/13: captions.
	TextCaption
	// TextCaption2 is 10/13 medium: badges.
	TextCaption2
	// TextTitle3 is 15/20: group titles.
	TextTitle3
	// TextTitle2 is 17/22: section titles.
	TextTitle2
	// TextTitle1 is 22/26: window-level titles.
	TextTitle1
	// TextLargeTitle is 26/32: empty-state titles.
	TextLargeTitle
	// TextMono is 13/16 SF Mono: code.
	TextMono

	// NumTextStyles is the number of label styles.
	NumTextStyles
)

// TextStyles are the theme text styles of the label styles.
var TextStyles = [NumTextStyles]theme.TextStyle{
	TextBody: theme.StyleBody, TextHeadline: theme.StyleHeadline, TextSemibold: theme.StyleBodySemibold,
	TextCallout: theme.StyleCallout, TextSubheadline: theme.StyleSubheadline, TextFootnote: theme.StyleFootnote,
	TextCaption: theme.StyleCaption1, TextCaption2: theme.StyleCaption2,
	TextTitle3: theme.StyleTitle3, TextTitle2: theme.StyleTitle2, TextTitle1: theme.StyleTitle1,
	TextLargeTitle: theme.StyleLargeTitle, TextMono: theme.StyleMono,
}

// LabelOpts are a label's options, passed by value. The zero value is body
// text (13 pt) in labelColor.
type LabelOpts struct {
	// Style is the text style.
	Style TextStyle
	// Color is the colour; the zero Tint is Window, so a zero Color means
	// Label (primary text).
	Color Tint
	// Wrap breaks the text into lines at the width it is given (the parent's
	// by default).
	Wrap bool
	// MetaPt, when set, sets the text in the UI face at MetaPt points with
	// tabular figures, on the style's line: Rex's metadata (times, sizes,
	// counts, hints), quieter than the label beside it but of its family.
	MetaPt float32
	// MonoPt, when set, sets the text in the monospaced face at MonoPt
	// points on the style's line: what is machine text (a path, a code to
	// type in). MetaPt wins when both are set.
	MonoPt float32
}

// MetaFont is Rex's metadata face at sizePt points: the UI face with
// tabular figures (LabelOpts.MetaPt). Asking for it each frame is a lookup.
func MetaFont(k *base.Kit, sizePt float32) text.FontKey {
	return k.TextSystem().Font(text.FontDesc{SizePt: sizePt, TabularDigits: true, Scale: k.Scale()})
}

// labelColor is o's colour: Label when none was set.
func (o *LabelOpts) labelColor(r *theme.Resolved) uint32 {
	if o.Color == (Tint{}) {
		return r.Colors[theme.Label]
	}
	return o.Color.RGBA(r)
}

// Label builds text in a text style and colour. The label follows ui's rules
// for box labels ("Name###id" shows Name); text too wide for its box fades
// out at the end. Disabled regions draw it in disabledControlTextColor.
//
//	rexui.Label(k, "General", rexui.LabelOpts{Style: rexui.TextTitle3})
//	rexui.Label(k, "Last opened today", rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
func Label(k *base.Kit, s string, o LabelOpts) ui.ID {
	c := k.Context()
	labelStyle(k, &o)
	display, _ := ui.SplitLabel(s)
	c.NextSemName(display)
	if o.Wrap {
		return c.Box(ui.DrawText|ui.WrapText, s)
	}
	return c.Box(ui.DrawText|ui.FadeOverflowX, s)
}

// LabelBytes is Label for text built at run time (ui.Context.Fmt); the bytes
// are copied and are not part of the box's identity, so labels built this
// way in one container are told apart by their order.
func LabelBytes(k *base.Kit, b []byte, o LabelOpts) ui.ID {
	c := k.Context()
	labelStyle(k, &o)
	c.NextSemNameBytes(b)
	flags := ui.DrawText | ui.FadeOverflowX
	if o.Wrap {
		flags = ui.DrawText | ui.WrapText
	}
	return c.BoxKeyed(flags, ui.HashString(c.ParentKey(), "label"), b)
}

// Caption builds a caption: Caption1 (10 pt) in secondaryLabelColor.
func Caption(k *base.Kit, s string) ui.ID {
	return Label(k, s, LabelOpts{Style: TextCaption, Color: Of(theme.Label2)})
}

// labelStyle sets the size, font, colour, alignment and role of the next
// label box.
func labelStyle(k *base.Kit, o *LabelOpts) {
	c, r := k.Context(), k.Theme()
	st := &r.Styles[TextStyles[o.Style%NumTextStyles]]
	col := o.labelColor(r)
	if k.Disabled() {
		col = r.Colors[theme.TextDisabled]
	}
	if o.Wrap {
		k.ApplySize(ui.Pct(1), ui.TextSize(0))
	} else {
		k.ApplySize(ui.TextSize(0), ui.Px(st.Cell))
	}
	font := st.Font
	switch {
	case o.MetaPt > 0:
		font = MetaFont(k, o.MetaPt)
	case o.MonoPt > 0:
		font = k.TextSystem().Font(text.FontDesc{SizePt: o.MonoPt, Mono: true, Scale: k.Scale()})
	}
	c.NextFont(font)
	c.NextTextColor(col)
	c.NextAlign(k.TakeAlign(ui.AlignLeft))
	c.NextRole(ui.RoleLabel)
}
