package gallery

import (
	"gonativeui/icon"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
)

// nameColPt is the width of the column of widget names, in points.
const nameColPt = 64

// cellPt is the width of one state column of the matrix, in points.
const cellPt = 84

var (
	stateNames = [5]string{"Normal", "Hover", "Pressed", "Focused", "Disabled"}
	stateCells = [5]string{"c0", "c1", "c2", "c3", "c4"}
	stateLooks = [5]widget.Look{0, widget.LookHover, widget.LookPressed, widget.LookFocused, 0}
)

// The widgets of the matrix, one row each.
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

var rowNames = [...]string{"Button", "Primary", "Plain", "Icon", "Checkbox", "Mixed", "Switch", "Radio", "Slider", "Field", "Select"}

var selectOptions = []string{"Choice", "Other"}
var radioOptions = []string{"Radio"}

// stateRow builds one row of the matrix: the name, then the widget kind in each
// of the five states.
func (g *Gallery) stateRow(kind int) {
	k := g.k
	name := rowNames[kind]
	k.NextGap(4)
	k.Row(name)
	k.NextSize(k.Fixed(nameColPt), k.Fixed(30))
	k.LabelStyle(name, widget.TextMuted)
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
		k.Button("Button")
	case rowPrimary:
		k.ButtonKind("Primary", widget.ButtonPrimary)
	case rowPlain:
		k.ButtonKind("Plain", widget.ButtonPlain)
	case rowIcon:
		k.IconButton(icon.Plus, "Add", widget.ButtonDefault)
	case rowCheck:
		k.Checkbox("Check", &m.chk[i])
	case rowMixed:
		k.CheckboxTri("Mixed", &m.tri[i])
	case rowSwitch:
		k.Switch("Wi-Fi", &m.sw[i])
	case rowRadio:
		k.RadioGroup("Radio", &m.radio[i], radioOptions)
	case rowSlider:
		k.Slider("Slider", &m.slide[i], 0, 100, 1)
	case rowField:
		k.TextField("Field", &m.text[i], "")
	case rowSelect:
		k.NextWidth(widget.Fill)
		k.Select("Choice", &m.sel[i], selectOptions)
	}
}

// statesPane shows every widget in each of its states.
func (g *Gallery) statesPane(c *ui.Context) {
	k := g.k
	sv := k.BeginScroll("states", &g.scroll[0], widget.ScrollVertical)
	k.NextGap(6)
	k.Column("states")
	g.heading("States")
	k.NextGap(4)
	k.Row("states-head")
	k.NextSize(k.Fixed(nameColPt), k.Fixed(20))
	k.Spacer()
	for _, n := range stateNames {
		k.NextSize(k.Fixed(cellPt), k.Fixed(20))
		k.LabelStyle(n, widget.TextSmall)
	}
	k.EndRow()
	for kind := range rowNames {
		g.stateRow(kind)
	}
	g.heading("Progress")
	k.ProgressBar("Empty", 0)
	k.ProgressBar("Half", 0.5)
	k.ProgressBar("Full", 1)
	if g.working {
		k.ProgressBar("Working", -1)
	}
	g.heading("Card")
	k.BeginCard("card")
	k.LabelStyle("A card groups related controls.", widget.TextMuted)
	k.NextGap(6)
	k.Row("card-buttons")
	k.Button("Revert")
	k.ButtonKind("Apply", widget.ButtonPrimary)
	k.EndRow()
	k.EndCard()
	k.EndColumn()
	k.EndScroll(sv)
}

// livePane has the controls to try.
func (g *Gallery) livePane(c *ui.Context) {
	k := g.k
	sv := k.BeginScroll("controls", &g.scroll[1], widget.ScrollVertical)
	k.NextGap(8)
	k.Column("controls")

	g.heading("Text")
	k.Label("Body text, the default style")
	k.LabelStyle("Muted secondary text", widget.TextMuted)
	k.LabelStyle("Small caption", widget.TextSmall)
	k.LabelStyle("Monospace 0123456789", widget.TextMono)
	k.Paragraph("Wrapped text breaks at the width it is given, at the line break opportunities of Unicode, and keeps the long words whole where it can.")

	g.heading("Entry")
	k.TextField("Notes", &g.notes, "Notes…")
	k.SearchField("Filter", &g.filter, "Search")

	g.heading("Choices")
	k.Checkbox("Enable feature", &g.check)
	// the tri-state checkbox shows what the two options below it add up to, and
	// sets them both (or neither) when clicked
	g.tri = widget.Mixed
	switch {
	case g.check && g.sw:
		g.tri = widget.Checked
	case !g.check && !g.sw:
		g.tri = widget.Unchecked
	}
	if k.CheckboxTri("All options", &g.tri).Changed {
		g.check, g.sw = g.tri == widget.Checked, g.tri == widget.Checked
	}
	k.Switch("Notifications", &g.sw)
	k.RadioGroup("Radio", &g.radio, []string{"One", "Two", "Three"})
	k.NextGap(8)
	k.Row("volume")
	k.NextWidth(k.Fixed(48))
	k.Label("Volume###volume-name")
	k.Slider("Volume", &g.slider, 0, 100, 1)
	k.NextWidth(k.Fixed(30))
	k.LabelBytes(c.Fmt().Int(int(g.slider)).Bytes())
	k.EndRow()
	k.Select("Format", &g.sel, []string{"Plain text", "Markdown", "HTML"})

	g.heading("Buttons")
	k.NextGap(6)
	k.Row("buttons")
	k.Tooltip(k.Button("Open…").ID, "Open a file")
	k.ButtonKind("Save", widget.ButtonPrimary)
	k.ButtonKind("More", widget.ButtonPlain)
	k.ButtonKind("Remove", widget.ButtonDanger)
	k.Tooltip(k.IconButton(icon.Plus, "Add item", widget.ButtonDefault).ID, "Add an item")
	k.EndRow()
	k.NextGap(6)
	k.Row("buttons2")
	k.MenuButton("Menu", menuFile)
	if k.Button("Delete…").Clicked {
		g.askDelete = true
	}
	k.EndRow()
	k.NextGap(6)
	k.Row("switches")
	dark := g.mode == theme.Dark
	if k.Switch("Dark mode", &dark).Changed {
		if dark {
			g.SetMode(theme.Dark)
		} else {
			g.SetMode(theme.Light)
		}
	}
	k.Switch("Show activity", &g.working)
	k.EndRow()

	g.heading("Tabs")
	k.Tabs("Sections", &g.tab, []string{"Overview", "Details", "History"})
	k.LabelStyle(tabText[g.tab], widget.TextMuted)

	g.heading("List")
	k.NextHeight(k.Fixed(120))
	l := k.BeginList("items", &g.list, listItems)
	g.listFocus = l.Focused
	for r := l.First; r < l.Last; r++ {
		l.Row(r, uint32(r))
		k.NextWidth(widget.Fill)
		k.LabelBytes(c.Fmt().Str("List item ").Int(r + 1).Bytes())
	}
	k.EndList(&l)

	k.EndColumn()
	k.EndScroll(sv)
}

var tabText = [3]string{"A summary of the thing.", "Every detail of it.", "What happened to it."}

// tablePane has the table of a million rows.
func (g *Gallery) tablePane(c *ui.Context) {
	k := g.k
	k.NextGap(8)
	k.Row("table-head")
	k.NextWidth(widget.Fill)
	k.NextHeight(k.Fixed(24))
	g.heading("Table")
	k.NextSize(k.Fixed(110), k.Fixed(24))
	k.LabelBytesStyle(g.rowsText(c), widget.TextMuted)
	k.EndRow()
	k.SearchField("Search rows", &g.search, "Search rows")

	t := k.BeginTable("rows", &g.table, g.cols[:], g.rows)
	if t.SortChanged {
		g.resort(t.Sort)
	}
	if t.RightClicked {
		k.OpenContextMenu(menuRow, t.ContextAt[0], t.ContextAt[1])
	}
	if t.Activated {
		g.status = append(g.status[:0], "Opened row "...)
		g.status = appendInt(g.status, int(t.Item))
	}
	for r := t.First; r < t.Last; r++ {
		item := uint32(r)
		if g.table.Order != nil {
			item = g.table.Order[r]
		}
		t.Row(r, item)
		n := int(item)
		t.CellBytes(0, c.Fmt().Int(n).Bytes())
		t.BeginCell(1)
		k.NextHeight(widget.Fill)
		k.Icon(icon.File, t.TextColor())
		k.NextHeight(widget.Fill)
		k.LabelBytes(c.Fmt().Str("item-").Int(n).Bytes())
		t.EndCell()
		t.CellText(2, kinds[n%len(kinds)])
		t.CellBytes(3, c.Fmt().Int(n%9973*17).Str(" KB").Bytes())
	}
	g.tableFocus = t.Focused
	k.EndTable(&t)

	k.NextGap(4)
	k.Row("status")
	k.LabelBytesStyle(c.Fmt().Int(g.table.Sel.Count()).Str(" selected ·").Bytes(), widget.TextSmall)
	k.LabelBytesStyle(g.status, widget.TextSmall)
	k.EndRow()
}

// resort applies a sort change. The two sortable columns are monotone in the
// row's item, so sorting is the identity order or its reverse.
func (g *Gallery) resort(s widget.SortState) {
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

// rowsText formats the row count with thousands separators.
func (g *Gallery) rowsText(c *ui.Context) []byte {
	f := c.Fmt()
	n := g.rows
	var parts [7]int
	np := 0
	for ; n >= 1000; n /= 1000 {
		parts[np] = n % 1000
		np++
	}
	f.Int(n)
	for i := np - 1; i >= 0; i-- {
		f.Str(",")
		if parts[i] < 100 {
			f.Str("0")
		}
		if parts[i] < 10 {
			f.Str("0")
		}
		f.Int(parts[i])
	}
	return f.Str(" rows").Bytes()
}
