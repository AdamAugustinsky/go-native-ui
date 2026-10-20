//go:build darwin

package live

import (
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/platform"
	"gonativeui/text"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/base"
	"gonativeui/widget/widgettest"
)

// The dilation check of D38 with the hosted NSTextView: the same 13 pt text
// in the same colour on the same background, drawn by ui in one row and by the
// editor below it, captured with GnuiShot; the strokes' widths must agree.
// D38 adopted AppKit's font smoothing from an NSTextField label; this checks
// it against the composer's own view, in light and dark.

const dilationText = "minimum illumination in humming mills"

type dilationApp struct {
	k        *widget.Kit
	a        *app.App
	ed       *base.NativeEditor
	dark     bool
	drawnID  ui.ID
	editorID ui.ID
	font     text.FontKey
	scale    float32
}

func (p *dilationApp) Init(a *app.App) error {
	p.a = a
	p.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	ed, err := base.NewNativeEditor(a.Win, platform.EditorOptions{})
	if err != nil {
		return err
	}
	p.ed = ed
	return ed.SetText([]byte(dilationText))
}

func (p *dilationApp) colors() (fg, bg uint32) {
	if p.dark {
		return 0xFFFFFFFF, 0x1E1E1EFF // NSColor.textColor in dark, a dark window
	}
	return 0x000000FF, 0xFFFFFFFF
}

func (p *dilationApp) Build(c *ui.Context) {
	k := p.k
	k.Begin(c)
	if s := c.Scale(); s != p.scale {
		p.scale = s
		p.font = p.a.Text.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: s})
	}
	fg, bg := p.colors()
	c.NextBG(bg)
	k.NextPad(20)
	k.NextGap(16)
	k.BeginContainer(ui.AxisY, ui.CrossStart, "page", ui.Pct(1), ui.Pct(1), ui.DrawBackground)
	k.Row("drawn")
	p.drawnID = k.Text(0, dilationText, p.font, fg, ui.AlignLeft, 24*c.Scale())
	k.EndRow()
	k.NextWidth(ui.Pct(1))
	p.editorID = k.NativeEditor(p.ed, "editor").ID
	k.EndContainer()
	k.End()
}

func TestEditorDilation(t *testing.T) {
	if !widgettest.CaptureEnabled() {
		t.Skip("captures are off: GNUI_GNUISHOT does not name GnuiShot's shot.sh")
	}
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			p := &dilationApp{dark: dark}
			l := widgettest.StartLive(t, app.Config{Title: "dilation", Width: 420, Height: 160}, p)
			defer l.Do(func(*app.App) { p.ed.Release(); widgettest.ResetAppAppearance() })
			l.Do(func(*app.App) { widgettest.SetAppAppearance(dark) })
			l.Frames(20)
			var drawn, ed [4]float32
			var w, h int
			l.Do(func(a *app.App) {
				drawn, ed = a.UI.Rect(p.drawnID), a.UI.Rect(p.editorID)
				ww, hh, _ := a.Size()
				w, h = int(ww), int(hh)
			})
			path := widgettest.Capture(t, l, "dilation-"+name)
			img := readRGBA(t, path)
			dy := img.Bounds().Dy() - h // the titlebar above the content
			if dx := img.Bounds().Dx() - w; dx != 0 || dy < 0 {
				t.Fatalf("capture %v for content %d×%d", img.Bounds(), w, h)
			}
			fg, bg := p.colors()
			sd := stems(img, drawn, dy, fg, bg)
			se := stems(img, ed, dy, fg, bg)
			t.Logf("%s: stems drawn by ui %.3f px, by the NSTextView %.3f px (%s)", name, sd, se, path)
			if sd == 0 || se == 0 {
				t.Fatalf("no stems measured: ui %.3f, NSTextView %.3f", sd, se)
			}
			if d := math.Abs(sd - se); d > 0.15 {
				t.Errorf("%s: the NSTextView's stems differ from ours by %.3f px (> 0.15): a weight difference", name, d)
			}
		})
	}
}

func readRGBA(t *testing.T, path string) *image.RGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	rgba := image.NewRGBA(m.Bounds())
	draw.Draw(rgba, rgba.Bounds(), m, m.Bounds().Min, draw.Src)
	return rgba
}

// stems is the mean width in pixels of the vertical strokes crossed by the
// middle of the x-height band of the text in rectangle r (content pixels; dy
// is the titlebar's height in the image), as app/smoothing_darwin_test.go
// measures: coverage from the red channel's distance between bg and fg.
func stems(img *image.RGBA, r [4]float32, dy int, fg, bg uint32) float64 {
	x0, x1 := int(r[0]), int(r[2])
	y0, y1 := int(r[1])+dy, int(r[3])+dy
	fgv, bgv := float64(fg>>24), float64(bg>>24)
	cov := func(x, y int) float64 {
		v := (float64(img.Pix[y*img.Stride+4*x]) - bgv) / (fgv - bgv)
		return min(max(v, 0), 1)
	}
	prof := make([]float64, y1-y0)
	mx := 0.0
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			prof[y-y0] += cov(x, y)
		}
		mx = max(mx, prof[y-y0])
	}
	b0, b1 := -1, -1
	for y, v := range prof {
		if v > 0.45*mx {
			if b0 < 0 {
				b0 = y
			}
			b1 = y
		}
	}
	if b0 < 0 {
		return 0
	}
	mid := y0 + (b0+b1)/2
	n, run, in, sum := 0, 0.0, false, 0.0
	for x := x0; x < x1; x++ {
		if c := cov(x, mid); c > 0.02 {
			run += c
			in = true
		} else if in {
			if run > 1 && run < 4.5 {
				sum += run
				n++
			}
			run, in = 0, false
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}
