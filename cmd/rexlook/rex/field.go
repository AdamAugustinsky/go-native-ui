package rex

// field is the window background and what depends on its geometry: the node
// grid of the background mesh, its cells (four-corner colour quads) and the
// composite of an unfocused pane's translucent fill. It is rebuilt when the
// window size or scale changes (every frame of a live resize), without
// allocating.

const (
	maxNodesX  = 20
	maxNodesY  = len(bgStops)
	maxBGCells = (maxNodesY - 1) * (maxNodesX - 1)
)

type bgCell struct {
	r [4]float32
	c [4]uint32 // TL, TR, BL, BR
}

type field struct {
	w, h int
	k    float32 // pixels per reference pixel (the reference is 2×)

	nx, ny         int
	nodesX, nodesY [maxNodesX]float32 // pixel positions of the mesh nodes
	cells          [maxBGCells]bgCell
	ncells         int
}

// yPx is the pixel position of a reference height: the top 90 reference
// pixels (the titlebar) keep their size, the rest stretches with the window.
func (f *field) yPx(y float32) float32 {
	if y <= 90 {
		return y * f.k
	}
	return 90*f.k + (y-90)*(float32(f.h)-90*f.k)/(bgStops[len(bgStops)-1].y-90)
}

// yRef is the inverse of yPx.
func (f *field) yRef(py float32) float32 {
	if py <= 90*f.k {
		return py / f.k
	}
	return 90 + (py-90*f.k)*(bgStops[len(bgStops)-1].y-90)/(float32(f.h)-90*f.k)
}

// build lays out the mesh for a w×h pixel window at k pixels per reference pixel.
func (f *field) build(w, h int, k float32) {
	if f.w == w && f.h == h && f.k == k {
		return
	}
	f.w, f.h, f.k = w, h, k
	W, H := float32(w), float32(h)
	refW := W / k

	// Mesh x nodes in reference pixels: dense near both edges.
	var xs [17]float32
	n := 0
	for _, d := range [...]float32{0, 8, 16, 30, 60, 120, 240, 400} {
		xs[n] = d
		n++
	}
	for _, d := range [...]float32{650, 400, 240, 120, 60, 30, 16, 8, 0} {
		xs[n] = refW - d
		n++
	}
	for i := 1; i < n; i++ { // insertion sort
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
	// Drop nodes that would not advance (very narrow windows).
	var nodes [17]float32
	nn := 1
	nodes[0] = xs[0]
	for _, x := range xs[1:n] {
		if x > nodes[nn-1]+1 && x <= refW {
			nodes[nn] = x
			nn++
		}
	}
	f.nx, f.ny = nn, len(bgStops)
	for i := 0; i < nn; i++ {
		f.nodesX[i] = snap(nodes[i] * k)
	}
	for j := range bgStops {
		f.nodesY[j] = snap(f.yPx(bgStops[j].y))
	}

	// Corner colours once per node.
	var grid [len(bgStops)][17]uint32
	for j := range bgStops {
		for i := 0; i < nn; i++ {
			grid[j][i] = bgAt(nodes[i], refW-nodes[i], bgStops[j].y).rgba(255)
		}
	}
	f.ncells = 0
	for j := 0; j+1 < len(bgStops); j++ {
		y0, y1 := f.nodesY[j], f.nodesY[j+1]
		if j+2 == len(bgStops) {
			y1 = H
		}
		for i := 0; i+1 < nn; i++ {
			x0, x1 := f.nodesX[i], f.nodesX[i+1]
			if i+2 == nn {
				x1 = W
			}
			f.cells[f.ncells] = bgCell{
				r: [4]float32{x0, y0, x1, y1},
				c: [4]uint32{grid[j][i], grid[j][i+1], grid[j+1][i], grid[j+1][i+1]},
			}
			f.ncells++
		}
	}
}

// Unfocused panes are translucent white over the background. The composite
// is evaluated here, on the CPU, into opaque cells (so the pane's shadow
// never shows through it and the cells can be animated as plain colours):
// white at fillAlpha over the background field, whose backdrop turns sandier
// toward the bottom of the window than the visible gutters do.
const fillAlpha = 0.575

var backdropDelta = [...]struct{ y, db float32 }{
	{0, 0}, {880, 0}, {1040, -7}, {1120, -14}, {1200, -17}, {1240, -27}, {1278, -30},
}

func backdropBlue(y float32) float32 {
	d := backdropDelta[:]
	for i := 1; i < len(d); i++ {
		if y <= d[i].y {
			t := (y - d[i-1].y) / (d[i].y - d[i-1].y)
			return d[i-1].db + (d[i].db-d[i-1].db)*t
		}
	}
	return d[len(d)-1].db
}

// fillAt is the composited colour of an unfocused pane at reference (x, y).
func (f *field) fillAt(xRef, yRef float32) uint32 {
	refW := float32(f.w) / f.k
	u := bgAt(xRef, refW-xRef, yRef)
	// The backdrop picks up the edge tints in a different hue balance than
	// the visible gutters: less green, more blue.
	v, _, _ := stopAt(yRef)
	u = v.add(vec3{u[0] - v[0], (u[1] - v[1]) * 0.5, (u[2] - v[2]) * 1.6})
	u[2] += backdropBlue(yRef)
	white := vec3{255, 255, 255}
	return white.mul(fillAlpha).add(u.mul(1 - fillAlpha)).rgba(255)
}

// ---- unfocused pane fill mesh ----

const maxMeshLines = maxNodesX + 4

type meshCell struct {
	r     [4]float32
	c     [4]uint32
	radii [4]float32
}

// mesh is the cached fill of one pane rectangle: a grid of cells whose corner
// colours are the unfocused composite. The outer cells are exactly 2·rad so
// that only they carry the rounded corners. The colours depend on nothing but
// the rectangle and the window, so one mesh can serve any pane.
type mesh struct {
	valid bool
	rect  [4]float32
	rad   float32
	w, h  int
	k     float32
	n     int
	cells [(maxMeshLines - 1) * (maxMeshLines - 1)]meshCell
}

// cut returns the grid lines between lo and hi: both ends, the corner arcs'
// extents and the field's nodes in between.
func cut(dst *[maxMeshLines]float32, lo, hi, rad float32, nodes []float32) int {
	n := 0
	dst[n] = lo
	n++
	dst[n] = lo + 2*rad
	n++
	for _, v := range nodes {
		if v > lo+2*rad+1 && v < hi-2*rad-1 {
			dst[n] = v
			n++
		}
	}
	dst[n] = hi - 2*rad
	n++
	dst[n] = hi
	n++
	return n
}

// update makes m the mesh of rect r (pixel rectangle, corner radius rad).
func (m *mesh) update(f *field, r [4]float32, rad float32) {
	if m.valid && m.rect == r && m.rad == rad && m.w == f.w && m.h == f.h && m.k == f.k {
		return
	}
	m.valid, m.rect, m.rad, m.w, m.h, m.k = true, r, rad, f.w, f.h, f.k
	var xs, ys [maxMeshLines]float32
	nxs := cut(&xs, r[0], r[2], rad, f.nodesX[:f.nx])
	nys := cut(&ys, r[1], r[3], rad, f.nodesY[:f.ny])
	var grid [maxMeshLines][maxMeshLines]uint32 // one colour per grid point
	for j := 0; j < nys; j++ {
		yr := f.yRef(ys[j])
		for i := 0; i < nxs; i++ {
			grid[j][i] = f.fillAt(xs[i]/f.k, yr)
		}
	}
	m.n = 0
	for j := 0; j+1 < nys; j++ {
		for i := 0; i+1 < nxs; i++ {
			c := meshCell{
				r: [4]float32{xs[i], ys[j], xs[i+1], ys[j+1]},
				c: [4]uint32{grid[j][i], grid[j][i+1], grid[j+1][i], grid[j+1][i+1]},
			}
			if i == 0 && j == 0 {
				c.radii[0] = rad
			}
			if i+2 == nxs && j == 0 {
				c.radii[1] = rad
			}
			if i+2 == nxs && j+2 == nys {
				c.radii[2] = rad
			}
			if i == 0 && j+2 == nys {
				c.radii[3] = rad
			}
			m.cells[m.n] = c
			m.n++
		}
	}
}
