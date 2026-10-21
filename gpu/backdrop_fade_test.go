package gpu

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// checker is a w×h checkerboard of cell-pixel black and white squares.
func checker(w, h, cell int) []Quad {
	var qs []Quad
	for y := 0; y < h; y += cell {
		for x := 0; x < w; x += cell {
			c := uint32(0x000000FF)
			if (x/cell+y/cell)%2 == 0 {
				c = 0xFFFFFFFF
			}
			qs = append(qs, solid(float32(x), float32(y), float32(x+cell), float32(y+cell), c))
		}
	}
	return qs
}

// rowSpread is the standard deviation of row y's luma (0..1) over x0..x1.
func rowSpread(m img, y, x0, x1 int) float64 {
	var s, s2 float64
	for x := x0; x < x1; x++ {
		p := m.at(x, y)
		l := (0.2126*float64(p[0]) + 0.7152*float64(p[1]) + 0.0722*float64(p[2])) / 255
		s += l
		s2 += l * l
	}
	n := float64(x1 - x0)
	return math.Sqrt(max(s2/n-(s/n)*(s/n), 0))
}

// savePNG writes m to dir/name.png when dir is set (GNUI_BACKDROP_PNG), for
// review.
func savePNG(t *testing.T, m img, name string) {
	dir := os.Getenv("GNUI_BACKDROP_PNG")
	if dir == "" {
		return
	}
	o := image.NewRGBA(image.Rect(0, 0, m.w, m.h))
	copy(o.Pix, m.pix)
	os.MkdirAll(dir, 0o755)
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, o); err != nil {
		t.Fatal(err)
	}
}

// A backdrop that fades out at an edge, over a 3-px black and white
// checkerboard: its blur's strength (how much of a row's contrast it takes
// away) is full where the fade has not begun, falls monotonically along
// the fade, by no more than 4.5 / Fade a row (a smoothstep over the fade
// falls by at most 1.5 / Fade; the strip-built fade this replaces, by a
// quarter at once), and is 0 at its faded edge, where the pixels are the
// content's own (within a level) as they are beyond it: no band and no edge,
// across the whole width. For FadeTop as for FadeBottom, for a blur alone,
// the Messages frost (ThinFilm, tinted) and the Hard recipe. A renderer
// without the blur fades its fallback colour out over the same rows: the
// colour where the fade has not begun, the content at the faded edge, and
// the colour's share falling monotonically between.
func TestBackdropFade(t *testing.T) {
	r := newTestRenderer(t)
	const w, h, fade = 160, 120, 64
	card := checker(w, h, 3)
	under := renderList(r, w, h, black, &DrawList{Quads: card})
	for _, c := range []struct {
		name string
		b    Backdrop
	}{
		{"blur", Backdrop{Sigma: 12, Down: 2, Saturation: 1}},
		{"frost", Backdrop{Sigma: 12, Down: 2, Tint: 0xF2F2F7FF, Mix: 0.3, Saturation: 1.5, Add: 0.02, TintUnder: true}},
		{"hard", Backdrop{Sigma: 12, Down: 4, Tint: 0xFFFFFFFF, Mix: 0.8245, Saturation: 1, Add: 0.03}},
	} {
		for _, edge := range []FadeEdge{FadeBottom, FadeTop} {
			name := fmt.Sprintf("%s, %s", c.name, map[FadeEdge]string{FadeBottom: "bottom", FadeTop: "top"}[edge])
			t.Run(name, func(t *testing.T) {
				b := c.b
				b.Fade, b.FadeEdge = fade, edge
				// the region: rows 10..110 (bottom) or 10..110 (top), the
				// fade its last or first 64 rows
				q := solid(0, 10, w, 110, 0x808080FF)
				list := &DrawList{Quads: card}
				list.AddBackdrop(q, b)
				m := renderList(r, w, h, black, list)
				savePNG(t, m, "fade-"+c.name+"-"+map[FadeEdge]string{FadeBottom: "bottom", FadeTop: "top"}[edge])
				// rows in order from the full recipe to the faded edge and
				// beyond: 10..119 (bottom), 109..0 (top)
				row := func(i int) int {
					if edge == FadeTop {
						return 109 - i
					}
					return 10 + i
				}
				strength := func(y int) float64 {
					return 1 - rowSpread(m, y, 0, w)/rowSpread(under, y, 0, w)
				}
				if !r.Backdrops() {
					fallbackFade(t, m, under, row, fade)
					return
				}
				full := strength(row(0))
				if full < 0.9 {
					t.Errorf("the full recipe takes %.3f of the contrast, want at least 0.9", full)
				}
				prev, worst, at := full, 0.0, 0
				var prof []string
				for i := 0; i < 110; i++ {
					y := row(i)
					s := strength(y)
					if i < 100-fade && math.Abs(s-full) > 0.02 {
						t.Errorf("row %d, before the fade: strength %.3f, want %.3f", y, s, full)
					}
					if s > prev+0.005 {
						t.Errorf("row %d: strength rises from %.3f to %.3f", y, prev, s)
					}
					if d := prev - s; d > worst {
						worst, at = d, y
					}
					if i%4 == 0 {
						prof = append(prof, fmt.Sprintf("%.2f", s))
					}
					prev = s
				}
				t.Logf("%s: strength every 4 rows %v; the largest step %.4f at row %d (limit %.4f)", name, prof, worst, at, 4.5/fade)
				if worst > 4.5/fade {
					t.Errorf("the strength falls by %.4f at row %d, want at most %.4f a row", worst, at, 4.5/fade)
				}
				for i := 99; i < 110; i++ { // the faded edge and beyond it
					y := row(i)
					for x := 0; x < w; x++ {
						near(t, fmt.Sprintf("at the faded edge (%d, %d)", x, y), m.at(x, y), under.at(x, y), 1)
					}
				}
				if n := testing.AllocsPerRun(10, func() { r.RenderToRGBA(w, h, black, list, m.pix) }); n != 0 {
					t.Errorf("%.1f allocations per frame with a fading backdrop", n)
				}
			})
		}
	}
}

// fallbackFade checks a fading backdrop drawn as its fallback colour
// (#808080) over the checkerboard: the colour itself on the rows before
// the fade (row(0) .. row(100-fade-1)), the content at the faded edge
// (row(99)) and beyond, and the colour's share falling monotonically
// between.
func fallbackFade(t *testing.T, m, under img, row func(int) int, fade int) {
	t.Helper()
	share := func(y int) float64 { // the fallback's share of a white cell's pixel
		for x := 0; x < m.w; x++ {
			if under.at(x, y)[0] == 255 {
				return float64(255-m.at(x, y)[0]) / (255 - 128)
			}
		}
		return 0
	}
	prev := 1.0
	for i := 0; i < 110; i++ {
		y := row(i)
		s := share(y)
		if i < 100-fade && s < 0.995 {
			t.Errorf("row %d, before the fade: the fallback's share %.3f, want 1", y, s)
		}
		if i >= 99 && s > 0.005 {
			t.Errorf("row %d, at the faded edge or beyond: the fallback's share %.3f, want 0", y, s)
		}
		if s > prev+0.005 {
			t.Errorf("row %d: the fallback's share rises from %.3f to %.3f", y, prev, s)
		}
		prev = s
	}
}
