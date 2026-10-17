package rex

import (
	"errors"
	"fmt"

	"gonativeui/app"
	"gonativeui/cmd/rexlook/rex/rexrig"
)

// RenderShot renders the initial state of the scene offscreen, through the
// same app.Builder the window runs, at w×h pixels and the given scale (the
// reference is 2114×1278 at 2×), with the traffic lights drawn by the scene
// and, with corners, the window's superellipse corners as transparency. It
// writes a PNG and path+".metrics.json" for tools/rexcompare, and returns the
// number of quads and draw calls. It runs on every OS the renderer does.
func RenderShot(path string, w, h int, scale float32, corners bool) (quads, drawCalls int, err error) {
	x := NewApp()
	x.Traffic = true
	o, err := app.NewOffscreen(Config(), w, h, scale, x)
	if err != nil {
		return 0, 0, err
	}
	defer o.Close()
	settled := false
	for n := 0; n < 600 && !settled; n++ {
		o.Frame()
		settled = !o.UI.Animating()
	}
	if !settled {
		return 0, 0, errors.New("rex: the scene did not settle")
	}
	img, _ := o.Frame() // one more, now at rest: the same image
	meta := map[string]any{
		"cellAdvancePt": x.CellAdvancePt(),
		"rowPitchPt":    x.RowPitchPt(),
		"monoFace":      x.MonoFace(),
		"monoSizePt":    x.MonoSizePt(),
		"quads":         len(o.Quads()),
		"drawCalls":     o.R.DrawCalls(),
		"note":          "layout geometry of the cell grid: advance of the monospace font in use, row pitch of the grid",
	}
	if err := rexrig.WritePNG(path, img.Pix, w, h, corners, meta); err != nil {
		return 0, 0, fmt.Errorf("rex: write %s: %w", path, err)
	}
	return len(o.Quads()), o.R.DrawCalls(), nil
}
