// Package notes is the example application of the guide (docs/GUIDE.md): a
// small list of notes laid out as macOS 27's Notes window (docs/design/
// liquid-glass-reference.md): a glass sidebar listing the notes under a
// compact toolbar with search, the open note beside it, and a bar to add one.
// It has a Switch for the order, a menu with shortcuts and a confirmation that
// is the system's NSAlert where there is one.
//
// It is the Builder the notes command runs in a window and in the benchmark,
// and that the tests drive offscreen. Everything the window remembers lives in
// the Notes value; a frame allocates nothing once it is warm.
package notes

import (
	"math"
	"strconv"
	"time"
	"unsafe"

	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"
)

// Limits. The notes live in an array, so adding one never allocates; when it
// is full, Add is disabled.
const (
	maxNotes = 64
	maxLen   = 80   // bytes of a note's title
	maxBody  = 2048 // bytes of its text
	maxLine  = 120  // bytes of the second line the list shows
)

// The groups a note belongs to, newest first, as Notes lists them.
const (
	groupToday = iota
	groupWeek
	groupMonth
	numGroups
)

var groupTitles = [numGroups]string{"Today", "Previous 7 Days", "Previous 30 Days"}

// note is one note: pointer-free, so the array of them is too.
type note struct {
	id     uint32 // never 0, and never reused: the list's selection follows it
	group  uint8
	tn, dn uint8  // bytes of the title and of the list's second line
	bn     uint16 // bytes of the text
	title  [maxLen]byte
	detail [maxLine]byte // the date and the start of the text, as the list shows them
	body   [maxBody]byte // the text; a line starting with "## " is a heading
}

func (n *note) titleText() []byte { return n.title[:n.tn] }
func (n *note) bodyText() []byte  { return n.body[:n.bn] }

// str views b as a string without copying: the notes live in an array of the
// app, so a view stays valid until the note changes, and the list is rebuilt
// every frame.
func str(b []byte) string { return unsafe.String(unsafe.SliceData(b), len(b)) }

// The commands of the menus.
const (
	cmdNew menu.ID = iota + 1
	cmdRemove
	cmdNewest
	cmdZoomIn
	cmdZoomOut
	cmdActualSize
)

// The text of the open note: its title and body as the text view shows them,
// the spans that set the title and the headings.
const (
	styleBody, styleTitle, styleHeading = 0, 1, 2
	maxSpans                            = 64
)

// The window, in points: the Notes scene's size and the width of its sidebar
// (tools/appkitscenes), and the height of the bar that adds a note.
const (
	windowW, windowH = 900, 600
	sidebarW         = 240
	addBarH          = 52
)

// Notes is the application: its notes, the state of its widgets and the kit
// that builds them. It implements app.Builder and app.Initer.
type Notes struct {
	// Traffic makes the window draw its traffic lights itself (offscreen
	// renders; in a window the system draws them).
	Traffic bool
	// Mode is the mode the window shows: the system's (the zero value), or
	// light or dark whatever the system says.
	Mode theme.Preference
	// Scrolled starts the open note scrolled to its end, as far as the Notes
	// scene's text view scrolls (114 pt with legacy scrollers, 98 with overlay
	// ones), under the toolbar (pictures).
	Scrolled bool

	a     *app.App
	k     *widget.Kit
	model *menu.Model
	ic    struct{ sidebar, trash, compose icon.ID }

	notes  [maxNotes]note
	count  int
	nextID uint32
	items  [maxNotes + 2*numGroups]rexui.SidebarItem // the sidebar's rows, rebuilt every frame
	rows   []rexui.SidebarItem                       // the part of items in use

	side     rexui.SidebarState // the sidebar's selection (a note's id) and scroll
	query    ui.TextBuffer      // the toolbar's search
	input    ui.TextBuffer      // the text field that adds a note
	field    ui.Key             // the text field's key, to focus it
	editor   base.ScrollState
	text     []byte    // the open note as the text view shows it: the title, then the text
	spans    []ui.Span // the title and the headings in it
	textID   uint32    // the note text is of
	styles   ui.RichStyles
	stylesAt float32 // the scale the styles are made for
	under    bool    // the editor is scrolled under the toolbar (the last frame's)
	focused  bool    // the first focus is given
	newest   bool    // the Switch: show the newest note first
	hidden   bool    // the sidebar is collapsed
	toTop    bool    // the open note changed: scroll it to its top
	pinned   float64 // where Scrolled put the note

	confirm   bool                  // the drawn dialog is open
	ticket    platform.DialogTicket // the system alert asked for, not answered yet
	drawnOnly bool                  // never ask the system: the drawn dialog (the benchmark's)
}

var (
	_ app.Builder = (*Notes)(nil)
	_ app.Initer  = (*Notes)(nil)
)

// Config is the window: the Notes scene's 900×600 points with a compact
// toolbar over the system's materials.
func Config() app.Config {
	cfg := app.Config{Title: "Notes", Width: windowW, Height: windowH, MinWidth: 720, MinHeight: 360, Clear: [4]float32{1, 1, 1, 1}, Materials: true}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(theme.ToolbarCompact)
	return cfg
}

// seeds are the notes at launch, oldest first, as the Notes scene lists them.
var seeds = [...]struct {
	group                   uint8
	title, when, line, body string
}{
	{groupMonth, "Quotes", "21/09/2026", "Simplicity is a great virtue", ""},
	{groupMonth, "Garden", "25/09/2026", "Plant the bulbs before the first frost", ""},
	{groupMonth, "Podcast list", "28/09/2026", "Design, history and science", ""},
	{groupMonth, "Workout plan", "02/10/2026", "Mon: run 5 km, Wed: strength", ""},
	{groupWeek, "Gift ideas", "Monday", "A good notebook, a plant", ""},
	{groupWeek, "Recipe: lentil soup", "Tuesday", "Onion, carrot, celery, red lentils", ""},
	{groupWeek, "Book notes", "Thursday", "Chapter 3: the habit loop", ""},
	{groupWeek, "Trip ideas", "Friday", "Lisbon in spring, the coast road", ""},
	{groupToday, "Grocery list", "08:15", "Oat milk, lemons, rice, coffee beans", ""},
	{groupToday, "Launch meeting", "09:41", "Agenda: review the launch checklist", launchBody},
}

// launchBody is the text of the Launch meeting note: the Notes scene's.
const launchBody = "## Agenda\n" +
	"• Review the launch checklist and owners\n" +
	"• Confirm the design freeze date\n" +
	"• Decide on the beta group size\n\n" +
	"## Decisions\n" +
	"The beta opens to the waiting list in two waves of five hundred people each. The second wave starts once the crash rate stays under a tenth of a percent for three days.\n\n" +
	"Pricing stays as proposed. The annual plan keeps its discount, and the monthly plan gets a seven-day trial.\n\n" +
	"## Open questions\n" +
	"Who owns the support inbox during the first week? Do we need a status page before the beta, or is the in-app banner enough?\n\n" +
	"## Action items\n" +
	"Sam drafts the announcement and the FAQ by Thursday. Priya prepares the onboarding flow and the empty states. Jordan checks the analytics events against the funnel we agreed on.\n\n" +
	"## Notes from the design review\n" +
	"The sidebar felt heavy in dark mode. We will try the lighter selection, smaller section headers and more space above each group. The toolbar keeps three groups: formatting, insert and share.\n\n" +
	"The editor should keep a comfortable measure of about seventy characters. Headings use the title style, and the body stays at the system size so it matches the rest of the app.\n\n" +
	"## Next meeting\n" +
	"Thursday at 10:00, same room. Bring the updated timeline and the list of risks.\n\n" +
	"## Risks\n" +
	"The migration of old notes may take longer than planned on large libraries. The sync service needs a load test before the second wave. Translations for three languages are still missing.\n\n" +
	"## Follow-up\n" +
	"Collect feedback from the first wave through the in-app form and summarise it every Friday.\n"

// New returns the application with a few notes in it, the newest open.
func New() (*Notes, error) {
	n := &Notes{newest: true}
	n.input.Max = maxLen
	n.input.Reserve(maxLen)
	n.query.Max = maxLen
	n.query.Reserve(maxLen)
	n.text = make([]byte, 0, maxLen+1+maxBody)
	n.spans = make([]ui.Span, 0, maxSpans)
	for _, s := range seeds {
		n.put(s.group, s.title, s.when, s.line, s.body)
	}
	n.side.Reserve(maxNotes)
	n.side.Select(n.notes[n.count-1].id)
	var err error
	if n.model, err = newModel(); err != nil {
		return nil, err
	}
	return n, nil
}

// newModel builds the commands and menus: a Note menu and a View menu in the
// bar. The View menu's zoom commands (menu.StandardCommand) zoom the window.
func newModel() (*menu.Model, error) {
	primary := func(k ui.KeyCode, m ui.Mods) menu.Shortcut { return menu.Shortcut{Key: k, Mods: m, Primary: true} }
	cmds := []menu.Command{
		{ID: cmdNew, Title: "New Note", Shortcut: primary('N', 0)},
		{ID: cmdRemove, Title: "Remove…", Shortcut: primary(ui.KeyBackspace, 0)},
		{ID: cmdNewest, Title: "Newest First", Shortcut: primary('N', ui.ModShift)},
		menu.StandardCommand(cmdZoomIn, menu.RoleZoomIn),
		menu.StandardCommand(cmdZoomOut, menu.RoleZoomOut),
		menu.StandardCommand(cmdActualSize, menu.RoleActualSize),
	}
	item := func(id menu.ID) menu.Item { return menu.Item{Kind: menu.ItemCommand, Command: id} }
	menus := []menu.Menu{
		{Title: "Note", Items: []menu.Item{item(cmdNew), item(cmdRemove)}},
		{Title: "View", Items: []menu.Item{item(cmdNewest), {Kind: menu.ItemSeparator}, item(cmdZoomIn), item(cmdZoomOut), item(cmdActualSize)}},
	}
	return menu.NewModel(cmds, menus, []int{0, 1})
}

// Init implements app.Initer: it makes the kit and sizes the caches.
func (n *Notes) Init(a *app.App) error {
	n.a = a
	n.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	n.k.SetPreference(n.Mode)
	n.k.Materials = a.Win != nil && a.Win.HasMaterials() // the system's glass in a window
	n.k.SetMenu(n.model)
	a.SetMenu(n.model) // the native menu bar on macOS
	n.addIcons()
	a.UI.Reserve(2048)
	a.Text.ReserveRuns(text.WorkingSetRuns, text.WorkingSetGlyphs, text.WorkingSetTextBytes)
	return nil
}

// addIcons adds the toolbar's symbols, at the size and scale AppKit's toolbars
// draw them.
func (n *Notes) addIcons() {
	n.ic.sidebar = n.k.AddIcon(rexui.ToolbarSymbol("sidebar.left", '▤'))
	n.ic.trash = n.k.AddIcon(rexui.ToolbarSymbol("trash", '⌫'))
	n.ic.compose = n.k.AddIcon(rexui.ToolbarSymbol("square.and.pencil", '✎'))
}

// Context is the window's ui context, which the tests and the scripted
// session read the semantic tree from (it is nil before Init).
func (n *Notes) Context() *ui.Context {
	if n.k == nil {
		return nil
	}
	return n.k.Context()
}

// Count is the number of notes.
func (n *Notes) Count() int { return n.count }

// Selected is the number of selected notes.
func (n *Notes) Selected() int {
	sel := 0
	for i := 0; i < n.count; i++ {
		if n.side.Has(n.notes[i].id) {
			sel++
		}
	}
	return sel
}

// open is the index of the open note: the selected one when it is alone, -1
// for none (or for several, which have no note open).
func (n *Notes) open() int {
	if n.Selected() != 1 {
		return -1
	}
	return n.find(n.side.Sel)
}

// Text is the title of note i (in the order they were made, oldest first),
// which is valid until the notes change.
func (n *Notes) Text(i int) []byte { return n.notes[i].titleText() }

// find is the index of the note with the id, -1 for none.
func (n *Notes) find(id uint32) int {
	for i := 0; i < n.count; i++ {
		if n.notes[i].id == id {
			return i
		}
	}
	return -1
}

// put appends a note, if there is room, and returns whether it did. when and
// line make the second line the list shows.
func (n *Notes) put(group uint8, title, when, line, body string) bool {
	if n.count == maxNotes {
		return false
	}
	n.nextID++
	nt := &n.notes[n.count]
	*nt = note{id: n.nextID, group: group}
	nt.tn = uint8(copy(nt.title[:], title))
	nt.bn = uint16(copy(nt.body[:], body))
	d := copy(nt.detail[:], when)
	if line != "" {
		d += copy(nt.detail[d:], "  ")
		d += copy(nt.detail[d:], line)
	}
	nt.dn = uint8(d)
	n.count++
	return true
}

// add appends the text of the field as a note, if there is room, and opens it.
func (n *Notes) add() {
	if n.input.Len() == 0 || n.count == maxNotes {
		return
	}
	var clock [8]byte
	n.put(groupToday, n.input.String(), string(time.Now().AppendFormat(clock[:0], "15:04")), "", "")
	n.side.Select(n.notes[n.count-1].id)
	n.input.Reset()
}

// removeSelected removes the selected notes. The notes after one move up,
// which is why a note is told by its id and not by its place.
func (n *Notes) removeSelected() {
	kept := 0
	for i := 0; i < n.count; i++ {
		if !n.side.Has(n.notes[i].id) {
			n.notes[kept] = n.notes[i]
			kept++
		}
	}
	clear(n.notes[kept:n.count])
	n.count = kept
	n.side.Clear()
}

// rebuild sets the sidebar's rows: the notes in the order the Switch asks for
// that the search lets through, each group under its header.
func (n *Notes) rebuild() {
	q := n.query.Bytes()
	rows := n.items[:0]
	group := -1
	for r := 0; r < n.count; r++ {
		i := r
		if n.newest {
			i = n.count - 1 - r
		}
		nt := &n.notes[i]
		if !containsFold(nt.titleText(), q) || len(rows)+2 > len(n.items) {
			continue
		}
		if int(nt.group) != group {
			group = int(nt.group)
			rows = append(rows, rexui.SidebarItem{ID: headerID + uint32(group), Title: groupTitles[group], Header: true})
		}
		rows = append(rows, rexui.SidebarItem{ID: nt.id, Title: str(nt.titleText()), Detail: str(nt.detail[:nt.dn])})
	}
	n.rows = rows
}

// headerID is the id of the first group's header; a note's id is never so large.
const headerID = 1 << 30

// containsFold reports whether s has q in it, ignoring the case of ASCII letters.
func containsFold(s, q []byte) bool {
	for i := 0; i+len(q) <= len(s); i++ {
		j := 0
		for j < len(q) && lower(s[i+j]) == lower(q[j]) {
			j++
		}
		if j == len(q) {
			return true
		}
	}
	return false
}

func lower(b byte) byte {
	if 'A' <= b && b <= 'Z' {
		return b + 'a' - 'A'
	}
	return b
}

// Build builds the frame. It implements app.Builder.
func (n *Notes) Build(c *ui.Context) {
	w, h := c.Size()
	if w <= 0 || h <= 0 || c.Scale() <= 0 {
		return
	}
	k := n.k
	k.Begin(c)
	n.answers()
	n.commands()
	n.rebuild()
	r := k.Theme()
	W, H := float32(w), float32(h)
	tbH := theme.Snap(r.Window.Toolbars[theme.ToolbarCompact].Height)
	sbW := theme.Snap(sidebarW * r.Scale)
	if n.hidden {
		sbW = 0
	}

	// Where the system draws no menu bar, the app's own is the window's first
	// strip, and the panes lie below it (a picture of the window with its
	// traffic lights is of the macOS window, whose menu bar is the screen's).
	barH := float32(0)
	if !c.NativeMenus() && !n.Traffic && n.model != nil {
		barH = theme.Snap(r.Px.ControlH)
		theme.At(c, 0, 0, W, barH)
		c.NextBG(r.Colors[theme.Window])
		c.Push(c.Box(base.Floating|ui.DrawBackground, "menubar"))
		k.MenuBar()
		c.Pop()
	}
	theme.At(c, 0, barH, W, H-barH)
	c.Push(c.Box(base.Floating, "panes"))
	H -= barH

	// The panes' glass, the content running under the toolbar's band (its
	// scroll view is inset by it: ToolbarOpts.ContentEdge), and the toolbar
	// over them.
	if sbW > 0 {
		theme.At(c, 0, 0, sbW, H)
		c.Push(c.Box(base.Floating, "sidebar"))
		n.sidebar(tbH)
		c.Pop()
	}
	theme.At(c, sbW, 0, W-sbW, H)
	c.NextBG(r.Colors[theme.TextBackground])
	c.Push(c.Box(base.Floating|ui.DrawBackground, "content"))
	n.content(W-sbW, H, tbH)
	c.Pop()
	n.toolbar(tbH, sbW)
	c.Pop()

	d := k.BeginDialog("remove", "Remove note?", &n.confirm)
	if d.Visible {
		k.Paragraph(n.removeText(c))
		d.BeginActions()
		d.Action("Cancel", widget.ButtonDefault)
		if d.Action("Remove", widget.ButtonPrimary) {
			n.removeSelected()
		}
		d.EndActions()
	}
	k.EndDialog(&d)
	k.End()
}

// removeText says what Remove removes: the note, by its title, or how many.
func (n *Notes) removeText(c *ui.Context) string {
	if sel := n.Selected(); sel > 1 {
		return c.Fmt().Int(sel).Str(" notes will be removed. This cannot be undone.").String()
	}
	return c.Fmt().Str("“").Str(str(n.selectedTitle())).Str("” will be removed. This cannot be undone.").String()
}

// selectedTitle is the open note's title, empty for none.
func (n *Notes) selectedTitle() []byte {
	if i := n.open(); i >= 0 {
		return n.notes[i].titleText()
	}
	return nil
}

var keyText = ui.HashString(0, "note text")

// setText makes the text of the open note nt: its title, then its text with
// the heading markers taken out, and the spans of the title and the
// headings. It runs when the open note changes, which also takes the scroll
// view to the top.
func (n *Notes) setText(nt *note) {
	r := n.k.Theme()
	if n.stylesAt != r.Scale {
		n.makeStyles(r.Scale)
	}
	if n.textID == nt.id {
		return
	}
	n.textID = nt.id
	n.text = append(n.text[:0], nt.titleText()...)
	n.spans = append(n.spans[:0], ui.Span{Off: 0, Len: uint32(len(n.text) + 1), Style: styleTitle})
	if nt.bn > 0 {
		n.text = append(n.text, '\n')
	}
	rest := nt.bodyText()
	for len(rest) > 0 {
		line := rest
		for i, b := range rest {
			if b == '\n' {
				line = rest[:i+1]
				break
			}
		}
		rest = rest[len(line):]
		if len(line) > 3 && line[0] == '#' && line[1] == '#' && line[2] == ' ' && len(n.spans) < cap(n.spans) {
			line = line[3:]
			n.spans = append(n.spans, ui.Span{Off: uint32(len(n.text)), Len: uint32(len(line)), Style: styleHeading})
		}
		n.text = append(n.text, line...)
	}
	n.toTop = true
}

// makeStyles makes the text view's fonts at scale s: the body in 13 pt, the
// title in Title 1 (22 pt) and the headings in 15-pt semibold, as the scene's
// text view sets them.
func (n *Notes) makeStyles(s float32) {
	n.stylesAt = s
	ts := n.k.TextSystem()
	f := func(pt float32, wt int) text.FontKey { return ts.Font(text.FontDesc{SizePt: pt, Weight: wt, Scale: s}) }
	n.styles = ui.RichStyles{}
	n.styles.Set(styleBody, ui.RichStyle{Font: f(13, theme.WeightRegular)})
	n.styles.Set(styleTitle, ui.RichStyle{Font: f(22, theme.WeightRegular)})
	n.styles.Set(styleHeading, ui.RichStyle{Font: f(15, theme.WeightSemibold)})
}

// sidebar builds the sidebar pane (the current parent): the system's glass
// with the notes as two-line rows, the selection a rounded block that is the
// accent while the list has the keyboard.
func (n *Notes) sidebar(tbH float32) {
	k := n.k
	c := k.Context()
	s := rexui.Sidebar(k.Kit, "Notes", &n.side, n.rows, rexui.SidebarOpts{Top: tbH, RowH: 52, HeaderH: 24, Multiple: true})
	if !n.focused { // the keyboard starts in the list, as in the Notes scene
		c.SetFocus(c.KeyOf(s.ID))
		n.focused = true
	}
}

// placeholder is what the content pane says when no one note is open.
func (n *Notes) placeholder(c *ui.Context) string {
	if sel := n.Selected(); sel > 1 {
		return c.Fmt().Int(sel).Str(" Notes Selected").String()
	}
	return "No Note Selected"
}

// The editor's text inset, in points: the text view's container inset (24,
// 12) and its line fragment padding (5).
const editorInsetX, editorInsetY = 24 + 5, 12

// content builds the content pane (the current parent) of w by h pixels, which
// runs under the toolbar's band of top pixels: the open note in a scroll view
// inset by the band, and below it the bar that adds a note.
func (n *Notes) content(w, h, top float32) {
	k := n.k
	c, r := k.Context(), k.Theme()
	s := r.Scale
	barH := theme.Snap(addBarH * s)

	k.NextSize(ui.Px(w), ui.Px(h-barH))
	k.NextScrollInset(top)
	v := k.BeginScroll("Note", &n.editor, base.ScrollVertical)
	if n.toTop {
		c.SetScroll(v.Inner, 0, 0)
		n.toTop = false
	}
	_, sy := c.Scroll(v.Inner)
	n.under = sy > 0.5
	px, py := theme.Snap(editorInsetX*s), theme.Snap(editorInsetY*s)
	c.NextSize(ui.Pct(1), ui.Children(1))
	// the text ends in a newline: NSTextView lays out the empty line after it
	// (its extra line fragment), a body line more below the text
	c.NextPadding(px, py, px, py+r.Styles[theme.StyleBody].Line)
	page := c.Box(0, "page")
	if n.Scrolled && n.textID != 0 { // keep the end of the note, as the Notes scene's picture has it
		if pr := c.Rect(page); pr[3] > pr[1] {
			end := float64(max(top+pr[3]-pr[1]-h, 0))
			switch {
			case n.pinned != 0 && math.Abs(sy-n.pinned) > 0.5: // somebody scrolled it
				n.Scrolled = false
			case math.Abs(sy-end) > 0.5: // the layout changed (the scroll bars' style)
				c.SetScroll(v.Inner, 0, end)
				n.pinned = end
				c.RequestFrame()
			}
		}
	}
	c.Push(page)
	if i := n.open(); i >= 0 {
		n.setText(&n.notes[i])
		c.NextSize(ui.Pct(1), ui.TextSize(0))
		c.NextTextColor(r.Colors[theme.Text])
		c.RichText(ui.KeyN(keyText, uint64(n.textID)), n.text, n.spans, &n.styles, ui.RichOpts{})
	} else {
		n.textID = 0
		rexui.Label(k.Kit, n.placeholder(c), rexui.LabelOpts{Style: rexui.TextTitle1, Color: rexui.Of(theme.Label3)})
	}
	c.Pop()
	k.CloseScrollContent(&v)
	rexui.ScrollBars(k.Kit, &v)
	k.CloseScroll(&v)

	// the bar: the line above it, the order, the field and Add, the count
	theme.At(c, 0, h-barH, w, r.Lines.Hairline)
	c.NextBG(r.Colors[theme.Separator])
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.HashString(c.ParentKey(), "bar line"), nil)
	theme.At(c, 0, h-barH, w, barH)
	k.NextPadding(16, 0, 16, 0)
	k.NextGap(12)
	k.BeginContainer(ui.AxisX, ui.CrossCenter, "add", ui.Px(w), ui.Px(barH), base.Floating)
	if k.Switch("Newest first", &n.newest).Changed {
		n.rebuild()
	}
	k.NextWidth(widget.Fill)
	f := k.TextField("New note", &n.input, "Add a note…")
	n.field = f.Key
	k.BeginDisabled(n.input.Len() == 0 || n.count == maxNotes)
	added := k.ButtonKind("Add", widget.ButtonPrimary).Clicked
	k.EndDisabled()
	k.LabelBytesStyle(c.Fmt().Int(n.count).Str(" notes").Bytes(), widget.TextMuted)
	k.EndContainer()
	if added || f.Submitted {
		n.add()
		k.Focus(n.field)
	}
}

// toolbar builds the compact toolbar over the panes: the sidebar's toggle and
// Remove in the sidebar's part, the title, New Note and the search in the
// content's.
func (n *Notes) toolbar(tbH, sbW float32) {
	k := n.k
	r := k.Theme()
	o := rexui.ToolbarOpts{Style: theme.ToolbarCompact, Title: "Notes", Panes: [2]float32{sbW, 0},
		ContentUnder: n.under, Traffic: n.Traffic, BandFill: r.Colors[theme.TextBackground], ContentEdge: true}
	tb := rexui.BeginToolbar(k.Kit, "Toolbar", o)
	if tb.Item(n.ic.sidebar, "Sidebar").Clicked {
		n.hidden = !n.hidden
	}
	k.BeginDisabled(n.Selected() == 0)
	remove := tb.Item(n.ic.trash, "Remove…").Clicked
	k.EndDisabled()
	if sbW > 0 {
		tb.NextPane()
	}
	tb.Title()
	compose := tb.Item(n.ic.compose, "New Note").Clicked
	tb.Flexible()
	tb.Search("Search", &n.query, "Search", 0)
	tb.End()
	if remove {
		n.askRemove()
	}
	if compose {
		k.Focus(n.field)
	}
}

// askRemove asks to remove the selected notes: with the system's alert, which
// answers in a later frame (answers), and where there is none (or it cannot
// be shown) with the drawn dialog.
func (n *Notes) askRemove() {
	if n.Selected() == 0 || n.ticket != 0 || n.confirm {
		return
	}
	if !n.drawnOnly {
		msg, info := "Remove “"+string(n.selectedTitle())+"”?", "The note will be removed. This cannot be undone."
		if sel := n.Selected(); sel > 1 {
			msg, info = "Remove "+strconv.Itoa(sel)+" notes?", "The notes will be removed. This cannot be undone."
		}
		t, err := n.a.Alert(platform.AlertSpec{
			Style:       platform.AlertWarning,
			Message:     msg,
			Info:        info,
			Buttons:     []string{"Remove", "Cancel"},
			Destructive: []int{0},
		})
		if err == nil {
			n.ticket = t
			return
		}
	}
	n.confirm = true
}

// answers reads what the system's alert was answered, at the start of a frame.
func (n *Notes) answers() {
	for r, ok := n.a.TakeDialog(); ok; r, ok = n.a.TakeDialog() {
		if r.Ticket != n.ticket {
			continue
		}
		n.ticket = 0
		switch {
		case r.Err != nil: // it could not be shown: ask with the drawn one
			n.confirm = true
		case r.Outcome == platform.DialogAnswered && r.Button == 0:
			n.removeSelected()
		}
	}
}

// commands applies the menu commands of the frame and sets their state.
func (n *Notes) commands() {
	k := n.k
	if k.Command(cmdNew) {
		k.Focus(n.field)
	}
	if k.Command(cmdNewest) {
		n.newest = !n.newest
	}
	if k.Command(cmdRemove) {
		n.askRemove()
	}
	n.model.SetEnabled(cmdRemove, n.Selected() > 0)
	n.model.SetChecked(cmdNewest, n.newest)
}
