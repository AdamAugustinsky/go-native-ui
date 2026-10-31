package rex

import (
	"testing"

	"gonativeui/gpu"
	"gonativeui/theme"
)

// A pane whose fill is opaque declares it a cover, and what it draws from
// there paints every pixel of the cover opaque: drawn alone over a clear
// target, those quads leave no pixel of it with an alpha under 255. A
// translucent pane (over a backdrop, not focused) declares none.
func TestOpaquePanesCoverWhatIsUnderThem(t *testing.T) {
	for _, scale := range []float32{1, 2} {
		g := newRig(t, int(900*scale), int(560*scale), scale)
		for _, mode := range []theme.Mode{theme.Light, theme.Dark} {
			for _, bd := range []bool{false, true} {
				for _, focus := range [][2]float32{{1, 0}, {0, 1}, {0.5, 0.25}} {
					r := Theme().Resolve(g.ts, scale, mode)
					r.Backdrop = bd
					s := &scene{r: &r, focus: focus, buttons: theme.ButtonsCaption, title: "A window title"}
					g.settle(s)
					want := 2
					if bd {
						want = 0 // a translucent fill over the material
						for _, f := range focus {
							if f >= 1 {
								want++
							}
						}
					}
					if len(g.list.Covers) != want {
						t.Errorf("scale %g mode %v backdrop %v focus %v: %d covers, want %d", scale, mode, bd, focus, len(g.list.Covers), want)
						continue
					}
					pix := make([]byte, g.w*g.h*4)
					for _, cv := range g.list.Covers {
						end := cv.At
						for end < len(g.list.Quads) && g.list.Quads[end].Mode != gpu.ModeBackdrop {
							end++
						}
						l := gpu.DrawList{Quads: g.list.Quads[cv.At:end], Backdrops: g.list.Backdrops}
						g.r.RenderToRGBA(g.w, g.h, [4]float32{}, &l, pix)
						for y := int(cv.Rect[1]); y < int(cv.Rect[3]); y++ {
							for x := int(cv.Rect[0]); x < int(cv.Rect[2]); x++ {
								if a := pix[(y*g.w+x)*4+3]; a != 255 {
									t.Fatalf("scale %g mode %v backdrop %v focus %v: pixel %d,%d of cover %v has alpha %d", scale, mode, bd, focus, x, y, cv.Rect, a)
								}
							}
						}
					}
				}
			}
		}
	}
}
