package rexui_test

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"gonativeui/app"
	"gonativeui/rexui"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

// surfaceScene draws each floating surface's box (a menu, a dialog's sheet,
// a toast, a tooltip-sized chip) at its lift over one backdrop: what the lift
// scale is tuned by (D108). The boxes are built as the components build
// theirs (NextSurface, the toast's lift), without their content, so that
// only the edge and the shadows are looked at.
type surfaceScene struct {
	k      *base.Kit
	mode   theme.Mode
	strict bool
	bd     int // 0: the window, 1: the surface's own colour, 2: the other mode's window
}

func (s *surfaceScene) Init(a *app.App) error {
	th := rex.Theme()
	th.StrictAppKit = s.strict
	s.k = base.NewKit(a.UI, a.Text, th)
	s.k.SetMode(s.mode)
	s.k.ReduceMotion = true
	a.UI.Reserve(1024)
	a.Text.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
	return nil
}

// surfaceBoxes are the boxes of a surface scene, in points: x, y, w, h,
// radius (0: the menu's), and the lift.
var surfaceBoxes = [...]struct {
	x, y, w, h, r float32
	lift          theme.Lift
}{
	{24, 24, 200, 132, 0, theme.LiftMenu},
	{256, 24, 300, 132, 26, theme.LiftDialog},
	{24, 196, 300, 56, 10, theme.LiftToast},
	{356, 196, 120, 22, 6, theme.LiftMenu},
}

func (s *surfaceScene) Build(c *ui.Context) {
	k := s.k
	k.Begin(c)
	r := k.Theme()
	other := theme.Measured[1-int(s.mode.T())][theme.Window]
	bg := [...]uint32{r.Colors[theme.Window] | 0xFF, r.Colors[theme.Raised] | 0xFF, other | 0xFF}[s.bd]
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG(bg)
	c.Push(c.Box(base.Floating|ui.DrawBackground, "bg"))
	for i, b := range surfaceBoxes {
		theme.At(c, k.Pt(b.x), k.Pt(b.y), k.Pt(b.w), k.Pt(b.h))
		c.NextBG(r.Colors[theme.Raised] | 0xFF)
		flags := rexui.NextSurface(k, k.Pt(b.r), b.lift)
		if b.lift == theme.LiftToast && r.StrictAppKit { // a toast's strict look: ElevRaised's shadow and a Separator hairline
			sh := r.Elevations[theme.ElevRaised].Shadows[0]
			c.NextShadow(ui.Shadow{OffY: sh.Y, Sigma: sh.Blur, Color: sh.Color(r.Colors[theme.Shadow], r.T)})
			c.NextBorder(r.Colors[theme.Separator], r.Lines.Hairline)
		}
		c.BoxKeyed(base.Floating|ui.DrawBackground|flags, ui.KeyN(1, uint64(i)), nil)
	}
	c.Pop()
	k.End()
}

// TestSurfaceShots writes the surfaces at their lifts over a light, a dark
// and a backdrop of their own colour, strict and default, and 4× crops of
// the menu's and the dialog's bottom-left corner, to $GNUI_SURFACE_SHOTS
// (skipped without it): the pictures the lift scale was tuned by.
func TestSurfaceShots(t *testing.T) {
	dir := os.Getenv("GNUI_SURFACE_SHOTS")
	if dir == "" {
		t.Skip("GNUI_SURFACE_SHOTS is not set")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	names := [...]string{"window", "own", "other"}
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		for bd := range names {
			for _, strict := range []bool{true, false} {
				s := &surfaceScene{mode: m, strict: strict, bd: bd}
				rg := widgettest.Open(t, app.Config{Title: "Surfaces", Width: 580, Height: 280}, 580*2, 280*2, 2, s)
				rg.Frames(3)
				img := rg.Image()
				look := "default"
				if strict {
					look = "strict"
				}
				mode := "light"
				if m == theme.Dark {
					mode = "dark"
				}
				name := mode + "-" + names[bd] + "-" + look
				writePNG(t, filepath.Join(dir, name+".png"), img)
				// the menu's corner (24,156) and the dialog's (256,156), 4×
				writePNG(t, filepath.Join(dir, name+"-menu-corner.png"), zoom(img, image.Rect(2*24-24, 2*156-56, 2*24+56, 2*156+24), 4))
				writePNG(t, filepath.Join(dir, name+"-dialog-corner.png"), zoom(img, image.Rect(2*256-24, 2*156-56, 2*256+56, 2*156+24), 4))
				writePNG(t, filepath.Join(dir, name+"-toast-corner.png"), zoom(img, image.Rect(2*24-24, 2*252-56, 2*24+56, 2*252+24), 4))
			}
		}
	}
}

func zoom(src *image.RGBA, r image.Rectangle, n int) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, r.Dx()*n, r.Dy()*n))
	for y := 0; y < r.Dy()*n; y++ {
		for x := 0; x < r.Dx()*n; x++ {
			p := image.Pt(r.Min.X+x/n, r.Min.Y+y/n)
			if p.In(src.Bounds()) {
				out.Set(x, y, src.At(p.X, p.Y))
			} else {
				out.Set(x, y, color.Black)
			}
		}
	}
	return out
}

func writePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
