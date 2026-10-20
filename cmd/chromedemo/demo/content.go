package demo

import (
	"gonativeui/rexui"
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
	placeGreen
	hdrFavorites
	hdrCloud
	hdrLocations
	hdrTags

	setWifi
	setBluetooth
	setNetwork
	setNotifications
	setSound
	setFocus
	setScreenTime
	setGeneral
	setAppearance
	setAccessibility
	setControl
	setDesktop
	setDisplays
	setWallpaper
	hdrConnections
	hdrSystem
	hdrPersonal

	noteLaunch
	noteGroceries
	noteTrip
	noteBooks
	noteSoup
	noteGifts
	noteWorkout
	hdrToday
	hdrWeek
	hdrMonth
)

type file struct {
	name, date, size, kind string
	folder                 bool
}

var files = []file{
	{"Annual Report.pdf", "18 Oct 2026 at 08:00", "2.1 MB", "PDF document", false},
	{"Budget 2026.numbers", "17 Oct 2026 at 09:07", "149 KB", "Numbers spreadsheet", false},
	{"Client Brief.pages", "16 Oct 2026 at 10:14", "286 KB", "Pages document", false},
	{"Design Review.key", "15 Oct 2026 at 11:21", "423 KB", "Keynote presentation", false},
	{"Invoices", "14 Oct 2026 at 12:28", "--", "Folder", true},
	{"Meeting Notes.txt", "13 Oct 2026 at 13:35", "697 KB", "Plain text", false},
	{"Moodboard.png", "12 Oct 2026 at 14:42", "834 KB", "PNG image", false},
	{"Photos", "11 Oct 2026 at 15:49", "--", "Folder", true},
	{"Product Roadmap.pdf", "10 Oct 2026 at 16:56", "1.1 MB", "PDF document", false},
	{"Project Plan.pages", "9 Oct 2026 at 17:03", "1.2 MB", "Pages document", false},
	{"README.md", "8 Oct 2026 at 18:10", "1.4 MB", "Markdown", false},
	{"Receipts", "7 Oct 2026 at 19:17", "--", "Folder", true},
	{"Screenshot 2026-10-12.png", "6 Oct 2026 at 20:24", "1.7 MB", "PNG image", false},
	{"Team Offsite.key", "5 Oct 2026 at 21:31", "1.8 MB", "Keynote presentation", false},
	{"Travel.numbers", "4 Oct 2026 at 22:38", "2.0 MB", "Numbers spreadsheet", false},
	{"Vacation.mov", "3 Oct 2026 at 23:45", "2.1 MB", "QuickTime movie", false},
	{"Wireframes.sketch", "2 Oct 2026 at 08:52", "2.3 MB", "Sketch document", false},
	{"Year in Review.pdf", "1 Oct 2026 at 09:59", "2.4 MB", "PDF document", false},
	{"Zettelkasten", "30 Sep 2026 at 10:06", "--", "Folder", true},
	{"Archive.zip", "29 Sep 2026 at 11:13", "3.1 MB", "ZIP archive", false},
	{"Brand Guide.pdf", "28 Sep 2026 at 12:20", "4.2 MB", "PDF document", false},
	{"Contacts.vcf", "27 Sep 2026 at 13:27", "12 KB", "vCard", false},
	{"Diagrams", "26 Sep 2026 at 14:34", "--", "Folder", true},
	{"Expenses.numbers", "25 Sep 2026 at 15:41", "98 KB", "Numbers spreadsheet", false},
}

const notesText = "Launch meeting\n\nAgenda: review the launch checklist, confirm the press date, and walk through the rollout plan with support.\n\n• Checklist owners report status\n• Press embargo lifts Tuesday 09:00\n• Support macros ready by Friday\n\nOpen questions: pricing page copy, the onboarding email, and whether the beta testers get early access to the new sync."

// model fills the scene's sidebar, path and tabs.
func (d *Demo) model() {
	ic := &d.ic
	g := rexui.Sym
	for i := range d.open {
		d.open[i] = true
	}
	switch d.opt.Scene {
	case Finder:
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
			{ID: placeDisk, Title: "Macintosh HD", Glyph: g(ic.disk), Open: &d.open[3]},
			{ID: placeNetwork, Title: "Network", Glyph: g(ic.network), Level: 1},
			{ID: hdrTags, Title: "Tags", Header: true, Open: &d.open[4]},
			{ID: placeRed, Title: "Red", Glyph: g(ic.dot), Color: 0xFF453AFF},
			{ID: placeOrange, Title: "Orange", Glyph: g(ic.dot), Color: 0xFF9F0AFF},
			{ID: placeGreen, Title: "Green", Glyph: g(ic.dot), Color: 0x32D74BFF},
		}
		d.path = []rexui.PathItem{
			{Title: "Macintosh HD", Glyph: g(ic.disk)}, {Title: "Users", Glyph: g(ic.folder)},
			{Title: "adam", Glyph: g(ic.folder)}, {Title: "Documents", Glyph: g(ic.folder)},
		}
		d.fileSel = [2]int{3, 4}
	case Settings:
		d.places = []rexui.SidebarItem{
			{ID: hdrConnections, Title: "Connections", Header: true},
			{ID: setWifi, Title: "Wi-Fi", Glyph: g(ic.wifi)},
			{ID: setBluetooth, Title: "Bluetooth", Glyph: g(ic.network)},
			{ID: setNetwork, Title: "Network", Glyph: g(ic.network)},
			{ID: hdrSystem, Title: "System", Header: true},
			{ID: setNotifications, Title: "Notifications", Glyph: g(ic.bell)},
			{ID: setSound, Title: "Sound", Glyph: g(ic.bell)},
			{ID: setFocus, Title: "Focus", Glyph: g(ic.moon)},
			{ID: setScreenTime, Title: "Screen Time", Glyph: g(ic.recents)},
			{ID: hdrPersonal, Title: "Personalisation", Header: true},
			{ID: setGeneral, Title: "General", Glyph: g(ic.gear)},
			{ID: setAppearance, Title: "Appearance", Glyph: g(ic.paint)},
			{ID: setAccessibility, Title: "Accessibility", Glyph: g(ic.person)},
			{ID: setControl, Title: "Control Centre", Glyph: g(ic.gear)},
			{ID: setDesktop, Title: "Desktop & Dock", Glyph: g(ic.desktop)},
			{ID: setDisplays, Title: "Displays", Glyph: g(ic.sun)},
			{ID: setWallpaper, Title: "Wallpaper", Glyph: g(ic.paint)},
		}
	case Notes:
		d.places = []rexui.SidebarItem{
			{ID: hdrToday, Title: "Today", Header: true},
			{ID: noteLaunch, Title: "Launch meeting", Detail: "09:41  Agenda: review the launch checklist"},
			{ID: noteGroceries, Title: "Grocery list", Detail: "08:15  Oat milk, lemons, rice, coffee beans"},
			{ID: hdrWeek, Title: "Previous 7 Days", Header: true},
			{ID: noteTrip, Title: "Trip ideas", Detail: "Friday  Lisbon in spring, the coast road"},
			{ID: noteBooks, Title: "Book notes", Detail: "Thursday  Chapter 4: habits and cues"},
			{ID: noteSoup, Title: "Recipe: lentil soup", Detail: "Tuesday  Red lentils, cumin, lemon"},
			{ID: noteGifts, Title: "Gift ideas", Detail: "Monday  A good pen, the jigsaw puzzle"},
			{ID: hdrMonth, Title: "Previous 30 Days", Header: true},
			{ID: noteWorkout, Title: "Workout plan", Detail: "12/10/2026  Three days a week, rowing"},
		}
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

// newTab adds a tab after the chosen one and chooses it.
func (d *Demo) newTab() {
	if len(d.tabs) >= rexui.MaxTabs {
		return
	}
	t := rexui.Tab{ID: d.nextTab, Title: "Untitled", Glyph: rexui.Sym(d.ic.doc)}
	d.nextTab++
	at := min(d.tabSel+1, len(d.tabs))
	d.tabs = append(d.tabs, rexui.Tab{})
	copy(d.tabs[at+1:], d.tabs[at:])
	d.tabs[at] = t
	d.tabSel = at
}

// sidebar builds the sidebar pane (the current parent).
func (d *Demo) sidebar(tbH float32) {
	k := d.k
	o := rexui.SidebarOpts{Top: tbH}
	if d.opt.Scene == Settings {
		o.Pad = 34 // the search field: 4 below the safe area, 24 tall, 6 above the first header
	}
	if d.opt.Scene == Notes {
		o.RowH, o.HeaderH, o.BodyHeaders = 52, 24, true
	}
	s := rexui.Sidebar(k, "Sidebar", &d.side, d.places, o)
	if !d.focused && d.opt.Scene != Finder { // settings and notes start with the keyboard in the sidebar
		k.Context().SetFocus(k.Context().KeyOf(s.ID))
		d.focused = true
	}
	if d.opt.Scene == Settings {
		c := k.Context()
		r := k.Theme()
		w := c.Rect(c.Parent())
		inset := theme.Snap(r.Window.Sidebar.SearchInset)
		theme.At(c, inset, tbH+theme.Snap(k.Pt(4)), max(w[2]-w[0]-2*inset, 0), theme.Snap(r.Window.Sidebar.SearchH))
		c.Push(c.Box(base.Floating, "search"))
		rexui.SidebarSearch(k, "Search Settings", &d.sideQ, "Search")
		c.Pop()
	}
}

// inspector builds the inspector pane (the current parent).
func (d *Demo) inspector(tbH float32) {
	k := d.k
	c, r := k.Context(), k.Theme()
	pr := c.Rect(c.Parent())
	w := pr[2] - pr[0]
	theme.At(c, 0, 0, w, pr[3]-pr[1])
	c.NextBG(rexui.GlassFill(k, rexui.GlassSidebar))
	c.Push(c.BoxKeyed(ui.DrawBackground|base.Floating, ui.HashString(0, "inspector glass"), nil))
	f := d.fileSel[0]
	if f < 0 || f >= len(files) {
		f = 0
	}
	pad := theme.Snap(k.Pt(16))
	y := tbH + theme.Snap(k.Pt(16))
	label := func(n uint64, s string, st theme.TextStyle, role theme.Role) {
		sf := &r.Styles[st]
		theme.At(c, pad, y, max(w-2*pad, 0), sf.Line)
		c.NextFont(sf.Font)
		c.NextTextColor(r.Colors[role])
		c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.KeyN(ui.HashString(0, "inspector"), n), base.StrBytes(s))
		y += sf.Line + theme.Snap(k.Pt(4))
	}
	label(1, files[f].name, theme.StyleHeadline, theme.Label)
	label(2, files[f].kind+" — "+files[f].size, theme.StyleSubheadline, theme.Label2)
	y += theme.Snap(k.Pt(12))
	label(3, "Modified", theme.StyleSubheadline, theme.Label2)
	label(4, files[f].date, theme.StyleBody, theme.Label)
	c.Pop()
}

// contentPane builds the content (the current parent) of width w and
// height h, starting under the toolbar of height tbH.
func (d *Demo) contentPane(tbH, w, h float32) {
	switch d.opt.Scene {
	case Finder:
		d.fileList(tbH, w, h)
	case Settings:
		d.settingsForm(tbH, w, h)
	case Notes:
		d.notesEditor(tbH, w, h)
	case Tabs:
		d.tabsContent(tbH, w, h)
	}
}

// The file list's header height and column starts, in points.
const fileHeaderH = 28

var fileCols = [4]float32{16, 260 + 17, 160 + 17, 64 + 17}

// fileList is the finder's table: a header under the toolbar, inset striped
// rows, an inset selection of two merged rows, and a path bar at the bottom.
func (d *Demo) fileList(tbH, w, h float32) {
	k := d.k
	c, r := k.Context(), k.Theme()
	s := r.Scale
	key := ui.HashString(0, "files")
	pathH := theme.Snap(28 * s)
	hdrH := theme.Snap(fileHeaderH * s)
	rowH := theme.Snap(24 * s)
	inset := theme.Snap(r.Window.Selection.Inset)
	cols := &fileCols
	body := &r.Styles[theme.StyleBody]

	k.NextSize(ui.Px(w), ui.Px(h-pathH))
	v := k.BeginScroll("Files", &d.content, base.ScrollVertical)
	if d.scrolled > 0 {
		c.SetScroll(v.Inner, 0, float64(3*rowH))
		d.scrolled--
	}
	_, sy := c.Scroll(v.Inner)
	d.under = sy > 0.5
	c.NextSize(ui.Pct(1), ui.Px(tbH+hdrH+theme.Snap(5*s)))
	c.BoxKeyed(0, ui.KeyN(key, 1), nil)
	for i := range files {
		f := &files[i]
		sel := i >= d.fileSel[0] && i <= d.fileSel[1]
		c.NextSize(ui.Pct(1), ui.Px(rowH))
		c.NextRole(ui.RoleListItem)
		c.NextSemName(f.name)
		var st ui.SemState
		if sel {
			st = ui.SemSelected
		}
		c.NextSemState(st)
		rk := ui.KeyN(key, 100+uint64(i))
		row := c.BoxKeyed(ui.Clickable, rk, nil)
		c.Push(row)
		if c.PeekSignal(rk).Pressed {
			d.fileSel = [2]int{i, i}
		}
		var fill uint32
		switch {
		case sel && k.Inactive():
			fill = r.Colors[theme.SelectionUnemph]
		case sel:
			fill = r.Colors[theme.Selection]
		case i%2 == 1:
			fill = r.Colors[theme.AltRow]
		}
		if fill != 0 {
			top := !(sel && i > d.fileSel[0])
			bot := !(sel && i < d.fileSel[1])
			rad := r.Window.Selection.Radius
			var tl, bl float32
			if top || !sel {
				tl = rad
			}
			if bot || !sel {
				bl = rad
			}
			theme.At(c, inset, 0, w-2*inset, rowH)
			c.NextBG(fill)
			c.NextRadii(tl, tl, bl, bl)
			c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(rk, 1), nil)
			if sel && !top && !k.Inactive() {
				theme.At(c, inset, 0, w-2*inset, r.Lines.Hairline)
				c.NextBG(r.Colors[theme.SelectionDivider])
				c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(rk, 2), nil)
			}
		}
		col := r.Colors[theme.Label]
		col2 := r.Colors[theme.Label2]
		if sel && !k.Inactive() {
			col, col2 = r.Colors[theme.OnSelection], r.Colors[theme.OnSelection]
		}
		x := cols[0] * s
		ic := d.ic.doc
		if f.folder {
			ic = d.ic.folder
		}
		if fl, disp := k.Icons().Prepare(c, ic, r.Colors[theme.Accent]|0xFF); fl != 0 && !sel {
			iw, ih := k.IconSize(ic)
			theme.At(c, theme.Snap(x+(16*s-iw)/2), theme.Snap((rowH-ih)/2), iw, ih)
			c.BoxKeyed(fl|base.Floating, ui.KeyN(rk, 3), disp)
		} else if fl != 0 {
			fl, disp = k.Icons().Prepare(c, ic, col)
			iw, ih := k.IconSize(ic)
			theme.At(c, theme.Snap(x+(16*s-iw)/2), theme.Snap((rowH-ih)/2), iw, ih)
			c.BoxKeyed(fl|base.Floating, ui.KeyN(rk, 3), disp)
		}
		texts := [4]string{f.name, f.date, f.size, f.kind}
		tx := x + 19*s
		for j, t := range texts {
			var cw float32
			switch j {
			case 0:
				cw = 239 * s
			case 3:
				cw = max(w-inset-tx-8*s, 0)
			default:
				cw = (cols[j+1] - 17) * s
			}
			cc := col
			if j > 0 {
				cc = col2
			}
			theme.At(c, theme.Snap(tx), theme.Snap((rowH-body.Line)/2), cw, body.Line)
			c.NextFont(body.Font)
			c.NextTextColor(cc)
			c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|base.Floating, ui.KeyN(rk, 10+uint64(j)), base.StrBytes(t))
			if j == 0 {
				tx = x - 16*s + cols[1]*s + 16*s
			} else if j < 3 {
				tx += cols[j+1] * s
			}
		}
		c.Pop()
	}
	k.CloseScrollContent(&v)
	k.CloseScroll(&v)

	// the path bar at the bottom
	theme.At(c, 0, h-pathH, w, pathH)
	c.NextBG(r.Colors[theme.Window])
	c.Push(c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 4), nil))
	theme.At(c, 0, 0, w, r.Lines.Hairline)
	c.NextBG(r.Colors[theme.Separator])
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 5), nil)
	theme.At(c, theme.Snap(8*s), theme.Snap(2*s), w-theme.Snap(16*s), pathH-theme.Snap(4*s))
	c.Push(c.BoxKeyed(base.Floating, ui.KeyN(key, 6), nil))
	if p := rexui.PathBar(k, "Path", d.path, rexui.PathBarOpts{Size: theme.SizeSmall}); p.Index >= 0 && p.Index < len(d.path)-1 {
		d.path = d.path[:p.Index+1]
	}
	c.Pop()
	c.Pop()
}

var (
	settingsCards  = [...]string{"appearance", "scrolling"}
	settingsGroups = [...][]string{{"Appearance", "Accent colour", "Highlight colour", "Sidebar icon size"}, {"Show scroll bars", "Click in the scroll bar to"}}
	// settingsOptions are the rows' choices, in the groups' order.
	settingsOptions = [...][]string{{"Auto", "Light", "Dark"}, {"Multicolour", "Blue", "Pink"}, {"Accent colour", "Blue", "Pink"}, {"Small", "Medium", "Large"}, {"Automatically", "When scrolling", "Always"}, {"Next page", "Spot clicked"}}
)

// fileHeader is the file list's header, pinned under the toolbar at x in a
// pane w wide: it lies in the toolbar's scroll-edge pocket (the toolbar draws
// the bar under it), so it is built after the toolbar.
func (d *Demo) fileHeader(x, tbH, w float32) {
	k := d.k
	c, r := k.Context(), k.Theme()
	s := r.Scale
	key := ui.HashString(0, "files header")
	hdrH := theme.Snap(fileHeaderH * s)
	cols := &fileCols
	theme.At(c, x, tbH, w, hdrH)
	c.Push(c.BoxKeyed(base.Floating, key, nil))
	sub := &r.Styles[theme.StyleSubheadline]
	for j, t := range [4]string{"Name", "Date Modified", "Size", "Kind"} {
		cx := float32(0)
		for q := 0; q <= j; q++ {
			cx += cols[q]
		}
		if j > 0 {
			cx += 3
		}
		theme.At(c, theme.Snap(cx*s), theme.Snap((hdrH-sub.Line)/2), 200*s, sub.Line)
		c.NextFont(sub.Font)
		c.NextTextColor(r.Colors[theme.HeaderText])
		c.BoxKeyed(ui.DrawText|base.Floating, ui.KeyN(key, 20+uint64(j)), base.StrBytes(t))
	}
	lw := theme.Snap(s)
	theme.At(c, 0, hdrH-lw, w, lw)
	c.NextBG(r.Colors[theme.Separator])
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 3), nil)
	c.Pop()
}

// settingsForm is grouped boxes of controls.
func (d *Demo) settingsForm(tbH, w, h float32) {
	k := d.k
	c := k.Context()
	theme.At(c, 0, tbH, w, h-tbH)
	c.Push(c.Box(base.Floating, "form"))
	k.NextPadding(20, 16, 20, 16)
	k.NextGap(20)
	k.Column("groups")
	oi := 0
	for gi, g := range settingsGroups {
		cd := rexui.BeginCard(k, settingsCards[gi], rexui.CardOpts{Variant: rexui.CardInset})
		for _, label := range g {
			cd.Row(k, label)
			rexui.Label(k, label, rexui.LabelOpts{})
			k.Spacer()
			rexui.Select(k, label+"###sel", &d.settings[oi], settingsOptions[oi], rexui.SelectOpts{})
			oi++
			cd.EndRow(k)
		}
		rexui.EndCard(k, &cd)
	}
	k.EndColumn()
	c.Pop()
}

// notesEditor is the open note.
func (d *Demo) notesEditor(tbH, w, h float32) {
	k := d.k
	c, r := k.Context(), k.Theme()
	s := r.Scale
	theme.At(c, theme.Snap(24*s), tbH+theme.Snap(16*s), w-theme.Snap(48*s), h-tbH-theme.Snap(32*s))
	c.Push(c.Box(base.Floating, "editor"))
	st := &r.Styles[theme.StyleBody]
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextFont(st.Font)
	c.NextTextColor(r.Colors[theme.Text])
	c.BoxKeyed(ui.DrawText|ui.WrapText|ui.Clip, ui.HashString(0, "note"), base.StrBytes(notesText))
	c.Pop()
}

// toolbarTabs builds the toolbar's tabs in the box (the current parent).
func (d *Demo) toolbarTabs(box ui.ID) {
	k := d.k
	c, r := k.Context(), k.Theme()
	br := c.Rect(box)
	rc := &rexui.TabsRecipes[rexui.TabsNeutral]
	hh := (rc.Height + 2*rc.Inset[1]) * r.Scale
	ev := rexui.Tabs(k, "Window Tabs", &d.bar, d.tabs, &d.tabSel, rexui.TabsOpts{Variant: rexui.TabsNeutral, X0: 0, X1: br[2] - br[0], Y: theme.Snap((br[3] - br[1] - hh) / 2)})
	d.apply(&d.tabs, &d.tabSel, ev)
}

// tabsContent is a pane with Rex tabs in content, in the Rex look.
func (d *Demo) tabsContent(tbH, w, h float32) {
	k := d.k
	c, r := k.Context(), k.Theme()
	s := r.Scale
	pad := theme.Snap(24 * s)
	ev := rexui.Tabs(k, "Sections", &d.inBar, d.inTabs, &d.inSel, rexui.TabsOpts{X0: pad, X1: w - pad, Y: tbH + theme.Snap(16*s), Focusable: true, NoDrag: true})
	d.apply(&d.inTabs, &d.inSel, ev)
	st := &r.Styles[theme.StyleTitle2]
	theme.At(c, pad, tbH+theme.Snap(80*s), w-2*pad, st.Line)
	c.NextFont(st.Font)
	c.NextTextColor(r.Colors[theme.Label])
	title := ""
	if d.tabSel >= 0 && d.tabSel < len(d.tabs) {
		title = d.tabs[d.tabSel].Title
	}
	c.BoxKeyed(ui.DrawText|base.Floating, ui.HashString(0, "tab title"), base.StrBytes(title))
	if d.inSel >= 0 && d.inSel < len(d.inTabs) {
		sub := &r.Styles[theme.StyleBody]
		theme.At(c, pad, tbH+theme.Snap(110*s), w-2*pad, sub.Line)
		c.NextFont(sub.Font)
		c.NextTextColor(r.Colors[theme.Label2])
		c.BoxKeyed(ui.DrawText|base.Floating, ui.HashString(0, "section"), base.StrBytes(d.inTabs[d.inSel].Title))
	}
}

// apply applies a tab bar's close and move to its tabs.
func (d *Demo) apply(tabs *[]rexui.Tab, sel *int, ev rexui.TabsEvents) {
	t := *tabs
	if ev.MoveFrom >= 0 && ev.MoveTo >= 0 && ev.MoveFrom < len(t) && ev.MoveTo < len(t) {
		m := t[ev.MoveFrom]
		if ev.MoveFrom < ev.MoveTo {
			copy(t[ev.MoveFrom:ev.MoveTo], t[ev.MoveFrom+1:ev.MoveTo+1])
		} else {
			copy(t[ev.MoveTo+1:ev.MoveFrom+1], t[ev.MoveTo:ev.MoveFrom])
		}
		t[ev.MoveTo] = m
	}
	if ev.Close >= 0 && ev.Close < len(t) && len(t) > 1 {
		copy(t[ev.Close:], t[ev.Close+1:])
		t = t[:len(t)-1]
		if *sel >= len(t) || *sel > ev.Close {
			*sel = max(*sel-1, 0)
		}
	}
	*tabs = t
}
