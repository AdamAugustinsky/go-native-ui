//go:build darwin

package gpu

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"unsafe"

	"gonativeui/internal/cielab"
	"gonativeui/internal/objc"
)

// bdModel is the CPU model of a backdrop, step by step as backdrop_darwin.go
// computes it: the region's down×down block means (bilinear taps clamped to
// the region's pixel centres), a separable Gaussian with normalised edges,
// the recipe, and the bilinear composite, with 8-bit storage between the
// passes. It returns the composite at pixel (x, y) of the target.
type bdModel struct {
	x0, y0, d, nw, nh int
	f                 [][3]float64 // the last pass, nw×nh
}

func newBDModel(src img, q Quad, b Backdrop) *bdModel {
	x0, y0, x1, y1, _ := backdropRegion(&q, src.w, src.h)
	d := backdropDown(&b)
	m := &bdModel{x0: x0, y0: y0, d: d, nw: (x1 - x0 + d - 1) / d, nh: (y1 - y0 + d - 1) / d}
	q8 := func(v float64) float64 { return math.Round(math.Min(math.Max(v, 0), 1)*255) / 255 }
	px := func(x, y int) [3]float64 {
		x, y = min(max(x, 0), src.w-1), min(max(y, 0), src.h-1)
		p := src.at(x, y)
		return [3]float64{float64(p[0]) / 255, float64(p[1]) / 255, float64(p[2]) / 255}
	}
	bilinear := func(x, y float64) (c [3]float64) { // x, y: pixel-corner coordinates
		u, v := x-0.5, y-0.5
		i, j := math.Floor(u), math.Floor(v)
		fu, fv := u-i, v-j
		for _, s := range [4]struct {
			dx, dy int
			w      float64
		}{{0, 0, (1 - fu) * (1 - fv)}, {1, 0, fu * (1 - fv)}, {0, 1, (1 - fu) * fv}, {1, 1, fu * fv}} {
			p := px(int(i)+s.dx, int(j)+s.dy)
			for k := range c {
				c[k] += s.w * p[k]
			}
		}
		return c
	}
	n := max(d/2, 1)
	down := make([][3]float64, m.nw*m.nh)
	for j := 0; j < m.nh; j++ {
		for i := 0; i < m.nw; i++ {
			ox, oy := float64(x0+i*d), float64(y0+j*d)
			var acc [3]float64
			for b := 0; b < n; b++ {
				for a := 0; a < n; a++ {
					tx, ty := ox+float64(2*a+1), oy+float64(2*b+1)
					if d < 2 {
						tx, ty = ox+0.5, oy+0.5
					}
					tx = math.Min(math.Max(tx, float64(x0)+0.5), float64(x1)-0.5)
					ty = math.Min(math.Max(ty, float64(y0)+0.5), float64(y1)-0.5)
					c := bilinear(tx, ty)
					for k := range acc {
						acc[k] += c[k]
					}
				}
			}
			for k := range acc {
				down[j*m.nw+i][k] = q8(acc[k] / float64(n*n))
			}
		}
	}
	sigma := float64(b.Sigma) / float64(d)
	r := int(math.Ceil(3 * sigma))
	pass := func(in [][3]float64, dx, dy int) [][3]float64 {
		out := make([][3]float64, len(in))
		for j := 0; j < m.nh; j++ {
			for i := 0; i < m.nw; i++ {
				var acc [3]float64
				ws := 0.0
				for k := -r; k <= r; k++ {
					x, y := i+dx*k, j+dy*k
					if x < 0 || y < 0 || x >= m.nw || y >= m.nh {
						continue
					}
					w := math.Exp(-0.5 * float64(k*k) / math.Max(sigma*sigma, 1e-6))
					for c := range acc {
						acc[c] += w * in[y*m.nw+x][c]
					}
					ws += w
				}
				for c := range acc {
					out[j*m.nw+i][c] = acc[c] / ws
				}
			}
		}
		return out
	}
	h := pass(down, 1, 0)
	for i := range h {
		for k := range h[i] {
			h[i][k] = q8(h[i][k])
		}
	}
	v := pass(h, 0, 1)
	tint := [3]float64{float64(b.Tint>>24) / 255, float64(b.Tint>>16&0xFF) / 255, float64(b.Tint>>8&0xFF) / 255}
	mix := math.Min(math.Max(float64(b.Mix), 0), 1)
	clamp := func(v float64) float64 { return math.Min(math.Max(v, 0), 1) }
	for i := range v {
		c := v[i]
		if b.TintUnder {
			for k := range c {
				c[k] = c[k]*(1-mix) + tint[k]*mix
			}
		}
		l := 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
		for k := range c {
			c[k] = clamp(l + float64(b.Saturation)*(c[k]-l) + float64(b.Add))
		}
		if !b.TintUnder {
			for k := range c {
				c[k] = c[k]*(1-mix) + clamp(tint[k]+float64(b.Add))*mix
			}
		}
		for k := range c {
			v[i][k] = q8(c[k])
		}
	}
	m.f = v
	return m
}

// at is the composite at target pixel (x, y): the last pass sampled
// bilinearly at (p - origin) / down, clamped to the region's texels.
func (m *bdModel) at(x, y int) (c [3]int) {
	u := (float64(x)+0.5-float64(m.x0))/float64(m.d) - 0.5
	v := (float64(y)+0.5-float64(m.y0))/float64(m.d) - 0.5
	i, j := math.Floor(u), math.Floor(v)
	fu, fv := u-i, v-j
	var acc [3]float64
	for _, s := range [4]struct {
		dx, dy int
		w      float64
	}{{0, 0, (1 - fu) * (1 - fv)}, {1, 0, fu * (1 - fv)}, {0, 1, (1 - fu) * fv}, {1, 1, fu * fv}} {
		tx, ty := min(max(int(i)+s.dx, 0), m.nw-1), min(max(int(j)+s.dy, 0), m.nh-1)
		for k := range acc {
			acc[k] += s.w * m.f[ty*m.nw+tx][k]
		}
	}
	for k := range c {
		c[k] = int(math.Round(math.Min(math.Max(acc[k], 0), 1) * 255))
	}
	return c
}

// pattern is a w×h test card of saturated stripes, a checkerboard, a
// gradient and thin lines, as quads.
func pattern(w, h int) []Quad {
	var qs []Quad
	cols := [...]uint32{0xFF2020FF, 0x20C040FF, 0x2040FFFF, 0xFFD000FF, 0x101010FF, 0xF0F0F0FF}
	for x := 0; x < w; x += 3 {
		qs = append(qs, solid(float32(x), 0, float32(x+3), float32(h/3), cols[(x/3)%len(cols)]))
	}
	for y := h / 3; y < 2*h/3; y += 4 {
		for x := 0; x < w; x += 4 {
			c := uint32(0x000000FF)
			if (x/4+y/4)%2 == 0 {
				c = 0xFFFFFFFF
			}
			qs = append(qs, solid(float32(x), float32(y), float32(x+4), float32(y+4), c))
		}
	}
	g := Quad{Rect: [4]float32{0, float32(2 * h / 3), float32(w), float32(h)}, Color: [4]uint32{0xFF0080FF, 0x00FFFFFF, 0x202020FF, 0xFFFF00FF}}
	qs = append(qs, g)
	for x := 5; x < w; x += 17 {
		qs = append(qs, solid(float32(x), 0, float32(x+1), float32(h), 0x000000FF))
	}
	return qs
}

// The blur's output on a test card matches the CPU model of the same steps
// within 2 levels (and a mean under half a level), for AppKit's two recipes
// and a saturation that leaves the range before the tint (3 levels), on a
// region that is not a multiple of the scale, cut by a clip. And the pixels
// around it, and a quad drawn after it, are untouched.
func TestBackdropMatchesModel(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 220, 90
	for _, c := range []struct {
		name string
		b    Backdrop
		tol  int // levels
	}{
		{"hard", Backdrop{Sigma: 12, Down: 4, Tint: 0xFFFFFFFF, Mix: 0.8245, Saturation: 1, Add: 0.03}, 2},
		{"hard, dark", Backdrop{Sigma: 12, Down: 4, Tint: 0x1E1E1EFF, Mix: 0.8245, Saturation: 1, Add: 0.03}, 2},
		{"thin film", Backdrop{Sigma: 12, Down: 2, Tint: 0x1E1E1EFF, Mix: 0.6, Saturation: 1.25, Add: 0.03, TintUnder: true}, 2},
		{"no tint", Backdrop{Sigma: 5, Down: 4, Saturation: 1}, 2},
		{"saturated past 1, then tinted", Backdrop{Sigma: 3, Down: 2, Tint: 0x808080FF, Mix: 0.5, Saturation: 1.5, Add: 0.03}, 3}, // the saturation amplifies the passes' rounding
		{"no blur, full scale", Backdrop{Down: 1, Saturation: 0.5}, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			card := pattern(w, h)
			under := renderList(r, w, h, black, &DrawList{Quads: card})
			q := solid(13, 7, 203, 81, 0x00FF00FF)
			q.Clip = [4]float32{0, 0, 201, 90}
			list := &DrawList{Quads: card}
			list.AddBackdrop(q, c.b)
			list.Add(solid(100, 40, 104, 44, 0xFF00FFFF))
			m := renderList(r, w, h, black, list)
			model := newBDModel(under, q, c.b)
			worst, sum, n := 0, 0, 0
			var at [2]int
			for y := 7; y < 81; y++ {
				for x := 13; x < 201; x++ {
					if x >= 100 && x < 104 && y >= 40 && y < 44 {
						continue
					}
					got, want := m.at(x, y), model.at(x, y)
					for k := 0; k < 3; k++ {
						d := got[k] - want[k]
						if d < 0 {
							d = -d
						}
						sum += d
						n++
						if d > worst {
							worst, at = d, [2]int{x, y}
						}
					}
				}
			}
			mean := float64(sum) / float64(n)
			t.Logf("%s: max |GPU - model| %d levels at %v (%v vs %v), mean %.3f", c.name, worst, at, m.at(at[0], at[1]), model.at(at[0], at[1]), mean)
			if worst > c.tol || mean > 0.5 {
				t.Errorf("%s: the blur differs from the model by up to %d levels (mean %.2f), want at most %d (0.5)", c.name, worst, mean, c.tol)
			}
			for _, p := range [][2]int{{12, 30}, {201, 30}, {50, 6}, {50, 81}, {0, 0}, {219, 89}} {
				near(t, fmt.Sprintf("outside at %v", p), m.at(p[0], p[1]), under.at(p[0], p[1]), 0)
			}
			near(t, "the quad after it", m.at(101, 41), [4]int{255, 0, 255, 255}, 0)
		})
	}
}

// A backdrop's corner radii cut it as they cut a shape: the content shows
// in the corner, the blur inside.
func TestBackdropRadii(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 64, 64
	card := pattern(w, h)
	under := renderList(r, w, h, black, &DrawList{Quads: card})
	list := &DrawList{Quads: card}
	q := solid(8, 8, 56, 56, 0)
	q.Radii = [4]float32{16, 16, 16, 16}
	b := Backdrop{Sigma: 6, Down: 2, Tint: 0x808080FF, Mix: 1, Saturation: 1}
	list.AddBackdrop(q, b)
	m := renderList(r, w, h, black, list)
	near(t, "the corner", m.at(9, 9), under.at(9, 9), 0)
	near(t, "inside", m.at(32, 32), [4]int{128, 128, 128, 255}, 1)
}

// Into a target that cannot be sampled (a framebuffer-only drawable's
// usage), a backdrop is its fallback colour, as on a renderer without the
// blur.
func TestBackdropUnreadableTarget(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 32, 16
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)
	d := objc.Send4(clsTexDesc, rsTexDesc, mtlBGRA8Unorm, w, h, 0)
	objc.Send1(d, rsSetUsage, 4)   // RenderTarget only
	objc.Send1(d, rsSetStorage, 0) // Shared
	target := objc.Send1(r.device, rsNewTexture, uintptr(d))
	defer objc.Release(target)
	if readable(uintptr(target)) {
		t.Fatal("a render-target-only texture reads as readable")
	}
	list := &DrawList{}
	list.Add(solid(0, 0, 16, 16, 0xFF0000FF))
	list.Add(solid(16, 0, 32, 16, 0x0000FFFF))
	list.AddBackdrop(solid(4, 4, 28, 12, 0x00FF00FF), Backdrop{Sigma: 4, Down: 2, Saturation: 1})
	cb := objc.Send(r.queue, rsCommandBuf)
	r.Encode(uintptr(cb), uintptr(target), w, h, black, list)
	objc.Send(cb, rsCommit)
	objc.Send(cb, rsWait)
	out := make([]byte, w*h*4)
	argRegion(objc.F.Reset().Arg(uintptr(target)).Arg(uintptr(rsGetBytes)).
		Arg(uintptr(unsafe.Pointer(&out[0]))).Arg(w*4), 0, 0, w, h).Arg(0).Call(objc.MsgSend)
	m := img{w, h, out} // BGRA
	near(t, "the fallback", m.at(15, 8), [4]int{0, 255, 0, 255}, 0)
	near(t, "beside it", m.at(1, 1), [4]int{0, 0, 255, 255}, 0) // red, as BGRA
}

// glassProbe is GlassProbe's captures (docs/research/glass-over-metal.md),
// outside the repository: GNUI_GLASS_PROBE, or ~/Projects/research/glass-probe.
func glassProbe(t *testing.T) string {
	dir := os.Getenv("GNUI_GLASS_PROBE")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, "Projects", "research", "glass-probe")
	}
	if _, err := os.Stat(filepath.Join(dir, "native", "content-light.png")); err != nil {
		t.Skip("no GlassProbe captures (GNUI_GLASS_PROBE):", err)
	}
	return dir
}

// loadPNG reads a PNG's rows y0..y1 (all: y1 0) as RGBA8.
func loadPNG(t *testing.T, path string, y0, y1 int) img {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	p, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	b := p.Bounds()
	if y1 == 0 {
		y1 = b.Dy()
	}
	o := image.NewRGBA(image.Rect(0, 0, b.Dx(), y1-y0))
	draw.Draw(o, o.Bounds(), p, image.Pt(b.Min.X, b.Min.Y+y0), draw.Src)
	return img{b.Dx(), y1 - y0, o.Pix}
}

// AppKit's two scroll-edge recipes (macOS 27.2) at 2x over a window colour
// bg: Hard (a quarter of the backing scale, σ 6 pt, the colour plus 0.03 at
// 82.45 % over the blur plus 0.03) and ThinFilm (half scale, the colour at
// 50 % / 60 % under the blur, saturation 1.25, plus 0.03; its blur's
// falloff not modelled: a constant 6 pt).
func appKitHard(bg uint32) Backdrop {
	return Backdrop{Sigma: 12, Down: 4, Tint: bg, Mix: 0.8245, Saturation: 1, Add: 0.03}
}

func appKitThin(bg uint32, mix float32) Backdrop {
	return Backdrop{Sigma: 12, Down: 2, Tint: bg, Mix: mix, Saturation: 1.25, Add: 0.03, TintUnder: true}
}

// The drawn recipes over a table's rows against AppKit's own pockets over
// the same pixels (GlassProbe's content scrolled 100 pt under a 52-pt band,
// text under it, or 160 pt, its blue selection; the Mac mini, 27.2
// 26B5101f; ΔE2000 over the band from x 90 pt to the width less 10, rows 2
// to 52 pt): Hard, the titlebar's pocket over our Metal layer, is within 1
// on average and 2 at the 99th percentile, light and dark, text or
// selection under it; ThinFilm, a plain NSScrollView's, within 1 and 2.5.
// Needs the probe's captures (glassProbe); skipped without them.
func TestBackdropMatchesAppKit(t *testing.T) {
	dir := glassProbe(t)
	r := newTestRenderer(t)
	for _, c := range []struct {
		ref          string
		theme        string
		scroll       int
		b            Backdrop
		mean, p99max float64
	}{
		{"native/none-light-inactive-scrolled.png", "light", 100, appKitHard(0xFFFFFFFF), 1, 2},
		{"native/none-dark-inactive-scrolled.png", "dark", 100, appKitHard(0x1E1E1EFF), 1, 2},
		{"native/none-light-inactive-scrolled:160.png", "light", 160, appKitHard(0xFFFFFFFF), 1, 2},
		{"native/none-dark-inactive-scrolled:160.png", "dark", 160, appKitHard(0x1E1E1EFF), 1, 2},
		{"ref/ref-light-active-scrolled.png", "light", 100, appKitThin(0xFFFFFFFF, 0.5), 1, 2.5},
		{"ref/ref-dark-active-scrolled.png", "dark", 100, appKitThin(0x1E1E1EFF, 0.6), 1, 2.5},
	} {
		content := loadPNG(t, filepath.Join(dir, "native", "content-"+c.theme+".png"), (c.scroll-52)*2, c.scroll*2)
		ref := loadPNG(t, filepath.Join(dir, c.ref), 0, 104)
		tex := r.NewTexture(TexRGBA8, content.w, content.h)
		r.Upload(tex, 0, 0, content.w, content.h, content.pix, content.w*4)
		w, h := float32(content.w), float32(content.h)
		list := &DrawList{}
		list.Add(Quad{Rect: [4]float32{0, 0, w, h}, UV: [4]float32{0, 0, w, h}, Mode: ModeRGBA, Tex: tex,
			Color: [4]uint32{0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF}})
		list.AddBackdrop(solid(0, 0, w, h, c.b.Tint), c.b)
		m := renderList(r, content.w, content.h, black, list)
		r.FreeTexture(tex)
		var d []float64
		for y := 4; y < 104; y++ {
			for x := 180; x < content.w-20; x++ {
				g, a := m.at(x, y), ref.at(x, y)
				d = append(d, cielab.DeltaE2000(cielab.SRGBToLab(uint8(g[0]), uint8(g[1]), uint8(g[2])),
					cielab.SRGBToLab(uint8(a[0]), uint8(a[1]), uint8(a[2]))))
			}
		}
		sort.Float64s(d)
		mean := 0.0
		for _, v := range d {
			mean += v
		}
		mean /= float64(len(d))
		p99, mx := d[len(d)*99/100], d[len(d)-1]
		t.Logf("%s, %d pt scrolled: ΔE2000 from AppKit's mean %.2f, p99 %.2f, max %.2f", c.ref, c.scroll, mean, p99, mx)
		if mean > c.mean || p99 > c.p99max {
			t.Errorf("%s: ΔE2000 mean %.2f, p99 %.2f from AppKit's pocket, want at most %.1f, %.1f", c.ref, mean, p99, c.mean, c.p99max)
		}
	}
}
