package gallery

import (
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"
)

// The scenes' forms are System Settings': 20 pt round the stack (24 below),
// grouped sections of 40 pt rows, the label at the leading edge and the
// control at the trailing one (rexui.BeginForm).
const (
	formPad, formPadBottom = 20, 24
	// fieldPt is the width of a text field or a pop-up in a form row, in points.
	fieldPt = 200
	// cellPt is the width of one state column of the matrix, in points, and
	// nameColPt the column of the widgets' names.
	cellPt, nameColPt = 82, 62
)

// beginForm starts scene sc's form in the detail pane (the current parent),
// w by h pixels: a vertical scroll view whose content is a padded stack with
// the form in it. It records whether the content lies under the toolbar.
func (g *Gallery) beginForm(sc int, w, h float32) (base.ScrollView, rexui.Form) {
	k := g.k
	c := k.Context()
	k.NextSize(ui.Px(w), ui.Px(h))
	v := k.BeginScroll(defs[sc].title, &g.scroll[sc], base.ScrollVertical)
	g.scrollStart(v.Inner)
	_, sy := c.Scroll(v.Inner)
	g.under = sy > 0.5
	pad, bottom := theme.Snap(k.Pt(formPad)), theme.Snap(k.Pt(formPadBottom))
	c.NextSize(ui.Pct(1), ui.Children(1))
	c.NextPadding(pad, pad, pad, bottom)
	c.NextAxis(ui.AxisY)
	c.Push(c.BoxKeyed(0, ui.HashString(0, "form"), nil))
	return v, rexui.BeginForm(k, defs[sc].title, rexui.FormOpts{})
}

// scrollStart scrolls the content of the scroll view inner 120 pt down in the
// first frames, when the gallery was asked to start scrolled.
func (g *Gallery) scrollStart(inner ui.ID) {
	if g.scrolled > 0 {
		g.k.Context().SetScroll(inner, 0, float64(theme.Snap(g.k.Pt(120))))
	}
}

// endForm ends the form and the scroll view beginForm started.
func (g *Gallery) endForm(v *base.ScrollView, f *rexui.Form) {
	k := g.k
	rexui.EndForm(k, f)
	k.Context().Pop()
	k.CloseScrollContent(v)
	rexui.ScrollBars(k, v)
	k.CloseScroll(v)
}

// named gives the control id the accessible name s: a control in a form row
// has no label of its own, the row's label is its caption.
func (g *Gallery) named(id ui.ID, s string) { g.c.SetSemNameBytes(id, base.StrBytes(s)) }

// fieldW sets the width of the next control of a form row.
func (g *Gallery) fieldW() { g.k.NextWidth(g.k.Fixed(fieldPt)) }

// popupPadPt is what a pop-up button takes beyond its widest option's text,
// in points (System Settings' pop-ups, measured: the label's inset and the
// chevron).
const popupPadPt = 47.5

// popupW sets the width of the next pop-up button of a form row to its widest
// option's, as Settings sizes its pop-ups.
func (g *Gallery) popupW(options []string) {
	k := g.k
	font := k.Theme().Styles[theme.StyleBody].Font
	wide := float32(0)
	for _, o := range options {
		wide = max(wide, k.TextWidth(font, o))
	}
	k.NextWidth(ui.Px(theme.Snap(wide + k.Pt(popupPadPt))))
}

// bytesIn builds the dynamic text b as a label in a box of its own, n of the
// key: the key of a label of bytes is its parent's, so a parent holds one.
func (g *Gallery) bytesIn(key ui.Key, n uint64, w ui.Size, b []byte, o rexui.LabelOpts) {
	c := g.k.Context()
	c.NextSize(w, ui.Children(1))
	c.Push(c.BoxKeyed(0, ui.KeyN(key, n), nil))
	rexui.LabelBytes(g.k, b, o)
	c.Pop()
}

var keyGallery = ui.HashString(0, "gallery")

var (
	selectOptions = []string{"Choice", "Other"}
	radioOptions  = []string{"Radio"}
	formats       = []string{"Plain text", "Markdown", "HTML"}
	modes         = []string{"One", "Two", "Three"}
	tabNames      = []string{"Overview", "Details", "History"}
	tabText       = [3]string{"A summary of the thing.", "Every detail of it.", "What happened to it."}
	appearances   = []string{"System", "Light", "Dark"}
)

// The widgets of the state matrix, one row each.
const (
	rowButton = iota
	rowPrimary
	rowPlain
	rowIcon
	rowCheck
	rowMixed
	rowSwitch
	rowRadio
	rowSlider
	rowField
	rowSelect
)

var (
	rowNames   = [...]string{"Button", "Primary", "Plain", "Icon", "Checkbox", "Mixed", "Switch", "Radio", "Slider", "Field", "Select"}
	stateNames = [5]string{"Normal", "Hover", "Pressed", "Focused", "Disabled"}
	stateCells = [5]string{"c0", "c1", "c2", "c3", "c4"}
	stateLooks = [5]widget.Look{0, widget.LookHover, widget.LookPressed, widget.LookFocused, 0}
)

// playgroundScene has the controls to try: text styles, entry, choices, buttons,
// tabs and a list, each in a section of the form.
func (g *Gallery) playgroundScene(w, h float32) {
	k, c := g.k, g.c
	wk := g.wk
	v, f := g.beginForm(scPlayground, w, h)

	f.Section(k, "Text")
	f.Row(k, "Body", rexui.RowOpts{})
	rexui.Label(k, "Body text, the default style", rexui.LabelOpts{})
	f.EndRow(k)
	f.Row(k, "Secondary", rexui.RowOpts{})
	rexui.Label(k, "Muted secondary text", rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	f.EndRow(k)
	f.Row(k, "Caption", rexui.RowOpts{})
	rexui.Label(k, "Small caption", rexui.LabelOpts{Style: rexui.TextCaption, Color: rexui.Of(theme.Label2)})
	f.EndRow(k)
	f.Row(k, "Monospace", rexui.RowOpts{})
	rexui.Label(k, "Monospace 0123456789", rexui.LabelOpts{Style: rexui.TextMono})
	f.EndRow(k)
	f.EndSection(k, "Wrapped text breaks at the width it is given, at the line break opportunities of Unicode, and keeps the long words whole where it can.")

	f.Section(k, "Entry")
	f.Row(k, "Notes", rexui.RowOpts{})
	g.fieldW()
	rexui.TextField(k, "Notes", &g.notes, "Notes…", rexui.FieldOpts{})
	f.EndRow(k)
	f.Row(k, "Filter", rexui.RowOpts{})
	g.fieldW()
	rexui.SearchField(k, "Filter", &g.filter, "Search", rexui.FieldOpts{})
	f.EndRow(k)
	f.EndSection(k, "")

	f.Section(k, "Choices")
	f.Row(k, "Feature", rexui.RowOpts{})
	rexui.Checkbox(k, "Enable feature", &g.check, rexui.CheckboxOpts{})
	f.EndRow(k)
	// the tri-state checkbox shows what the two options below it add up to,
	// and sets them both (or neither) when clicked
	g.tri = widget.Mixed
	switch {
	case g.check && g.sw:
		g.tri = widget.Checked
	case !g.check && !g.sw:
		g.tri = widget.Unchecked
	}
	f.Row(k, "Options", rexui.RowOpts{})
	if rexui.CheckboxTri(k, "All options", &g.tri, rexui.CheckboxOpts{}).Changed {
		g.check, g.sw = g.tri == widget.Checked, g.tri == widget.Checked
	}
	f.EndRow(k)
	f.Row(k, "Notifications", rexui.RowOpts{})
	g.named(rexui.Switch(k, "##Notifications", &g.sw, rexui.SwitchOpts{}).ID, "Notifications")
	f.EndRow(k)
	f.Row(k, "Choice", rexui.RowOpts{})
	rexui.RadioGroup(k, "Choice", &g.radio, modes, rexui.RadioOpts{Horizontal: true})
	f.EndRow(k)
	f.Row(k, "Volume", rexui.RowOpts{})
	k.NextWidth(k.Fixed(160))
	rexui.Slider(k, "Volume", &g.slider, 0, 100, 1, rexui.SliderOpts{})
	g.bytesIn(keyGallery, 1, k.Fixed(28), c.Fmt().Int(int(g.slider)).Bytes(), rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	f.EndRow(k)
	f.Row(k, "Format", rexui.RowOpts{})
	g.popupW(formats)
	wk.Select("Format###format", &g.sel, formats)
	f.EndRow(k)
	f.EndSection(k, "")

	f.Section(k, "Buttons")
	f.Row(k, "Actions", rexui.RowOpts{})
	wk.Tooltip(rexui.Button(k, "Open…", rexui.ButtonOpts{}).ID, "Open a file")
	rexui.Button(k, "Save", rexui.ButtonOpts{Variant: rexui.ButtonPrimary})
	rexui.Button(k, "More", rexui.ButtonOpts{Variant: rexui.ButtonPlain})
	rexui.Button(k, "Remove", rexui.ButtonOpts{Variant: rexui.ButtonDestructive})
	f.EndRow(k)
	f.Row(k, "More", rexui.RowOpts{})
	wk.Tooltip(wk.IconButton(icon.Plus, "Add item", widget.ButtonDefault).ID, "Add an item")
	wk.MenuButton("Menu", menuFile)
	if rexui.Button(k, "Delete…", rexui.ButtonOpts{}).Clicked {
		g.askDelete = true
	}
	f.EndRow(k)
	f.EndSection(k, "")

	f.Section(k, "Tabs")
	f.Row(k, "Sections", rexui.RowOpts{})
	rexui.SegmentedControl(k, "Sections", &g.tab, tabNames, rexui.SegmentedOpts{Role: rexui.SegmentedTabs})
	f.EndRow(k)
	f.EndSection(k, tabText[g.tab%len(tabText)])

	f.Section(k, "List")
	f.Row(k, "Items", rexui.RowOpts{Height: 148})
	k.NextSize(k.Fixed(240), k.Fixed(124))
	l := rexui.BeginList(k, "items", &g.list, listItems, rexui.ListOpts{})
	g.listFocus = l.Focused
	for r := l.First; r < l.Last; r++ {
		name := c.Fmt().Str("List item ").Int(r + 1).Bytes()
		l.Row(r, uint32(r), name)
		rexui.LabelBytes(k, name, rexui.LabelOpts{Color: l.Text()})
	}
	rexui.EndList(k, &l)
	f.EndRow(k)
	f.EndSection(k, "")

	g.endForm(&v, &f)
}

// statesScene shows every widget in each of its states, then progress and a
// card.
func (g *Gallery) statesScene(w, h float32) {
	k := g.k
	v, f := g.beginForm(scStates, w, h)

	f.Section(k, "States")
	k.NextPadding(10, 8, 10, 8)
	k.NextGap(4)
	k.NextWidth(ui.Children(1))
	k.Column("matrix")
	k.NextGap(4)
	k.NextWidth(ui.Children(1))
	k.Row("states-head")
	k.NextSize(k.Fixed(nameColPt), k.Fixed(20))
	k.Spacer()
	for _, n := range stateNames {
		k.NextSize(k.Fixed(cellPt), k.Fixed(20))
		rexui.Label(k, n, rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Label2)})
	}
	k.EndRow()
	for kind := range rowNames {
		g.stateRow(kind)
	}
	k.EndColumn()
	f.EndSection(k, "")

	f.Section(k, "Progress")
	f.Row(k, "Empty", rexui.RowOpts{})
	g.fieldW()
	rexui.ProgressBar(k, "Empty", 0, rexui.ProgressOpts{})
	f.EndRow(k)
	f.Row(k, "Half", rexui.RowOpts{})
	g.fieldW()
	rexui.ProgressBar(k, "Half", 0.5, rexui.ProgressOpts{})
	f.EndRow(k)
	f.Row(k, "Full", rexui.RowOpts{})
	g.fieldW()
	rexui.ProgressBar(k, "Full", 1, rexui.ProgressOpts{})
	f.EndRow(k)
	if g.working {
		f.Row(k, "Working", rexui.RowOpts{})
		g.fieldW()
		rexui.ProgressBar(k, "Working", -1, rexui.ProgressOpts{})
		f.EndRow(k)
	}
	f.EndSection(k, "")

	f.Section(k, "Card")
	f.Row(k, "A card groups related controls.", rexui.RowOpts{})
	rexui.Button(k, "Revert", rexui.ButtonOpts{})
	rexui.Button(k, "Apply", rexui.ButtonOpts{Variant: rexui.ButtonPrimary})
	f.EndRow(k)
	f.EndSection(k, "")

	g.endForm(&v, &f)
}

// stateRow builds one row of the matrix: the name, then the widget kind in each
// of the five states.
func (g *Gallery) stateRow(kind int) {
	k := g.k
	name := rowNames[kind]
	k.NextGap(4)
	k.NextWidth(ui.Children(1))
	k.Row(name)
	k.NextSize(k.Fixed(nameColPt), k.Fixed(30))
	rexui.Label(k, name, rexui.LabelOpts{Color: rexui.Of(theme.Label2)})
	for i := range stateCells {
		k.NextSize(k.Fixed(cellPt), k.Fixed(30))
		k.NextGap(0)
		k.Row(stateCells[i])
		k.BeginDisabled(i == 4)
		k.Force(stateLooks[i])
		g.stateWidget(kind, i)
		k.EndDisabled()
		k.EndRow()
	}
	k.EndRow()
}

// stateWidget builds the widget of a matrix row in state column i.
func (g *Gallery) stateWidget(kind, i int) {
	k := g.k
	m := &g.m
	switch kind {
	case rowButton:
		rexui.Button(k, "Button", rexui.ButtonOpts{})
	case rowPrimary:
		rexui.Button(k, "Primary", rexui.ButtonOpts{Variant: rexui.ButtonPrimary})
	case rowPlain:
		rexui.Button(k, "Plain", rexui.ButtonOpts{Variant: rexui.ButtonPlain})
	case rowIcon:
		g.wk.IconButton(icon.Plus, "Add", widget.ButtonDefault)
	case rowCheck:
		rexui.Checkbox(k, "Check", &m.chk[i], rexui.CheckboxOpts{})
	case rowMixed:
		rexui.CheckboxTri(k, "Mixed", &m.tri[i], rexui.CheckboxOpts{})
	case rowSwitch:
		rexui.Switch(k, "On", &m.sw[i], rexui.SwitchOpts{})
	case rowRadio:
		rexui.RadioGroup(k, "Radio", &m.radio[i], radioOptions, rexui.RadioOpts{})
	case rowSlider:
		rexui.Slider(k, "Slider", &m.slide[i], 0, 100, 1, rexui.SliderOpts{})
	case rowField:
		rexui.TextField(k, "Field", &m.text[i], "", rexui.FieldOpts{})
	case rowSelect:
		k.NextWidth(widget.Fill)
		g.wk.Select("Choice###choice", &m.sel[i], selectOptions)
	}
}

// appearanceScene is the window's appearance: the mode, and the motion the
// indeterminate bar and the spinner show while they are asked for.
func (g *Gallery) appearanceScene(w, h float32) {
	k := g.k
	v, f := g.beginForm(scAppearance, w, h)

	f.Section(k, "")
	f.Row(k, "Appearance", rexui.RowOpts{})
	appearance := int(g.pref)
	g.popupW(appearances)
	g.wk.Select("Appearance###appearance", &appearance, appearances)
	if appearance != int(g.pref) {
		g.SetPreference(theme.Preference(appearance))
	}
	f.EndRow(k)
	f.EndSection(k, "System follows the Mac’s light or dark mode and accent colour as they change. ⇧⌘D cycles System, Light and Dark.")

	f.Section(k, "Activity")
	f.Row(k, "Show activity", rexui.RowOpts{})
	g.named(rexui.Switch(k, "##Show activity", &g.working, rexui.SwitchOpts{}).ID, "Show activity")
	f.EndRow(k)
	if g.working {
		f.Row(k, "Progress", rexui.RowOpts{})
		g.fieldW()
		rexui.ProgressBar(k, "Progress", -1, rexui.ProgressOpts{})
		f.EndRow(k)
		f.Row(k, "Spinner", rexui.RowOpts{})
		rexui.Spinner(k, "Spinner", rexui.SpinnerOpts{Size: theme.SizeSmall})
		f.EndRow(k)
	}
	f.EndSection(k, "The indeterminate bar and the spinner ask for frames for as long as they show, and stand still under Reduce Motion.")

	g.endForm(&v, &f)
}

// tableScene is the table of a million rows, its header in the toolbar's
// pocket (Finder's file list), with a status bar below it.
func (g *Gallery) tableScene(w, h float32) {
	k := g.k
	c, r := k.Context(), k.Theme()
	barH := theme.Snap(k.Pt(28))
	if g.scrolled > 0 { // the scrolled phase: five rows down
		g.table.SetTopRow(5, 0)
	}
	k.NextSize(ui.Px(w), ui.Px(h-barH))
	t := rexui.BeginTable(k, "rows", &g.table, g.cols[:], g.rows, rexui.TableOpts{Striped: true, Glyphs: g.glyphs, ClearHeader: true})
	if t.SortChanged {
		g.resort(t.Sort)
	}
	if t.RightClicked {
		g.wk.OpenContextMenu(menuRow, t.ContextAt[0], t.ContextAt[1])
	}
	if t.Activated {
		g.status = append(g.status[:0], "Opened row "...)
		g.status = appendInt(g.status, int(t.Item))
	}
	iconW := theme.Snap(k.Pt(16))
	for row := t.First; row < t.Last; row++ {
		item := g.table.Item(row)
		t.Row(row, item, nil)
		n := int(item)
		t.CellBytes(0, c.Fmt().Int(n).Bytes())
		t.BeginCell(1)
		k.NextSize(ui.Px(iconW), widget.Fill)
		k.Icon(icon.File, t.TextColor())
		k.ClearSize() // an icon that is not ready yet does not take the size: the next label would
		name := c.Fmt().Str("item-").Int(n).Bytes()
		c.NextSize(ui.Pct(1), ui.Pct(1))
		c.NextFont(r.Styles[theme.StyleBody].Font)
		c.NextTextColor(t.TextColor())
		c.NextRole(ui.RoleLabel)
		c.NextSemNameBytes(name)
		c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(c.ParentKey(), 7), name)
		t.EndCell()
		t.CellText(2, kinds[n%len(kinds)])
		t.CellBytes2(3, c.Fmt().Int(n%9973*17).Str(" KB").Bytes())
	}
	g.tableFocus = t.Focused
	_, sy := c.Scroll(t.ScrollView().Inner)
	g.under = sy > 0.5
	rexui.EndTable(k, &t)

	// the status bar: a line above it, the count of selected rows and the
	// status in it
	key := ui.KeyN(keyGallery, 2)
	theme.At(c, 0, h-barH, w, barH)
	c.NextBG(r.Colors[theme.Window])
	c.NextAxis(ui.AxisX)
	c.NextCross(ui.CrossCenter)
	c.NextGap(theme.Snap(k.Pt(4)))
	c.NextPadding(theme.Snap(k.Pt(12)), 0, theme.Snap(k.Pt(12)), 0)
	c.Push(c.BoxKeyed(ui.DrawBackground|base.Floating, key, nil))
	theme.At(c, 0, 0, w, r.Lines.Hairline)
	c.NextBG(r.Colors[theme.Separator])
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 1), nil)
	small := rexui.LabelOpts{Style: rexui.TextSubheadline, Color: rexui.Of(theme.Label2)}
	g.line = appendInt(g.line[:0], g.table.Sel.Count())
	g.line = append(append(g.line, " selected · "...), g.status...)
	g.bytesIn(key, 2, ui.Children(1), g.line, small)
	k.Spacer()
	rexui.Label(k, g.rowsText, small)
	c.Pop()
}

// resort applies a sort change. The two sortable columns are monotone in the
// row's item, so sorting is the identity order or its reverse.
func (g *Gallery) resort(s base.SortState) {
	if s.Active && s.Descending {
		g.table.Order = g.rev
	} else {
		g.table.Order = nil
	}
}

func appendInt(b []byte, v int) []byte {
	if v == 0 {
		return append(b, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	for ; v > 0; v /= 10 {
		i--
		tmp[i] = byte('0' + v%10)
	}
	return append(b, tmp[i:]...)
}
