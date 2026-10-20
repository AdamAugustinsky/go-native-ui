// Package demo is the window-chrome demo: Finder-, Settings- and Notes-like
// windows, and one with Rex's tabs, built from rexui's Toolbar, Sidebar,
// PathBar and Tabs on the macOS 27.2 layouts measured by tools/appkitscenes
// (docs/design/liquid-glass-reference.md). The finder, settings and notes
// windows have the scenes' sizes and contents, so that their geometry can be
// compared with AppKit's number for number.
package demo

import (
	"gonativeui/app"
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// Scene is one of the demo's windows.
type Scene uint8

const (
	// Finder is a file browser: sidebar, file list, inspector, a unified
	// toolbar (the finder scene, 1120 × 640).
	Finder Scene = iota
	// Settings is a settings window: a sidebar with a search field and
	// grouped forms (the settings scene, 820 × 620).
	Settings
	// Notes is a notes app: a list of notes and an editor under a compact
	// toolbar (the notes scene, 900 × 600).
	Notes
	// Tabs is a window of Rex's tabs: in the toolbar, and in content.
	Tabs

	// NumScenes is the number of scenes.
	NumScenes
)

// SceneNames are the scenes' names, as the command's -scene takes them.
var SceneNames = [NumScenes]string{"finder", "settings", "notes", "tabs"}

// SceneOf is the scene named s, -1 for none.
func SceneOf(s string) int {
	for i, n := range SceneNames {
		if n == s {
			return i
		}
	}
	return -1
}

// layout is a scene's window: its size and panes, in points.
type layout struct {
	w, h          float32
	style         theme.ToolbarStyle
	sidebar       float32 // the sidebar's width (0: none)
	inspector     float32 // the inspector's width (0: none)
	title         string
	selectedPlace uint32
}

var layouts = [NumScenes]layout{
	Finder:   {w: 1120, h: 640, style: theme.ToolbarUnified, sidebar: 180, inspector: 230, title: "Documents", selectedPlace: placeDocuments},
	Settings: {w: 820, h: 620, style: theme.ToolbarUnified, sidebar: 215, title: "Appearance", selectedPlace: setAppearance},
	Notes:    {w: 900, h: 600, style: theme.ToolbarCompact, sidebar: 240, title: "Notes", selectedPlace: noteLaunch},
	Tabs:     {w: 1000, h: 640, style: theme.ToolbarUnified, sidebar: 0, title: "Projects"},
}

// Config is the window of scene s: its size, and the titlebar and traffic
// lights its toolbar style asks for (rexui.TitlebarFor).
func Config(s Scene) app.Config {
	l := &layouts[s%NumScenes]
	cfg := app.Config{Title: l.title, Width: l.w, Height: l.h, MinWidth: 640, MinHeight: 400,
		Clear: [4]float32{1, 1, 1, 1}}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(l.style)
	return cfg
}

// Options configure New.
type Options struct {
	// Scene is the window to show.
	Scene Scene
	// Preference is the mode: the system's (zero), or forced light or dark.
	Preference theme.Preference
	// Traffic draws the traffic lights (offscreen; a window has the system's).
	Traffic bool
	// Scrolled starts the content scrolled under the toolbar.
	Scrolled bool
	// Still holds animations still, for pictures.
	Still bool
}

// Demo is the demo window. It implements app.Builder and app.Initer.
type Demo struct {
	opt Options
	l   *layout
	c   *ui.Context
	k   *base.Kit
	ic  icons

	side     rexui.SidebarState
	places   []rexui.SidebarItem
	open     [8]bool // the disclosure states the places point at
	focused  bool    // the first focus is given
	content  base.ScrollState
	under    bool // the content is under the toolbar
	scrolled int  // frames left to apply Options.Scrolled
	view     int
	views    [4]icon.ID
	query    ui.TextBuffer
	sideQ    ui.TextBuffer
	dark     int
	accent   int
	path     []rexui.PathItem
	files    []file
	fileSel  [2]int
	settings [len(settingsOptions)]int

	bar      rexui.TabBar
	tabs     []rexui.Tab
	tabSel   int
	inBar    rexui.TabBar
	inTabs   []rexui.Tab
	inSel    int
	nextTab  uint64
	editor   ui.TextBuffer
	winW     int
	winH     int
	winScale float32
}

// icons are the symbols the scenes add to the kit.
type icons struct {
	sidebar, inspector, back, forward, grid, list, columns, gallery, group, action, share, tag, search icon.ID
	trash, compose, format, checklist, table, media, plus                                              icon.ID
	airdrop, recents, apps, desktop, docs, downloads, cloud, shared, disk, network, folder, dot        icon.ID
	sun, moon, gear, bell, lock, person, wifi, paint                                                   icon.ID
	note, doc                                                                                          icon.ID
}

// New returns the demo.
func New(o Options) *Demo {
	d := &Demo{opt: o, l: &layouts[o.Scene%NumScenes], nextTab: 1}
	if o.Scrolled {
		d.scrolled = 4
	}
	d.query.Reserve(64)
	d.sideQ.Reserve(64)
	d.editor.Reserve(4096)
	d.editor.SetString(notesText)
	return d
}

var (
	_ app.Builder = (*Demo)(nil)
	_ app.Initer  = (*Demo)(nil)
)

// Init implements app.Initer.
func (d *Demo) Init(a *app.App) error {
	d.Attach(a.UI, a.Text)
	a.UI.Reserve(4096)
	return nil
}

// Attach makes the kit on c with the text system ts; Init calls it, tests
// call it with a fake text system.
func (d *Demo) Attach(c *ui.Context, ts base.TextSystem) {
	d.c = c
	d.k = base.NewKit(c, ts, rex.Theme())
	d.k.SetPreference(d.opt.Preference)
	d.k.ReduceMotion = d.opt.Still
	d.addIcons()
	d.views = [4]icon.ID{d.ic.grid, d.ic.list, d.ic.columns, d.ic.gallery}
	d.view = 1 // as List, as the finder scene
	d.model()
}

// Context is the window's ui context (widgettest.Host).
func (d *Demo) Context() *ui.Context { return d.c }

// Kit is the demo's kit.
func (d *Demo) Kit() *base.Kit { return d.k }

// WindowPx is the window's size in pixels and its scale as of the last frame.
func (d *Demo) WindowPx() (w, h int, scale float32) { return d.winW, d.winH, d.winScale }

func (d *Demo) addIcons() {
	k := d.k
	tool := func(sym string, fb rune, pt float32) icon.ID {
		return k.AddIcon(icon.Spec{Symbol: sym, Fallback: fb, Pt: pt})
	}
	const pt = 13 // AppKit's toolbar symbols, in both styles
	ic := &d.ic
	ic.sidebar = tool("sidebar.left", '▤', pt)
	ic.inspector = tool("sidebar.right", '▥', pt)
	ic.back = k.AddIcon(icon.Spec{Symbol: "chevron.left", Fallback: '‹', Pt: pt, Weight: 0.23})
	ic.forward = k.AddIcon(icon.Spec{Symbol: "chevron.right", Fallback: '›', Pt: pt, Weight: 0.23})
	ic.grid = tool("square.grid.2x2", '▦', pt)
	ic.list = tool("list.bullet", '≡', pt)
	ic.columns = tool("rectangle.split.3x1", '▥', pt)
	ic.gallery = tool("square.grid.3x1.below.line.grid.1x2", '▭', pt)
	ic.group = tool("square.grid.3x1.folder.badge.plus", '▦', pt)
	ic.action = tool("ellipsis.circle", '…', pt)
	ic.share = tool("square.and.arrow.up", '⇪', pt)
	ic.tag = tool("tag", '⌗', pt)
	ic.search = tool("magnifyingglass", '⌕', pt)
	ic.trash = tool("trash", '⌫', pt)
	ic.compose = tool("square.and.pencil", '✎', pt)
	ic.format = tool("textformat", 'A', pt)
	ic.checklist = tool("checklist", '☑', pt)
	ic.table = tool("tablecells", '▦', pt)
	ic.media = tool("photo.on.rectangle", '▨', pt)
	ic.plus = tool("plus", '+', pt)
	si := &rexui.SidebarIcons
	ic.recents, ic.apps, ic.desktop, ic.docs = k.AddIcon(si.Recents), k.AddIcon(si.Applications), k.AddIcon(si.Desktop), k.AddIcon(si.Documents)
	ic.downloads, ic.cloud, ic.shared, ic.disk = k.AddIcon(si.Downloads), k.AddIcon(si.Cloud), k.AddIcon(si.Shared), k.AddIcon(si.Disk)
	ic.network, ic.folder, ic.dot = k.AddIcon(si.Network), k.AddIcon(si.Folder), k.AddIcon(si.Tag)
	ic.airdrop = k.AddIcon(icon.Spec{Symbol: "dot.radiowaves.left.and.right", Fallback: '◉', Pt: 16})
	side := func(sym string, fb rune) icon.ID { return k.AddIcon(icon.Spec{Symbol: sym, Fallback: fb, Pt: 16}) }
	ic.sun, ic.moon, ic.gear, ic.bell = side("sun.max", '☀'), side("moon", '☾'), side("gearshape", '⚙'), side("bell", '♪')
	ic.lock, ic.person, ic.wifi, ic.paint = side("lock", '⚿'), side("person.crop.circle", '☺'), side("wifi", '≋'), side("paintpalette", '◐')
	ic.note, ic.doc = side("note.text", '▤'), k.AddIcon(icon.Spec{Symbol: "doc", Fallback: '▢', Pt: 12})
}

// Build builds the frame. It implements app.Builder.
func (d *Demo) Build(c *ui.Context) {
	w, h := c.Size()
	if w <= 0 || h <= 0 || c.Scale() <= 0 {
		return
	}
	d.winW, d.winH, d.winScale = w, h, c.Scale()
	k := d.k
	k.Begin(c)
	r := k.Theme()
	W, H := float32(w), float32(h)
	s := r.Scale
	tbH := theme.Snap(r.Window.Toolbars[d.l.style].Height)

	// the window's content colour under everything
	theme.At(c, 0, 0, W, H)
	c.NextBG(r.Colors[theme.Window])
	c.BoxKeyed(ui.DrawBackground|base.Floating, ui.HashString(0, "bg"), nil)

	sbW := theme.Snap(d.l.sidebar * s)
	inW := theme.Snap(d.l.inspector * s)
	if sbW > 0 {
		theme.At(c, 0, 0, sbW, H)
		c.Push(c.Box(base.Floating, "sidebar"))
		d.sidebar(tbH)
		c.Pop()
	}
	if inW > 0 {
		theme.At(c, W-inW, 0, inW, H)
		c.Push(c.Box(base.Floating, "inspector"))
		d.inspector(tbH)
		c.Pop()
	}
	theme.At(c, sbW, 0, W-sbW-inW, H)
	c.Push(c.Box(base.Floating, "content"))
	d.contentPane(tbH, W-sbW-inW, H)
	c.Pop()

	d.toolbar(sbW, W-inW)
	if d.opt.Scene == Finder {
		d.fileHeader(sbW, tbH, W-sbW-inW)
	}
	k.End()
}

// toolbar builds the scene's toolbar over the panes.
func (d *Demo) toolbar(sbW, inX float32) {
	k := d.k
	ic := &d.ic
	panes := [2]float32{sbW, 0}
	if d.l.inspector > 0 {
		panes[1] = inX
	}
	o := rexui.ToolbarOpts{Style: d.l.style, Title: d.l.title, Panes: panes, ContentUnder: d.under, Traffic: d.opt.Traffic}
	if d.opt.Scene == Finder { // the file list's header is in the toolbar's pocket
		o.ContentUnder, o.Pocket = true, theme.Snap(fileHeaderH*d.k.Theme().Scale)
	}
	switch d.opt.Scene {
	case Finder:
		o.Subtitle = ""
		tb := rexui.BeginToolbar(k, "Toolbar", o)
		tb.Item(ic.sidebar, "Sidebar")
		tb.NextPane()
		tb.Nav(ic.back, ic.forward, true, false)
		tb.Title()
		tb.Flexible()
		tb.Picker("View", &d.view, d.views[:], viewNames, true)
		tb.Menu(ic.group, "Group", -1)
		tb.BeginGroup("share-tag")
		tb.Item(ic.share, "Share")
		tb.Item(ic.tag, "Tags")
		tb.EndGroup()
		tb.Menu(ic.action, "Action", -1)
		tb.SearchButton(ic.search, "Search")
		tb.NextPane()
		tb.Item(ic.inspector, "Inspector")
		tb.End()
	case Settings:
		tb := rexui.BeginToolbar(k, "Toolbar", o)
		tb.NextPane()
		tb.Nav(ic.back, ic.forward, true, true)
		tb.Title()
		tb.End()
	case Notes:
		tb := rexui.BeginToolbar(k, "Toolbar", o)
		tb.Item(ic.sidebar, "Sidebar")
		tb.Item(ic.trash, "Delete")
		tb.NextPane()
		tb.Title()
		tb.Item(ic.compose, "New Note")
		tb.Flexible()
		tb.BeginGroup("format")
		tb.Item(ic.format, "Format")
		tb.Item(ic.checklist, "Checklist")
		tb.Item(ic.table, "Table")
		tb.EndGroup()
		tb.Item(ic.media, "Media")
		tb.Item(ic.share, "Share")
		tb.Search("Search", &d.query, "Search", 0)
		tb.End()
	case Tabs:
		o.Panes = [2]float32{}
		tb := rexui.BeginToolbar(k, "Toolbar", o)
		tb.Nav(ic.back, ic.forward, true, false)
		box := tb.BeginCustom("tab strip", ui.Pct(1))
		d.toolbarTabs(box)
		tb.EndCustom()
		if tb.Item(ic.plus, "New Tab").Clicked {
			d.newTab()
		}
		tb.End()
	}
}

var viewNames = []string{"as Icons", "as List", "as Columns", "as Gallery"}
