package text

import "math"

// rasterizer fills closed paths into an 8-bit coverage mask using exact
// signed-area accumulation (the algorithm of golang.org/x/image/vector and
// font-rs): every edge adds its signed trapezoid area to the cells it
// crosses, and a running sum along each row turns the accumulated deltas
// into coverage. Anti-aliasing is exact for straight edges (curves are
// flattened first), there is no hinting and no gamma.
//
// Fill rule: coverage is min(|winding area|, 1). That is non-zero winding
// for the usual case of non-overlapping contours and of overlapping contours
// with the same direction (TrueType and CFF outlines), and cancels where
// opposite directions overlap.
//
// Coordinates are pixels with the origin at the top-left corner of the mask
// and y increasing downwards. Points outside [0,w]×[0,h] are clamped, so the
// caller sizes the mask to contain the path.
//
// A rasterizer reuses its buffer across reset calls. Not safe for
// concurrent use.
type rasterizer struct {
	w, h   int
	stride int       // w + 2: edges write up to two cells right of the last pixel
	acc    []float32 // h rows of stride cells
	px, py float32   // current point
	sx, sy float32   // start of the open contour
	open   bool
}

// flattenTol is the maximum distance in pixels between a curve and its
// polyline approximation.
const flattenTol = 0.02

func (z *rasterizer) reset(w, h int) {
	z.w, z.h, z.stride = w, h, w+2
	n := z.stride * h
	if cap(z.acc) < n {
		z.acc = make([]float32, n)
	} else {
		z.acc = z.acc[:n]
		clear(z.acc)
	}
	z.px, z.py, z.sx, z.sy, z.open = 0, 0, 0, 0, false
}

func (z *rasterizer) clampX(x float32) float32 {
	if !(x > 0) { // also catches NaN
		return 0
	}
	if m := float32(z.w); x > m {
		return m
	}
	return x
}

func (z *rasterizer) clampY(y float32) float32 {
	if !(y > 0) {
		return 0
	}
	if m := float32(z.h); y > m {
		return m
	}
	return y
}

// moveTo starts a new contour, closing the previous one.
func (z *rasterizer) moveTo(x, y float32) {
	z.closePath()
	x, y = z.clampX(x), z.clampY(y)
	z.px, z.py, z.sx, z.sy, z.open = x, y, x, y, true
}

// lineTo adds an edge from the current point.
func (z *rasterizer) lineTo(x, y float32) {
	if !z.open {
		z.moveTo(x, y)
		return
	}
	bx, by := z.clampX(x), z.clampY(y)
	z.edge(z.px, z.py, bx, by)
	z.px, z.py = bx, by
}

// closePath closes the open contour with a straight edge to its start.
func (z *rasterizer) closePath() {
	if z.open && (z.px != z.sx || z.py != z.sy) {
		z.edge(z.px, z.py, z.sx, z.sy)
	}
	z.px, z.py = z.sx, z.sy
	z.open = false
}

// quadTo adds a quadratic Bézier from the current point.
func (z *rasterizer) quadTo(cx, cy, x, y float32) {
	if !z.open {
		z.moveTo(cx, cy)
	}
	x0, y0 := z.px, z.py
	// Flattening error of n uniform segments is |p0-2c+p1|/(4n²).
	dd := math.Hypot(float64(x0-2*cx+x), float64(y0-2*cy+y))
	n := int(math.Ceil(math.Sqrt(dd / (4 * flattenTol))))
	n = max(1, min(n, 64))
	for i := 1; i < n; i++ {
		t := float32(i) / float32(n)
		u := 1 - t
		z.lineTo(u*u*x0+2*u*t*cx+t*t*x, u*u*y0+2*u*t*cy+t*t*y)
	}
	z.lineTo(x, y)
}

// cubeTo adds a cubic Bézier from the current point.
func (z *rasterizer) cubeTo(c1x, c1y, c2x, c2y, x, y float32) {
	if !z.open {
		z.moveTo(c1x, c1y)
	}
	x0, y0 := z.px, z.py
	d1 := math.Hypot(float64(x0-2*c1x+c2x), float64(y0-2*c1y+c2y))
	d2 := math.Hypot(float64(c1x-2*c2x+x), float64(c1y-2*c2y+y))
	// Flattening error of n uniform segments is at most 3·max(d1,d2)/(4n²).
	n := int(math.Ceil(math.Sqrt(0.75 * max(d1, d2) / flattenTol)))
	n = max(1, min(n, 64))
	for i := 1; i < n; i++ {
		t := float32(i) / float32(n)
		u := 1 - t
		a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
		z.lineTo(a*x0+b*c1x+c*c2x+d*x, a*y0+b*c1y+c*c2y+d*y)
	}
	z.lineTo(x, y)
}

// edge accumulates the signed area of the segment (ax,ay)-(bx,by), whose
// ends are already clamped into the mask.
func (z *rasterizer) edge(ax, ay, bx, by float32) {
	if ay == by {
		return
	}
	dir := float32(1)
	if ay > by {
		dir = -1
		ax, ay, bx, by = bx, by, ax, ay
	}
	dxdy := (bx - ax) / (by - ay)
	x := ax
	y := int(ay) // ay >= 0
	for y < z.h && float32(y) < by {
		row := z.acc[y*z.stride : (y+1)*z.stride]
		top := max(float32(y), ay)
		dy := min(float32(y+1), by) - top
		xNext := x + dy*dxdy
		d := dy * dir
		x0, x1 := x, xNext
		if x0 > x1 {
			x0, x1 = x1, x0
		}
		x0i := int(x0) // x0 >= 0
		x0Floor := float32(x0i)
		x1i := int(math.Ceil(float64(x1)))
		x1Ceil := float32(x1i)
		if x1i <= x0i+1 {
			xmf := 0.5*(x+xNext) - x0Floor
			row[x0i] += d - d*xmf
			row[x0i+1] += d * xmf
		} else {
			s := 1 / (x1 - x0)
			x0f := x0 - x0Floor
			a0 := 0.5 * s * (1 - x0f) * (1 - x0f)
			x1f := x1 - x1Ceil + 1
			am := 0.5 * s * x1f * x1f
			row[x0i] += d * a0
			if x1i == x0i+2 {
				row[x0i+1] += d * (1 - a0 - am)
			} else {
				a1 := s * (1.5 - x0f)
				row[x0i+1] += d * (a1 - a0)
				for xi := x0i + 2; xi < x1i-1; xi++ {
					row[xi] += d * s
				}
				a2 := a1 + s*float32(x1i-x0i-3)
				row[x1i-1] += d * (1 - a2 - am)
			}
			row[x1i] += d * am
		}
		x = xNext
		y++
	}
}

// coverage closes the open contour and writes the w×h mask to dst with the
// given row stride.
func (z *rasterizer) coverage(dst []byte, stride int) {
	z.closePath()
	for y := 0; y < z.h; y++ {
		row := z.acc[y*z.stride : y*z.stride+z.w]
		out := dst[y*stride : y*stride+z.w]
		var sum float32
		for x, v := range row {
			sum += v
			a := sum
			if a < 0 {
				a = -a
			}
			if a > 1 {
				a = 1
			}
			out[x] = uint8(a*255 + 0.5)
		}
	}
}
