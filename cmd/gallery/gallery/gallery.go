// Package gallery is the widget gallery: rexui's components in every state,
// the AppKit catalogue pages, and a table of a million rows, in a window laid
// out as System Settings is on macOS 27.2: a full-height glass sidebar with a
// search field and the scenes in grouped sections on colour tiles, a unified
// toolbar with back and forward and the scene's title, and the scene in the
// detail pane, in grouped form sections where it fits. It is the Builder the
// gallery command runs in a window, in the benchmark and offscreen.
//
// The scenes (scenes.go) are Controls (text, entry, choices, buttons, tabs
// and a list to try), States (each control normal, hovered, pressed, focused
// and disabled), Table (a million rows, in the toolbar's pocket as Finder's
// list is), Appearance, Dialogs (a native alert and file panels, and the
// drawn sheet), and the pages of rexui's AppKit catalogue, whose content is
// laid out exactly as it is in the standalone appkit scenes.
//
// The window follows the system's light or dark mode and accent colour; the
// Appearance scene's pop-up or ⇧⌘D (the View menu's Cycle Appearance, which
// goes System, Light, Dark and round again) changes that, and a change
// crossfades.
package gallery

import (
	"strings"

	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/rexui/catalog"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"
)

// Window geometry, in points.
const (
	WindowWidthPt  = 1100
	WindowHeightPt = 720
	MinWidthPt     = 770
	MinHeightPt    = 480
)

// sidebarPt is the sidebar's width: System Settings' (the settings scene's
// 215).
const sidebarPt = 215

// DefaultRows is the number of rows of the table unless Options.Rows says
// otherwise.
const DefaultRows = 1_000_000

// listItems is the number of items of the small list.
const listItems = 40

// Config is the gallery's window: a unified toolbar's titlebar and traffic
// lights (rexui.TitlebarFor), and the system's materials under the glass.
func Config() app.Config {
	cfg := app.Config{
		Title: "Widget gallery", Width: WindowWidthPt, Height: WindowHeightPt,
		MinWidth: MinWidthPt, MinHeight: MinHeightPt,
		Clear: [4]float32{1, 1, 1, 1}, Materials: true,
	}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(theme.ToolbarUnified)
	return cfg
}

// Options configure New.
type Options struct {
	// Rows is the number of rows of the table (DefaultRows when zero).
	Rows int
	// Preference is the mode the window shows: the system's (the zero value),
	// or a forced light or dark.
	Preference theme.Preference
	// Traffic makes the toolbar draw the macOS traffic lights (offscreen
	// renders; in a window the system draws them).
	Traffic bool
	// Still makes the scene hold still for screenshots: the indeterminate
	// progress bar does not sweep (Kit.ReduceMotion) and is shown from the
	// start.
	Still bool
	// Synthetic makes the scripted session's clock the frame count at 60 Hz,
	// for runs without a clock (offscreen); a window uses the platform's.
	Synthetic bool
	// Legacy pins the system's scroller style to legacy (a window follows
	// the system's; offscreen it is overlay otherwise).
	Legacy bool
	// Start is the scene to open on, by its title in the sidebar ("Table",
	// "Lists"; the Playground when empty or unknown).
	Start string
	// Scrolled starts the content scrolled under the toolbar (120 pt, or as
	// far as it goes; a table 5 rows).
	Scrolled bool
	// PanelStart is the directory the open and save panels start in (empty:
	// the system's choice).
	PanelStart string
}

// The ids of the table's columns.
const (
	colID uint32 = iota + 1
	colName
	colKind
	colSize
)

var kinds = [...]string{"Document", "Image", "Archive", "Folder", "Audio"}

// histMax is how many scenes back and forward can reach.
const histMax = 32

// Gallery is the gallery scene. It implements app.Builder and app.Initer; its
// Bench wrapper adds the scripted session.
type Gallery struct {
	opt   Options
	wk    *widget.Kit
	k     *base.Kit
	c     *ui.Context
	a     *app.App // nil without a window (tests, offscreen)
	model *menu.Model
	pref  theme.Preference

	// the shell
	side     rexui.SidebarState
	items    []rexui.SidebarItem
	query    ui.TextBuffer // the sidebar's search
	hist     [histMax]uint32
	histN    int // entries, the last being the newest
	histAt   int // the one shown
	focused  bool
	under    bool    // the content is under the toolbar (the last frame's)
	top      float32 // the toolbar band's height this frame, in pixels: the inset of the scene's scroll view
	scrolled int     // frames left to apply Options.Scrolled
	scroll   [numScenes]base.ScrollState
	ic       icons
	glyphs   rexui.ListGlyphs
	st       *catalog.State // the catalogue pages' state
	pages    [len(catalog.Pages)]struct {
		cells []catalog.Cell
		w, h  float32
	}

	// the table
	rows     int
	table    base.TableState
	cols     [4]base.Column
	rev      []uint32 // the rows in descending order, for the descending sorts
	search   ui.TextBuffer
	status   []byte
	line     []byte // the table's status line
	rowsText string // the row count with thousands separators

	// widgets with state
	notes     ui.TextBuffer
	filter    ui.TextBuffer
	check     bool
	tri       widget.CheckState
	sw        bool
	radio     int
	slider    float32
	sel       int
	tab       int
	list      base.ListState
	askDelete bool
	// which list had the keyboard in the last frame, for Select All
	tableFocus, listFocus bool
	working               bool
	alert                 platform.DialogTicket // the alert, save or open panel on screen (0: none)

	m matrix // the values of the state matrix

	// the window of the last frame, for the benchmark's report
	winW, winH int
	winScale   float32
}

// icons are the symbols the gallery adds to the kit.
type icons struct {
	back, forward, appearance icon.ID
	tiles                     [numScenes]icon.ID
	file                      icon.ID
}

// Kit is the widget kit; the alias keeps the gallery's signatures short.
type Kit = widget.Kit

// New returns a Gallery.
func New(o Options) (*Gallery, error) {
	if o.Rows <= 0 {
		o.Rows = DefaultRows
	}
	m, err := newModel()
	if err != nil {
		return nil, err
	}
	g := &Gallery{opt: o, model: m, pref: o.Preference, rows: o.Rows}
	g.cols = [4]base.Column{
		{ID: colID, Title: "ID", Width: 64, Align: ui.AlignRight, Sortable: true},
		{ID: colName, Title: "Name", Width: 190, Sortable: true},
		{ID: colKind, Title: "Kind", Width: 120},
		{ID: colSize, Title: "Size", Width: 80, Align: ui.AlignRight, Fill: true},
	}
	g.table.Sel.Reserve(g.rows)
	g.table.Sort = base.SortState{Active: true, Column: colID}
	g.rev = make([]uint32, g.rows)
	for i := range g.rev {
		g.rev[i] = uint32(g.rows - 1 - i)
	}
	g.slider, g.working = 40, o.Still
	g.check, g.sw = true, false
	g.notes.SetString("Hello, gallery")
	g.query.Reserve(64)
	g.search.Reserve(64)
	g.status = make([]byte, 0, 96)
	g.line = make([]byte, 0, 128)
	g.rowsText = rowsText(o.Rows)
	g.items = make([]rexui.SidebarItem, 0, numScenes+numGroups)
	for i := range g.m.text {
		g.m.text[i].SetString("Field")
		g.m.slide[i] = 40
		g.m.chk[i], g.m.sw[i] = true, true
		g.m.tri[i] = widget.Mixed
	}
	g.side.Sel = sceneID(scPlayground)
	for sc := range defs {
		if strings.EqualFold(defs[sc].title, o.Start) {
			g.side.Sel = sceneID(sc)
		}
	}
	if o.Scrolled {
		g.scrolled = 4
	}
	g.hist[0], g.histN = g.side.Sel, 1
	g.setStatus("Ready")
	return g, nil
}

var _ app.Builder = (*Gallery)(nil)
var _ app.Initer = (*Gallery)(nil)

// sceneID is the sidebar row id of scene s.
func sceneID(s int) uint32 { return uint32(s) + 1 }

// Scene is the scene shown, a row of the sidebar.
func (g *Gallery) scene() int {
	if s := int(g.side.Sel) - 1; s >= 0 && s < numScenes {
		return s
	}
	return scPlayground
}

// Init implements app.Initer: it makes the kit and sizes the caches for the
// scene.
func (g *Gallery) Init(a *app.App) error {
	g.a = a
	g.Attach(a.UI, a.Text)
	g.k.Materials = a.Win != nil && a.Win.HasMaterials()
	a.SetMenu(g.model) // the native menu bar on macOS
	a.UI.Reserve(8192)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}

// Attach makes the kit of the gallery's window on c with the text system ts.
// Init calls it; tests call it with a fake text system.
func (g *Gallery) Attach(c *ui.Context, ts widget.TextSystem) {
	g.c = c
	g.wk = widget.NewKit(c, ts, rex.Theme())
	g.k = g.wk.Kit
	g.wk.SetMenu(g.model)
	g.wk.SetPreference(g.pref)
	g.k.ReduceMotion = g.opt.Still
	if g.opt.Legacy {
		g.k.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: theme.ScrollerLegacy}})
	}
	g.addIcons()
	g.st = catalog.NewState(g.k)
	for p := range g.pages {
		g.pages[p].cells, g.pages[p].w, g.pages[p].h = catalog.Grid(catalog.Page(p))
	}
}

func (g *Gallery) addIcons() {
	k := g.k
	pt := theme.MacWindow.Capsule.Glyph // AppKit's toolbar symbols
	g.ic.back = k.AddIcon(icon.Spec{Symbol: "chevron.left", Fallback: '‹', Pt: pt, Weight: 0.23})
	g.ic.forward = k.AddIcon(icon.Spec{Symbol: "chevron.right", Fallback: '›', Pt: pt, Weight: 0.23})
	for i := range g.ic.tiles {
		g.ic.tiles[i] = k.AddIcon(rexui.SidebarTileSymbol(defs[i].symbol, '■'))
	}
	g.glyphs = rexui.AddListGlyphs(k)
}

// WindowPx is the size in pixels and the scale of the window as of the last
// frame.
func (g *Gallery) WindowPx() (w, h int, scale float32) { return g.winW, g.winH, g.winScale }

// Context is the window's ui context (nil before Init or Attach); it makes the
// gallery a widgettest.Host.
func (g *Gallery) Context() *ui.Context { return g.c }

// Kit is the gallery's widget kit (nil before Init or Attach).
func (g *Gallery) Kit() *Kit { return g.wk }

// Mode is the mode the gallery shows (during a crossfade, the one it fades to).
func (g *Gallery) Mode() theme.Mode {
	if g.k == nil {
		return theme.Light
	}
	return g.k.Mode()
}

// Preference is the mode the gallery has been asked to show.
func (g *Gallery) Preference() theme.Preference { return g.pref }

// SetPreference sets the mode to show; the palette crossfades to it.
func (g *Gallery) SetPreference(p theme.Preference) {
	g.pref = p
	if g.k != nil {
		g.k.SetPreference(p)
	}
}

// setStatus formats the status line.
func (g *Gallery) setStatus(s string) { g.status = append(g.status[:0], s...) }

// contentFill is the detail pane's background: a table's, as the Settings
// scene's.
func (g *Gallery) contentFill() uint32 { return g.k.Theme().Colors[theme.ControlBackground] }

// Build builds the frame. It implements app.Builder.
func (g *Gallery) Build(c *ui.Context) {
	w, h := c.Size()
	if w <= 0 || h <= 0 || c.Scale() <= 0 {
		return
	}
	g.winW, g.winH, g.winScale = w, h, c.Scale()
	k := g.k
	g.wk.Begin(c)
	g.commands(c)
	g.dialogResults()
	r := k.Theme()
	W, H := float32(w), float32(h)
	tbH := theme.Snap(r.Window.Toolbars[theme.ToolbarUnified].Height)
	sbW := theme.Snap(sidebarPt * r.Scale)
	sc := g.scene()

	// The sidebar's glass, then the content pane, which runs under the
	// toolbar's band with its scroll view inset by it (the scroll edge is the
	// view's: ToolbarOpts.ContentEdge), and the toolbar over both.
	theme.At(c, 0, 0, sbW, H)
	c.Push(c.Box(base.Floating, "sidebar"))
	g.sidebar(tbH)
	c.Pop()
	sc = g.scene() // the sidebar may have changed it
	theme.At(c, sbW, 0, W-sbW, H)
	flags := base.Floating
	if sc != scTable { // the table paints its rows' background
		c.NextBG(g.contentFill())
		flags |= ui.DrawBackground
	}
	c.Push(c.Box(flags, "content"))
	g.content(sc, W-sbW, H, tbH)
	c.Pop()
	g.toolbar(sc, sbW)
	g.dialog(c)
	if g.scrolled > 0 {
		g.scrolled--
	}
	g.wk.End()
}

// commands applies the commands of the frame.
func (g *Gallery) commands(c *ui.Context) {
	k := g.wk
	if k.Command(cmdCycleAppearance) {
		g.SetPreference((g.pref + 1) % 3) // System, Light, Dark, System …
	}
	g.model.SetEnabled(cmdBack, g.histAt > 0)
	g.model.SetEnabled(cmdForward, g.histAt+1 < g.histN)
	if k.Command(cmdBack) {
		g.back()
	}
	if k.Command(cmdForward) {
		g.forward()
	}
	g.model.SetEnabled(cmdPrint, false)
	if k.Command(cmdOpen) {
		g.openPanel()
	}
	if k.Command(cmdSave) {
		g.savePanel()
	}
	for _, id := range [...]menu.ID{cmdRecent1, cmdRecent2, cmdRecent3, cmdRowOpen, cmdRowCopyPath, cmdRowInfo} {
		if k.Command(id) {
			g.chose(id)
		}
	}
	for id := cmdUndo; id <= cmdSelectAll; id++ { // edit commands that no text field took
		if k.Command(id) && id == cmdSelectAll { // Select All, which the menu's shortcut takes from the lists
			switch {
			case g.tableFocus:
				g.table.Sel.SelectAll(g.rows)
			case g.listFocus:
				g.list.Sel.SelectAll(listItems)
			}
		}
	}
}

// chose says in the status that command id was chosen.
func (g *Gallery) chose(id menu.ID) {
	cmd, _ := g.model.Command(id)
	g.status = append(g.status[:0], "Chose “"...)
	g.status = append(g.status, cmd.Title...)
	g.status = append(g.status, "”"...)
}

// visit makes scene sel (a sidebar row) the newest of the history, dropping
// what lay ahead of the one shown.
func (g *Gallery) visit(sel uint32) {
	g.histN = g.histAt + 1
	if g.histN == histMax {
		copy(g.hist[:], g.hist[1:])
		g.histN--
	}
	g.hist[g.histN] = sel
	g.histN++
	g.histAt = g.histN - 1
}

func (g *Gallery) back() {
	if g.histAt > 0 {
		g.histAt--
		g.side.Sel = g.hist[g.histAt]
		g.c.RequestFrame()
	}
}

func (g *Gallery) forward() {
	if g.histAt+1 < g.histN {
		g.histAt++
		g.side.Sel = g.hist[g.histAt]
		g.c.RequestFrame()
	}
}

// Go shows scene s (a test's or a script's way of choosing a sidebar row).
func (g *Gallery) Go(s int) {
	if s != g.scene() && s >= 0 && s < numScenes {
		g.side.Sel = sceneID(s)
		g.visit(g.side.Sel)
		if g.c != nil {
			g.c.RequestFrame()
		}
	}
}

// matrix is the values the state matrix's widgets edit: one per state column.
type matrix struct {
	chk, sw [5]bool
	tri     [5]widget.CheckState
	radio   [5]int
	sel     [5]int
	slide   [5]float32
	text    [5]ui.TextBuffer
}

// rowsText formats the row count with thousands separators.
func rowsText(n int) string {
	var b []byte
	b = appendInt(b, n)
	for i := len(b) - 3; i > 0; i -= 3 {
		b = append(b[:i], append([]byte{','}, b[i:]...)...)
	}
	return string(b) + " rows"
}
