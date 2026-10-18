// Package gallery is the widget gallery: every widget of package widget in
// every state, in the Rex theme, in light and dark, and a table of a million
// rows. It is the Builder the gallery command runs in a window, in the
// benchmark and offscreen.
//
// The window has three panes in a splitter: the state matrix (each widget
// drawn normal, hovered, pressed, focused and disabled), live controls
// (text entry with copy, cut, paste and undo, choices, buttons with tooltips,
// menus, a dialog, tabs and progress), and the table with a context menu on
// its rows. The window follows the system's light or dark mode and accent
// colour; the Appearance select (System, Light, Dark) or ⇧⌘D (the View menu's
// Cycle Appearance, which goes System, Light, Dark and round again) changes
// that, and a change crossfades.
package gallery

import (
	"gonativeui/app"
	"gonativeui/menu"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
)

// Window geometry, in points.
const (
	WindowWidthPt  = 1280
	WindowHeightPt = 800
	MinWidthPt     = 1000
	MinHeightPt    = 600
)

// DefaultRows is the number of rows of the table unless Options.Rows says
// otherwise.
const DefaultRows = 1_000_000

// listItems is the number of items of the small list.
const listItems = 40

// Config is the gallery's window: the Rex titlebar band (46 pt, traffic lights
// at (21, 22)) at 1280×800 pt.
func Config() app.Config {
	return app.Config{
		Title: "Widget gallery", Width: WindowWidthPt, Height: WindowHeightPt,
		MinWidth: MinWidthPt, MinHeight: MinHeightPt,
		Titlebar: 46, TrafficLights: [2]float32{21, 22},
		Clear: [4]float32{0.9, 0.9, 0.9, 1}, // the background mesh covers every pixel
	}
}

// Options configure New.
type Options struct {
	// Rows is the number of rows of the table (DefaultRows when zero).
	Rows int
	// Preference is the mode the window shows: the system's (the zero value),
	// or a forced light or dark.
	Preference theme.Preference
	// Traffic makes the titlebar draw the macOS traffic lights (offscreen
	// renders; in a window the system draws them).
	Traffic bool
	// Still makes the scene hold still for screenshots: the indeterminate
	// progress bar does not sweep (Kit.ReduceMotion) and is shown from the
	// start.
	Still bool
	// Synthetic makes the scripted session's clock the frame count at 60 Hz,
	// for runs without a clock (offscreen); a window uses the platform's.
	Synthetic bool
}

// The ids of the table's columns.
const (
	colID uint32 = iota + 1
	colName
	colKind
	colSize
)

var kinds = [...]string{"Document", "Image", "Archive", "Folder", "Audio"}

// Gallery is the gallery scene. It implements app.Builder and app.Initer; its
// Bench wrapper adds the scripted session.
type Gallery struct {
	opt   Options
	k     *Kit
	c     *ui.Context
	model *menu.Model
	pref  theme.Preference

	// the table
	rows   int
	table  widget.TableState
	cols   [4]widget.Column
	rev    []uint32 // the rows in descending order, for the descending sorts
	search ui.TextBuffer
	status []byte

	// layout
	split  widget.SplitState
	active int // the pane that last got a press
	scroll [2]widget.ScrollState

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
	list      widget.ListState
	askDelete bool
	showRows  bool
	// which list had the keyboard in the last frame, for Select All
	tableFocus, listFocus bool
	working               bool

	m matrix // the values of the state matrix

	// the benchmark and the screenshots
	nodes int
	// the window of the last frame, for the benchmark's report
	winW, winH int
	winScale   float32
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
	g.cols = [4]widget.Column{
		{ID: colID, Title: "ID", Width: 52, MinWidth: 48, Align: ui.AlignRight, Sortable: true},
		{ID: colName, Title: "Name", Width: 92, MinWidth: 64, Sortable: true},
		{ID: colKind, Title: "Kind", Width: 88, MinWidth: 80},
		{ID: colSize, Title: "Size", Width: 56, MinWidth: 44, Align: ui.AlignRight, Fill: true},
	}
	g.table.Sel.Reserve(g.rows)
	g.table.Sort = widget.SortState{Active: true, Column: colID}
	g.rev = make([]uint32, g.rows)
	for i := range g.rev {
		g.rev[i] = uint32(g.rows - 1 - i)
	}
	g.split = widget.NewSplitState(0.42, 0.27, 0.31)
	g.split.Min = [widget.MaxPanes]float32{360, 280, 300}
	g.slider, g.working = 40, o.Still
	g.check, g.sw, g.showRows = true, false, true
	g.notes.SetString("Hello, gallery")
	g.status = make([]byte, 0, 96)
	for i := range g.m.text {
		g.m.text[i].SetString("Field")
		g.m.slide[i] = 40
		g.m.chk[i], g.m.sw[i] = true, true
		g.m.tri[i] = widget.Mixed
	}
	g.setStatus("Ready")
	return g, nil
}

var _ app.Builder = (*Gallery)(nil)
var _ app.Initer = (*Gallery)(nil)

// Init implements app.Initer: it makes the kit and sizes the caches for the
// scene.
func (g *Gallery) Init(a *app.App) error {
	g.Attach(a.UI, a.Text)
	a.UI.Reserve(8192)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}

// Attach makes the kit of the gallery's window on c with the text system ts.
// Init calls it; tests call it with a fake text system.
func (g *Gallery) Attach(c *ui.Context, ts widget.TextSystem) {
	g.c = c
	g.k = widget.NewKit(c, ts, rex.Theme())
	g.k.SetMenu(g.model)
	g.k.SetPreference(g.pref)
	g.k.ReduceMotion = g.opt.Still
}

// WindowPx is the size in pixels and the scale of the window as of the last
// frame.
func (g *Gallery) WindowPx() (w, h int, scale float32) { return g.winW, g.winH, g.winScale }

// Context is the window's ui context (nil before Init or Attach); it makes the
// gallery a widgettest.Host.
func (g *Gallery) Context() *ui.Context { return g.c }

// Kit is the gallery's widget kit (nil before Init or Attach).
func (g *Gallery) Kit() *Kit { return g.k }

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

var keyPane = ui.HashString(ui.HashString(0, "gallery"), "pane")

// Build builds the frame. It implements app.Builder.
func (g *Gallery) Build(c *ui.Context) {
	w, h := c.Size()
	if w <= 0 || h <= 0 || c.Scale() <= 0 {
		return
	}
	k := g.k
	g.winW, g.winH, g.winScale = w, h, c.Scale()
	k.Begin(c)
	g.commands(c)
	r := k.Theme()

	k.Background()
	buttons := theme.ButtonsNone
	if g.opt.Traffic {
		buttons = theme.ButtonsTraffic
	}
	bar := k.Titlebar("Widget gallery", buttons, false)
	g.menuBar(c, r, bar.Free[0])

	inset := r.Px.Inset
	top := theme.Snap(r.Px.TitlebarH)
	theme.At(c, inset, top, float32(w)-2*inset, float32(h)-top-inset)
	area := c.Box(theme.Floating, "area")
	c.Push(area)
	g.panes(c, r)
	c.Pop()

	g.dialog(c)
	k.End()
}

// commands applies the commands of the frame.
func (g *Gallery) commands(c *ui.Context) {
	k := g.k
	if k.Command(cmdCycleAppearance) {
		g.SetPreference((g.pref + 1) % 3) // System, Light, Dark, System …
	}
	if k.Command(cmdRowNumbers) {
		g.showRows = !g.showRows
	}
	g.model.SetChecked(cmdRowNumbers, g.showRows)
	g.model.SetEnabled(cmdPrint, false)
	for _, id := range [...]menu.ID{cmdOpen, cmdSave, cmdRecent1, cmdRecent2, cmdRecent3, cmdRowOpen, cmdRowCopyPath, cmdRowInfo} {
		if k.Command(id) {
			cmd, _ := g.model.Command(id)
			title := cmd.Title
			g.status = append(g.status[:0], "Chose “"...)
			g.status = append(g.status, title...)
			g.status = append(g.status, "”"...)
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

// menuBar places the menu bar in the titlebar band, after the traffic lights.
func (g *Gallery) menuBar(c *ui.Context, r *theme.Resolved, x float32) {
	k := g.k
	h := theme.Snap(r.Px.TitlebarH)
	bh := theme.Snap(r.Px.ControlH)
	x = max(x, theme.Snap(r.Px.Pad))
	theme.At(c, x, theme.Snap((h-bh)/2), 0, bh)
	c.NextWidth(ui.Children(1))
	holder := c.Box(theme.Floating, "menubar-holder")
	c.Push(holder)
	k.MenuBar()
	c.Pop()
}

// panes builds the splitter and its three panes.
func (g *Gallery) panes(c *ui.Context, r *theme.Resolved) {
	k := g.k
	sp := k.BeginSplitter("main", &g.split, ui.AxisX)
	in := c.Input()
	origin := [2]float32{r.Px.Inset, theme.Snap(r.Px.TitlebarH)} // where the area, the splitter's box, is in the window
	for i := 0; i < 3; i++ {
		pr := sp.PaneRect(i)
		focus := c.AnimateValue(ui.KeyN(keyPane, uint64(100+i)), b2f(g.active == i), r.Motion.Dim)
		r.Chrome.SurfaceShadow(c, r, g.surface(i, pr, origin, focus))
	}
	for i := 0; i < 3; i++ {
		pr := sp.PaneRect(i)
		if in.Pressed[ui.ButtonLeft] {
			cx, cy := c.Rect(c.Parent())[0], c.Rect(c.Parent())[1]
			if in.MouseX >= cx+pr[0] && in.MouseX < cx+pr[2] && in.MouseY >= cy+pr[1] && in.MouseY < cy+pr[3] {
				g.active = i
			}
		}
		focus := c.AnimateValue(ui.KeyN(keyPane, uint64(100+i)), b2f(g.active == i), r.Motion.Dim)
		sp.Pane(i)
		s := g.surface(i, [4]float32{0, 0, pr[2] - pr[0], pr[3] - pr[1]}, [2]float32{origin[0] + pr[0], origin[1] + pr[1]}, focus)
		kg := r.Chrome.BeginSurface(c, r, s)
		// The surface's fill and edges are floating boxes, which draw above the
		// flow's, so the content is a floating box too (built after them).
		theme.At(c, 0, 0, pr[2]-pr[0], pr[3]-pr[1])
		wrap := c.Box(theme.Floating, "wrap")
		c.Push(wrap)
		k.NextSize(widget.Fill, widget.Fill)
		k.NextPad(8)
		k.NextGap(8)
		k.Column("content")
		switch i {
		case 0:
			g.statesPane(c)
		case 1:
			g.livePane(c)
		case 2:
			g.tablePane(c)
		}
		k.EndColumn()
		c.Pop()
		r.Chrome.EndSurface(c, r, s, kg)
		sp.EndPane()
	}
	k.EndSplitter(&sp)
}

func (g *Gallery) surface(i int, rect [4]float32, origin [2]float32, focus float32) theme.Surface {
	return theme.Surface{Key: ui.KeyN(keyPane, uint64(i)), Slot: i, Rect: rect, Origin: origin, Focus: focus, Opacity: 1}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// heading builds a section title.
func (g *Gallery) heading(s string) { g.k.LabelStyle(s, widget.TextSemi) }

// dialog builds the confirmation dialog of the "Delete…" button.
func (g *Gallery) dialog(c *ui.Context) {
	k := g.k
	d := k.BeginDialog("delete", "Delete the selected rows?", &g.askDelete)
	if d.Visible {
		n := g.table.Sel.Count()
		k.Paragraph(c.Fmt().Int(n).Str(" rows are selected. Deleting them cannot be undone, and this text is long enough to wrap inside the dialog.").String())
		d.BeginActions()
		d.Action("Cancel", widget.ButtonDefault)
		if d.Action("Delete", widget.ButtonPrimary) {
			g.table.Sel.Clear()
			g.setStatus("Deleted")
		}
		d.EndActions()
	}
	k.EndDialog(&d)
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
