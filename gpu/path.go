package gpu

import "math"

// Path is an outline to fill with one colour: subpaths of lines and
// quadratic and cubic Bézier curves, in pixels, its origin the top left of
// the mask it is filled into (PathMask), which is as large as the path's
// furthest point (what lies left of or above the origin is cut off). Every
// subpath is closed when it is filled. Fill rule: non-zero.
//
// A Path is pointer-free and reuses its storage: Reset it and build it
// again each frame, or keep it.
type Path struct {
	d []float32 // ops (pathMove, …) each followed by its coordinates
}

const (
	pathMove float32 = iota
	pathLine
	pathQuad
	pathCube
	pathClose
)

// Reset empties the path and keeps its storage.
func (p *Path) Reset() { p.d = p.d[:0] }

// Empty reports whether the path has no segment.
func (p *Path) Empty() bool { return len(p.d) == 0 }

// MoveTo starts a subpath at (x, y).
func (p *Path) MoveTo(x, y float32) { p.d = append(p.d, pathMove, x, y) }

// LineTo adds a line to (x, y).
func (p *Path) LineTo(x, y float32) { p.d = append(p.d, pathLine, x, y) }

// QuadTo adds a quadratic Bézier curve through the control point (cx, cy) to
// (x, y).
func (p *Path) QuadTo(cx, cy, x, y float32) { p.d = append(p.d, pathQuad, cx, cy, x, y) }

// CubeTo adds a cubic Bézier curve through the control points (c1x, c1y)
// and (c2x, c2y) to (x, y).
func (p *Path) CubeTo(c1x, c1y, c2x, c2y, x, y float32) {
	p.d = append(p.d, pathCube, c1x, c1y, c2x, c2y, x, y)
}

// Close closes the subpath with a line to its start.
func (p *Path) Close() { p.d = append(p.d, pathClose) }

// hash is FNV-1a over the path's values.
func (p *Path) hash() uint64 {
	h := uint64(14695981039346656037)
	for _, v := range p.d {
		h = (h ^ uint64(math.Float32bits(v))) * 1099511628211
	}
	return h
}

// size is the mask a fill of p needs: as large as its furthest point.
func (p *Path) size() (w, h int) {
	var mx, my float32
	for i := 0; i < len(p.d); {
		n := pathArgs(p.d[i])
		for j := i + 1; j+1 < i+1+n; j += 2 {
			mx, my = max(mx, p.d[j]), max(my, p.d[j+1])
		}
		i += 1 + n
	}
	return int(math.Ceil(float64(mx))), int(math.Ceil(float64(my)))
}

// pathArgs is how many coordinates follow op.
func pathArgs(op float32) int {
	switch op {
	case pathMove, pathLine:
		return 2
	case pathQuad:
		return 4
	case pathCube:
		return 6
	}
	return 0
}

// pathRaster accumulates the signed area a path's edges cover in each pixel
// (the accumulation rasteriser of font-rs and x/image/vector): a row's running
// sum is then the pixel's coverage, anti-aliased exactly for straight edges.
type pathRaster struct {
	acc    []float32 // h rows of w+2 cells
	w, h   int
	sx, sy float32 // the subpath's start
	px, py float32 // the pen
}

// fill rasterises p into mask (w×h coverage bytes, stride w).
func (r *pathRaster) fill(p *Path, mask []byte, w, h int) {
	r.w, r.h = w, h
	n := (w + 2) * h
	if cap(r.acc) < n {
		r.acc = make([]float32, n)
	}
	r.acc = r.acc[:n]
	clear(r.acc)
	r.sx, r.sy, r.px, r.py = 0, 0, 0, 0
	d := p.d
	for i := 0; i < len(d); {
		op := d[i]
		a := d[i+1 : i+1+pathArgs(op)]
		switch op {
		case pathMove:
			r.closeSub()
			r.sx, r.sy, r.px, r.py = a[0], a[1], a[0], a[1]
		case pathLine:
			r.lineTo(a[0], a[1])
		case pathQuad:
			r.quadTo(a[0], a[1], a[2], a[3])
		case pathCube:
			r.cubeTo(a[0], a[1], a[2], a[3], a[4], a[5])
		case pathClose:
			r.closeSub()
		}
		i += 1 + len(a)
	}
	r.closeSub()
	for y := 0; y < h; y++ {
		row := r.acc[y*(w+2) : (y+1)*(w+2)]
		out := mask[y*w : (y+1)*w]
		var s float32
		for x := range out {
			s += row[x]
			v := s
			if v < 0 {
				v = -v
			}
			out[x] = uint8(min(v, 1)*255 + 0.5)
		}
	}
}

func (r *pathRaster) closeSub() {
	if r.px != r.sx || r.py != r.sy {
		r.lineTo(r.sx, r.sy)
	}
}

func (r *pathRaster) lineTo(x, y float32) {
	r.line(r.px, r.py, x, y)
	r.px, r.py = x, y
}

// curveTol is how far, in pixels, the lines a curve is cut into may lie
// from it.
const curveTol = 0.03

// quadTo and cubeTo cut a curve into n lines, n from its second difference:
// a chord of a curve whose second derivative is at most M strays from it by
// at most M·h²/8 over a parameter step h = 1/n (M is 2|p0−2c+p1| for a
// quadratic, 6·max(|a|, |b|) for a cubic's second differences a and b).
func (r *pathRaster) quadTo(cx, cy, x, y float32) {
	x0, y0 := r.px, r.py
	dx, dy := x0-2*cx+x, y0-2*cy+y
	n := segments(2 * hypot(dx, dy))
	for i := 1; i <= n; i++ {
		t := float32(i) / float32(n)
		u := 1 - t
		r.lineTo(u*u*x0+2*u*t*cx+t*t*x, u*u*y0+2*u*t*cy+t*t*y)
	}
}

func (r *pathRaster) cubeTo(c1x, c1y, c2x, c2y, x, y float32) {
	x0, y0 := r.px, r.py
	ax, ay := x0-2*c1x+c2x, y0-2*c1y+c2y
	bx, by := c1x-2*c2x+x, c1y-2*c2y+y
	n := segments(6 * max(hypot(ax, ay), hypot(bx, by)))
	for i := 1; i <= n; i++ {
		t := float32(i) / float32(n)
		u := 1 - t
		r.lineTo(u*u*u*x0+3*u*u*t*c1x+3*u*t*t*c2x+t*t*t*x, u*u*u*y0+3*u*u*t*c1y+3*u*t*t*c2y+t*t*t*y)
	}
}

func hypot(x, y float32) float32 { return float32(math.Sqrt(float64(x*x + y*y))) }

// segments is how many lines a curve whose second derivative is at most m
// is cut into to stay within curveTol of it.
func segments(m float32) int {
	n := int(math.Ceil(math.Sqrt(float64(m / (8 * curveTol)))))
	return min(max(n, 1), 256)
}

// line accumulates the edge from (x0, y0) to (x1, y1).
func (r *pathRaster) line(x0, y0, x1, y1 float32) {
	if y0 == y1 {
		return
	}
	dir := float32(1)
	if y0 > y1 {
		dir = -1
		x0, y0, x1, y1 = x1, y1, x0, y0
	}
	fw, fh := float32(r.w), float32(r.h)
	if y1 <= 0 || y0 >= fh {
		return
	}
	dxdy := (x1 - x0) / (y1 - y0)
	x := x0
	if y0 < 0 {
		x -= y0 * dxdy
		y0 = 0
	}
	y1 = min(y1, fh)
	stride := r.w + 2
	for y := int(y0); y < r.h && float32(y) < y1; y++ {
		row := r.acc[y*stride : (y+1)*stride]
		dy := min(float32(y+1), y1) - max(float32(y), y0)
		xn := x + dxdy*dy
		d := dy * dir
		xa, xb := x, xn
		if xa > xb {
			xa, xb = xb, xa
		}
		xa, xb = min(max(xa, 0), fw), min(max(xb, 0), fw)
		fa := float32(math.Floor(float64(xa)))
		ia := int(fa)
		cb := float32(math.Ceil(float64(xb)))
		ib := int(cb)
		if ib <= ia+1 { // within one pixel
			xm := 0.5*(xa+xb) - fa
			row[ia] += d - d*xm
			row[ia+1] += d * xm
		} else {
			s := 1 / (xb - xa)
			f0 := xa - fa
			a0 := 0.5 * s * (1 - f0) * (1 - f0)
			f1 := xb - cb + 1
			am := 0.5 * s * f1 * f1
			row[ia] += d * a0
			if ib == ia+2 {
				row[ia+1] += d * (1 - a0 - am)
			} else {
				a1 := s * (1.5 - f0)
				row[ia+1] += d * (a1 - a0)
				for i := ia + 2; i < ib-1; i++ {
					row[i] += d * s
				}
				a2 := a1 + float32(ib-ia-3)*s
				row[ib-1] += d * (1 - a2 - am)
			}
			row[ib] += d * am
		}
		x = xn
	}
}

// PathUploader is what a PathMask needs of a renderer: *Renderer (and
// ui.Uploader) has it. If it can also FreeTexture, the mask frees the
// texture of a path it no longer shows.
type PathUploader interface {
	NewTexture(kind TextureKind, w, h int) TextureID
	Upload(t TextureID, x, y, w, h int, pixels []byte, stride int)
}

// PathMask is a Path filled into an R8 coverage texture, anti-aliased, for a
// ModeMaskR8 quad (Quad) of any colour: a shape no rounded rectangle makes
// (a message bubble's curled tail). It keeps the texture until the path
// changes, so a path that is the same as last time costs a hash and allocates
// nothing; a changed one is filled on the CPU and uploaded into a new
// texture (the old one is freed: frames in flight still sample it). Keep one
// PathMask per shape (per size and scale).
type PathMask struct {
	up   PathUploader
	tex  TextureID
	sum  uint64
	w, h int
	r    pathRaster
	pix  []byte
}

// Mask returns the texture of p filled, w×h texels, made on u: the one it
// returned last time when p and u are the same. An empty path (or one of no
// area) has texture 0.
func (m *PathMask) Mask(u PathUploader, p *Path) (tex TextureID, w, h int) {
	if u == nil || p.Empty() {
		return 0, 0, 0
	}
	sum := p.hash()
	if m.tex != 0 && m.up == u && m.sum == sum {
		return m.tex, m.w, m.h
	}
	m.Release()
	w, h = p.size()
	if w <= 0 || h <= 0 {
		return 0, 0, 0
	}
	if cap(m.pix) < w*h {
		m.pix = make([]byte, w*h)
	}
	m.pix = m.pix[:w*h]
	m.r.fill(p, m.pix, w, h)
	m.tex = u.NewTexture(TexR8, w, h)
	u.Upload(m.tex, 0, 0, w, h, m.pix, w)
	m.up, m.sum, m.w, m.h = u, sum, w, h
	return m.tex, w, h
}

// Quad is the ModeMaskR8 quad that fills p in col (0xRRGGBBAA) with the
// path's origin at (x, y), window pixels (whole pixels keep its edges as
// filled), clipped to clip (all zero: none). Add it to a DrawList. With no
// texture (Mask) it draws nothing.
func (m *PathMask) Quad(u PathUploader, p *Path, x, y float32, col uint32, clip [4]float32) Quad {
	tex, w, h := m.Mask(u, p)
	if tex == 0 {
		return Quad{} // (no area: draws nothing)
	}
	return Quad{
		Rect:  [4]float32{x, y, x + float32(w), y + float32(h)},
		Clip:  clip,
		UV:    [4]float32{0, 0, float32(w), float32(h)},
		Color: [4]uint32{col, col, col, col},
		Mode:  ModeMaskR8,
		Tex:   tex,
	}
}

// Release frees the mask's texture (on an uploader that can) and forgets
// it; the next Mask fills the path again.
func (m *PathMask) Release() {
	if f, ok := m.up.(interface{ FreeTexture(TextureID) }); ok && m.tex != 0 {
		f.FreeTexture(m.tex)
	}
	m.tex, m.up, m.sum = 0, nil, 0
}
