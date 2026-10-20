package demo

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"time"

	"gonativeui/app"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// ShotOptions describe a picture Shot makes.
type ShotOptions struct {
	// Scene is the window.
	Scene Scene
	// Scale is the pixels per point (2 by default); the picture is the
	// scene's window size at that scale.
	Scale float32
	// Preference is the mode; System renders the appearance in System
	// (light when it is not set: an offscreen window has no system).
	Preference theme.Preference
	System     ui.Appearance
	// Inactive renders the window as not key.
	Inactive bool
	// Scrolled renders the content scrolled under the toolbar.
	Scrolled bool
	// Legacy renders legacy scroll bars (overlay ones otherwise).
	Legacy bool
}

// shot is the demo with the window state an offscreen run lacks.
type shot struct {
	*Demo
	sys      ui.Appearance
	inactive bool
}

func (s *shot) Script(frame int, in *ui.Input) bool {
	s.c.SetSystemAppearance(s.sys)
	in.Focused = !s.inactive
	in.Time = widgettest.Clock(frame) // a clock, so that the overlay scroll bars fade
	return true
}

// render runs the scene offscreen until it is at rest and returns the window
// with its last frame.
func render(o ShotOptions) (*app.Offscreen, *Demo, *image.RGBA, error) {
	if o.Scale <= 0 {
		o.Scale = 2
	}
	d := New(Options{Scene: o.Scene, Preference: o.Preference, Traffic: true, Still: true, Scrolled: o.Scrolled, Legacy: o.Legacy})
	return renderDemo(d, o)
}

func renderDemo(d *Demo, o ShotOptions) (*app.Offscreen, *Demo, *image.RGBA, error) {
	if o.Scale <= 0 {
		o.Scale = 2
	}
	cfg := Config(o.Scene)
	w, h := int(cfg.Width*o.Scale+0.5), int(cfg.Height*o.Scale+0.5)
	off, err := app.NewOffscreen(cfg, w, h, o.Scale, &shot{Demo: d, sys: o.System, inactive: o.Inactive})
	if err != nil {
		return nil, nil, nil, err
	}
	settled := false
	for n := 0; n < 600 && !settled; n++ {
		off.Frame()
		// past the scroll bars' fade, the file icons decoded
		settled = n > 90 && !off.UI.Animating() && !d.iconsPending
		if d.iconsPending {
			time.Sleep(time.Millisecond)
		}
	}
	if !settled {
		d.Close()
		off.Close()
		return nil, nil, nil, errors.New("chromedemo: the scene did not settle")
	}
	img, _ := off.Frame()
	return off, d, img, nil
}

// Shot renders the scene offscreen at rest, with the traffic lights drawn by
// the toolbar, and writes the PNG to path. It returns the frame's quads.
func Shot(path string, o ShotOptions) (quads int, err error) {
	off, d, img, err := render(o)
	if err != nil {
		return 0, err
	}
	defer off.Close()
	defer d.Close()
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return 0, fmt.Errorf("chromedemo: write %s: %w", path, err)
	}
	return len(off.Quads()), f.Close()
}

// Node is one element of the scene's semantic tree, its frame in window
// points, as tools/appkitscenes reports AppKit's.
type Node struct {
	Role  string
	Name  string
	Frame [4]float32 // x, y, w, h
}

// Measure renders the scene at rest and returns the frames of its toolbar
// items, title, sidebar rows and path components.
func Measure(o ShotOptions) ([]Node, error) {
	off, d, _, err := render(o)
	if err != nil {
		return nil, err
	}
	defer off.Close()
	defer d.Close()
	if o.Scale <= 0 {
		o.Scale = 2
	}
	var t ui.SemTree
	d.c.Semantics(&t)
	var out []Node
	for i := range t.Nodes {
		n := &t.Nodes[i]
		switch n.Role {
		case ui.RoleButton, ui.RoleGroup, ui.RoleRadioGroup, ui.RoleRadio, ui.RoleComboBox, ui.RoleSearchField,
			ui.RoleTextField, ui.RoleLabel, ui.RoleListItem, ui.RoleTab, ui.RoleTabList, ui.RoleList:
		default:
			continue
		}
		b := n.Bounds
		s := o.Scale
		out = append(out, Node{Role: widgettest.RoleName(n.Role), Name: string(t.Text(n.Name)),
			Frame: [4]float32{b[0] / s, b[1] / s, (b[2] - b[0]) / s, (b[3] - b[1]) / s}})
	}
	return out, nil
}
