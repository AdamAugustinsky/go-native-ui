package demo

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The sidebar rows' ids.
const (
	placeAirDrop uint32 = iota + 1
	placeRecents
	placeApplications
	placeDesktop
	placeDocuments
	placeDownloads
	placeCloud
	placeShared
	placeDisk
	placeNetwork
	placeRed
	placeOrange
	placeYellow
	placeGreen
	placeBlue
	placePurple
	placeGrey
	placeAllTags
	placeWork
	placeDrafts
	placePersonal
	hdrFavorites
	hdrCloud
	hdrLocations
	hdrTags

	setFirst // the settings rows: setFirst + their index in settingsTiles
)

// The settings rows the scenes start on (settingsTiles' order).
const (
	setAppearance = setFirst + 8
	setWifi       = setFirst
)

// The notes rows.
const (
	noteLaunch uint32 = 200 + iota
	noteGroceries
	noteTrip
	noteBooks
	noteSoup
	noteGifts
	noteWorkout
	notePodcast
	noteGarden
	noteQuotes
	hdrToday
	hdrWeek
	hdrMonth
	hdrConnections
	hdrSystem
	hdrPersonal
	hdrSecurity
)

// file is a row of the finder's list.
type file struct {
	name, date, size, kind []byte
	folder                 bool
}

// The finder scene's files (tools/appkitscenes: Files): 48 rows of 16 names,
// numbered after the first 16, with their dates and sizes from the same
// formulas.
var (
	fileNames = [...]string{"Annual Report.pdf", "Budget 2026.numbers", "Client Brief.pages", "Design Review.key", "Invoices", "Meeting Notes.txt",
		"Moodboard.png", "Photos", "Product Roadmap.pdf", "Project Plan.pages", "README.md", "Receipts", "Screenshot 2026-10-12.png",
		"Team Offsite.key", "Timesheet.numbers", "Travel.pdf"}
	fileKinds = [...]string{"PDF document", "Numbers spreadsheet", "Pages document", "Keynote presentation", "Folder", "Plain text", "PNG image",
		"Folder", "PDF document", "Pages document", "Markdown document", "Folder", "PNG image", "Keynote presentation", "Numbers spreadsheet", "PDF document"}
)

const fileRows = 48

func makeFiles() []file {
	fs := make([]file, fileRows)
	for row := range fs {
		i := row % len(fileNames)
		name := fileNames[i]
		if n := row/len(fileNames) + 1; n > 1 {
			ext := path.Ext(name)
			name = fmt.Sprintf("%s %d%s", strings.TrimSuffix(name, ext), n, ext)
		}
		folder := fileKinds[i] == "Folder"
		size := "--"
		if !folder {
			size = fmt.Sprintf("%d KB", row*137%900+12)
		}
		fs[row] = file{name: []byte(name), date: []byte(fmt.Sprintf("%d Oct 2026 at %02d:%02d", 18-row%17, 8+row%11, row*7%60)),
			size: []byte(size), kind: []byte(fileKinds[i]), folder: folder}
	}
	return fs
}

// finderFonts are the inspector's title (15 pt semibold) and heading (13 pt
// semibold), made at the window's scale (Demo.fonts).
var finderFonts [2]text.FontKey

// model fills the scene's sidebar, files, path and tabs.
func (d *Demo) model() {
	ic := &d.ic
	g := rexui.Sym
	for i := range d.open {
		d.open[i] = true
	}
	switch d.opt.Scene {
	case Finder:
		tag := func(id uint32, t string) rexui.SidebarItem {
			return rexui.SidebarItem{ID: id, Title: t, Glyph: g(ic.dot)}
		}
		d.places = []rexui.SidebarItem{
			{ID: hdrFavorites, Title: "Favorites", Header: true, Open: &d.open[0]},
			{ID: placeAirDrop, Title: "AirDrop", Glyph: g(ic.airdrop)},
			{ID: placeRecents, Title: "Recents", Glyph: g(ic.recents)},
			{ID: placeApplications, Title: "Applications", Glyph: g(ic.apps)},
			{ID: placeDesktop, Title: "Desktop", Glyph: g(ic.desktop)},
			{ID: placeDocuments, Title: "Documents", Glyph: g(ic.docs)},
			{ID: placeDownloads, Title: "Downloads", Glyph: g(ic.downloads)},
			{ID: hdrCloud, Title: "iCloud", Header: true, Open: &d.open[1]},
			{ID: placeCloud, Title: "iCloud Drive", Glyph: g(ic.cloud)},
			{ID: placeShared, Title: "Shared", Glyph: g(ic.shared)},
			{ID: hdrLocations, Title: "Locations", Header: true, Open: &d.open[2]},
			{ID: placeDisk, Title: "Macintosh HD", Glyph: g(ic.disk)},
			{ID: placeNetwork, Title: "Network", Glyph: g(ic.network)},
			{ID: hdrTags, Title: "Tags", Header: true, Open: &d.open[3]},
			tag(placeRed, "Red"), tag(placeOrange, "Orange"), tag(placeYellow, "Yellow"), tag(placeGreen, "Green"),
			tag(placeBlue, "Blue"), tag(placePurple, "Purple"), tag(placeGrey, "Grey"),
			{ID: placeAllTags, Title: "All Tags…", Glyph: g(ic.dashed)},
		}
		if d.opt.Nested {
			for i := range d.places {
				if d.places[i].ID == placeDocuments {
					d.places[i].Open = &d.open[4]
					d.open[5] = false // Work closed, as the scene's
					d.places = slices.Insert(d.places, i+1,
						rexui.SidebarItem{ID: placeWork, Title: "Work", Glyph: g(ic.folder), Level: 1, Open: &d.open[5]},
						rexui.SidebarItem{ID: placeDrafts, Title: "Drafts", Glyph: g(ic.folder), Level: 2},
						rexui.SidebarItem{ID: placePersonal, Title: "Personal", Glyph: g(ic.folder), Level: 1})
					break
				}
			}
		}
		d.tagColours()
		d.path = []rexui.PathItem{
			{Title: "Macintosh HD", Glyph: g(ic.disk)}, {Title: "Users", Glyph: g(ic.folder)},
			{Title: "adam", Glyph: g(ic.folder)}, {Title: "Documents", Glyph: g(ic.folder)},
		}
		d.files = makeFiles()
		// AppKit's columns (260, 160, 64, and 130 that takes the rest) with
		// their share of the 17-pt intercell spacing (rexui's catalogue)
		d.cols = [4]base.Column{
			{ID: 1, Title: "Name", Width: 260 + 15, Sortable: true},
			{ID: 2, Title: "Date Modified", Width: 160 + 17, Sortable: true},
			{ID: 3, Title: "Size", Width: 64 + 17, Align: ui.AlignRight, Sortable: true},
			{ID: 4, Title: "Kind", Width: 130 + 14, Fill: true, Sortable: true},
		}
		d.table.Sort = base.SortState{Active: true, Column: 1}
		d.table.Sel.Reserve(fileRows)
		d.table.Sel.SelectRange(nil, 3, 4)
	case Settings:
		d.places = d.places[:0]
		for i, t := range settingsTiles {
			if t.header != "" {
				d.places = append(d.places, rexui.SidebarItem{ID: hdrConnections + uint32(len(d.places)), Title: t.header, Header: true})
			}
			d.places = append(d.places, rexui.SidebarItem{ID: setFirst + uint32(i), Title: t.title, Glyph: g(ic.tiles[i]), Tile: 1})
		}
		d.tileColours()
		for i := range d.pops {
			d.pops[i] = settingsPopups[i].sel
		}
		for i := range d.switches {
			d.switches[i] = settingsSwitches[i].on
		}
		d.sliders = [2]float32{0.7, 0.45}
	case Notes:
		d.places = []rexui.SidebarItem{{ID: hdrToday, Title: "Today", Header: true}}
		for i, n := range notes {
			if n.header != "" {
				d.places = append(d.places, rexui.SidebarItem{ID: hdrToday + uint32(len(d.places)), Title: n.header, Header: true})
			}
			d.places = append(d.places, rexui.SidebarItem{ID: noteLaunch + uint32(i), Title: n.title, Detail: n.date + "  " + n.preview})
		}
		d.noteText()
	case Tabs:
		titles := [...]string{"Inbox", "Roadmap", "Release notes", "Design review", "Budget"}
		d.tabs = make([]rexui.Tab, 0, rexui.MaxTabs)
		for _, t := range titles {
			d.tabs = append(d.tabs, rexui.Tab{ID: d.nextTab, Title: t, Glyph: g(ic.doc)})
			d.nextTab++
		}
		for _, t := range [...]string{"Overview", "Activity", "Files", "Settings"} {
			d.inTabs = append(d.inTabs, rexui.Tab{ID: d.nextTab, Title: t, NoClose: true})
			d.nextTab++
		}
	}
	d.side.Sel = d.l.selectedPlace
}

// tagColours are the finder's tags (NSColor.systemRed … systemGray) as the
// scene's sidebar shows them (measured: the glass's vibrancy darkens them in
// light mode and lightens them in dark).
var tagColours = [7]rexui.Pair{
	rexui.Solid(0xEE2C21, 0xFF675C), rexui.Solid(0xEE8600, 0xFFBF2B), rexui.Solid(0xEFBC00, 0xFFF429),
	rexui.Solid(0x19BC32, 0x56FB6F), rexui.Solid(0x006BEF, 0x2BA5FF), rexui.Solid(0x9643C0, 0xE17BFF),
	rexui.Solid(0x7B7B80, 0xB5B5BA),
}

// tagColours sets the tags' colours for the mode shown.
func (d *Demo) tagColours() {
	t := d.k.Theme().T
	n := 0
	for i := range d.places {
		if id := d.places[i].ID; id >= placeRed && id <= placeGrey {
			d.places[i].Color = tagColours[n%len(tagColours)].At(t)
			n++
		}
	}
}

// fileList is the finder's table: its header in the toolbar's pocket, inset
// striped rows, a selection of two merged rows; a path bar below it when
// asked.
func (d *Demo) fileList(top, w, h float32) {
	k := d.k
	c, r := k.Context(), k.Theme()
	s := r.Scale
	pathH := float32(0)
	if d.opt.PathBar {
		pathH = theme.Snap(28 * s)
	}
	if d.opt.Scene == Finder {
		d.tagColours()
	}
	if d.scrolled > 0 { // the scene's scrolled phase: 130 pt down (the table at y -50, 80 at rest)
		at := float32(130) / 24
		d.table.SetTopRow(int(at), at-float32(int(at)))
		d.scrolled--
	}
	k.NextSize(ui.Px(w), ui.Px(h-pathH))
	t := rexui.BeginTable(k, "Files", &d.table, d.cols[:], len(d.files), rexui.TableOpts{Striped: true, Glyphs: d.glyphs, ClearHeader: true, TopInset: top})
	if t.SortChanged {
		d.sortFiles(t.Sort)
	}
	iconW := theme.Snap(16 * s)
	for row := t.First; row < t.Last; row++ {
		item := d.table.Item(row)
		f := &d.files[item]
		t.Row(row, item, f.name)
		// the name: a 16-pt icon, the text 5 after it (AppKit's image view
		// and text field). The cell starts with the icon centred in its
		// square at the title's x, and icons differ in ink (a folder, a
		// page, a platform's fallback glyph) as AppKit's file icons do in
		// their one 198–214 image view (finder capture: a folder's ink
		// 199–213, a page's 200–212), so the column audit does not apply.
		c.NextAuditExempt(ui.AuditColumn.Mask())
		t.BeginCell(0)
		cell := c.Parent()
		cr := c.Rect(cell)
		pl, pr := t.CellPads(0)
		c.NextSize(ui.Px(iconW+theme.Snap(5*s)-r.Px.GapSmall), ui.Pct(1))
		c.Push(c.BoxKeyed(0, ui.KeyN(c.KeyOf(cell), 1), nil))
		d.fileIcon(c, f.folder, iconW, t.RowH())
		c.Pop()
		c.NextSize(ui.Px(max(cr[2]-cr[0]-pl-pr-iconW-theme.Snap(5*s), 0)), ui.Pct(1))
		c.NextFont(r.Styles[theme.StyleBody].Font)
		c.NextTextColor(t.TextColor())
		c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX, ui.KeyN(c.KeyOf(cell), 2), f.name)
		t.EndCell()
		t.CellBytes2(1, f.date)
		t.CellBytes2(2, f.size)
		t.CellBytes2(3, f.kind)
	}
	v := t.ScrollView()
	_, sy := c.Scroll(v.Inner)
	d.under = sy > 0.5
	if !d.focused { // the file list has the keyboard, as in the scene
		c.SetFocus(c.KeyOf(v.Inner))
		d.focused = true
	}
	rexui.EndTable(k, &t)

	if d.opt.PathBar {
		key := ui.HashString(0, "path")
		theme.At(c, 0, h-pathH, w, pathH)
		c.NextBG(r.Colors[theme.Window])
		c.Push(c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 1), nil))
		theme.At(c, 0, 0, w, r.Lines.Hairline)
		c.NextBG(r.Colors[theme.Separator])
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 2), nil)
		theme.At(c, theme.Snap(8*s), theme.Snap(2*s), w-theme.Snap(16*s), pathH-theme.Snap(4*s))
		c.Push(c.BoxKeyed(base.Floating, ui.KeyN(key, 3), nil))
		if p := rexui.PathBar(k, "Path", d.path, rexui.PathBarOpts{Size: theme.SizeSmall}); p.Index >= 0 && p.Index < len(d.path)-1 {
			d.path = d.path[:p.Index+1]
		}
		c.Pop()
		c.Pop()
	}
}

// The file icons' colours: a white page with a grey edge, a blue folder
// (measured #F5F5F5 and #DFDFDF, #0086FC and #008FFC on 27.2).
var (
	pageFill   = rexui.Solid(0xF5F5F5, 0xF5F5F5)
	pageEdge   = rexui.Solid(0xBDBDBD, 0x9A9A9A)
	folderFill = rexui.Solid(0x0086FC, 0x008FFC)
	// the inspector's page: an edge as faint as the system's generic icon's
	previewEdge = rexui.Solid(0xDCDCDC, 0xDCDCDC)
)

// fileIcon builds a file's 16-pt icon at the left of the current parent, a
// row h high: the box is the icon's square, the symbol centred in it.
func (d *Demo) fileIcon(c *ui.Context, folder bool, side, h float32) {
	k := d.k
	t := k.Theme().T
	key := c.ParentKey()
	layer := func(n uint64, ic icon.ID, col uint32) {
		if f, disp := k.Icons().Prepare(c, ic, col); f != 0 {
			theme.At(c, 0, theme.Snap((h-side)/2), side, side)
			c.BoxKeyed(f|base.Floating, ui.KeyN(key, n), disp)
		}
	}
	if folder {
		layer(1, d.ic.fileFolder, folderFill.At(t))
		return
	}
	layer(1, d.ic.fileDocFill, pageFill.At(t))
	layer(2, d.ic.fileDoc, pageEdge.At(t))
}

// sortFiles orders the rows by the sort the header asks for.
func (d *Demo) sortFiles(st base.SortState) {
	if !st.Active {
		d.table.Order = nil
		return
	}
	o := make([]uint32, len(d.files))
	for i := range o {
		o[i] = uint32(i)
	}
	key := func(f *file) []byte {
		switch st.Column {
		case 2:
			return f.date
		case 3:
			return f.size
		case 4:
			return f.kind
		}
		return f.name
	}
	sort.SliceStable(o, func(a, b int) bool {
		x, y := key(&d.files[o[a]]), key(&d.files[o[b]])
		if st.Descending {
			return string(y) < string(x)
		}
		return string(x) < string(y)
	})
	d.table.Order = o
}

// inspectorContent is the finder's inspector (the scene's Get Info pane): a
// 128-pt preview, the name, the kind and size, and the information rows, in
// a stack 16 from the edges with 6 between.
func (d *Demo) inspectorContent(tbH, w float32) {
	k := d.k
	c, r := k.Context(), k.Theme()
	s := r.Scale
	key := ui.HashString(0, "inspector")
	pad := theme.Snap(16 * s)
	y := tbH + pad
	t := r.T
	// the preview: a page, as the scene's presentation icon
	big := theme.Snap(128 * s)
	theme.At(c, pad, y, big, big)
	c.NextJustify(ui.JustifyCenter)
	c.NextCross(ui.CrossCenter)
	c.NextAxis(ui.AxisX)
	c.Push(c.BoxKeyed(base.Floating, ui.KeyN(key, 1), nil))
	for i, layer := range [2]struct {
		ic  icon.ID
		col uint32
	}{{d.ic.previewFill, pageFill.At(t)}, {d.ic.preview, previewEdge.At(t)}} {
		if f, disp := k.Icons().Prepare(c, layer.ic, layer.col); f != 0 {
			iw, ih := k.IconSize(layer.ic)
			theme.At(c, theme.Snap((big-iw)/2), theme.Snap((big-ih)/2), iw, ih)
			c.BoxKeyed(f|base.Floating, ui.KeyN(key, 2+uint64(i)), disp)
		}
	}
	c.Pop()
	y += big + theme.Snap(6*s)
	label := func(n uint64, x, y, lh float32, b string, f text.FontKey, col uint32) float32 {
		theme.At(c, x, y, max(w-x-pad, 0), lh)
		c.NextFont(f)
		c.NextTextColor(col)
		c.BoxKeyed(ui.DrawText|ui.EllipsisOverflowX|base.Floating, ui.KeyN(key, n), base.StrBytes(b))
		return k.TextWidth(f, b)
	}
	lab, lab2 := r.Colors[theme.Label], r.Colors[theme.Label2]
	sub := &r.Styles[theme.StyleSubheadline]
	tx := pad
	label(10, tx, y, theme.Snap(19*s), "Design Review.key", finderFonts[0], lab)
	y += theme.Snap(19*s) + theme.Snap(6*s)
	label(11, tx, y, sub.Line, "Keynote presentation – 2.4 MB", sub.Font, lab2)
	y += sub.Line + theme.Snap(14*s)
	label(12, tx, y, theme.Snap(16*s), "Information", finderFonts[1], lab)
	y += theme.Snap(16*s) + theme.Snap(6*s)
	for i, kv := range [3][2]string{{"Created", "2 Oct 2026 at 10:12"}, {"Modified", "15 Oct 2026 at 16:40"}, {"Last opened", "Today at 09:41"}} {
		kw := label(20+uint64(2*i), tx, y, sub.Line, kv[0], sub.Font, lab2)
		label(21+uint64(2*i), tx+kw+theme.Snap(8*s), y, sub.Line, kv[1], sub.Font, lab)
		y += sub.Line + theme.Snap(6*s)
	}
}
