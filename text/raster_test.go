package text

import (
	"math"
	"testing"
)

func rasterize(w, h int, build func(z *rasterizer)) []byte {
	var z rasterizer
	z.reset(w, h)
	build(&z)
	out := make([]byte, w*h)
	z.coverage(out, w)
	return out
}

func addRect(z *rasterizer, x0, y0, x1, y1 float32) {
	z.moveTo(x0, y0)
	z.lineTo(x1, y0)
	z.lineTo(x1, y1)
	z.lineTo(x0, y1)
}

func total(m []byte) (s float64) {
	for _, v := range m {
		s += float64(v) / 255
	}
	return
}

func TestRasterPixelAlignedRect(t *testing.T) {
	m := rasterize(8, 8, func(z *rasterizer) { addRect(z, 2, 3, 6, 5) })
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			want := byte(0)
			if x >= 2 && x < 6 && y >= 3 && y < 5 {
				want = 255
			}
			if m[y*8+x] != want {
				t.Fatalf("pixel (%d,%d) = %d, want %d", x, y, m[y*8+x], want)
			}
		}
	}
}

func TestRasterFractionalEdges(t *testing.T) {
	// Left edge at x=1.25 (pixel 1 is 75% covered), right edge at 3.5 (pixel
	// 3 half covered), top edge at y=0.5.
	m := rasterize(5, 3, func(z *rasterizer) { addRect(z, 1.25, 0.5, 3.5, 3) })
	at := func(x, y int) int { return int(m[y*5+x]) }
	for _, c := range []struct {
		x, y int
		want float64
	}{{0, 1, 0}, {1, 1, 0.75}, {2, 1, 1}, {3, 1, 0.5}, {4, 1, 0}, {1, 0, 0.375}, {2, 0, 0.5}, {3, 0, 0.25}} {
		if got := float64(at(c.x, c.y)) / 255; math.Abs(got-c.want) > 0.01 {
			t.Errorf("pixel (%d,%d) coverage %.3f, want %.3f", c.x, c.y, got, c.want)
		}
	}
	if got, want := total(m), 2.25*2.5; math.Abs(got-want) > 0.05 {
		t.Errorf("total coverage %.3f, want area %.3f", got, want)
	}
}

func TestRasterWindingAndOverlap(t *testing.T) {
	cw := rasterize(8, 8, func(z *rasterizer) { addRect(z, 1, 1, 7, 7) })
	ccw := rasterize(8, 8, func(z *rasterizer) {
		z.moveTo(1, 1)
		z.lineTo(1, 7)
		z.lineTo(7, 7)
		z.lineTo(7, 1)
	})
	if string(cw) != string(ccw) {
		t.Fatal("reversed winding changes the mask")
	}
	// Two overlapping squares of the same direction union without exceeding 1.
	u := rasterize(10, 10, func(z *rasterizer) { addRect(z, 1, 1, 6, 6); addRect(z, 4, 4, 9, 9) })
	if got := u[5*10+5]; got != 255 {
		t.Errorf("overlap pixel = %d, want 255", got)
	}
	if got, want := total(u), 25.0+25-4; math.Abs(got-want) > 0.05 {
		t.Errorf("union area %.2f, want %.2f", got, want)
	}
	// A hole (opposite direction inside) is empty.
	h := rasterize(10, 10, func(z *rasterizer) {
		addRect(z, 1, 1, 9, 9)
		z.moveTo(3, 3)
		z.lineTo(3, 7)
		z.lineTo(7, 7)
		z.lineTo(7, 3)
	})
	if h[5*10+5] != 0 || h[2*10+2] != 255 {
		t.Errorf("hole/ring pixels = %d, %d", h[5*10+5], h[2*10+2])
	}
}

func TestRasterDiagonalAndCircle(t *testing.T) {
	// A right triangle with legs 8: exact area 32.
	tri := rasterize(8, 8, func(z *rasterizer) { z.moveTo(0, 0); z.lineTo(8, 0); z.lineTo(0, 8) })
	if got := total(tri); math.Abs(got-32) > 0.1 {
		t.Errorf("triangle area %.3f, want 32", got)
	}
	// A circle from four cubic Béziers.
	const r, c = 10.0, 12.0
	const k = 0.5522847498
	circle := rasterize(24, 24, func(z *rasterizer) {
		z.moveTo(c+r, c)
		z.cubeTo(c+r, c+k*r, c+k*r, c+r, c, c+r)
		z.cubeTo(c-k*r, c+r, c-r, c+k*r, c-r, c)
		z.cubeTo(c-r, c-k*r, c-k*r, c-r, c, c-r)
		z.cubeTo(c+k*r, c-r, c+r, c-k*r, c+r, c)
	})
	if got, want := total(circle), math.Pi*r*r; math.Abs(got-want) > 1.0 {
		t.Errorf("circle area %.2f, want %.2f", got, want)
	}
	// The same circle from quadratics (8 arcs of 45°) is as accurate.
	quad := rasterize(24, 24, func(z *rasterizer) {
		const n = 8
		z.moveTo(c+r, c)
		for i := 0; i < n; i++ {
			a0, a1 := 2*math.Pi*float64(i)/n, 2*math.Pi*float64(i+1)/n
			am := (a0 + a1) / 2
			rc := r / math.Cos((a1-a0)/2)
			z.quadTo(float32(c+rc*math.Cos(am)), float32(c+rc*math.Sin(am)), float32(c+r*math.Cos(a1)), float32(c+r*math.Sin(a1)))
		}
	})
	if got, want := total(quad), math.Pi*r*r; math.Abs(got-want) > 1.0 {
		t.Errorf("quad circle area %.2f, want %.2f", got, want)
	}
	// Symmetric about the vertical axis through the centre.
	for y := 0; y < 24; y++ {
		for x := 0; x < 12; x++ {
			if d := int(circle[y*24+x]) - int(circle[y*24+23-x]); d > 3 || d < -3 {
				t.Fatalf("asymmetric at (%d,%d): %d vs %d", x, y, circle[y*24+x], circle[y*24+23-x])
			}
		}
	}
}

func TestRasterSubpixelShift(t *testing.T) {
	centroid := func(dx float32) float64 {
		m := rasterize(12, 6, func(z *rasterizer) { addRect(z, 4+dx, 1, 6+dx, 5) })
		var sx, s float64
		for y := 0; y < 6; y++ {
			for x := 0; x < 12; x++ {
				v := float64(m[y*12+x])
				sx += v * (float64(x) + 0.5)
				s += v
			}
		}
		return sx / s
	}
	base := centroid(0)
	for q := 1; q <= 3; q++ {
		got := centroid(float32(q)*0.25) - base
		if math.Abs(got-float64(q)*0.25) > 0.01 {
			t.Errorf("shift %d/4: centroid moved %.3f", q, got)
		}
	}
}

func TestRasterClampsAndReuses(t *testing.T) {
	var z rasterizer
	z.reset(4, 4)
	addRect(&z, -5, -5, 20, 20) // far outside: clamped, must not panic
	out := make([]byte, 16)
	z.coverage(out, 4)
	for i, v := range out {
		if v != 255 {
			t.Fatalf("pixel %d = %d, want 255 for a covering rect", i, v)
		}
	}
	z.reset(4, 4) // a reset clears the previous contents
	z.coverage(out, 4)
	for i, v := range out {
		if v != 0 {
			t.Fatalf("pixel %d = %d after reset", i, v)
		}
	}
	// NaN coordinates are ignored rather than crashing.
	z.reset(4, 4)
	z.moveTo(float32(math.NaN()), 1)
	z.lineTo(2, float32(math.NaN()))
	z.coverage(out, 4)
}
