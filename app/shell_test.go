package app

import (
	"testing"

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
	mats    int
	lastMat []platform.MaterialRegion
}

func (f *fakeShell) SetMaterials(r []platform.MaterialRegion) {
	f.mats++
	f.lastMat = append(f.lastMat[:0], r...)
}

// shellApp is an App whose builder marks a sidebar material.
type shellApp struct {
	a     *App
	in    ui.Input
	sideW float32
}

// noText is a shaper for frames without text.
type noText struct{ ui.Shaper }

func (noText) AtlasGeneration() uint32 { return 0 }
func (noText) RunGeneration() uint32   { return 0 }
func (noText) Pages() int              { return 0 }

func newShellApp() *shellApp {
	return &shellApp{a: &App{UI: ui.New(noText{}), scale: 2}, sideW: 200}
}

func (s *shellApp) frame() {
	c := s.a.UI
	c.Begin(&s.in, 800, 600, 2, 1.0/60)
	c.NextSize(ui.Px(s.sideW), ui.Px(600))
	c.NextPos(0, 0)
	c.Material(c.Box(ui.FloatingX|ui.FloatingY, "side"), ui.AreaSidebar, 4, 0)
	c.End(&s.a.list)
	s.in.ClearFrame()
}

// The glue sends the regions once and again only when they change, or when
// the appearance serial moves (Reduce Transparency may have changed).
func TestShellSync(t *testing.T) {
	s := newShellApp()
	f := &fakeShell{}
	s.frame()
	s.a.syncShellTo(f, 1)
	if f.mats != 1 || len(f.lastMat) != 1 || f.lastMat[0].Area != platform.AreaSidebar || f.lastMat[0].Rect != [4]float32{0, 0, 200, 600} {
		t.Fatalf("first frame: %d material sends, materials %+v", f.mats, f.lastMat)
	}
	s.frame()
	s.a.syncShellTo(f, 1)
	if f.mats != 1 {
		t.Errorf("an unchanged frame sent %d materials lists, want none", f.mats-1)
	}
	allocs := testing.AllocsPerRun(20, func() { s.a.syncShellTo(f, 1) })
	if allocs != 0 {
		t.Errorf("an unchanged sync allocates %.1f times", allocs)
	}
	s.a.syncShellTo(f, 2)
	if f.mats != 2 {
		t.Errorf("after an appearance change: %d material sends, want 2", f.mats)
	}
	s.sideW = 240
	s.frame()
	s.a.syncShellTo(f, 2)
	if f.mats != 3 || f.lastMat[0].Rect[2] != 240 {
		t.Errorf("a wider sidebar: %d material sends, right edge %v", f.mats, f.lastMat[0].Rect[2])
	}
}
