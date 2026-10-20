package rexui

import (
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
	// Height is the row's height in points, for a control taller than the
	// layout's row (0: FormLook's, a grouped row's 40 pt; an aligned row is
	// as high as its tallest control).
	Height float32
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
	Label: Of(theme.Label),
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
			k.Space(rc.FooterBelow)
		case header != "":
			k.Space(rc.HeaderGap)
		default:
			k.Space(rc.SectionGap)
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
		k.Space(rc.HeaderBelow)
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
	k.Space(rc.FooterGap)
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
			k.Space(rc.RowGap)
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
		f.label = formLabel(k, label)
		return f.row
	}
	if !f.inCard { // rows without a section: one anonymous section
		f.Section(k, "")
	}
	f.card.separate(k)         // its own settings come after: they are the row's
	k.NextGap(rc.LabelGap / 2) // either side of the spacer
	k.Context().NextRole(ui.RoleGroup)
	k.Context().NextSemName(label)
	f.row = f.card.row(k, label, o.Height)
	f.label = formLabel(k, label)
	k.Spacer()
	return f.row
}

// formLabel builds a row's label. Its key does not come from its text: a
// control in the row is often named after its label, and two boxes of one
// parent with one key are one box to the framework.
func formLabel(k *base.Kit, label string) ui.ID {
	return LabelBytes(k, base.StrBytes(label), LabelOpts{Color: FormLook.Label})
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
