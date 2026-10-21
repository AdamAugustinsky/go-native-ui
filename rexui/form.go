package rexui

import (
	"cmp"

	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// FormVariant selects a form's layout.
type FormVariant uint8

const (
	// FormGrouped is macOS 27.2's Settings layout: sections of rows in
	// grouped inset boxes (CardInset), each row a label at the leading edge
	// and its control at the trailing edge, with a line between rows, a
	// header above a section and a footer below it.
	FormGrouped FormVariant = iota
	// FormAligned is an NSGridView form: a column of labels right-aligned to
	// a shared width, and the controls left-aligned in the column beside it,
	// row after row, as the HIG lays out a dialog's fields.
	FormAligned

	// NumFormVariants is the number of variants.
	NumFormVariants
)

// FormOpts are a form's options, passed by value.
type FormOpts struct {
	// Variant is the layout.
	Variant FormVariant
	// Labels are the labels of a FormAligned form, which the form measures
	// for the width of its label column (the widest one: NSGridView sizes
	// the column the same way). Pass the labels the rows will use.
	Labels []string
	// LabelW is the label column's width in points, instead of the widest of
	// Labels. A FormAligned form with neither has FormLook.LabelW.
	LabelW float32
}

// RowOpts are a form row's options, passed by value.
type RowOpts struct {
	// Height is the row's least height in points, for a control taller than
	// the layout's row (0: FormLook's, a grouped row's 40 pt; an aligned row
	// is as high as its tallest control). A label too long for its column
	// wraps onto more lines and the row grows with them, as in System
	// Settings.
	Height float32
	// Detail is a secondary text under the row's label, as System Settings'
	// rows have: smaller text in the secondary colour. A grouped row's label
	// column takes the width the row's controls leave and Detail wraps to it,
	// up to MaxLines lines (the last ends in "…" when text is left out); the
	// row is as high as its label column and controls need, so Height does
	// not apply to a row with a Detail. An aligned form's rows ignore it.
	Detail string
	// MaxLines is the most lines Detail takes (0: 3).
	MaxLines int
}

// FormRecipe is the look of forms. Lengths are points.
type FormRecipe struct {
	// SectionGap is the space between two grouped sections, and HeaderGap
	// the space above a section's header (after the section or footer
	// before): the stack of AppKitRef's Settings form spaces boxes 8 apart
	// and puts 18 above each header. HeaderBelow is the space between a
	// header and its box (6).
	SectionGap, HeaderGap, HeaderBelow float32
	// FooterGap is the space between a section and its footer (6), and
	// FooterBelow the space after a footer before the next section (18).
	// Designed; System Settings' footers are not in D0's measurement.
	FooterGap, FooterBelow float32
	// TextX is the indent of headers and footers from the section's edge:
	// the label's inset in a row (10).
	TextX float32
	// Header is a section header's text style and colour (13 semibold, as
	// measured); Footer a footer's (11 pt, Label2).
	HeaderStyle, FooterStyle TextStyle
	Header, Footer           Tint
	// LabelGap is the least space between a grouped row's label and its
	// control (12; the row's gap is half of it on either side of the spacer
	// between them): a long label fades out before it.
	LabelGap float32
	// ColumnGap is the space between the label column of an aligned form and
	// its controls, and RowGap that between its rows: NSGridView's defaults,
	// 6 and 6.
	ColumnGap, RowGap float32
	// LabelW is the width of the label column when the form was given none
	// (120).
	LabelW float32
	// Label is a row's label (labelColor).
	Label Tint
	// DetailStyle and Detail are a row's description line under its label
	// (RowOpts.Detail): 11 pt, secondaryLabelColor.
	DetailStyle TextStyle
	Detail      Tint
	// DetailPadY is the space above and below the lines of a row with a
	// description (10; designed, System Settings' rows with one are about
	// 50 pt high).
	DetailPadY float32
}

// FormLook is the forms' recipe.
var FormLook = FormRecipe{
	SectionGap: 8, HeaderGap: 18, HeaderBelow: 6,
	FooterGap: 6, FooterBelow: 18,
	TextX:       10,
	HeaderStyle: TextSemibold, FooterStyle: TextSubheadline,
	Header: Of(theme.Label), Footer: Of(theme.Label2),
	LabelGap:  12,
	ColumnGap: 6, RowGap: 6, LabelW: 120,
	Label:       Of(theme.Label),
	DetailStyle: TextSubheadline, Detail: Of(theme.Label2), DetailPadY: 10,
}

// Form is a form being built: BeginForm returns it, Row adds a row, Section
// and EndSection group rows of a FormGrouped form, EndForm closes it.
type Form struct {
	variant  FormVariant
	outer    ui.ID
	key      ui.Key
	labelW   float32 // pixels, an aligned form's label column
	sections int
	footer   bool // the section before ended with a footer
	card     Card
	inCard   bool
	rows     int
	row      ui.ID
	label    ui.ID
	rowLabel string // the open row's label, which names its first unnamed control
	mark     int    // where the open row's semantics start (ui.Context.SemMark)
	spaces   int    // the form's spaces so far (space)
}

// space builds pt points of room down the form's column, each with a key of
// its own (base.Kit.Space keys them all alike).
func (f *Form) space(k *base.Kit, pt float32) {
	c := k.Context()
	f.spaces++
	c.NextSize(ui.Px(0), k.Fixed(pt))
	c.BoxKeyed(0, ui.KeyN(f.key, 3000+uint64(f.spaces)), nil)
}

// BeginForm starts a form and makes it the parent: a column as wide as its
// parent, with the room a form's parts need laid out by Section and Row (the
// column's own gap is 0). Pad it as the window's content is padded (20 pt in
// AppKit's Settings). Close it with EndForm.
//
// A grouped form (the Settings layout):
//
//	f := rexui.BeginForm(k, "sound", rexui.FormOpts{})
//	f.Section(k, "Output")
//	f.Row(k, "Alert volume", rexui.RowOpts{})
//	rexui.Slider(k, "Alert volume", &v.volume, 0, 100, 0, rexui.SliderOpts{})
//	f.EndRow(k)
//	f.Row(k, "Play sound on startup", rexui.RowOpts{})
//	rexui.Switch(k, "Play sound on startup", &v.startup, rexui.SwitchOpts{})
//	f.EndRow(k)
//	f.EndSection(k, "Applies to alerts and sound effects.")
//	rexui.EndForm(k, &f)
//
// An aligned form (an NSGridView):
//
//	f := rexui.BeginForm(k, "account", rexui.FormOpts{Variant: rexui.FormAligned, Labels: labels[:]})
//	f.Row(k, "Name:", rexui.RowOpts{})
//	rexui.TextField(k, "Name", &v.name, "", rexui.FieldOpts{})
//	f.EndRow(k)
//	rexui.EndForm(k, &f)
//
// A row is a named group (the row's label is its name), and a control built
// in it with an empty label ("") shows nothing of its own: the row's label is
// its caption, as in AppKit's forms. The Tab order is the order the rows are
// built in, row after row; a label takes no focus. A disabled region (base.Kit.BeginDisabled) around a
// row draws its label and control disabled.
func BeginForm(k *base.Kit, label string, o FormOpts) Form {
	c, r := k.Context(), k.Theme()
	rc := &FormLook
	f := Form{variant: o.Variant % NumFormVariants}
	if f.variant == FormAligned {
		switch {
		case o.LabelW > 0:
			f.labelW = theme.Snap(k.Pt(o.LabelW))
		case len(o.Labels) > 0:
			font := r.Styles[theme.StyleBody].Font
			for _, s := range o.Labels {
				f.labelW = max(f.labelW, k.TextWidth(font, s))
			}
			f.labelW = theme.Snap(f.labelW + 0.5) // a label's advance, whole pixels
		default:
			f.labelW = theme.Snap(k.Pt(rc.LabelW))
		}
	}
	k.NextGap(0)
	c.NextRole(ui.RoleGroup)
	c.NextSemName(label)
	f.outer = k.BeginContainer(ui.AxisY, ui.CrossStart, label, ui.Pct(1), ui.Children(1), 0)
	f.key = c.KeyOf(f.outer)
	return f
}

// ID is the form's outermost box.
func (f *Form) ID() ui.ID { return f.outer }

// Section starts a section of a FormGrouped form: its header, if there is
// one, and the grouped box its rows go in. Close it with EndSection. Rows of
// an aligned form are added without sections.
func (f *Form) Section(k *base.Kit, header string) {
	c, r := k.Context(), k.Theme()
	rc := &FormLook
	if f.sections > 0 {
		switch {
		case f.footer:
			f.space(k, rc.FooterBelow)
		case header != "":
			f.space(k, rc.HeaderGap)
		default:
			f.space(k, rc.SectionGap)
		}
	}
	f.footer = false
	key := ui.KeyN(f.key, 1000+uint64(f.sections))
	f.sections++
	if header != "" {
		st := &r.Styles[TextStyles[rc.HeaderStyle%NumTextStyles]]
		c.NextSize(ui.Pct(1), ui.Px(st.Line))
		c.NextPadding(k.Pt(rc.TextX), 0, 0, 0)
		c.Push(c.BoxKeyed(0, ui.KeyN(key, 1), nil))
		Label(k, header, LabelOpts{Style: rc.HeaderStyle, Color: rc.Header})
		c.Pop()
		f.space(k, rc.HeaderBelow)
	}
	f.card = BeginCard(k, header, CardOpts{Variant: CardInset})
	f.inCard = true
}

// EndSection closes the section Section started and builds its footer: a
// line of smaller, secondary text under the box (empty: none).
func (f *Form) EndSection(k *base.Kit, footer string) {
	c := k.Context()
	rc := &FormLook
	if f.inCard {
		EndCard(k, &f.card)
		f.inCard = false
	}
	if footer == "" {
		return
	}
	f.footer = true
	f.space(k, rc.FooterGap)
	c.NextPadding(k.Pt(rc.TextX), 0, k.Pt(rc.TextX), 0)
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.Push(c.BoxKeyed(0, ui.KeyN(f.key, 2000+uint64(f.sections)), nil))
	Label(k, footer, LabelOpts{Style: rc.FooterStyle, Color: rc.Footer, Wrap: true})
	c.Pop()
}

// Row starts a row: its label, then, until EndRow, the controls built after it.
// In a grouped form the row is a line of the open section, the label at its
// leading edge and the controls pushed to the trailing edge; in an aligned
// form the label is right-aligned in the shared column and the controls
// follow it, left-aligned. label is also the row's identity and should be
// unique in its section. It returns the row's box.
func (f *Form) Row(k *base.Kit, label string, o RowOpts) ui.ID {
	rc := &FormLook
	f.rows++
	f.rowLabel, f.mark = label, k.Context().SemMark()
	if f.variant == FormAligned {
		if f.rows > 1 {
			f.space(k, rc.RowGap)
		}
		k.NextGap(rc.ColumnGap)
		if o.Height > 0 {
			k.NextHeight(k.Fixed(o.Height))
		}
		k.Context().NextRole(ui.RoleGroup)
		k.Context().NextSemName(label)
		f.row = k.Row(label)
		k.NextWidth(ui.Px(f.labelW))
		k.NextAlign(ui.AlignRight)
		f.label = formLabel(k, label, false) // its column is as wide as the labels
		return f.row
	}
	if !f.inCard { // rows without a section: one anonymous section
		f.Section(k, "")
	}
	f.card.separate(k) // its own settings come after: they are the row's
	if o.Detail != "" {
		// The label column fills what the controls leave and the row grows
		// with the wrapped lines, so there is no spacer to share the room.
		k.NextGap(rc.LabelGap)
		k.Context().NextRole(ui.RoleGroup)
		k.Context().NextSemName(label)
		f.row = f.card.rowFit(k, label, rc.DetailPadY)
		f.label = formLabelDetail(k, label, o.Detail, o.MaxLines)
		return f.row
	}
	// The label column fills what the controls leave and wraps to it; the
	// row is a row's height, or the lines' if they are higher. The label's
	// own padding is what makes the one-line height (centring its line in
	// it, as the row centred the label) and keeps half the gap to the
	// controls, which the spacer of a row with a short label used to hold.
	c, r := k.Context(), k.Theme()
	h := CardLook.RowH
	if o.Height > 0 {
		h = o.Height
	}
	m := k.TextSystem().Metrics(r.Styles[TextStyles[TextBody]].Font) // the line the box lays its text in
	lh := m.LineHeight
	if lh <= 0 {
		lh = m.Ascent + m.Descent + m.LineGap
	}
	padY := max((k.Fixed(h).Value-lh)/2, 0)
	k.NextGap(rc.LabelGap / 2) // between the controls
	c.NextRole(ui.RoleGroup)
	c.NextSemName(label)
	f.row = f.card.rowFit(k, label, CardLook.RowPadY)
	c.NextPadding(0, padY, k.Pt(rc.LabelGap/2), padY)
	f.label = formLabel(k, label, true)
	return f.row
}

// formLabel builds a row's label. A grouped row's wraps at the width its box
// is given, as System Settings' row titles do, so a long title in a narrow
// window or a language of long words takes more lines and the row grows. Its
// key does not come from its text: a control in the row is often named after
// its label, and two boxes of one parent with one key are one box to the
// framework.
func formLabel(k *base.Kit, label string, wrap bool) ui.ID {
	return LabelBytes(k, base.StrBytes(label), LabelOpts{Color: FormLook.Label, Wrap: wrap})
}

// formLabelDetail builds a row's label with its description under it, in a
// column that fills the row's width left of the controls. The description
// wraps to the column's width, up to maxLines lines (0: 3). The column is as
// high as the lines, and the row follows it.
func formLabelDetail(k *base.Kit, label, detail string, maxLines int) ui.ID {
	rc := &FormLook
	c, r := k.Context(), k.Theme()
	k.NextGap(0)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "label", ui.Pct(1), ui.Children(1), 0)
	k.NextWidth(ui.Pct(1))
	l := formLabel(k, label, true)
	col := rc.Detail.RGBA(r)
	if k.Disabled() {
		col = r.Colors[theme.TextDisabled]
	}
	c.NextSize(ui.Pct(1), ui.TextSize(0))
	c.NextFont(r.Styles[TextStyles[rc.DetailStyle%NumTextStyles]].Font)
	c.NextTextColor(col)
	c.NextAlign(ui.AlignLeft)
	c.RichText(ui.HashString(c.ParentKey(), "detail"), base.StrBytes(detail), nil, nil,
		ui.RichOpts{MaxLines: cmp.Or(maxLines, 3), Ellipsis: true})
	k.EndContainer()
	return l
}

// Label is the box of the current row's label.
func (f *Form) Label() ui.ID { return f.label }

// EndRow ends the row Row started. The row's label names the row's first
// control that has no accessible name of its own, so that a switch built with
// an empty label reads "Wi-Fi, switch" as AppKit's form rows do; a control
// that has a name keeps it, and of several controls in a row only the first
// unnamed interactive one (button, check box, switch, radio button, slider,
// text or search field, pop-up) takes the label.
func (f *Form) EndRow(k *base.Kit) {
	k.Context().NameFirstUnnamed(f.mark, base.StrBytes(f.rowLabel))
	k.EndRow()
}

// EndForm closes the form BeginForm started, and the section still open.
func EndForm(k *base.Kit, f *Form) {
	if f.inCard {
		f.EndSection(k, "")
	}
	k.EndContainer()
}
