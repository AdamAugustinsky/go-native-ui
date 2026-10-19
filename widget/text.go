package widget

import (
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// labelSlack is the pixel a label's box is wider than its text: layout snaps
// the box's edges to whole pixels, which can leave it a fraction narrower than
// the text, and a label that is narrower than its text fades out at the edge.
const labelSlack = base.LabelSlack

// TextStyle selects the font and colour of a label.
type TextStyle uint8

const (
	// TextBody is body text in the theme's text colour.
	TextBody TextStyle = iota
	// TextMuted is secondary text.
	TextMuted
	// TextSemi is the emphasised (semibold) variant, for titles and headers.
	TextSemi
	// TextSmall is the caption size, muted.
	TextSmall
	// TextMono is the monospace font.
	TextMono
	// TextAccent is semibold in the accent colour.
	TextAccent
	// TextDanger is body text in the danger colour.
	TextDanger
)

// Label builds a line of text in the body style. The label follows ui's rules
// for box labels: text before "##" is shown and the whole label identifies the
// box, "Name###id" shows "Name" and is identified by "id". Text too wide for
// its box fades out at the right edge.
func (k *Kit) Label(s string) ui.ID { return k.LabelStyle(s, TextBody) }

// LabelStyle is Label in a style.
func (k *Kit) LabelStyle(s string, st TextStyle) ui.ID {
	k.size(ui.TextSize(labelSlack), ui.TextSize(0))
	k.styleNext(st)
	k.c.NextRole(ui.RoleLabel)
	display, _ := ui.SplitLabel(s)
	k.c.NextSemName(display)
	return k.c.Box(ui.DrawText|ui.FadeOverflowX, s)
}

// LabelBytes is Label for text built at run time (see ui.Context.Fmt); the
// bytes are copied. The text is not part of the box's identity, so it may
// change every frame. Any number of them can be built in one container: they
// are told apart by their order among the container's children that were built
// the same way, as any boxes with equal labels are (ui.Context.Box), so keep
// that order from frame to frame. A label's state is not interactive, so a
// shift costs only its text run being shaped again.
func (k *Kit) LabelBytes(b []byte) ui.ID { return k.LabelBytesStyle(b, TextBody) }

// LabelBytesStyle is LabelBytes in a style.
func (k *Kit) LabelBytesStyle(b []byte, st TextStyle) ui.ID {
	k.size(ui.TextSize(labelSlack), ui.TextSize(0))
	k.styleNext(st)
	k.c.NextRole(ui.RoleLabel)
	k.c.NextSemNameBytes(b)
	return k.c.BoxKeyed(ui.DrawText|ui.FadeOverflowX, ui.HashString(k.c.ParentKey(), "label"), b)
}

// Paragraph builds text that wraps at the width it is given, breaking lines at
// the opportunities of UAX #14 and at newlines (ui.WrapText). The width
// defaults to the parent's; the height is the lines'.
func (k *Kit) Paragraph(s string) ui.ID { return k.ParagraphStyle(s, TextBody) }

// ParagraphStyle is Paragraph in a style.
func (k *Kit) ParagraphStyle(s string, st TextStyle) ui.ID {
	k.size(ui.Pct(1), ui.TextSize(0))
	k.styleNext(st)
	k.c.NextRole(ui.RoleLabel)
	display, _ := ui.SplitLabel(s)
	k.c.NextSemName(display)
	return k.c.Box(ui.DrawText|ui.WrapText, s)
}

// styleNext sets the font, colour and alignment of the next text box.
func (k *Kit) styleNext(st TextStyle) {
	c := k.c
	c.NextFont(k.font(st))
	c.NextTextColor(k.color(st))
	c.NextAlign(k.align(ui.AlignLeft))
}

// Separator builds a hairline across the parent: horizontal in a column,
// vertical in a row.
func (k *Kit) Separator() ui.ID {
	c := k.c
	horizontal := true
	if c.Parent() != 0 {
		horizontal = !k.InRow()
	}
	lw := k.r.Px.BorderW
	if horizontal {
		c.NextSize(ui.Pct(1), ui.Px(lw))
	} else {
		c.NextSize(ui.Px(lw), ui.Pct(1))
	}
	c.NextBG(k.r.Pal.Line)
	return c.Box(ui.DrawBackground, "separator")
}
