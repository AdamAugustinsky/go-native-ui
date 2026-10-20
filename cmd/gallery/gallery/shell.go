package gallery

import (
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The window's shell: the sidebar of scenes on glass, the unified toolbar,
// and the detail pane the scene is built in (docs/design/liquid-glass-reference.md,
// System Settings' layout: tools/appkitscenes' settings scene).

// buildItems makes the sidebar's rows of this frame: each group's header and
// the scenes whose titles match the search.
func (g *Gallery) buildItems() {
	t := g.k.Theme().T
	q := g.query.Bytes()
	g.items = g.items[:0]
	for grp := 0; grp < numGroups; grp++ {
		n0 := len(g.items)
		g.items = append(g.items, rexui.SidebarItem{ID: 1000 + uint32(grp), Title: groupTitles[grp], Header: true})
		for s := range defs {
			d := &defs[s]
			if d.group != grp || !contains(d.title, q) {
				continue
			}
			g.items = append(g.items, rexui.SidebarItem{ID: sceneID(s), Title: d.title,
				Glyph: rexui.Sym(g.ic.tiles[s]), Tile: tileColours[d.tile].At(t)})
		}
		if len(g.items) == n0+1 { // no scene of the group matches: no header either
			g.items = g.items[:n0]
		}
	}
}

// contains reports whether s holds q, ignoring the case of ASCII letters.
func contains(s string, q []byte) bool {
	if len(q) == 0 {
		return true
	}
	lower := func(b byte) byte {
		if 'A' <= b && b <= 'Z' {
			return b + 'a' - 'A'
		}
		return b
	}
next:
	for i := 0; i+len(q) <= len(s); i++ {
		for j := range q {
			if lower(s[i+j]) != lower(q[j]) {
				continue next
			}
		}
		return true
	}
	return false
}

// sidebar builds the sidebar pane (the current parent): the search field in
// its place under the toolbar's band, then the scenes.
func (g *Gallery) sidebar(tbH float32) {
	k := g.k
	c, r := k.Context(), k.Theme()
	g.buildItems()
	s := rexui.Sidebar(k, "Sidebar", &g.side, g.items, rexui.SidebarOpts{Top: tbH, Pad: 34}) // the search field: 4 below the safe area, 24 tall, 6 above the rows
	if s.Changed {
		g.visit(g.side.Sel)
	}
	if !g.focused { // as Settings' window, the keyboard starts in the sidebar
		c.SetFocus(c.KeyOf(s.ID))
		g.focused = true
	}
	w := c.Rect(c.Parent())
	inset := theme.Snap(r.Window.Sidebar.SearchInset)
	theme.At(c, inset, tbH+theme.Snap(k.Pt(4)), max(w[2]-w[0]-2*inset, 0), theme.Snap(r.Window.Sidebar.SearchH))
	c.Push(c.Box(base.Floating, "search"))
	rexui.SidebarSearch(k, "Search", &g.query, "Search")
	c.Pop()
}

// toolbar builds the toolbar over the panes: back and forward and the title
// of the scene, in the detail pane's part of the band; the table's search
// field; and, where the system draws no menu bar (not on macOS in a window),
// the gallery's own.
func (g *Gallery) toolbar(sc int, sbW float32) {
	k := g.k
	c, r := k.Context(), k.Theme()
	o := rexui.ToolbarOpts{Style: theme.ToolbarUnified, Title: defs[sc].title, Panes: [2]float32{sbW, 0},
		ContentUnder: g.under, Traffic: g.opt.Traffic, BandFill: g.contentFill(),
		ContentEdge: true}
	tb := rexui.BeginToolbar(k, "Toolbar", o)
	tb.NextPane()
	b, f := tb.Nav(g.ic.back, g.ic.forward, g.histAt > 0, g.histAt+1 < g.histN)
	if b.Clicked {
		g.back()
	}
	if f.Clicked {
		g.forward()
	}
	tb.Title()
	if sc == scTable {
		tb.Search("Search rows", &g.search, "Search", 0)
	}
	if !c.NativeMenus() {
		bh := theme.Snap(r.Px.ControlH)
		tb.BeginCustom("menus", ui.Children(1))
		c.NextPadding(0, theme.Snap((tb.Height()-bh)/2), theme.Snap(k.Pt(8)), 0)
		c.NextSize(ui.Children(1), ui.Pct(1))
		c.NextAxis(ui.AxisY)
		c.Push(c.BoxKeyed(0, ui.KeyN(keyGallery, 9), nil))
		g.wk.MenuBar()
		c.Pop()
		tb.EndCustom()
	}
	tb.End()
}

// content builds scene sc in the detail pane (the current parent), w by h
// pixels, the pane running under the toolbar's band: the scene's scroll view
// starts its content top pixels down, below the band.
func (g *Gallery) content(sc int, w, h, top float32) {
	g.top = top
	switch sc {
	case scPlayground:
		g.playgroundScene(w, h)
	case scStates:
		g.statesScene(w, h)
	case scTable:
		g.tableScene(w, h)
	case scAppearance:
		g.appearanceScene(w, h)
	case scDialogs:
		g.dialogsScene(w, h)
	default:
		g.catalogueScene(w, h, sc-scCatalogue)
	}
}
