package rexui_test

import (
	"encoding/json"
	"math"
	"os"
	"runtime"
	"testing"

	"gonativeui/gpu"
	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
)

// TitlebarFor gives each toolbar style the titlebar height and the close
// button's centre AppKit's window of that style has on 27.2
// (theme/testdata/appkit-ref.json, windows), and Rex's band its own.
func TestTitlebarForMatchesAppKit(t *testing.T) {
	b, err := os.ReadFile("../theme/testdata/appkit-ref.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Windows map[string]struct {
			Titlebar float32
			Close    struct{ X, W, CenterYFromTop float32 }
		}
	}
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		style theme.ToolbarStyle
		win   string
	}{
		{theme.ToolbarPlain, "titled"},
		{theme.ToolbarCompact, "toolbarUnifiedCompact"},
		{theme.ToolbarUnified, "toolbarUnified"},
	} {
		w, ok := ref.Windows[c.win]
		if !ok {
			t.Fatalf("no window %q in the reference", c.win)
		}
		h, tl := rexui.TitlebarFor(c.style)
		want := [2]float32{w.Close.X + w.Close.W/2, w.Close.CenterYFromTop}
		if h != w.Titlebar || tl != want {
			t.Errorf("style %d: titlebar %v, traffic lights %v; AppKit's %s: %v, %v", c.style, h, tl, c.win, w.Titlebar, want)
		}
	}
	if h, tl := rexui.TitlebarFor(theme.ToolbarRex); h != 46 || tl != [2]float32{23, 23} {
		t.Errorf("Rex: %v, %v; want 46, (23, 23)", h, tl)
	}
}

// chromeIcons are the symbols the chrome tests use.
type chromeIcons struct{ sidebar, back, fwd, grid, list, group, share, tag, action, search, inspector, doc, folder icon.ID }

func addChromeIcons(g *rig) *chromeIcons {
	k := g.k
	sym := func(s string, fb rune) icon.ID { return k.AddIcon(icon.Spec{Symbol: s, Fallback: fb, Pt: 13}) }
	return &chromeIcons{
		sidebar: sym("sidebar.left", '▤'), back: sym("chevron.left", '‹'), fwd: sym("chevron.right", '›'),
		grid: sym("square.grid.2x2", '▦'), list: sym("list.bullet", '≡'), group: sym("square.grid.3x1.folder.badge.plus", '▦'),
		share: sym("square.and.arrow.up", '⇪'), tag: sym("tag", '⌗'), action: sym("ellipsis.circle", '…'),
		search: sym("magnifyingglass", '⌕'), inspector: sym("sidebar.right", '▥'),
		doc: k.AddIcon(rexui.SidebarIcons.Documents), folder: k.AddIcon(rexui.SidebarIcons.Folder),
	}
}

// finderToolbar builds the finder scene's toolbar (tools/appkitscenes) over
// a 180-pt sidebar and an inspector from 890 pt.
type finderToolbar struct {
	ic    *chromeIcons
	style theme.ToolbarStyle
	view  int
	views []icon.ID
	q     ui.TextBuffer
	under bool // content is under the toolbar
}

func (f *finderToolbar) build(g *rig) {
	ic := f.ic
	o := rexui.ToolbarOpts{Style: f.style, Title: "Documents", Panes: [2]float32{g.k.Pt(180), g.k.Pt(890)}, ContentUnder: f.under}
	tb := rexui.BeginToolbar(g.k, "Toolbar", o)
	tb.Item(ic.sidebar, "Sidebar")
	tb.NextPane()
	tb.Nav(ic.back, ic.fwd, true, false)
	tb.Title()
	tb.Flexible()
	tb.Picker("View", &f.view, f.views, []string{"as Icons", "as List"}, true)
	tb.Menu(ic.group, "Group", -1)
	tb.BeginGroup("share-tag")
	tb.Item(ic.share, "Share")
	tb.Item(ic.tag, "Tags")
	tb.EndGroup()
	tb.Menu(ic.action, "Action", -1)
	if f.style == theme.ToolbarCompact {
		tb.Search("Search", &f.q, "Search", 0)
	} else {
		tb.SearchButton(ic.search, "Search")
	}
	tb.NextPane()
	tb.Item(ic.inspector, "Inspector")
	tb.End()
}

func newFinderToolbar(t *testing.T, style theme.ToolbarStyle) (*rig, *finderToolbar) {
	g := openRig(t, theme.Light, nil, func(*rig) {})
	g.w, g.h = 2240, 400
	f := &finderToolbar{ic: addChromeIcons(g), style: style}
	f.views = []icon.ID{f.ic.grid, f.ic.list}
	f.q.Reserve(32)
	g.build = f.build
	g.settle()
	return g, f
}

// frame is a node's frame in points (x0, y0, x1, y1).
func (g *rig) frameOf(role ui.Role, name string) [4]float32 {
	g.t.Helper()
	b := g.node(role, name).Bounds
	return [4]float32{b[0] / g.scale, b[1] / g.scale, b[2] / g.scale, b[3] / g.scale}
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) <= 0.5 }

// The toolbar lays its items out as AppKit lays out the finder scene's
// (docs/design/liquid-glass-reference.md §1): the first capsule 17 after
// the zoom light (x 96), sections at the panes' edges, 4 between capsules
// and 8 after a picker and before search, the title 10 after the capsule
// before it, the items after the flexible title packed against the pane's
// end, 36-tall capsules at y 8.
func TestToolbarLayoutUnified(t *testing.T) {
	g, _ := newFinderToolbar(t, theme.ToolbarUnified)
	f := g.frameOf
	sb := f(ui.RoleButton, "Sidebar")
	if !near(sb[0], 96) || !near(sb[1], 8) || !near(sb[3]-sb[1], 36) || sb[2]-sb[0] < 36-0.5 {
		t.Errorf("the sidebar toggle at %v, want x 96, y 8, 36 tall, at least 36 wide", sb)
	}
	nav := f(ui.RoleGroup, "Back/Forward")
	if !near(nav[0], 188) || !near(nav[2]-nav[0], 86) {
		t.Errorf("back/forward at %v, want 188..274", nav)
	}
	title := f(ui.RoleLabel, "Documents")
	if !near(title[0], nav[2]+10) || !near(title[1], 16.5) {
		t.Errorf("the title at %v, want x %v, y 16.5", title, nav[2]+10)
	}
	view, grp := f(ui.RoleComboBox, "View"), f(ui.RoleButton, "Group")
	shareTag, action, search := f(ui.RoleGroup, "share-tag"), f(ui.RoleButton, "Action"), f(ui.RoleButton, "Search")
	if !near(view[2]-view[0], 46) {
		t.Errorf("the collapsed picker is %v wide, want 46", view[2]-view[0])
	}
	for _, c := range []struct {
		name string
		a, b [4]float32
		gap  float32
	}{
		{"picker to menu", view, grp, 8},
		{"menu to group", grp, shareTag, 4},
		{"group to menu", shareTag, action, 4},
		{"menu to search", action, search, 8},
	} {
		if g := c.b[0] - c.a[2]; !near(g, c.gap) {
			t.Errorf("%s: %v apart, want %v", c.name, g, c.gap)
		}
	}
	if !near(search[2], 890-8) || !near(search[2]-search[0], 36) {
		t.Errorf("the search capsule at %v, want 36 wide ending at 882", search)
	}
	if in := f(ui.RoleButton, "Inspector"); !near(in[0], 890+8) {
		t.Errorf("the inspector toggle at %v, want x 898", in)
	}
	share, tags := f(ui.RoleButton, "Share"), f(ui.RoleButton, "Tags")
	if !near(tags[0]-share[2], 8) || share[0] != shareTag[0] || tags[2] != shareTag[2] {
		t.Errorf("the group's items %v, %v in %v: want 8 apart filling it", share, tags, shareTag)
	}
}

// A compact toolbar: 24-tall capsules, the first at x 91.5, an expanded
// search field 166 wide 10 after the item before it.
func TestToolbarLayoutCompact(t *testing.T) {
	g, _ := newFinderToolbar(t, theme.ToolbarCompact)
	sb := g.frameOf(ui.RoleButton, "Sidebar")
	if !near(sb[0], 91.5) || !near(sb[1], 8) || !near(sb[3]-sb[1], 24) {
		t.Errorf("the sidebar toggle at %v, want x 91.5, y 8, 24 tall", sb)
	}
	if title := g.frameOf(ui.RoleLabel, "Documents"); !near(title[1], 12) {
		t.Errorf("the compact title at %v, want y 12", title)
	}
	action, search := g.frameOf(ui.RoleButton, "Action"), g.frameOf(ui.RoleSearchField, "Search")
	if !near(search[0]-action[2], 10) {
		t.Errorf("the search field is %v after the item before it, want 10", search[0]-action[2])
	}
}

// The band's empty parts move the window; its items keep their presses.
func TestToolbarBandDragsWindow(t *testing.T) {
	g, _ := newFinderToolbar(t, theme.ToolbarUnified)
	regs := g.c.DragRegions(nil)
	drags := func(xPt, yPt float32) bool {
		x, y := xPt*g.scale, yPt*g.scale
		d := false
		for _, r := range regs {
			if x >= r.Rect[0] && x < r.Rect[2] && y >= r.Rect[1] && y < r.Rect[3] {
				d = !r.Block
			}
		}
		return d
	}
	for _, p := range [][2]float32{{150, 26}, {500, 26}, {1000, 4}, {890, 48}} {
		if !drags(p[0], p[1]) {
			t.Errorf("(%v, %v) in the empty band does not move the window", p[0], p[1])
		}
	}
	for _, n := range []string{"Sidebar", "Back", "Share", "Inspector"} {
		x, y := centre(g.node(ui.RoleButton, n))
		if drags(x/g.scale, y/g.scale) {
			t.Errorf("a press on %s would move the window", n)
		}
	}
	if drags(150, 60) {
		t.Error("below the band moves the window")
	}
}

// The title is the title colour in the key window and §7's dimmed one when
// the window is not key.
func TestToolbarTitleInactive(t *testing.T) {
	g, _ := newFinderToolbar(t, theme.ToolbarUnified)
	r := g.k.Theme()
	title := g.frameOf(ui.RoleLabel, "Documents")
	ink := func() uint32 { return textColourIn(g, title) }
	if c := ink(); c != r.Colors[theme.TitleText] {
		t.Errorf("the key window's title is %08x, want %08x", c, r.Colors[theme.TitleText])
	}
	g.in.Focused = false
	g.settle()
	if c := ink(); c != r.Colors[theme.TitleTextInactive] {
		t.Errorf("the inactive window's title is %08x, want %08x", c, r.Colors[theme.TitleTextInactive])
	}
}

// textColourIn is the colour of the first text quad inside the frame f
// (points).
func textColourIn(g *rig, f [4]float32) uint32 {
	for _, q := range g.list.Quads {
		x, y := q.Rect[0]/g.scale, q.Rect[1]/g.scale
		if q.Mode == gpu.ModeMaskR8 && x >= f[0]-1 && x <= f[2] && y >= f[1]-4 && y <= f[3] {
			return q.Color[0]
		}
	}
	return 0
}

// sidebarModel is an app's sidebar: two groups, a row with a child, a
// group that can be hidden.
type sidebarModel struct {
	st    rexui.SidebarState
	items []rexui.SidebarItem
	open  [3]bool
	sig   rexui.SidebarSignal
	o     rexui.SidebarOpts
}

func newSidebarModel(ic *chromeIcons) *sidebarModel {
	m := &sidebarModel{open: [3]bool{true, true, true}}
	g := rexui.Sym
	m.items = []rexui.SidebarItem{
		{ID: 1, Title: "Places", Header: true},
		{ID: 2, Title: "Documents", Glyph: g(ic.doc)},
		{ID: 3, Title: "Projects", Glyph: g(ic.folder), Open: &m.open[0]},
		{ID: 4, Title: "Rex", Glyph: g(ic.folder), Level: 1},
		{ID: 5, Title: "Tags", Header: true, Open: &m.open[1]},
		{ID: 6, Title: "Red", Glyph: g(ic.folder)},
	}
	return m
}

func (m *sidebarModel) build(g *rig) {
	c := g.k.Context()
	m.o.Top = g.k.Pt(52)
	theme.At(c, 0, 0, g.k.Pt(180), float32(g.h))
	c.Push(c.Box(theme.Floating, "pane"))
	m.sig = rexui.Sidebar(g.k, "Sidebar", &m.st, m.items, m.o)
	c.Pop()
}

func newSidebarRig(t *testing.T) (*rig, *sidebarModel) {
	g := openRig(t, theme.Light, nil, func(*rig) {})
	g.w, g.h = 1000, 800
	m := newSidebarModel(addChromeIcons(g))
	g.build = m.build
	g.settle()
	return g, m
}

func (g *rig) has(role ui.Role, name string) bool {
	g.c.Semantics(&g.sem)
	for _, n := range g.sem.Nodes {
		if n.Role == role && string(g.sem.Text(n.Name)) == name {
			return true
		}
	}
	return false
}

// selectionFill is the colour of the rounded fill inset 10 from the
// sidebar's sides behind the row f (points), 0 for none.
func selectionFill(g *rig, f [4]float32) uint32 {
	for _, q := range g.list.Quads {
		r := q.Rect
		if q.Mode == gpu.ModeShape && near(r[0]/g.scale, 10) && near(r[2]/g.scale, 170) &&
			near(r[1]/g.scale, f[1]) && near(r[3]/g.scale, f[3]) && near(q.Radii[0]/g.scale, 8) {
			return q.Color[0]
		}
	}
	return 0
}

// The sidebar's rows are 32 tall below 19-tall headers, 13 apart between
// groups (§2); a press selects a row and gives the sidebar the keyboard, so
// that the selection is the accent (§3); in an inactive window it is the
// inactive grey.
func TestSidebarGeometryAndSelection(t *testing.T) {
	g, m := newSidebarRig(t)
	r := g.k.Theme()
	hdr := g.frameOf(ui.RoleGroup, "Places")
	doc := g.frameOf(ui.RoleListItem, "Documents")
	tags := g.frameOf(ui.RoleGroup, "Tags")
	rex := g.frameOf(ui.RoleListItem, "Rex")
	if hdr[1] != 52 || hdr[3]-hdr[1] != 19 || doc[1] != 71 || doc[3]-doc[1] != 32 {
		t.Errorf("the header at %v and the first row at %v: want 52..71 and 71..103", hdr, doc)
	}
	if tags[1]-rex[3] != 13 {
		t.Errorf("%v between the groups, want 13", tags[1]-rex[3])
	}
	g.click(centre(g.node(ui.RoleListItem, "Documents")))
	if m.st.Sel != 2 {
		t.Fatalf("the press selected %d", m.st.Sel)
	}
	if n := g.node(ui.RoleListItem, "Documents"); n.State&ui.SemSelected == 0 {
		t.Error("the selected row is not selected in the semantic tree")
	}
	g.settle()
	if c := selectionFill(g, doc); c != r.Colors[theme.SidebarSel] {
		t.Errorf("the selection with the keyboard in the sidebar is %08x, want SidebarSel %08x", c, r.Colors[theme.SidebarSel])
	}
	g.c.SetFocus(0)
	g.settle()
	if c := selectionFill(g, doc); c != r.Colors[theme.SidebarSelUnemph] {
		t.Errorf("the selection without the keyboard is %08x, want SidebarSelUnemph %08x", c, r.Colors[theme.SidebarSelUnemph])
	}
	g.in.Focused = false
	g.settle()
	if c := selectionFill(g, doc); c != r.Colors[theme.SidebarSelInactive] {
		t.Errorf("the selection in an inactive window is %08x, want SidebarSelInactive %08x", c, r.Colors[theme.SidebarSelInactive])
	}
}

// Up and Down move the selection over the visible rows; Left closes a row
// with children and from a child goes to its parent, Right opens it; the
// disclosures and a header's Show or Hide toggle with the pointer.
func TestSidebarKeysAndDisclosure(t *testing.T) {
	g, m := newSidebarRig(t)
	g.click(centre(g.node(ui.RoleListItem, "Documents")))
	for _, c := range []struct {
		code ui.KeyCode
		want uint32
	}{{ui.KeyDown, 3}, {ui.KeyDown, 4}, {ui.KeyDown, 6}, {ui.KeyDown, 6}, {ui.KeyUp, 4}, {ui.KeyLeft, 3}} {
		g.key(c.code, 0)
		if m.st.Sel != c.want {
			t.Fatalf("key %d: row %d selected, want %d", c.code, m.st.Sel, c.want)
		}
	}
	g.key(ui.KeyLeft, 0) // closes Projects
	if m.open[0] || g.has(ui.RoleListItem, "Rex") {
		t.Fatal("Left did not close the row's children")
	}
	g.key(ui.KeyDown, 0)
	if m.st.Sel != 6 {
		t.Fatalf("Down over a closed row selected %d, want 6", m.st.Sel)
	}
	g.key(ui.KeyUp, 0)
	g.key(ui.KeyRight, 0)
	if !m.open[0] || !g.has(ui.RoleListItem, "Rex") {
		t.Fatal("Right did not open the row")
	}
	if n := g.node(ui.RoleListItem, "Projects"); n.State&ui.SemExpanded == 0 {
		t.Error("the open row is not expanded in the semantic tree")
	}

	// the disclosure with the pointer
	g.click(centre(g.node(ui.RoleButton, "Disclosure")))
	if m.open[0] {
		t.Fatal("a click on the disclosure did not close the row")
	}
	g.click(centre(g.node(ui.RoleButton, "Disclosure")))
	if !m.open[0] {
		t.Fatal("a second click on the disclosure did not open the row")
	}
	// a header's Show or Hide shows under the pointer and hides its group
	g.in.MouseX, g.in.MouseY = centre(g.node(ui.RoleGroup, "Tags"))
	g.settle()
	g.click(centre(g.node(ui.RoleButton, "Show or Hide")))
	if m.open[1] || g.has(ui.RoleListItem, "Red") {
		t.Fatal("Show or Hide did not hide the group")
	}
}

// pathModel is a path bar of four components in a box w points wide.
type pathModel struct {
	items []rexui.PathItem
	w     float32
	sig   rexui.PathSignal
	got   int
}

func (m *pathModel) build(g *rig) {
	c := g.k.Context()
	theme.At(c, g.k.Pt(10), g.k.Pt(10), g.k.Pt(m.w), g.k.Pt(40))
	c.Push(c.Box(theme.Floating, "box"))
	m.sig = rexui.PathBar(g.k, "Path", m.items, rexui.PathBarOpts{})
	if m.sig.Index >= 0 {
		m.got = m.sig.Index
	}
	c.Pop()
}

// A click on a component reports it; when the components do not fit, the
// middle ones keep only their symbols and the first and the last their
// names, as NSPathControl's standard style.
func TestPathBarReportsAndElides(t *testing.T) {
	g := openRig(t, theme.Light, nil, func(*rig) {})
	ic := addChromeIcons(g)
	s := rexui.Sym
	m := &pathModel{w: 600, got: -1, items: []rexui.PathItem{
		{Title: "Macintosh HD", Glyph: s(ic.doc)}, {Title: "Users", Glyph: s(ic.folder)},
		{Title: "adam", Glyph: s(ic.folder)}, {Title: "Documents", Glyph: s(ic.folder)},
	}}
	g.build = m.build
	g.settle()
	g.click(centre(g.node(ui.RoleButton, "Users")))
	if m.got != 1 {
		t.Fatalf("a click on Users reported %d", m.got)
	}
	wide := g.frameOf(ui.RoleButton, "Users")
	m.w = 150
	g.settle()
	narrow := g.frameOf(ui.RoleButton, "Users")
	iw, _ := g.k.IconSize(ic.folder)
	if !near(narrow[2]-narrow[0], iw/g.scale) || wide[2]-wide[0] <= narrow[2]-narrow[0] {
		t.Errorf("Users is %v wide in a narrow bar (was %v), want its symbol's %v", narrow[2]-narrow[0], wide[2]-wide[0], iw/g.scale)
	}
	if first := g.frameOf(ui.RoleButton, "Macintosh HD"); first[2]-first[0] <= iw/g.scale+1 {
		t.Error("the first component lost its name")
	}
}

// The window chrome, built in one frame, allocates nothing once the first
// frames have run: a toolbar, a sidebar, a path bar and two tab bars.
func TestChromeAllocatesNothingPerFrame(t *testing.T) {
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		g := openRig(t, m, nil, func(*rig) {})
		g.w, g.h = 2240, 1200
		ic := addChromeIcons(g)
		f := &finderToolbar{ic: ic, style: theme.ToolbarUnified, views: []icon.ID{ic.grid, ic.list}}
		sb := newSidebarModel(ic)
		p := &pathModel{w: 300, items: []rexui.PathItem{{Title: "Macintosh HD", Glyph: rexui.Sym(ic.doc)}, {Title: "Users", Glyph: rexui.Sym(ic.folder)}}}
		tabs, inTabs := newTabsModel(5), newTabsModel(3)
		tabs.opts = rexui.TabsOpts{Variant: rexui.TabsNeutral, X0: 400, X1: 1600, Y: 10}
		inTabs.opts = rexui.TabsOpts{X0: 400, X1: 1600, Y: 200, Focusable: true, NoDrag: true}
		g.build = func(g *rig) {
			sb.build(g)
			p.build(g)
			tabs.build(g)
			inTabs.build(g)
			f.build(g)
		}
		g.settle()
		// hover a row and a tab, so that their hot states are built too
		g.in.MouseX, g.in.MouseY = centre(g.node(ui.RoleListItem, "Documents"))
		g.frames(5)
		if n := testing.AllocsPerRun(50, g.frame); n != 0 {
			t.Fatalf("mode %d: %.1f allocations per frame", m, n)
		}
	}
}

// Over the sidebar a compact toolbar's capsules lie 14.5 apart (Notes'
// sidebar toggle and Delete: AppKit centres each in a 44-pt viewer), and
// items that share a capsule without being a group lie 2 apart in it
// (Notes' Media and Share).
func TestToolbarSidebarGapAndSharedCapsule(t *testing.T) {
	g := openRig(t, theme.Light, nil, func(*rig) {})
	g.w, g.h = 1800, 200
	ic := addChromeIcons(g)
	var q ui.TextBuffer
	q.Reserve(16)
	g.build = func(g *rig) {
		tb := rexui.BeginToolbar(g.k, "Toolbar", rexui.ToolbarOpts{Style: theme.ToolbarCompact, Title: "Notes", Panes: [2]float32{g.k.Pt(240), 0}})
		tb.Item(ic.sidebar, "Sidebar")
		tb.Item(ic.action, "Delete")
		tb.NextPane()
		tb.Title()
		tb.Flexible()
		tb.BeginShared("insert-share")
		tb.Item(ic.group, "Media")
		tb.Item(ic.share, "Share")
		tb.EndGroup()
		tb.Search("Search", &q, "Search", 0)
		tb.End()
	}
	g.settle()
	sb, del := g.frameOf(ui.RoleButton, "Sidebar"), g.frameOf(ui.RoleButton, "Delete")
	if !near(del[0]-sb[2], 14.5) {
		t.Errorf("Delete is %v after the sidebar toggle, want 14.5", del[0]-sb[2])
	}
	media, share, both := g.frameOf(ui.RoleButton, "Media"), g.frameOf(ui.RoleButton, "Share"), g.frameOf(ui.RoleGroup, "insert-share")
	if !near(share[0]-media[2], 2) || media[0] != both[0] || share[2] != both[2] {
		t.Errorf("Media %v and Share %v in %v: want 2 apart filling one capsule", media, share, both)
	}
}

// In a window with the system's materials the chrome marks its glass as
// material regions: the sidebar, the toolbar band's bar over the content,
// and on macOS each capsule; elsewhere, and under Reduce Transparency, it
// marks none but the band's bar (opaque: the layer cannot show the
// capsules over its own pixels) and draws the fallbacks.
func TestChromeMarksMaterialRegions(t *testing.T) {
	for _, rt := range []bool{false, true} {
		sys := theme.SystemValues{Known: true, Settings: theme.Settings{ReduceTransparency: rt}}
		g := openRig(t, theme.Light, &sys, func(*rig) {})
		g.w, g.h = 2240, 800
		f := &finderToolbar{ic: addChromeIcons(g), style: theme.ToolbarUnified, under: true}
		f.views = []icon.ID{f.ic.grid, f.ic.list}
		m := newSidebarModel(f.ic)
		g.build = func(g *rig) {
			m.build(g)
			f.build(g)
		}
		g.k.Materials = true
		g.settle()
		var n [ui.AreaHUD + 1]int
		for _, r := range g.c.MaterialRegions(nil) {
			n[r.Area]++
		}
		native := runtime.GOOS == "darwin" && !rt
		wantSide, wantCaps := 0, 0
		if native {
			wantSide, wantCaps = 1, 7 // sidebar toggle, nav, view, group, share-tag, action, search, inspector
		}
		if n[ui.AreaSidebar] != wantSide || n[ui.AreaTitlebar] != 1 || (native && n[ui.AreaToolbarCapsule] < wantCaps) || (!native && n[ui.AreaToolbarCapsule] != 0) {
			t.Errorf("reduce transparency %v: regions %v; want %d sidebar, 1 titlebar, %d+ capsules", rt, n, wantSide, wantCaps)
		}
	}
}
