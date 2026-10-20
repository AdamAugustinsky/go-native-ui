package app

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/ui"
)

// The ui material areas are handed to the platform by value: the enums
// must agree.
func TestMaterialAreasAgree(t *testing.T) {
	for _, c := range []struct {
		u ui.MaterialArea
		p platform.MaterialArea
	}{
		{ui.AreaWindow, platform.AreaWindow}, {ui.AreaSidebar, platform.AreaSidebar},
		{ui.AreaInspector, platform.AreaInspector}, {ui.AreaTitlebar, platform.AreaTitlebar},
		{ui.AreaToolbarCapsule, platform.AreaToolbarCapsule}, {ui.AreaComposer, platform.AreaComposer},
		{ui.AreaMenu, platform.AreaMenu}, {ui.AreaPopover, platform.AreaPopover}, {ui.AreaHUD, platform.AreaHUD},
	} {
		if uint8(c.u) != uint8(c.p) {
			t.Errorf("ui area %d is platform area %d", c.u, c.p)
		}
	}
	if platform.NumMaterialAreas != platform.AreaHUD+1 {
		t.Error("a platform area has no ui counterpart")
	}
}

// fakeShell records what the glue hands the window.
type fakeShell struct {
	mats, hosts int
	lastMat     []platform.MaterialRegion
	lastHost    []platform.HostRegion
	focus       []platform.HostID
}

func (f *fakeShell) SetMaterials(r []platform.MaterialRegion) {
	f.mats++
	f.lastMat = append(f.lastMat[:0], r...)
}

func (f *fakeShell) SetHosts(r []platform.HostRegion) {
	f.hosts++
	f.lastHost = append(f.lastHost[:0], r...)
}

func (f *fakeShell) FocusHost(id platform.HostID) { f.focus = append(f.focus, id) }

// shellApp is an App whose builder marks a sidebar material and a host.
type shellApp struct {
	a        *App
	in       ui.Input
	hostX    float32
	focusBox bool
}

// noText is a shaper for frames without text.
type noText struct{ ui.Shaper }

func (noText) AtlasGeneration() uint32 { return 0 }
func (noText) RunGeneration() uint32   { return 0 }
func (noText) Pages() int              { return 0 }

func newShellApp() *shellApp {
	return &shellApp{a: &App{appObject: &appObject{}, UI: ui.New(noText{}), scale: 2}, hostX: 300}
}

func (s *shellApp) frame() {
	c := s.a.UI
	c.Begin(&s.in, 800, 600, 2, 1.0/60)
	c.NextSize(ui.Px(200), ui.Px(600))
	c.NextPos(0, 0)
	c.Material(c.Box(ui.FloatingX|ui.FloatingY, "side"), ui.AreaSidebar, 4, 0)
	c.NextSize(ui.Px(100), ui.Px(40))
	c.NextPos(s.hostX, 100)
	c.NextPad(0)
	c.Push(c.Box(ui.FloatingX|ui.FloatingY, "slot"))
	c.NextSize(ui.Px(100), ui.Px(40))
	c.Host(5, 1, ui.HostFocusable)
	c.Pop()
	c.End(&s.a.list)
	s.in.ClearFrame()
}

// The glue sends the regions once and again only when they change (or when
// the appearance serial moves: Reduce Transparency may have changed), and
// gives the keyboard to a host ui focused unless the window says it has it.
func TestShellSync(t *testing.T) {
	s := newShellApp()
	f := &fakeShell{}
	s.frame()
	s.a.syncShellTo(f, 1)
	if f.mats != 1 || f.hosts != 1 || len(f.lastMat) != 1 || f.lastMat[0].Area != platform.AreaSidebar || f.lastMat[0].Rect != [4]float32{0, 0, 200, 600} {
		t.Fatalf("first frame: %d material and %d host sends, materials %+v", f.mats, f.hosts, f.lastMat)
	}
	if len(f.lastHost) != 1 || f.lastHost[0].ID != 5 || f.lastHost[0].Rect != [4]float32{300, 100, 400, 140} || f.lastHost[0].Hidden {
		t.Fatalf("hosts %+v: want host 5 at 300,100–400,140", f.lastHost)
	}
	s.frame()
	s.a.syncShellTo(f, 1)
	if f.mats != 1 || f.hosts != 1 {
		t.Errorf("an unchanged frame sent %d materials and %d hosts lists, want none", f.mats-1, f.hosts-1)
	}
	allocs := testing.AllocsPerRun(20, func() { s.a.syncShellTo(f, 1) })
	if allocs != 0 {
		t.Errorf("an unchanged sync allocates %.1f times", allocs)
	}
	s.a.syncShellTo(f, 2)
	if f.mats != 2 || f.hosts != 1 {
		t.Errorf("after an appearance change: %d material sends (want 2), %d host sends (want 1)", f.mats, f.hosts)
	}
	s.hostX = 320
	s.frame()
	s.a.syncShellTo(f, 2)
	if f.hosts != 2 || f.mats != 2 || f.lastHost[0].Rect[0] != 320 {
		t.Errorf("a moved host: %d host sends at x %v, %d material sends", f.hosts, f.lastHost[0].Rect[0], f.mats)
	}

	// ui focuses the host's box: the window gives the native view the
	// keyboard, once.
	s.a.UI.FocusHostBox(5)
	s.frame()
	s.a.syncShellTo(f, 2)
	s.frame()
	s.a.syncShellTo(f, 2)
	if len(f.focus) != 1 || f.focus[0] != 5 {
		t.Errorf("FocusHost calls %v, want [5]", f.focus)
	}
	// The native view gives the keyboard back to the app's view
	// (EvHostFocus 0): ui's focus leaves the box, and the window is not
	// asked again.
	s.a.hostFocus(0)
	s.frame()
	s.a.syncShellTo(f, 2)
	if len(f.focus) != 1 || s.a.UI.FocusedHost() != 0 {
		t.Errorf("after EvHostFocus 0: FocusHost calls %v, ui focus on host %d", f.focus, s.a.UI.FocusedHost())
	}
	// The user clicks into the native view (EvHostFocus 5): ui follows,
	// and the window, which did it, is not asked to.
	s.a.hostFocus(5)
	s.frame()
	s.a.syncShellTo(f, 2)
	if len(f.focus) != 1 || s.a.UI.FocusedHost() != 5 {
		t.Errorf("after EvHostFocus 5: FocusHost calls %v, ui focus on host %d", f.focus, s.a.UI.FocusedHost())
	}
}

// Offscreen, where no native view exists, each visible host is a grey
// placeholder in the draw list.
func TestOffscreenHostPlaceholder(t *testing.T) {
	s := newShellApp()
	s.frame()
	n := len(s.a.list.Quads)
	s.a.drawPlaceholders()
	if len(s.a.list.Quads) != n+1 {
		t.Fatalf("%d quads added, want one placeholder", len(s.a.list.Quads)-n)
	}
	q := s.a.list.Quads[n]
	if q.Rect != [4]float32{300, 100, 400, 140} || q.Color[0] != placeholderFill || q.Mode != gpu.ModeShape {
		t.Errorf("placeholder %+v: want the host's rectangle in the placeholder grey", q)
	}
}

// A scroll a host hands back reaches the scroll container around the host:
// the pointer left ui when it went onto the host (EvPointerLeave), and the
// scroll's own position (FlagPos) finds the container. Nothing is
// hovered meanwhile.
func TestHostScrollReachesItsContainer(t *testing.T) {
	c := ui.New(noText{})
	tr := newTranslator()
	var list ui.ID
	var l gpu.DrawList
	build := func() {
		c.Begin(&tr.in, 800, 600, 2, 1.0/60)
		c.NextSize(ui.Px(300), ui.Px(200))
		c.NextPos(100, 100)
		c.NextPad(0)
		list = c.Box(ui.ScrollY|ui.Clip|ui.Hoverable|ui.FloatingX|ui.FloatingY, "page")
		c.Push(list)
		c.NextSize(ui.Px(300), ui.Px(40))
		c.Box(0, "above")
		c.NextSize(ui.Px(200), ui.Px(60))
		c.Host(5, 1, 0)
		c.NextSize(ui.Px(300), ui.Px(800))
		c.Box(0, "below")
		c.Pop()
		c.End(&l)
		tr.endFrame()
	}
	build()
	build()
	tr.event(&platform.Event{Kind: platform.EvPointerMove, X: 150, Y: 120}, nil)
	build()
	if c.HotKey() == 0 {
		t.Fatal("the page is not hovered with the pointer over it")
	}
	tr.event(&platform.Event{Kind: platform.EvPointerLeave}, nil) // onto the host at 100,140–300,200
	tr.event(&platform.Event{Kind: platform.EvScroll, DY: -30, Flags: platform.FlagPrecise | platform.FlagPos, X: 150, Y: 160}, nil)
	build()
	if _, y := c.Scroll(list); y != 30 {
		t.Errorf("the page scrolled to %v by a scroll its host handed back, want 30", y)
	}
	if c.HotKey() != 0 {
		t.Error("a scroll over the host hovers a box")
	}
	// A scroll the host handed back without its position (as before) is lost.
	tr.event(&platform.Event{Kind: platform.EvScroll, DY: -30, Flags: platform.FlagPrecise}, nil)
	build()
	if _, y := c.Scroll(list); y != 30 {
		t.Errorf("a scroll with no position moved the page to %v", y)
	}
}

// A trackpad gesture that begins over a host, which hands it back, latches
// to the scroll container around the host and keeps it while the pointer
// moves over another scroll box and back over the host.
func TestHostGestureLatchesItsContainer(t *testing.T) {
	c := ui.New(noText{})
	tr := newTranslator()
	var page, inner ui.ID
	var l gpu.DrawList
	build := func() {
		c.Begin(&tr.in, 800, 600, 2, 1.0/60)
		c.NextSize(ui.Px(300), ui.Px(300))
		c.NextPos(100, 100)
		c.NextPad(0)
		page = c.Box(ui.ScrollY|ui.Clip|ui.FloatingX|ui.FloatingY, "page")
		c.Push(page)
		c.NextSize(ui.Px(200), ui.Px(60))
		c.Host(5, 1, 0) // 100,100–300,160
		c.NextSize(ui.Px(300), ui.Px(100))
		c.NextPad(0)
		inner = c.Box(ui.ScrollY|ui.Clip, "inner") // 100,160–400,260
		c.Push(inner)
		c.NextSize(ui.Px(300), ui.Px(1000))
		c.Box(0, "inner content")
		c.Pop()
		c.NextSize(ui.Px(300), ui.Px(1000))
		c.Box(0, "below")
		c.Pop()
		c.End(&l)
		tr.endFrame()
	}
	build()
	build()
	tr.event(&platform.Event{Kind: platform.EvPointerLeave}, nil) // the pointer is over the host
	ev := func(x, y, dy float32, ph platform.ScrollPhase) {
		tr.event(&platform.Event{Kind: platform.EvScroll, DY: dy, Phase: ph, X: x, Y: y,
			Flags: platform.FlagPrecise | platform.FlagPos}, nil)
	}
	ev(150, 130, -10, platform.PhaseBegan) // handed back by the host
	build()
	tr.event(&platform.Event{Kind: platform.EvPointerMove, X: 150, Y: 200}, nil)
	ev(150, 200, -10, platform.PhaseChanged) // over the inner box now
	build()
	ev(150, 130, -10, platform.PhaseChanged) // back over the host
	ev(150, 130, 0, platform.PhaseEnded)
	build()
	if _, y := c.Scroll(page); y != 30 {
		t.Errorf("the page scrolled to %v, want 30 (all three moves)", y)
	}
	if _, y := c.Scroll(inner); y != 0 {
		t.Errorf("the inner box scrolled to %v: the gesture is the page's", y)
	}
}
