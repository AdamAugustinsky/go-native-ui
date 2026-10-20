package pilot

import (
	"os"
	"path/filepath"
	"runtime"

	"gonativeui/icon"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// The window's chrome, as Finder has it (tools/appkitscenes: the finder scene):
// a flush sidebar of locations on the system's glass, a unified toolbar over
// both panes with back, forward and up, the folder's name as the title and a
// filter button, and a path bar under each list.

// Layout, in points.
const (
	sidebarPt = 180 // the sidebar's width (the finder scene's)
	pathBarPt = 28  // the path bar's band, a hairline above it
	statusPt  = 22  // the status line's band
	filterPt  = 34  // the filter's band: a 24 pt field, 5 pt above and below
)

// Place is a location in the sidebar.
type Place struct {
	Title, Path string
	// Group is 0 for Favorites, 1 for Locations.
	Group int
	// Symbol names the place's icon; "" picks the folder's.
	Symbol string
}

// place is a Place with its sidebar row id.
type place struct {
	Place
	id uint32
}

// The sidebar rows' ids: the groups' headers, then the places.
const (
	hdrFavorites uint32 = iota + 1
	hdrLocations
	placeFirst = 16
)

// symbols are the icons of the chrome, added to the kit's atlas before the
// first frame (SF Symbols on macOS, glyphs of the system font elsewhere).
type symbols struct {
	sidebar, back, forward, up, search icon.ID
	folder, disk, home                 icon.ID
	byName                             map[string]icon.ID
}

func (pl *Pilot) addSymbols() {
	k := pl.bk
	pt := theme.MacWindow.Capsule.Glyph // AppKit's toolbar symbols
	s := &pl.sym
	s.sidebar = k.AddIcon(icon.Spec{Symbol: "sidebar.left", Fallback: '▤', Pt: pt})
	s.back = k.AddIcon(icon.Spec{Symbol: "chevron.left", Fallback: '‹', Pt: pt, Weight: 0.23})
	s.forward = k.AddIcon(icon.Spec{Symbol: "chevron.right", Fallback: '›', Pt: pt, Weight: 0.23})
	s.up = k.AddIcon(icon.Spec{Symbol: "chevron.up", Fallback: '˄', Pt: pt, Weight: 0.23})
	s.search = k.AddIcon(icon.Spec{Symbol: "magnifyingglass", Fallback: '⌕', Pt: pt})
	si := &rexui.SidebarIcons
	s.folder, s.disk = k.AddIcon(si.Folder), k.AddIcon(si.Disk)
	s.home = k.AddIcon(icon.Spec{Symbol: "house", Fallback: '⌂', Pt: 16})
	s.byName = map[string]icon.ID{
		"house": s.home, "folder": s.folder, "internaldrive": s.disk,
		"menubar.dock.rectangle": k.AddIcon(si.Desktop), "doc": k.AddIcon(si.Documents),
		"arrow.down.circle": k.AddIcon(si.Downloads), "square.grid.2x2": k.AddIcon(si.Applications),
	}
}

// Probe is how Pilot looks at the file system for the sidebar's places: the
// usual folders of the home folder that exist, and the startup volume's name.
// It runs on a worker, never in the window's start or a frame (a home on a
// network volume, or a link that does not answer, would stall them). The zero
// value looks at the real file system.
type Probe struct {
	// IsDir reports whether path is a folder (default: os.Stat).
	IsDir func(path string) bool
	// VolumeName is the startup volume's name (default: macOS's /Volumes
	// entry that links to "/").
	VolumeName func() string
}

func (pr Probe) isDir() func(string) bool {
	if pr.IsDir != nil {
		return pr.IsDir
	}
	return isDir
}

func (pr Probe) volumeName() func() string {
	if pr.VolumeName != nil {
		return pr.VolumeName
	}
	return volumeName
}

// placesProbed is what the worker found.
type placesProbed struct {
	places []Place
	volume string
}

// makePlaces fills the sidebar: Favorites (the home folder and the usual
// folders in it) and Locations (the startup volume), or Options.Places. Of the
// usual places, the first frame has the ones that need no look at the disk (the
// home folder and the volume, under the name the system usually gives it);
// probePlaces finds the rest.
func (pl *Pilot) makePlaces() {
	ps := pl.opt.Places
	if ps == nil {
		ps = defaultPlaces(pl.opt.Home, pl.rootTitle, nil)
	}
	pl.setPlaces(ps)
}

func (pl *Pilot) setPlaces(ps []Place) {
	pl.places = pl.places[:0]
	for i, p := range ps {
		pl.places = append(pl.places, place{p, placeFirst + uint32(i)})
	}
	pl.open = [2]bool{true, true}
	pl.items = pl.items[:0]
	for g, title := range [2]string{"Favorites", "Locations"} {
		first := true
		for i := range pl.places {
			p := &pl.places[i]
			if p.Group != g {
				continue
			}
			if first {
				pl.items = append(pl.items, rexui.SidebarItem{ID: hdrFavorites + uint32(g), Title: title, Header: true, Open: &pl.open[g]})
				first = false
			}
			id, ok := pl.sym.byName[p.Symbol]
			if !ok {
				id = pl.sym.folder
			}
			pl.items = append(pl.items, rexui.SidebarItem{ID: p.id, Title: p.Title, Glyph: rexui.Sym(id)})
		}
	}
	pl.sideFor = "" // the row of the folder shown is looked for again
}

// startProbe looks for the places on a worker: the result is taken by
// pollPlaces in a frame (the worker wakes the loop). Options.Places are
// the app's own and need no look.
func (pl *Pilot) startProbe() {
	if pl.opt.Places != nil {
		return
	}
	ch := make(chan placesProbed, 1)
	pl.probed = ch
	pr, home, root, wake := pl.opt.Probe, pl.opt.Home, pl.rootTitle, pl.wake
	named := pl.opt.RootTitle != ""
	go func() {
		r := placesProbed{}
		if !named {
			r.volume = pr.volumeName()()
			root = r.volume
		}
		r.places = defaultPlaces(home, root, pr.isDir())
		ch <- r // (buffered: the worker never waits for the window)
		wake()
	}()
}

// pollPlaces takes the worker's places and the volume's name, if they are
// there.
func (pl *Pilot) pollPlaces(c *ui.Context) {
	if pl.probed == nil {
		return
	}
	select {
	case r := <-pl.probed:
		pl.probed = nil
		if r.volume != "" && r.volume != pl.rootTitle {
			pl.rootTitle = r.volume
			for _, p := range pl.panes {
				p.rootTitle = r.volume
				if p.dir != "" {
					p.setDir(p.dir)
				}
			}
		}
		pl.setPlaces(r.places)
		c.RequestFrame()
	default:
	}
}

// PlacesPending reports whether the sidebar's places are still being looked
// for: a picture taken now would lack them.
func (pl *Pilot) PlacesPending() bool { return pl.probed != nil }

// defaultPlaces are the home folder, the usual folders in it that exist
// (isDir says; nil: none is looked for) and the root.
func defaultPlaces(home, rootTitle string, isDir func(string) bool) []Place {
	var ps []Place
	if home != "" {
		ps = append(ps, Place{Title: filepath.Base(home), Path: home, Symbol: "house"})
		if isDir != nil {
			for _, f := range [...]struct{ name, symbol string }{
				{"Desktop", "menubar.dock.rectangle"}, {"Documents", "doc"}, {"Downloads", "arrow.down.circle"},
			} {
				if p := filepath.Join(home, f.name); isDir(p) {
					ps = append(ps, Place{Title: f.name, Path: p, Symbol: f.symbol})
				}
			}
		}
	}
	if runtime.GOOS == "darwin" && (isDir == nil || isDir("/Applications")) {
		ps = append(ps, Place{Title: "Applications", Path: "/Applications", Symbol: "square.grid.2x2"})
	}
	root := string(filepath.Separator)
	if vol := filepath.VolumeName(home); vol != "" {
		root = vol + root
	}
	return append(ps, Place{Title: rootTitle, Path: root, Group: 1, Symbol: "internaldrive"})
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// startupVolumeName is the name of the startup volume where it is known
// without looking: macOS's usual one.
func startupVolumeName() string {
	switch runtime.GOOS {
	case "darwin":
		return "Macintosh HD"
	case "windows":
		return "This PC"
	}
	return "Computer"
}

// volumeName is the name of the startup volume: on macOS the entry of
// /Volumes that is a link to "/" ("Macintosh HD"); "Computer" where it is not
// known. It reads the disk: a worker calls it.
func volumeName() string {
	if runtime.GOOS == "darwin" {
		if es, err := os.ReadDir("/Volumes"); err == nil {
			for _, e := range es {
				if t, err := os.Readlink(filepath.Join("/Volumes", e.Name())); err == nil && t == "/" {
					return e.Name()
				}
			}
		}
	}
	return startupVolumeName()
}

// placeFor is the sidebar row of the folder dir (0: none).
func (pl *Pilot) placeFor(dir string) uint32 {
	for i := range pl.places {
		if pl.places[i].Path == dir {
			return pl.places[i].id
		}
	}
	return 0
}

// drawSidebar builds the sidebar in the current parent (the pane's box,
// full height): its rows scroll below the toolbar. A press on a place opens
// it in the pane that has the keyboard.
func (pl *Pilot) drawSidebar(tbH float32) {
	k := pl.bk
	a := pl.panes[pl.active]
	if a.dir != pl.sideFor { // the folder shown changed: the row that is it, if any
		pl.sideFor = a.dir
		pl.side.Sel = pl.placeFor(a.dir)
	}
	s := rexui.Sidebar(k, "Sidebar", &pl.side, pl.items, rexui.SidebarOpts{Top: tbH})
	if s.Changed {
		for i := range pl.places {
			if pl.places[i].id == pl.side.Sel {
				a.goTo(pl.places[i].Path)
				a.pathEdit = false
			}
		}
	}
}

// drawToolbar builds the toolbar over the panes: the sidebar's button, back
// and forward in one capsule, up, the title and the filter. sbW is the
// sidebar's width in pixels (0: hidden).
func (pl *Pilot) drawToolbar(c *ui.Context, sbW float32) {
	k := pl.bk
	a := pl.panes[pl.active]
	o := rexui.ToolbarOpts{Style: toolbarStyle, Title: a.base, Panes: [2]float32{sbW, 0}, Traffic: pl.Traffic, ContentEdge: true,
		BandFill: k.Theme().Colors[theme.ControlBackground]}
	tb := rexui.BeginToolbar(k, "Toolbar", o)
	if tb.Item(pl.sym.sidebar, "Sidebar").Clicked {
		pl.sidebar = !pl.sidebar
	}
	if sbW > 0 {
		tb.NextPane()
	}
	back, fwd := tb.Nav(pl.sym.back, pl.sym.forward, a.hist.canBack(), a.hist.canForward())
	if back.Clicked {
		a.goBack()
	}
	if fwd.Clicked {
		a.goForward()
	}
	k.BeginDisabled(!a.canUp())
	if tb.Item(pl.sym.up, "Enclosing Folder").Clicked {
		a.goUp()
	}
	k.EndDisabled()
	tb.Title()
	tb.Flexible()
	if tb.SearchButton(pl.sym.search, "Filter").Clicked {
		a.filterOpen = true
		c.SetFocus(filterKey(pl.active))
	}
	tb.End()
}

// pathBandKey is the key of the band of pane i's path bar.
func pathBandKey(i int) ui.Key { return ui.KeyN(paneKey(i), 20) }

// drawPathBar builds pane i's path bar band at the bottom of its column: the
// components of the folder, each opening it; ⌘L swaps in the path field, which
// takes a typed path.
func (pl *Pilot) drawPathBar(c *ui.Context, r *theme.Resolved, i int, p *pane, w float32) {
	k := pl.bk
	s := r.Scale
	h := theme.Snap(pathBarPt * s)
	pl.band(c, r, pathBandKey(i), w, h, true)
	if p.pathOK { // the folder typed is shown
		p.pathOK = false
		if p.pathEdit {
			p.pathEdit = false
			if c.FocusKey() == pathKey(i) {
				c.SetFocus(listKey(i))
			}
		}
	}
	if p.pathEdit {
		theme.At(c, theme.Snap(8*s), theme.Snap(2*s), max(w-theme.Snap(16*s), 0), h-theme.Snap(4*s))
		c.Push(c.BoxKeyed(base.Floating, pathBoxKey(i), nil))
		f := pl.k.TextField("path", &p.path, "")
		c.Pop()
		pl.pathSignals(c, i, p, f)
	} else {
		// a press on the band beside the components types a path, as in a
		// file manager's address bar
		// (a button for the keyboard and assistive technology as well: ⌘L does the same)
		theme.At(c, 0, 0, w, h)
		c.NextRole(ui.RoleButton)
		c.NextSemName("Edit Path")
		if sig := c.Signal(c.BoxKeyed(ui.Clickable|ui.Focusable|base.Floating, ui.KeyN(paneKey(i), 23), nil)); sig.Pressed || sig.Activated {
			p.pathEdit = true
			p.path.SelectAll()
			c.SetFocus(pathKey(i))
			c.RequestFrame()
			pl.active = i
		}
		theme.At(c, theme.Snap(8*s), theme.Snap(2*s), max(w-theme.Snap(16*s), 0), h-theme.Snap(4*s))
		c.Push(c.BoxKeyed(base.Floating, ui.KeyN(paneKey(i), 21), nil))
		for n := range p.crumbItem {
			g := pl.sym.folder
			if n == 0 {
				g = pl.sym.disk
			}
			p.crumbItem[n].Glyph = rexui.Sym(g)
		}
		if bar := rexui.PathBar(k, "Path", p.crumbItem, rexui.PathBarOpts{Size: theme.SizeSmall}); bar.Index >= 0 && bar.Index < len(p.crumbs) {
			if path := p.crumbs[bar.Index].path; path != p.dir {
				p.goTo(path)
			}
			pl.active = i
		}
		c.Pop()
	}
	c.Pop() // the band
}

// band builds the band of a pane's bottom bar (the path bar, the status line):
// the window's colour with a hairline on top when line is set, as the finder's
// path bar, and pushes it (the caller pops).
func (pl *Pilot) band(c *ui.Context, r *theme.Resolved, key ui.Key, w, h float32, line bool) {
	c.NextSize(ui.Px(w), ui.Px(h))
	c.NextBG(r.Colors[theme.Window])
	c.Push(c.BoxKeyed(ui.DrawBackground, key, nil))
	if line {
		theme.At(c, 0, 0, w, r.Lines.Hairline)
		c.NextBG(r.Colors[theme.Separator])
		c.BoxKeyed(ui.DrawBackground|base.Floating, ui.KeyN(key, 1), nil)
	}
}
