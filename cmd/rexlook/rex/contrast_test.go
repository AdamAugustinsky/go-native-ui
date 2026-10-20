package rex

import (
	"math"
	"testing"

	"gonativeui/gpu"
	"gonativeui/ui"
)

// The titlebar's text over the glass (draw.go, labelAlphas): every glyph
// keeps AppKit's contrast floors over the glass's measured range, with its
// background composed from the frame's own quads as the GPU shades them.

// The glass behind the titlebar, measured from screen captures on the
// MacBook (27.2, docs/design/native-shell-spike.md): the band's grey with a
// light (#FFFFFF) and a dark (#1E1E1E) window behind, key or not, widened a
// little.
var glassGrey = [2][2]float64{{150, 255}, {38, 115}} // light, dark: min, max

// AppKit's contrast floors (labelAlphas' comment): active text, and the rest
// or anything in a window that is not key; light, dark.
var floorActive, floorInactive = [2]float64{3.95, 5.84}, [2]float64{1.98, 2.25}

func srgbLum(c [3]float64) float64 {
	var l float64
	for i, w := range [3]float64{0.2126, 0.7152, 0.0722} {
		v := c[i] / 255
		if v <= 0.04045 {
			v /= 12.92
		} else {
			v = math.Pow((v+0.055)/1.055, 2.4)
		}
		l += w * v
	}
	return l
}

func contrast(a, b [3]float64) float64 {
	la, lb := srgbLum(a), srgbLum(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// The shader's arithmetic at one pixel centre (gpu/shaders_darwin.go,
// fs_main), in Go: premultiplied RGBA, 0..1.

type rgba [4]float64

func unpack(c uint32) rgba {
	a := float64(c&255) / 255
	return rgba{float64(c>>24) / 255 * a, float64(c>>16&255) / 255 * a, float64(c>>8&255) / 255 * a, a}
}

func (c rgba) scale(k float64) rgba { return rgba{c[0] * k, c[1] * k, c[2] * k, c[3] * k} }

func mixC(a, b rgba, t float64) rgba {
	return rgba{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t, a[2] + (b[2]-a[2])*t, a[3] + (b[3]-a[3])*t}
}

func clamp01(v float64) float64 { return min(1, max(0, v)) }

func rrectSDF(px, py float64, r, radii [4]float32) float64 {
	cx, cy := float64(r[0]+r[2])/2, float64(r[1]+r[3])/2
	hx, hy := float64(r[2]-r[0])/2, float64(r[3]-r[1])/2
	qx, qy := px-cx, py-cy
	var rad float32
	switch {
	case qx < 0 && qy < 0:
		rad = radii[0]
	case qx < 0:
		rad = radii[3]
	case qy < 0:
		rad = radii[1]
	default:
		rad = radii[2]
	}
	rr := min(float64(rad), min(hx, hy))
	dx, dy := math.Abs(qx)-hx+rr, math.Abs(qy)-hy+rr
	return math.Hypot(max(dx, 0), max(dy, 0)) + min(max(dx, dy), 0) - rr
}

func erf2(x float64) float64 {
	s, a := math.Copysign(1, x), math.Abs(x)
	v := 1 + (0.278393+(0.230389+0.078108*(a*a))*a)*a
	v *= v
	return s - s/(v*v)
}

func gaussian(x, sigma float64) float64 {
	return math.Exp(-(x*x)/(2*sigma*sigma)) / (2.50662827463 * sigma)
}

func shadowX(x, y, sigma, corner, hx, hy float64) float64 {
	delta := min(hy-corner-math.Abs(y), 0)
	curved := hx - corner + math.Sqrt(max(0, corner*corner-delta*delta))
	return (0.5 + 0.5*erf2((x+curved)*(0.70710678/sigma))) - (0.5 + 0.5*erf2((x-curved)*(0.70710678/sigma)))
}

func rrectShadow(px, py float64, r, radii [4]float32, sigma float64) float64 {
	cx, cy := float64(r[0]+r[2])/2, float64(r[1]+r[3])/2
	hx, hy := float64(r[2]-r[0])/2, float64(r[3]-r[1])/2
	px, py = px-cx, py-cy
	var rad float32
	switch {
	case px < 0 && py < 0:
		rad = radii[0]
	case px < 0:
		rad = radii[3]
	case py < 0:
		rad = radii[1]
	default:
		rad = radii[2]
	}
	rr := min(float64(rad), min(hx, hy))
	lo, hi := py-hy, py+hy
	start, end := min(max(-3*sigma, lo), hi), min(max(3*sigma, lo), hi)
	step := (end - start) * 0.25
	y, v := start+step*0.5, 0.0
	for range 4 {
		v += shadowX(px, py-y, sigma, rr, hx, hy) * gaussian(y, sigma) * step
		y += step
	}
	return clamp01(v)
}

// shade is what quad q puts at pixel centre (px, py) for its fill (a mask
// quad: its colour at full ink).
func shade(q *gpu.Quad, px, py float64) rgba {
	w, h := max(float64(q.Rect[2]-q.Rect[0]), 1e-3), max(float64(q.Rect[3]-q.Rect[1]), 1e-3)
	tx, ty := clamp01((px-float64(q.Rect[0]))/w), clamp01((py-float64(q.Rect[1]))/h)
	col := mixC(mixC(unpack(q.Color[0]), unpack(q.Color[1]), tx), mixC(unpack(q.Color[2]), unpack(q.Color[3]), tx), ty)
	var res rgba
	if q.Mode == gpu.ModeShape {
		if q.Soft > 0 {
			res = col.scale(rrectShadow(px, py, q.Rect, q.Radii, float64(q.Soft)))
		} else {
			sd := rrectSDF(px, py, q.Rect, q.Radii)
			outer := clamp01(0.5 - sd)
			if q.Border > 0 {
				col = mixC(unpack(q.BorderColor), col, clamp01(0.5-(sd+float64(q.Border))))
			}
			res = col.scale(outer)
		}
	} else {
		cov := 1.0
		if q.Radii != [4]float32{} {
			cov = clamp01(0.5 - rrectSDF(px, py, q.Rect, q.Radii))
		}
		res = col.scale(cov)
	}
	if q.Clip != [4]float32{} {
		c := q.Clip
		res = res.scale(clamp01(px-float64(c[0])+0.5) * clamp01(py-float64(c[1])+0.5) * clamp01(float64(c[2])-px+0.5) * clamp01(float64(c[3])-py+0.5))
	}
	if q.FadeX != [2]float32{} {
		res = res.scale(clamp01((float64(q.FadeX[1]) - px) / max(float64(q.FadeX[1]-q.FadeX[0]), 1e-4)))
	}
	return res
}

// blend puts premultiplied c over the opaque sRGB bg (0..255), as the
// layer's One / OneMinusSrcAlpha blending does.
func blend(c rgba, bg [3]float64) [3]float64 {
	return [3]float64{c[0]*255 + (1-c[3])*bg[0], c[1]*255 + (1-c[3])*bg[1], c[2]*255 + (1-c[3])*bg[2]}
}

func inRect(r [4]float32, x, y float32) bool { return x >= r[0] && x < r[2] && y >= r[1] && y < r[3] }

// bandShortfall is one glyph sample under its floor.
type bandShortfall struct {
	x, y, glass, ratio, floor float64
	color                     uint32
}

// bandContrast checks the titlebar's glyphs in the frame h last built: each
// glyph at three heights (its top, middle and bottom rows of pixels, where a
// vertical gradient differs most), at both ends of the glass range. It
// returns the samples under their floor, the worst ratio to its floor and the
// glyphs checked.
func bandContrast(h *hrn, dark, key bool) (bad []bandShortfall, worst float64, glyphs int) {
	look := 0
	if dark {
		look = 1
	}
	band, textX := float32(45*2), float32(240) // the band's height; the host name's left (after the device glyph)
	worst = math.Inf(1)
	quads := h.list.Quads
	for i := range quads {
		q := &quads[i]
		w := q.Rect[2] - q.Rect[0]
		rgb := q.Color[0] >> 8
		if q.Mode != gpu.ModeMaskR8 || q.Rect[3] > band || q.Rect[0] < textX || w > 12 || (rgb != 0 && rgb != 0xFFFFFF) {
			continue // not a glyph of the band's text (the fake glyphs are 6 px wide; icons are wider or coloured)
		}
		x := (q.Rect[0] + q.Rect[2]) / 2
		if q.FadeX != [2]float32{} && x > q.FadeX[0] {
			continue // fading out where the title does not fit
		}
		floor := floorInactive[look]
		if key && (x < h.x.capsule[0] || inRect(h.x.pill, x, (q.Rect[1]+q.Rect[3])/2)) {
			floor = floorActive[look]
		}
		glyphs++
		px := math.Floor(float64(x)) + 0.5
		for _, py := range []float64{math.Floor(float64(q.Rect[1])) + 0.5, math.Floor(float64(q.Rect[1]+q.Rect[3])/2) + 0.5, math.Ceil(float64(q.Rect[3])) - 0.5} {
			for _, g := range glassGrey[look] {
				bg := [3]float64{g, g, g}
				for j := range quads[:i] {
					if f := &quads[j]; f.Mode == gpu.ModeShape {
						bg = blend(shade(f, px, py), bg)
					}
				}
				ink := blend(unpack(q.Color[0]), bg) // full ink: the core of a stroke
				c := contrast(bg, ink)
				worst = min(worst, c/floor)
				if c < floor {
					bad = append(bad, bandShortfall{px, py, g, c, floor, q.Color[0]})
				}
			}
		}
	}
	return bad, worst, glyphs
}

// bandState puts the scene in one of the states the doc's claim covers.
type bandState struct {
	name string
	key  bool
	set  func(h *hrn)
	in   func(h *hrn) bool // the scene shows the state
}

func hot(h *hrn, i int) bool { return h.c.HotTOf(h.x.tabKey(h.x.tabs[i].uid)) > 0.99 }
func held(h *hrn, i int) bool {
	return h.c.ActiveTOf(h.x.tabKey(h.x.tabs[i].uid)) > 0.99 && h.x.active == i
}

var bandStates = []bandState{
	{"rest", true, func(*hrn) {}, func(h *hrn) bool { return h.x.keyW == 1 }},
	{"hover on an unchosen tab", true, func(h *hrn) { h.move(centre(h.tabRect(2))) }, func(h *hrn) bool { return hot(h, 2) }},
	{"tab held down", true, func(h *hrn) {
		h.move(centre(h.tabRect(1)))
		h.frame()
		h.in.SetButton(ui.ButtonLeft, true, 1)
	}, func(h *hrn) bool { return held(h, 1) }},
	{"not key", false, func(*hrn) {}, func(h *hrn) bool { return h.x.keyW == 0 }},
	{"not key, pointer over a tab", false, func(h *hrn) { h.move(centre(h.tabRect(2))) }, func(h *hrn) bool { return h.x.keyW == 0 && hot(h, 2) }},
}

// newBandHarness is the native-shell scene in a state, settled.
func newBandHarness(t *testing.T, dark bool, s bandState) *hrn {
	h := newHarnessRaw(t, WindowWidthPt, WindowHeightPt, 2, ui.Appearance{Dark: dark, Known: true})
	h.x.backdrop, h.x.btns = true, &fakeButtons{}
	h.in.Focused = s.key
	h.settle()
	s.set(h)
	h.settle()
	return h
}

// Every glyph of the titlebar's text over the glass keeps AppKit's floors,
// light and dark, at rest, with a tab hovered, with one held down, and in a
// window that is not key, at both ends of the glass's measured range.
func TestBandTextContrastOverGlass(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, s := range bandStates {
			h := newBandHarness(t, dark, s)
			if !s.in(h) {
				t.Errorf("dark %v: the scene does not show %q", dark, s.name)
			}
			bad, worst, glyphs := bandContrast(h, dark, s.key)
			if glyphs < 40 {
				t.Errorf("dark %v, %s: only %d glyphs of titlebar text found", dark, s.name, glyphs)
			}
			for i, b := range bad {
				if i == 5 {
					t.Errorf("… %d more", len(bad)-5)
					break
				}
				t.Errorf("dark %v, %s, glass %v: glyph at %.1f,%.1f (colour %08X) has %.2f:1, floor %.2f", dark, s.name, b.glass, b.x, b.y, b.color, b.ratio, b.floor)
			}
			t.Logf("dark %v, %s: %d glyphs, worst %.2f× its floor", dark, s.name, glyphs, worst)
		}
	}
}

// The check sees the gradients the shader interpolates: darkening only the
// bottom corners of the band's fills (the capsule and pill gradients' lower
// colours) breaks the light look's floors, though their top colours, all the
// old check read, are unchanged.
func TestBandContrastSeesGradients(t *testing.T) {
	h := newBandHarness(t, false, bandStates[0])
	if bad, _, _ := bandContrast(h, false, true); len(bad) != 0 {
		t.Fatalf("the unchanged frame already fails %d samples", len(bad))
	}
	darkened := 0
	for i := range h.list.Quads {
		q := &h.list.Quads[i]
		if q.Mode != gpu.ModeShape || q.Soft != 0 || q.Rect[3] > 45*2 || q.Rect[2]-q.Rect[0] < 100 {
			continue // the band's large fills only: the capsule, the pill
		}
		for _, c := range []int{2, 3} {
			q.Color[c] = 0x202020<<8 | q.Color[c]&255
		}
		darkened++
	}
	bad, _, _ := bandContrast(h, false, true)
	if darkened < 2 || len(bad) == 0 {
		t.Errorf("darkened the bottom colours of %d fills: %d samples under their floor, want some", darkened, len(bad))
	}
	t.Logf("%d fills darkened at the bottom: %d samples under their floor", darkened, len(bad))
}
