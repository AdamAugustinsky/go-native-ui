// Package pilot is the dual-pane file browser on app + ui + fs, built from
// rexui, the Rex theme's components on macOS Finder's measured layout: a
// unified toolbar (sidebar button, back and forward, up, the folder's name,
// search) over a glass sidebar of places, a striped table for each file list
// (selection by entry index, sorting from the header, resizable columns, the
// system's icons), a splitter between the panes, and a path bar and a status
// line under each. The filter is a search field and the path a text field.
// It follows the system's light or dark mode, accent colour and settings. It
// is OS-independent: the window loop, the file system service and the text
// system come in through Options and app.App, so the same code is the window,
// the benchmark and the offscreen screenshot.
//
//	pl := pilot.New(pilot.Options{...})
//	app.RunWith(pilot.Config(), pl, app.RunOptions{})   // or RunBench / NewOffscreen
//
// Package layout: view.go and pane.go are the model (listing, rows,
// selection, navigation and history, reconciling a new listing of the same
// directory by name, all in slices of bounded work per frame); keys.go maps
// the keys the table does not take to it; build.go builds a frame, chrome.go
// the toolbar, sidebar and path bar; icons.go caches the system's file icons;
// menus.go is the View menu's zoom; script.go
// drives the benchmark; synth.go makes synthetic directories; shot.go renders
// the offscreen scene.
package pilot

import (
	"runtime"
	"sync"
	"time"

	"gonativeui/app"
	"gonativeui/fs"
	"gonativeui/img"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"
)

// Window geometry, in points.
const (
	WindowWidthPt  = 1200
	WindowHeightPt = 760
	MinWidthPt     = 640
	MinHeightPt    = 400
)

// toolbarStyle is Pilot's toolbar: Finder's unified one (52 pt, capsule
// items, a 15 pt bold title), with the system's glass behind the sidebar and
// the capsules.
const toolbarStyle = theme.ToolbarUnified

// Config is Pilot's window: the unified toolbar's titlebar and the system's
// window buttons placed for it (rexui.TitlebarFor), the system's materials
// (DECISIONS D52), at 1200×760 pt, at least 640×400.
func Config() app.Config {
	cfg := app.Config{
		Title: "Pilot", Width: WindowWidthPt, Height: WindowHeightPt,
		MinWidth: MinWidthPt, MinHeight: MinHeightPt,
		Clear: [4]float32{1, 1, 1, 1}, Materials: true,
	}
	cfg.Titlebar, cfg.TrafficLights = rexui.TitlebarFor(toolbarStyle)
	return cfg
}

// Ready is a listing made before the window existed (content-first startup):
// the directory, the sorted listing (its Err set on failure) and when it was
// finished (platform.Now).
type Ready struct {
	Dir string
	L   *fs.Listing
	At  int64
}

// Options configure New.
type Options struct {
	Home string // the home directory, for "~" in the path field

	// Src serves listings and watches. nil: the panes show only the listings
	// they are given (screenshots); navigation reports "Not available".
	Src Source

	// Initial are the first listings of the two panes, in flight or done (a
	// channel that already holds its Ready works as well). Init waits for them
	// up to Wait; a pane whose listing has not landed shows "Loading…" and
	// takes it when it does (the sender must wake the loop: platform.Wake, which is safe before the window exists).
	Initial [2]<-chan Ready
	Wait    time.Duration // default 150 ms

	// Preference is which mode the window shows: the system's (the zero value),
	// or a forced light or dark one.
	Preference theme.Preference

	// Traffic makes the scene draw the traffic lights (offscreen only).
	Traffic bool
	// Mono sets the file list in Menlo (the theme's monospace font off macOS)
	// in 7.5 pt cells, as a terminal's, instead of the system font.
	Mono bool
	// Places are the sidebar's locations (nil: the home folder and the usual
	// folders in it that exist, and the root); RootTitle names the root
	// volume ("" looks for the startup volume's name).
	Places    []Place
	RootTitle string
	// Probe is how the places are looked for (default: the real file system,
	// on a worker, after the first frame).
	Probe Probe
	// ScrollLeft scrolls the left list 130 pt, under the toolbar, once it has
	// rows (pictures, and pilot -scrolled).
	ScrollLeft bool
	// Still holds animations still and Legacy pins the legacy scroll bars, for
	// pictures (an offscreen window has no system to follow).
	Still, Legacy bool
	// Loc is the time zone modification times are shown in (default time.Local).
	Loc *time.Location
}

// Pilot is the scene: both panes and the chrome. It implements app.Builder and
// app.Initer.
type Pilot struct {
	k  *widget.Kit
	bk *base.Kit // k's base kit, which rexui's components take
	// Traffic makes the scene draw the window's traffic lights (offscreen output).
	Traffic bool
	opt     Options
	sys     *sysIcons
	sym     symbols
	a       *app.App
	src     Source
	loc     *time.Location
	mac     bool
	win     bool // Windows: AltGr is Ctrl+Alt

	panes  [2]*pane
	active int
	split  widget.SplitState // the share of the width each pane gets

	// the window's chrome: the sidebar (shown or hidden), its places and
	// state, the disclosure of its groups, the dir of the active pane its
	// selection was last set for, and whether rows are under the toolbar
	sidebar    bool
	scrollDone bool // Options.ScrollLeft has been applied
	side       rexui.SidebarState
	places     []place
	items      []rexui.SidebarItem
	open       [2]bool
	sideFor    string
	tbH        float32 // the toolbar's height in pixels, as of the last frame
	rootTitle  string
	closeOnce  sync.Once
	probeStop  guard             // ends the places worker's publishing at Close
	probeWG    sync.WaitGroup    // the places worker, for whoever wants to know it ended
	probed     chan placesProbed // the worker's places, until they are taken
	wake       func()            // Window.Wake after Init; platform.Wake before it

	init     [2]<-chan Ready
	listedAt [2]int64
	atInit   [2]bool // the listing was there when Init finished
	res      []fs.Result

	geo         [2]geometry
	lastW       int
	lastH       int
	prevFocus   ui.Key // the focus key at the end of the previous frame
	syncedFocus ui.Key // the focus key syncFocus last saw: a change into a pane's list or field makes it active
	composing   bool   // an input method was composing in the previous frame

	// What the last Build did, for the script and the tests.
	counting                       bool
	frameRows                      int
	rowsBuilt, buildCalls, rowsMax int
}

// New returns a Pilot; Init finishes it once the window and text system exist.
func New(o Options) *Pilot {
	if o.Wait <= 0 {
		o.Wait = 150 * time.Millisecond
	}
	if o.Loc == nil {
		o.Loc = time.Local
	}
	pl := &Pilot{wake: platform.Wake, opt: o, src: o.Src, loc: o.Loc, mac: runtime.GOOS == "darwin", win: runtime.GOOS == "windows", init: o.Initial, sidebar: true}
	pl.rootTitle = o.RootTitle
	if pl.rootTitle == "" {
		pl.rootTitle = startupVolumeName() // (the worker looks for the real name)
	}
	pl.split = widget.NewSplitState(1, 1)
	pl.split.Min = [widget.MaxPanes]float32{minPanePt, minPanePt}
	pl.Traffic = o.Traffic
	for i := range pl.panes {
		pl.panes[i] = newPane(uint32(i+1), o.Src, o.Home)
		pl.panes[i].rootTitle = pl.rootTitle
		pl.panes[i].initColumns()
	}
	return pl
}

var _ app.Builder = (*Pilot)(nil)
var _ app.Initer = (*Pilot)(nil)

// Init implements app.Initer: the widget kit with the icons of the entry kinds,
// the box and run caches sized for scrolling through endless new names, and the
// first listings.
func (pl *Pilot) Init(a *app.App) error {
	pl.a = a
	if a.Win != nil {
		pl.wake = a.Win.Wake
		if src, ok := pl.src.(interface{ SetWake(func()) }); ok {
			src.SetWake(pl.wake)
		}
	}
	pl.k = widget.NewKit(a.UI, a.Text, look(pl.opt.Mono))
	pl.bk = pl.k.Kit
	pl.bk.SetPreference(pl.opt.Preference)
	pl.bk.Materials = a.Win != nil && a.Win.HasMaterials()
	pl.bk.ReduceMotion = pl.opt.Still
	if pl.opt.Legacy {
		pl.bk.PinSystem(&theme.SystemValues{Known: true, Settings: theme.Settings{Scroller: theme.ScrollerLegacy}})
	}
	pl.sys = newSysIcons(img.NewCache(img.Options{Renderer: a.R}), pl.wake)
	pl.bk.SetImages(pl.sys.cache)
	pl.addSymbols()
	pl.makePlaces()
	pl.startProbe()
	m, err := newMenuModel()
	if err != nil {
		return err
	}
	pl.bk.SetMenu(m) // the shortcuts, and the zoom the model's commands do
	a.SetMenu(m)     // the macOS menu bar
	a.UI.Reserve(4096)
	a.Text.ReserveRuns(8192, 128<<10, 512<<10)

	deadline := time.NewTimer(pl.opt.Wait)
	defer deadline.Stop()
	for i := range pl.init {
		ch := pl.init[i]
		if ch == nil {
			continue
		}
		select {
		case r := <-ch:
			pl.land(i, r)
		case <-deadline.C:
			// the rest keep loading: Build takes them when they land
			deadline.Reset(0)
		}
	}
	for i := range pl.atInit {
		pl.atInit[i] = pl.init[i] == nil
	}
	return nil
}

// ListedAtInit reports whether pane i's first listing was installed by Init, so
// that the first frame shows it.
func (pl *Pilot) ListedAtInit(i int) bool { return pl.atInit[i] }

// land installs an initial listing.
func (pl *Pilot) land(i int, r Ready) {
	pl.init[i] = nil
	pl.listedAt[i] = r.At
	p := pl.panes[i]
	if r.L.Err != nil {
		p.setDir(r.Dir)
		p.path.SetString(r.Dir)
		p.setMsg(errText(r.L.Err), true)
		return
	}
	p.setInitial(r.Dir, r.L)
}

// ListedAt is when pane i's first listing was finished (platform.Now), 0 if it
// has not landed.
func (pl *Pilot) ListedAt(i int) int64 { return pl.listedAt[i] }

// Close stops the Source and the workers (the icons', the style's). It may be
// called more than once.
func (pl *Pilot) Close() {
	pl.closeOnce.Do(func() {
		pl.probeStop.close() // (the places worker, if a probe is blocked, ends when it returns)
		if pl.src != nil {
			pl.src.Close()
		}
		if pl.sys != nil {
			pl.sys.close()
		}
	})
}

// poll takes the listings that landed and what the Source reports.
func (pl *Pilot) poll() {
	for i := range pl.init {
		if ch := pl.init[i]; ch != nil {
			select {
			case r := <-ch:
				pl.land(i, r)
			default:
			}
		}
	}
	if pl.src == nil {
		return
	}
	pl.res = pl.src.Poll(pl.res[:0])
	for k := range pl.res {
		r := &pl.res[k]
		if i := int(r.ID) - 1; i >= 0 && i < len(pl.panes) {
			pl.panes[i].onResult(r)
		} else if r.Listing != nil {
			pl.src.Release(r.Listing, r.Lease)
		}
	}
}

// Active is the index of the pane that has the keyboard.
func (pl *Pilot) Active() int { return pl.active }
