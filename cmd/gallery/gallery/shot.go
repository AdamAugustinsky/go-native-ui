package gallery

import (
	"errors"
	"fmt"
	"image/png"
	"os"

	"gonativeui/app"
	"gonativeui/theme"
	"gonativeui/ui"
)

// ShotOptions are the settings of Shot.
type ShotOptions struct {
	// Width, Height and Scale are the image's size in pixels and its pixels
	// per point (2560×1600 at 2 by default).
	Width, Height int
	Scale         float32
	Mode          theme.Mode
	// Scene is the state to render: one of SceneNames (main by default).
	Scene string
	// Rows is the table's row count (DefaultRows when zero).
	Rows int
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
		return 0, fmt.Errorf("gallery: no scene %q (have %v)", o.Scene, SceneNames())
	}
	g, err := New(Options{Rows: o.Rows, Mode: o.Mode, Traffic: true, Still: true})
	if err != nil {
		return 0, err
	}
	pl := newPlayer(g, sc.steps(), false, true)
	b := &scripted{Gallery: g, p: pl}
	off, err := app.NewOffscreen(Config(), o.Width, o.Height, o.Scale, b)
	if err != nil {
		return 0, err
	}
	defer off.Close()
	for n := 0; ; n++ {
		if _, more := off.Frame(); !more {
			break
		}
		if n > 20000 {
			return 0, errors.New("gallery: the scene's script did not end")
		}
	}
	if err := pl.Err(); err != nil {
		return 0, err
	}
	for n := 0; off.UI.Animating(); n++ { // let the crossfades and the scroll bars settle
		if n > 600 {
			return 0, errors.New("gallery: the scene did not settle")
		}
		off.Frame()
	}
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

// scripted is a Gallery with a script.
type scripted struct {
	*Gallery
	p *Player
}

func (s *scripted) Script(frame int, in *ui.Input) bool { return s.p.Script(frame, in) }
