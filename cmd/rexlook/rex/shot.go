package rex

import (
	"errors"
	"fmt"

	"gonativeui/app"
	"gonativeui/cmd/rexlook/rex/rexrig"
	"gonativeui/theme"
	"gonativeui/ui"
)

// ShotOptions describe a picture RenderShot makes.
type ShotOptions struct {
	// Width and Height are the size in pixels, Scale the pixels per point (the
	// reference is 2114×1278 at 2×).
	Width, Height int
	Scale         float32
	// Corners makes the window's superellipse corners transparent.
	Corners bool
	// Preference is the mode to render. System renders the appearance in
	// System, which is light when it is not set: an offscreen window has no
	// system of its own.
	Preference theme.Preference
	System     ui.Appearance
}

// shotRex is the scene with the system appearance an offscreen window lacks.
type shotRex struct {
	*Rex
	sys ui.Appearance
}

func (s *shotRex) Build(c *ui.Context) {
	c.SetSystemAppearance(s.sys)
	s.Rex.Build(c)
}

// RenderShot renders the initial state of the scene offscreen, through the
// same app.Builder the window runs, with the traffic lights drawn by the scene
// and, with o.Corners, the window's superellipse corners as transparency. The
// first frame is already in the mode asked for. It writes a PNG and
// path+".metrics.json" for tools/rexcompare, and returns the number of quads
// and draw calls. It runs on every OS the renderer does.
func RenderShot(path string, o ShotOptions) (quads, drawCalls int, err error) {
	x := NewApp()
	x.Traffic = true
	x.SetPreference(o.Preference)
	w, h := o.Width, o.Height
	off, err := app.NewOffscreen(Config(), w, h, o.Scale, &shotRex{Rex: x, sys: o.System})
	if err != nil {
		return 0, 0, err
	}
	defer off.Close()
	settled := false
	for n := 0; n < 600 && !settled; n++ {
		off.Frame()
		settled = !off.UI.Animating()
	}
	if !settled {
		return 0, 0, errors.New("rex: the scene did not settle")
	}
	img, _ := off.Frame() // one more, now at rest: the same image
	mode := "light"
	if x.th.Dark() {
		mode = "dark"
	}
	meta := map[string]any{
		"cellAdvancePt": x.CellAdvancePt(),
		"rowPitchPt":    x.RowPitchPt(),
		"monoFace":      x.MonoFace(),
		"monoSizePt":    x.MonoSizePt(),
		"quads":         len(off.Quads()),
		"drawCalls":     off.R.DrawCalls(),
		"mode":          mode,
		"note":          "layout geometry of the cell grid: advance of the monospace font in use, row pitch of the grid",
	}
	if err := rexrig.WritePNG(path, img.Pix, w, h, o.Corners, meta); err != nil {
		return 0, 0, fmt.Errorf("rex: write %s: %w", path, err)
	}
	return len(off.Quads()), off.R.DrawCalls(), nil
}
