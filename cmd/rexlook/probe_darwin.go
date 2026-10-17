//go:build darwin

package main

import (
	"gonativeui/app"
	"gonativeui/cmd/rexlook/rex"
	"gonativeui/ui"
)

// probe runs the scene for a few frames in a real window and then asks AppKit
// and our drag map, for points of the titlebar band, who gets a press there.
type probe struct {
	*rex.Rex
	a *app.App
	n int
}

func (p *probe) Init(a *app.App) error { p.a = a; return p.Rex.Init(a) }

func (p *probe) Build(c *ui.Context) {
	p.Rex.Build(c)
	c.RequestFrame() // keep frames coming until the probe has run
	if p.n++; p.n == 8 {
		p.report()
		p.a.Quit()
	}
}

func (p *probe) report() {
	_, _, scale := p.a.Size()
	type point struct {
		Name       string
		XPt, YPt   float32
		HitView    string // class of the view AppKit's hit test finds there
		IsOurView  bool
		WindowDrag bool // our drag map: a press here moves the window
	}
	W := float32(rex.WindowWidthPt)
	var out []point
	for _, q := range []struct {
		name string
		x, y float32
	}{
		{"empty band, between the traffic lights and the host block", 90, 23},
		{"empty band, left of the capsule", 195, 23},
		{"empty band, right of the capsule", W - 180, 23},
		{"empty band, far right edge", W - 2, 6},
		{"band above the tab capsule", 600, 3},
		{"tab 1", 330, 23},
		{"tab 2", 600, 23},
		{"+ button", W - 23, 23},
		{"⌘ button", W - 57, 23},
		{"host block text", 130, 22},
		{"close traffic light", 21, 22},
		{"zoom traffic light", 69, 22},
		{"just below the band", 195, 47},
		{"pane body", 300, 300},
	} {
		px, py := q.x*scale, q.y*scale
		out = append(out, point{q.name, q.x, q.y, p.a.Win.DebugHitTest(px, py), p.a.Win.DebugIsContentView(px, py), p.a.Win.DebugDragAt(px, py)})
	}
	printJSON(struct {
		Points             []point
		DoubleClickSetting string // AppleActionOnDoubleClick ("" = unset: zoom)
	}{out, p.a.Win.DebugDoubleClickSetting()})
}

func runProbe() error {
	_, err := app.RunWith(rex.Config(), &probe{Rex: rex.NewApp()}, app.RunOptions{})
	return err
}
