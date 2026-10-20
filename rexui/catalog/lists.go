package catalog

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The lists' samples: AppKitRef's tables (a bordered NSScrollView of an
// NSTableView, 14 rows of files, rows 2 and 3 selected) and outlines (a file
// tree, Documents and Projects expanded, Projects selected), in the styles,
// focus and window states of their names.

// listState is what the lists' samples edit.
type listState struct {
	tables  [numTables]base.TableState
	cols    [numTables][3]base.Column
	tree    base.Tree
	outline [numOutlines]base.OutlineState
	glyphs  rexui.ListGlyphs
	made    bool
}

const (
	numTables   = 6
	numOutlines = 3
	tableRows   = 14
)

var (
	fileNames = [...]string{"Annual Report.pdf", "Budget.numbers", "Notes.txt", "Photos", "Presentation.key", "README.md", "Screenshot.png"}
	fileKinds = [...]string{"PDF Document", "Numbers", "Plain Text", "Folder", "Keynote", "Markdown", "PNG image"}
	fileSizes [tableRows]string
)

// The tree of AppKitRef's outlines, in pre-order.
var (
	treeNames = [...]string{"Documents", "Projects", "Report.pdf", "Budget.numbers", "Invoices", "March.pdf", "Notes.txt",
		"Downloads", "Installer.dmg", "Music", "Song.m4a", "Pictures", "Photo.heic", "README.md", "Screenshot.png", "Todo.txt"}
	treeSize  = [...]uint32{7, 3, 1, 1, 2, 1, 1, 2, 1, 2, 1, 2, 1, 1, 1, 1}
	treeDepth = [...]uint16{0, 1, 2, 2, 1, 2, 1, 0, 1, 0, 1, 0, 1, 0, 0, 0}
)

func init() {
	for r := range fileSizes {
		fileSizes[r] = itoa(r*37+12) + " KB"
	}
	Samples = append(Samples, listSamples...)
}

func itoa(n int) string {
	var b [12]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}

func (ls *listState) make() {
	if ls.made {
		return
	}
	ls.made = true
	for i := range ls.tables {
		st := &ls.tables[i]
		st.Sel.Reserve(tableRows)
		st.Sel.SelectRange(nil, 2, 3)
		st.Sort = base.SortState{Active: true, Column: 1}
	}
	ls.tree = base.Tree{Size: treeSize[:], Depth: treeDepth[:]}
	for i := range ls.outline {
		st := &ls.outline[i]
		st.Reserve(len(treeSize))
		st.Expand(&ls.tree, 0, true, false)
		st.Expand(&ls.tree, 1, true, false)
		st.Sel.Only(1)
	}
}

// AppKit's columns are 180, 120 and 70 pt (the fitted inset table's 160, 110
// and 60) between 17 pt of intercell spacing; ours are as wide with their
// share of it: the first column 6 pt before its cell and 9 after, the others
// 8 and 9, and the last takes the rest.
//
// The full-width table's last column fills the rest; the fitted inset one's
// does not, as AppKit's (its header ends at 386 of 408).
func (ls *listState) columns(i int, fit bool) []base.Column {
	w := [3]float32{180, 120, 70}
	if fit {
		w = [3]float32{160, 110, 60}
	}
	ls.cols[i] = [3]base.Column{
		{ID: 1, Title: "Name", Width: w[0] + 15, Sortable: true},
		{ID: 2, Title: "Kind", Width: w[1] + 17, Sortable: true},
		{ID: 3, Title: "Size", Width: w[2] + 14, Align: ui.AlignRight, Fill: !fit, Sortable: true},
	}
	return ls.cols[i][:]
}

// bezel is the bordered scroll view the AppKit samples sit in: a 1-pt line
// round the frame.
func bezel(k *base.Kit, w, h float32) ui.ID {
	c, r := k.Context(), k.Theme()
	c.NextSize(ui.Px(k.Pt(w)), ui.Px(k.Pt(h)))
	lw := theme.Snap(k.Pt(1))
	c.NextPadding(lw, lw, lw, lw)
	c.NextBorder(r.Colors[theme.Separator], lw)
	id := c.Box(ui.DrawBorder|ui.Clip, "bezel")
	c.Push(id)
	return id
}

func table(i int, style rexui.ListStyle, striped, fit bool, look base.Look) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		k := s.k
		ls := &s.lists
		ls.make()
		id := bezel(k, 420, 240)
		k.Force(look)
		st := &ls.tables[i]
		t := rexui.BeginTable(k, "Files", st, ls.columns(i, fit), tableRows, rexui.TableOpts{Style: style, Striped: striped, Glyphs: ls.glyphs})
		for r := t.First; r < t.Last; r++ {
			t.Row(r, st.Item(r), nil)
			t.CellText(0, fileNames[r%len(fileNames)])
			t.CellText(1, fileKinds[r%len(fileKinds)])
			t.CellText(2, fileSizes[r])
		}
		rexui.EndTable(k, &t)
		k.Context().Pop()
		return id
	}
}

func outline(i int, style rexui.ListStyle, look base.Look) func(s *State) ui.ID {
	return func(s *State) ui.ID {
		k := s.k
		ls := &s.lists
		ls.make()
		id := bezel(k, 260, 240)
		k.Force(look)
		ol := rexui.BeginOutline(k, "Files", &ls.outline[i], &ls.tree, rexui.OutlineOpts{Style: style, Glyphs: ls.glyphs})
		for r := ol.First; r < ol.Last; r++ {
			node := ol.Row(r, nil)
			ol.TextBytes(base.StrBytes(treeNames[node]))
		}
		rexui.EndOutline(k, &ol)
		k.Context().Pop()
		return id
	}
}

// The probes, in points from the frame: the frame has a 1-pt border, the
// header is 28 pt, and a row 24. Selected rows 2 and 3 are text-free across
// x 125–185 (between the names and the kinds); row 1 too.
const (
	fullRow2  = 1 + 28 + 2*24     // the full-width tables' row 2
	insetRow2 = 1 + 28 + 5 + 2*24 // the inset ones' (5 pt below the header)
	outRow1   = 1 + 24            // the full-width outline's row 1 (no header)
	outInRow1 = 1 + 10 + 24       // the inset outline's
)

// listSpec probes the selection at (x, y), 5 pt into its top row, for its
// edges, radius, fill and label (in the region lab), and the stripe at y2.
func listSpec(x, y float32, lab [4]float32, tol int, y2 float32) Spec {
	sp := Spec{Probe: [2]float32{x, y}, What: shapeW | MeasureLabel, Label: lab, Inside: true, Tol: tol}
	if y2 > 0 {
		sp.What |= MeasureFill2
		sp.Probe2 = [2]float32{x, y2}
	}
	return sp
}

// unemph is an unemphasised selection's listSpec, without its label:
// AppKitRef's offscreen render draws the label twice, darker than AppKit
// draws it on screen, where the label is checked.
func unemph(sp Spec) Spec {
	sp.What &^= MeasureLabel
	sp.Note = "label: AppKitRef's offscreen render draws it twice; checked live"
	return sp
}

// The labels of the selected rows: row 2's name.
var (
	fullLabel  = [4]float32{8, fullRow2 + 4, 60, fullRow2 + 20}
	insetLabel = [4]float32{18, insetRow2 + 4, 70, insetRow2 + 20}
	outLabel   = [4]float32{30, outRow1 + 4, 80, outRow1 + 20}
	outInLabel = [4]float32{40, outInRow1 + 4, 90, outInRow1 + 20}
)

var listSamples = []Sample{
	{"table-fullwidth-emphasized", 420, 240, table(0, rexui.ListFullWidth, true, false, base.LookFocused),
		listSpec(150, fullRow2+5, fullLabel, 30, fullRow2-12)},
	{"table-fullwidth-unemphasized", 420, 240, table(1, rexui.ListFullWidth, true, false, 0),
		unemph(listSpec(150, fullRow2+5, fullLabel, 15, fullRow2-12))},
	{"table-fullwidth-inactive", 420, 240, table(2, rexui.ListFullWidth, true, false, base.LookFocused|base.LookInactive),
		unemph(listSpec(150, fullRow2+5, fullLabel, 15, fullRow2-12))},
	{"table-inset-emphasized", 420, 240, table(3, rexui.ListInset, false, false, base.LookFocused),
		Spec{Probe: [2]float32{150, insetRow2 + 5}, What: MeasureFill | MeasureLabel, Label: insetLabel, Inside: true, Tol: 30,
			Note: "AppKit's columns overflow this view, and it draws the selection full width: the shape is measured on table-inset-fit-*"}},
	{"table-inset-fit-emphasized", 420, 240, table(4, rexui.ListInset, true, true, base.LookFocused),
		listSpec(150, insetRow2+5, insetLabel, 30, insetRow2-12)},
	{"table-inset-fit-inactive", 420, 240, table(5, rexui.ListInset, true, true, base.LookFocused|base.LookInactive),
		unemph(listSpec(150, insetRow2+5, insetLabel, 15, insetRow2-12))},
	{"outline-emphasized", 260, 240, outline(0, rexui.ListFullWidth, base.LookFocused),
		listSpec(150, outRow1+5, outLabel, 30, 0)},
	{"outline-unemphasized", 260, 240, outline(1, rexui.ListFullWidth, 0),
		unemph(listSpec(150, outRow1+5, outLabel, 15, 0))},
	{"outline-inset-emphasized", 260, 240, outline(2, rexui.ListInset, base.LookFocused),
		listSpec(150, outInRow1+5, outInLabel, 30, 0)},
}

// listPage is the grid page of the lists' samples (Pages).
var listPage = []string{
	"table-fullwidth-emphasized", "table-fullwidth-unemphasized", "table-fullwidth-inactive", "table-inset-emphasized",
	"table-inset-fit-emphasized", "table-inset-fit-inactive", "outline-emphasized", "outline-unemphasized", "outline-inset-emphasized",
}
