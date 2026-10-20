package gallery

import (
	"errors"
	"fmt"
	"image/png"
	"os"

	"gonativeui/app"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// ShotOptions are the settings of Shot.
type ShotOptions struct {
	// Width, Height and Scale are the image's size in pixels and its pixels
	// per point (2560×1600 at 2 by default).
	Width, Height int
	Scale         float32
	// Preference is the mode to render; System renders the appearance in
	// System (light when it is not set).
	Preference theme.Preference
	// System is the system appearance the scene sees (the zero value: not known,
	// so light).
	System ui.Appearance
	// Scene is the state to render: one of SceneNames (main by default).
	Scene string
	// Rows is the table's row count (DefaultRows when zero).
	Rows int
	// Legacy renders legacy scroll bars (overlay ones otherwise).
	Legacy bool
	// Inactive renders the window as not key.
	Inactive bool
	// Scrolled renders the content scrolled under the toolbar.
	Scrolled bool
}

// SceneNames lists the scenes Shot renders: the whole window, each kind of
// popup, the dialog and a tooltip.
func SceneNames() []string {
	n := make([]string, len(scenes))
	for i := range scenes {
		n[i] = scenes[i].name
	}
	return n
}

// Shot renders the gallery offscreen in a scene and writes the PNG to path. It
// returns the number of quads of the frame.
func Shot(path string, o ShotOptions) (quads int, err error) {
	off, err := runScene(o)
	if err != nil {
		return 0, err
	}
	defer off.Close()
	img, _ := off.Frame()
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return 0, fmt.Errorf("gallery: write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	return len(off.Quads()), nil
}

// runScene opens the offscreen window of Shot and plays the scene to its
// settled state; the caller closes the window.
func runScene(o ShotOptions) (*app.Offscreen, error) {
	if o.Width <= 0 || o.Height <= 0 {
		o.Width, o.Height = 2*WindowWidthPt, 2*WindowHeightPt
	}
	if o.Scale <= 0 {
		o.Scale = 2
	}
	if o.Scene == "" {
		o.Scene = "main"
	}
	var sc *scene
	for i := range scenes {
		if scenes[i].name == o.Scene {
			sc = &scenes[i]
		}
	}
	if sc == nil {
		return nil, fmt.Errorf("gallery: no scene %q (have %v)", o.Scene, SceneNames())
	}
	g, err := New(Options{Rows: o.Rows, Preference: o.Preference, Traffic: true, Still: true, Synthetic: true, Legacy: o.Legacy, Scrolled: o.Scrolled})
	if err != nil {
		return nil, err
	}
	pl := widgettest.NewPlayer(g, sc.steps(), widgettest.Options{Synthetic: true})
	b := &scripted{Gallery: g, p: pl, sys: o.System, inactive: o.Inactive}
	off, err := app.NewOffscreen(Config(), o.Width, o.Height, o.Scale, b)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*app.Offscreen, error) { off.Close(); return nil, err }
	for n := 0; ; n++ {
		if _, more := off.Frame(); !more {
			break
		}
		if n > 20000 {
			return fail(errors.New("gallery: the scene's script did not end"))
		}
	}
	if err := pl.Err(); err != nil {
		return fail(err)
	}
	for n := 0; off.UI.Animating(); n++ { // let the crossfades and the scroll bars settle
		if n > 600 {
			return fail(errors.New("gallery: the scene did not settle"))
		}
		off.Frame()
	}
	return off, nil
}

// scripted is a Gallery with a script.
type scripted struct {
	*Gallery
	p        *widgettest.Player
	sys      ui.Appearance // what the system says, in every frame (an offscreen run has no system)
	inactive bool          // the window is not key
}

func (s *scripted) Script(frame int, in *ui.Input) bool {
	s.Gallery.c.SetSystemAppearance(s.sys)
	more := s.p.Script(frame, in)
	if s.inactive {
		in.Focused = false
	}
	return more
}
