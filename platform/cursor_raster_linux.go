//go:build linux

package platform

import "math"

// Our own cursors, for compositors that offer neither wp_cursor_shape_v1 nor a
// cursor theme: black shapes with a white outline, drawn from geometric
// primitives on a 24-unit design grid and sampled 4x4 per pixel.

// shape is a cursor image: the five Cursor values plus the diagonal and
// window-edge resizes that client-side decorations need.
type shape uint8

const (
	shArrow   = shape(CursorArrow)
	shPointer = shape(CursorPointer)
	shText    = shape(CursorText)
	shEW      = shape(CursorResizeEW)
	shNS      = shape(CursorResizeNS)
)

const (
	shNWSE  = shape(numCursors) + iota // diagonal resize, top-left to bottom-right
	shNESW                             // diagonal resize, top-right to bottom-left
	shEdgeN                            // window edge resizes
	shEdgeS
	shEdgeE
	shEdgeW
	shEdgeNE
	shEdgeNW
	shEdgeSE
	shEdgeSW
	numShapes
)

// rasterLook maps an edge shape to the drawing that stands for it.
func (s shape) rasterLook() shape {
	switch s {
	case shEdgeN, shEdgeS:
		return shNS
	case shEdgeE, shEdgeW:
		return shEW
	case shEdgeNW, shEdgeSE:
		return shNWSE
	case shEdgeNE, shEdgeSW:
		return shNESW
	}
	return s
}

// prim is one geometric primitive in design coordinates.
type prim struct {
	kind       byte // 'r' rectangle, 'c' circle, 'p' polygon
	a, b, c, d float32
	pts        [][2]float32
}

func inPoly(pts [][2]float32, x, y float32) bool {
	in := false
	j := len(pts) - 1
	for i := range pts {
		if (pts[i][1] > y) != (pts[j][1] > y) &&
			x < (pts[j][0]-pts[i][0])*(y-pts[i][1])/(pts[j][1]-pts[i][1])+pts[i][0] {
			in = !in
		}
		j = i
	}
	return in
}

func (p *prim) has(x, y float32) bool {
	switch p.kind {
	case 'r':
		return x >= p.a && x <= p.c && y >= p.b && y <= p.d
	case 'c':
		dx, dy := x-p.a, y-p.b
		return dx*dx+dy*dy <= p.c*p.c
	}
	return inPoly(p.pts, x, y)
}

func rect(x0, y0, x1, y1 float32) prim { return prim{kind: 'r', a: x0, b: y0, c: x1, d: y1} }
func circ(x, y, r float32) prim        { return prim{kind: 'c', a: x, b: y, c: r} }
func poly(pts ...[2]float32) prim      { return prim{kind: 'p', pts: pts} }

var (
	arrowPrims = []prim{poly([2]float32{1, 1}, [2]float32{1, 18}, [2]float32{5, 14.2}, [2]float32{8, 21},
		[2]float32{11, 19.6}, [2]float32{8.1, 13}, [2]float32{13.5, 13})}
	textPrims = []prim{rect(11, 5, 13, 19), rect(8, 4, 16, 6), rect(8, 18, 16, 20)}
	handPrims = []prim{rect(8.5, 2.5, 11.5, 12), circ(10, 2.5, 1.5), circ(13.2, 11.2, 1.7), circ(16.2, 11.8, 1.6),
		rect(6, 11, 17.8, 19), rect(7, 19, 16.8, 20.5),
		poly([2]float32{6, 12}, [2]float32{3.5, 10}, [2]float32{2.5, 11.5}, [2]float32{6, 17}, [2]float32{8, 17})}
)

// hot is the hotspot of each look, in design units.
var rasterHot = [numShapes][2]float32{
	shArrow: {1, 1}, shPointer: {10, 2.5}, shText: {12, 12}, shEW: {12, 12}, shNS: {12, 12}, shNWSE: {12, 12}, shNESW: {12, 12},
}

// doubleArrow reports whether the point (x, y) is in a double-headed arrow
// through the grid's centre at angle degrees (0: horizontal).
func doubleArrow(x, y, deg float32) bool {
	s, c := math.Sincos(float64(deg) * math.Pi / 180)
	dx, dy := float64(x)-12, float64(y)-12
	u, v := dx*c+dy*s, -dx*s+dy*c
	if u < 0 {
		u = -u
	}
	if v < 0 {
		v = -v
	}
	switch {
	case u <= 7 && v <= 1.1: // shaft
		return true
	case u >= 6.5 && u <= 11.5: // head: a triangle narrowing to the tip
		return v <= (11.5-u)*0.9
	}
	return false
}

func lookHas(look shape, x, y float32) bool {
	var ps []prim
	switch look {
	case shArrow:
		ps = arrowPrims
	case shText:
		ps = textPrims
	case shPointer:
		ps = handPrims
	case shEW:
		return doubleArrow(x, y, 0)
	case shNS:
		return doubleArrow(x, y, 90)
	case shNWSE:
		return doubleArrow(x, y, 45)
	case shNESW:
		return doubleArrow(x, y, 135)
	}
	for i := range ps {
		if ps[i].has(x, y) {
			return true
		}
	}
	return false
}

// rasterCursor draws sh into dst as size x size ARGB8888 pixels (premultiplied,
// little endian, stride size*4) and returns the hotspot in pixels. dst must
// hold size*size*4 bytes.
func rasterCursor(dst []byte, sh shape, size int) (hx, hy int32) {
	look := sh.rasterLook()
	const ss = 4 // samples per pixel side
	n := size * ss
	unit := float32(24) / float32(n) // design units per sample
	body := make([]bool, n*n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			body[y*n+x] = lookHas(look, (float32(x)+0.5)*unit, (float32(y)+0.5)*unit)
		}
	}
	// The outline is the body grown by about one design unit.
	r := int(1.1/unit + 0.5)
	grown := make([]bool, n*n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if !body[y*n+x] {
				continue
			}
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					if dx*dx+dy*dy > r*r {
						continue
					}
					if X, Y := x+dx, y+dy; X >= 0 && X < n && Y >= 0 && Y < n {
						grown[Y*n+X] = true
					}
				}
			}
		}
	}
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var black, white int
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					i := (py*ss+sy)*n + px*ss + sx
					if body[i] {
						black++
					} else if grown[i] {
						white++
					}
				}
			}
			a := (black + white) * 255 / (ss * ss)
			w := white * 255 / (ss * ss) // premultiplied: white contributes its coverage
			o := (py*size + px) * 4
			dst[o], dst[o+1], dst[o+2], dst[o+3] = byte(w), byte(w), byte(w), byte(a)
		}
	}
	h := rasterHot[look]
	return int32(h[0]*float32(size)/24 + 0.5), int32(h[1]*float32(size)/24 + 0.5)
}
