package widget

import (
	"gonativeui/rexui"
	"gonativeui/theme"
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
	// TextBody is body text in labelColor.
	TextBody TextStyle = iota
	// TextMuted is body text in secondaryLabelColor.
	TextMuted
	// TextSemi is the headline style (13 pt bold), for titles and headers.
	TextSemi
	// TextSmall is the subheadline style (11 pt) in secondaryLabelColor.
	TextSmall
	// TextMono is the monospaced style.
	TextMono
	// TextAccent is semibold body text in the accent colour.
	TextAccent
	// TextDanger is body text in systemRed.
	TextDanger
)

// labelOpts are the rexui label options of the styles.
var labelOpts = [...]rexui.LabelOpts{
	TextBody:   {},
	TextMuted:  {Color: rexui.Of(theme.Label2)},
	TextSemi:   {Style: rexui.TextHeadline},
	TextSmall:  {Style: rexui.TextSubheadline, Color: rexui.Of(theme.Label2)},
	TextMono:   {Style: rexui.TextMono},
	TextAccent: {Style: rexui.TextSemibold, Color: rexui.Of(theme.Accent)},
	TextDanger: {Color: rexui.Of(theme.Red)},
}

func (st TextStyle) opts() rexui.LabelOpts {
	if int(st) < len(labelOpts) {
		return labelOpts[st]
	}
	return rexui.LabelOpts{}
}

// Label builds a line of text in the body style (rexui.Label). The label
// follows ui's rules for box labels: text before "##" is shown and the whole
// label identifies the box, "Name###id" shows "Name" and is identified by
// "id". Text too wide for its box fades out at the right edge.
func (k *Kit) Label(s string) ui.ID { return k.LabelStyle(s, TextBody) }

// LabelStyle is Label in a style.
func (k *Kit) LabelStyle(s string, st TextStyle) ui.ID { return rexui.Label(k.Kit, s, st.opts()) }

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
	return rexui.LabelBytes(k.Kit, b, st.opts())
}

// Paragraph builds text that wraps at the width it is given, breaking lines at
// the opportunities of UAX #14 and at newlines (ui.WrapText). The width
// defaults to the parent's; the height is the lines'.
func (k *Kit) Paragraph(s string) ui.ID { return k.ParagraphStyle(s, TextBody) }

// ParagraphStyle is Paragraph in a style.
func (k *Kit) ParagraphStyle(s string, st TextStyle) ui.ID {
	o := st.opts()
	o.Wrap = true
	return rexui.Label(k.Kit, s, o)
}

// Separator builds a line across the parent (rexui.Separator): horizontal in
// a column, vertical in a row.
func (k *Kit) Separator() ui.ID { return rexui.Separator(k.Kit, rexui.SeparatorOpts{}) }
