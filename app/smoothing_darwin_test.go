//go:build darwin

package app

import (
	"image"
	"testing"

	"gonativeui/text"
	"gonativeui/ui"
)

// smoothRows draws the same string four times at 2x: smoothed and with
// ui.NoFontSmoothing, dark on light and light on dark.
type smoothRows struct {
	a    *App
	font text.FontKey
}

const (
	smoothStr = "Body — The quick brown fox 0123"
	rowH      = 40 // points; the rows are drawn at 2x
)

// The rows, top to bottom: text colour, background, flags.
var smoothCases = [...]struct {
	fg, bg uint32
	flags  ui.Flags
}{
	{0x000000FF, 0xFFFFFFFF, 0},
	{0x000000FF, 0xFFFFFFFF, ui.NoFontSmoothing},
	{0xFFFFFFFF, 0x000000FF, 0},
	{0xFFFFFFFF, 0x000000FF, ui.NoFontSmoothing},
}

func (b *smoothRows) Init(a *App) error { b.a = a; return nil }

func (b *smoothRows) Build(c *ui.Context) {
	if b.font == 0 {
		b.font = b.a.Text.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: c.Scale()})
	}
	c.PushFont(b.font)
	k := c.Scale() // ui positions and sizes are in pixels
	for i, r := range smoothCases {
		c.NextPos(0, float32(i*rowH)*k)
		c.NextSize(ui.Px(400*k), ui.Px(rowH*k))
		c.NextBG(r.bg)
		c.BoxKeyed(ui.DrawBackground|ui.FloatingX|ui.FloatingY, ui.KeyN(c.ParentKey(), uint64(2*i)), nil)
		c.NextPos(10*k, float32(i*rowH+10)*k)
		c.NextSize(ui.TextSize(0), ui.TextSize(0))
		c.NextTextColor(r.fg)
		c.BoxKeyed(ui.DrawText|ui.FloatingX|ui.FloatingY|r.flags, ui.KeyN(c.ParentKey(), uint64(2*i+1)), []byte(smoothStr))
	}
	c.PopFont()
}

// rowInk returns the coverage of row i (from the pixels' distance to the
// background, as a fraction of the text-to-background distance) and the
// mean width of the vertical stems crossed by the row's x-height line, in
// pixels: a measure of stroke weight that the pen's subpixel phase does not
// change.
func rowInk(img *image.RGBA, i int, fg, bg uint32) (ink, stem float64) {
	const scale = 2
	y0, y1 := i*rowH*scale, (i+1)*rowH*scale
	w := img.Bounds().Dx()
	fgv, bgv := float64(fg>>24), float64(bg>>24)
	cov := func(x, y int) float64 {
		v := (float64(img.Pix[y*img.Stride+4*x]) - bgv) / (fgv - bgv)
		return min(max(v, 0), 1)
	}
	prof := make([]float64, y1-y0)
	mx := 0.0
	for y := y0; y < y1; y++ {
		for x := 0; x < w; x++ {
			c := cov(x, y)
			ink += c
			prof[y-y0] += c
		}
		mx = max(mx, prof[y-y0])
	}
	b0, b1 := -1, -1
	for y, p := range prof {
		if p > 0.45*mx {
			if b0 < 0 {
				b0 = y
			}
			b1 = y
		}
	}
	mid := y0 + (b0+b1)/2
	n, run, in := 0, 0.0, false
	for x := 0; x < w; x++ {
		if c := cov(x, mid); c > 0.02 {
			run += c
			in = true
		} else if in {
			if run > 1 && run < 4.5 {
				stem += run
				n++
			}
			run, in = 0, false
		}
	}
	if n > 0 {
		stem /= float64(n)
	}
	return
}

// TestNoFontSmoothing renders the rows and checks, from the pixels, that the
// default text is smoothed (stems thicker, more so for light text, as
// CoreGraphics does by the text colour) and that ui.NoFontSmoothing draws the
// outline weight: thinner stems, and the same coverage whatever the colour.
func TestNoFontSmoothing(t *testing.T) {
	if !smoothingOn() {
		t.Skip("font smoothing is off in this user's settings (AppleFontSmoothing = 0)")
	}
	o, err := NewOffscreen(Config{}, 800, len(smoothCases)*rowH*2, 2, &smoothRows{})
	if err != nil {
		skipOrFail(t, "offscreen rendering", err)
	}
	defer o.Close()
	var img *image.RGBA
	for i := 0; i < 3; i++ {
		img, _ = o.Frame()
	}
	var ink, stem [len(smoothCases)]float64
	for i, r := range smoothCases {
		ink[i], stem[i] = rowInk(img, i, r.fg, r.bg)
		t.Logf("text #%08X on #%08X, flags %#x: coverage %.0f, stems %.3f px", r.fg, r.bg, uint32(r.flags), ink[i], stem[i])
		if ink[i] == 0 || stem[i] == 0 {
			t.Fatalf("row %d has no text", i)
		}
	}
	// Measured on macOS 27.2 for 13 pt SF at 2x: stems 2.23 px unsmoothed,
	// 2.58 smoothed dark on light, 2.91 light on dark.
	for _, p := range [][2]int{{0, 1}, {2, 3}} {
		if d := stem[p[0]] - stem[p[1]]; d < 0.2 {
			t.Errorf("rows %d/%d: smoothing thickens stems by %.3f px, want >= 0.2", p[0], p[1], d)
		}
		if r := ink[p[0]] / ink[p[1]]; r < 1.1 {
			t.Errorf("rows %d/%d: smoothing adds %.1f %% ink, want >= 10 %%", p[0], p[1], 100*(r-1))
		}
	}
	if stem[2] < stem[0]+0.15 {
		t.Errorf("light text stems %.3f px, dark %.3f: smoothing should thicken light text more", stem[2], stem[0])
	}
	// Unsmoothed coverage does not depend on the colour.
	if r := ink[3] / ink[1]; r < 0.99 || r > 1.01 {
		t.Errorf("unsmoothed coverage light %.0f, dark %.0f: the opt-out still depends on the colour", ink[3], ink[1])
	}
}

// smoothingOn says whether the text system smooths at all here: SmoothLevel
// is 0 for every colour only when the user turned smoothing off.
func smoothingOn() bool { return text.SmoothLevel(0x000000FF) != 0 }
