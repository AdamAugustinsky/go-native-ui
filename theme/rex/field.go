package rex

import (
	"math"

	"gonativeui/theme"
)

// ---- window background ----
//
// The window background is a smooth field: a vertical multi-stop gradient
// down the middle (pink-lavender at the top, neutral in the middle, warm
// sand at the bottom in the light look; warm plum to umber in the dark one) plus
// warmer tints that run in from the left and right edges. It is drawn as a
// mesh of four-corner quads whose corner colours are sampled from the model
// below, so no texture or blur is needed. During a light/dark crossfade the
// corners are the mix of the two models.

type vec3 [3]float32

func hx(c uint32) vec3 {
	return vec3{float32(c >> 16 & 255), float32(c >> 8 & 255), float32(c & 255)}
}

func (a vec3) add(b vec3) vec3            { return vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a vec3) sub(b vec3) vec3            { return vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a vec3) mul(k float32) vec3         { return vec3{a[0] * k, a[1] * k, a[2] * k} }
func (a vec3) mix(b vec3, t float32) vec3 { return a.add(b.sub(a).mul(t)) }

func (a vec3) rgba(alpha uint8) uint32 {
	q := func(v float32) uint32 {
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		return uint32(v + 0.5)
	}
	return q(a[0])<<24 | q(a[1])<<16 | q(a[2])<<8 | uint32(alpha)
}

// bgStop is the field at one height (reference pixels at 2×): v in the
// middle, l and r the colours at the left and right edge.
type bgStop struct {
	y       float32
	v, l, r vec3
}

const nStops = 20

// lightStops is the light field, fitted to the reference screenshot.
var lightStops = [nStops]bgStop{
	{0, hx(0xFFFFFF), hx(0xFFFFFF), hx(0xFFFFFF)},
	{2, hx(0xFCF1FC), hx(0xFCEAFC), hx(0xFCE8EE)},
	{4, hx(0xF8EDF8), hx(0xF8E6F8), hx(0xF8E4EA)},
	{6, hx(0xF6EBF6), hx(0xF6E4F6), hx(0xF6E2E8)},
	{8, hx(0xF5E8F5), hx(0xF5E1F5), hx(0xF5DFE7)},
	{10, hx(0xF4E7F4), hx(0xF4E0F4), hx(0xF4DEE6)},
	{40, hx(0xF2E9F1), hx(0xF3DFF3), hx(0xF3DDE3)},
	{80, hx(0xECE6EC), hx(0xEFDCEF), hx(0xEFDAE0)},
	{120, hx(0xE8E2E8), hx(0xE8D5E6), hx(0xE8D4D8)},
	{160, hx(0xE7E2E7), hx(0xE7D5E3), hx(0xE7D3D7)},
	{280, hx(0xE7E3E7), hx(0xE7D5DD), hx(0xE7D4D2)},
	{400, hx(0xE7E5E7), hx(0xE7D5D3), hx(0xE7D4CF)},
	{520, hx(0xE6E5E6), hx(0xE7D5CD), hx(0xE7D4CC)},
	{640, hx(0xE6E5E5), hx(0xE7D7CB), hx(0xE7D5CA)},
	{760, hx(0xE7E5E3), hx(0xE7D9C8), hx(0xE7D5C8)},
	{880, hx(0xE7E5E1), hx(0xE7D9C4), hx(0xE7D7C6)},
	{1000, hx(0xE7E4DC), hx(0xE7DBBF), hx(0xE7D7C2)},
	{1120, hx(0xE7E3D4), hx(0xE7DBBA), hx(0xE7D8BE)},
	{1240, hx(0xE7E0C9), hx(0xE7DBB5), hx(0xE7D7B8)},
	{1278, hx(0xE7DEC3), hx(0xE7DAB2), hx(0xE7D6B5)},
}

// darkAnchors are the few stops the dark field is fitted from, sampled from
// the reference's dark screenshot (rex-screenshot-dark@2x): a warm plum at
// the top that settles to a quiet warm ink and then umber at the bottom, the
// edges a little rosier (left) and more amber (right) than the middle. init
// interpolates them at lightStops' heights, so that both fields share one
// mesh.
var darkAnchors = [...]bgStop{
	{0, hx(0x3A3238), hx(0x3B3035), hx(0x3B3036)},
	{6, hx(0x342C33), hx(0x34292D), hx(0x332A30)},
	{40, hx(0x30282E), hx(0x31262A), hx(0x30282E)},
	{160, hx(0x2C2529), hx(0x2C2324), hx(0x2C2328)},
	{520, hx(0x262122), hx(0x2C2220), hx(0x2C2220)},
	{900, hx(0x23201F), hx(0x2B211E), hx(0x2A211D)},
	{1278, hx(0x27221C), hx(0x2A221D), hx(0x27211A)},
}

var darkStops [nStops]bgStop

func init() {
	for i := range darkStops {
		y := lightStops[i].y
		v, l, r := stopAt(darkAnchors[:], y)
		darkStops[i] = bgStop{y, v, l, r}
	}
}

// model is one mode's look of the field and of the translucent glass an
// unfocused surface is made of.
type model struct {
	stops []bgStop
	fill  vec3    // the glass colour
	alpha float32 // its opacity over the field
	keep  float32 // 1 - alpha, as an exact constant
	// The glass picks up the edge tints in a different hue balance than the
	// visible gutters (light: less green, more blue), and over the lower
	// window turns sandier than the gutters do (backdrop).
	hueG, hueB float32
	backdrop   bool
}

// fillAlpha is the opacity of the white glass of the light look; the dark
// glass is a lighter warm ink, darkGlass, at darkAlpha.
const (
	fillAlpha = 0.575
	darkAlpha = 0.5
	darkGlass = 0x362F32
)

var (
	lightModel = model{stops: lightStops[:], fill: vec3{255, 255, 255}, alpha: fillAlpha, keep: 1 - fillAlpha, hueG: 0.5, hueB: 1.6, backdrop: true}
	darkModel  = model{stops: darkStops[:], fill: hx(darkGlass), alpha: darkAlpha, keep: 1 - darkAlpha, hueG: 1, hueB: 1}
)

// FieldStop is the field's colour at height Y in reference pixels (the
// reference window is 1278 high at 2×; its top 90, the titlebar, keep their
// size and the rest stretches with the window): Mid down the middle, Left
// and Right at the window's edges, whose tints run in a few hundred
// reference pixels. Colours are 0xRRGGBB.
type FieldStop struct {
	Y                float32
	Mid, Left, Right uint32
}

// FieldColors recolour the window background of a theme made from Rex
// (ThemeWithField): the light and the dark field's stops by height (nil
// keeps Rex's), and the colour of the dark glass an unfocused surface is
// made of (0 keeps Rex's; the light glass is white). Rex's fitted hue
// balance of the light glass goes with its own field only.
type FieldColors struct {
	Light, Dark []FieldStop
	DarkGlass   uint32
}

// look is one theme's field and glass, light and dark.
type look struct {
	light, dark model
	stops       [2][nStops]bgStop // the stops of a recoloured field
}

var rexLook = &look{light: lightModel, dark: darkModel}

// newLook is Rex's look recoloured by fc, its stops sampled at the mesh's
// heights.
func newLook(fc FieldColors) *look {
	lk := &look{light: lightModel, dark: darkModel}
	if len(fc.Light) > 0 {
		lk.stops[0] = resample(fc.Light)
		lk.light.stops = lk.stops[0][:]
		lk.light.hueG, lk.light.hueB, lk.light.backdrop = 1, 1, false
	}
	if len(fc.Dark) > 0 {
		lk.stops[1] = resample(fc.Dark)
		lk.dark.stops = lk.stops[1][:]
	}
	if fc.DarkGlass != 0 {
		lk.dark.fill = hx(fc.DarkGlass)
	}
	return lk
}

// resample interpolates stops at the heights of the mesh (lightStops').
func resample(stops []FieldStop) [nStops]bgStop {
	src := make([]bgStop, len(stops))
	for i, s := range stops {
		src[i] = bgStop{s.Y, hx(s.Mid), hx(s.Left), hx(s.Right)}
	}
	var out [nStops]bgStop
	for i := range out {
		y := lightStops[i].y
		v, l, r := stopAt(src, y)
		out[i] = bgStop{y, v, l, r}
	}
	return out
}

// edgeWeight is how much of the edge tint remains at distance d (reference
// pixels) from a window edge: a fast fall-off near the edge plus a slow
// linear one.
func edgeWeight(d, lambda float32) float32 {
	slow := 1 - d/lambda
	if slow < 0 {
		slow = 0
	}
	return 0.5 * (float32(math.Exp(float64(-d/45))) + slow)
}

const (
	leftLambda  = 400
	rightLambda = 650
)

// stopAt interpolates a stop table at reference height y.
func stopAt(stops []bgStop, y float32) (v, l, r vec3) {
	n := len(stops)
	if y <= stops[0].y {
		s := stops[0]
		return s.v, s.l, s.r
	}
	if y >= stops[n-1].y {
		s := stops[n-1]
		return s.v, s.l, s.r
	}
	for i := 1; i < n; i++ {
		if y <= stops[i].y {
			a, b := stops[i-1], stops[i]
			t := (y - a.y) / (b.y - a.y)
			return a.v.mix(b.v, t), a.l.mix(b.l, t), a.r.mix(b.r, t)
		}
	}
	return
}

// bgAt is the field colour at reference coordinates (x from the left edge,
// dr from the right edge, y from the top).
func (m *model) bgAt(x, dr, y float32) vec3 {
	v, l, r := stopAt(m.stops, y)
	// Amplitudes are normalised at the sampled distance (8 px) of the edge colours.
	wl8, wr8 := edgeWeight(8, leftLambda), edgeWeight(8, rightLambda)
	c := v
	c = c.add(l.sub(v).mul(edgeWeight(x, leftLambda) / wl8))
	c = c.add(r.sub(v).mul(edgeWeight(dr, rightLambda) / wr8))
	return c
}

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

// fillAt is the composited colour of an unfocused surface at reference
// (xRef, yRef) in a window refW wide.
func (m *model) fillAt(refW, xRef, yRef float32) uint32 {
	u := m.bgAt(xRef, refW-xRef, yRef)
	v, _, _ := stopAt(m.stops, yRef)
	u = v.add(vec3{u[0] - v[0], (u[1] - v[1]) * m.hueG, (u[2] - v[2]) * m.hueB})
	if m.backdrop {
		u[2] += backdropBlue(yRef)
	}
	return m.fill.mul(m.alpha).add(u.mul(m.keep)).rgba(255)
}

// fieldAt returns the field colour of the mix t of light and dark.
func (lk *look) fieldAt(t, xRef, drRef, yRef float32) uint32 {
	switch t {
	case 0:
		return lk.light.bgAt(xRef, drRef, yRef).rgba(255)
	case 1:
		return lk.dark.bgAt(xRef, drRef, yRef).rgba(255)
	}
	return theme.MixRGBA(lk.light.bgAt(xRef, drRef, yRef).rgba(255), lk.dark.bgAt(xRef, drRef, yRef).rgba(255), t)
}

// glassAt returns the unfocused glass colour of the mix t.
func (lk *look) glassAt(t, refW, xRef, yRef float32) uint32 {
	switch t {
	case 0:
		return lk.light.fillAt(refW, xRef, yRef)
	case 1:
		return lk.dark.fillAt(refW, xRef, yRef)
	}
	return theme.MixRGBA(lk.light.fillAt(refW, xRef, yRef), lk.dark.fillAt(refW, xRef, yRef), t)
}

const (
	maxNodesX  = 20
	maxNodesY  = nStops
	maxBGCells = (maxNodesY - 1) * (maxNodesX - 1)
)

type bgCell struct {
	r [4]float32
	c [4]uint32 // TL, TR, BL, BR
}

// Field is the window background and what depends on its geometry: the node
// grid of the background mesh and its cells (four-corner colour quads). It is
// rebuilt when the window size, the scale or the light/dark mix changes
// (every frame of a live resize or a crossfade), without allocating.
type Field struct {
	valid bool
	w, h  int
	k     float32 // pixels per reference pixel (the reference is 2×)
	t     float32 // light/dark mix

	nx, ny         int
	nodesX, nodesY [maxNodesX]float32 // pixel positions of the mesh nodes
	cells          [maxBGCells]bgCell
	ncells         int

	look *look // its colours, nil: Rex's
}

func (f *Field) colors() *look {
	if f.look == nil {
		return rexLook
	}
	return f.look
}

// yPx is the pixel position of a reference height: the top 90 reference
// pixels (the titlebar) keep their size, the rest stretches with the window.
func (f *Field) yPx(y float32) float32 {
	if y <= 90 {
		return y * f.k
	}
	return 90*f.k + (y-90)*(float32(f.h)-90*f.k)/(lightStops[nStops-1].y-90)
}

// yRef is the inverse of yPx.
func (f *Field) yRef(py float32) float32 {
	if py <= 90*f.k {
		return py / f.k
	}
	return 90 + (py-90*f.k)*(lightStops[nStops-1].y-90)/(float32(f.h)-90*f.k)
}

// Build lays out the mesh for a w×h pixel window at k pixels per reference
// pixel, in the mix t of light (0) and dark (1).
func (f *Field) Build(w, h int, k, t float32) {
	if f.valid && f.w == w && f.h == h && f.k == k && f.t == t {
		return
	}
	f.valid, f.w, f.h, f.k, f.t = true, w, h, k, t
	W, H := float32(w), float32(h)
	refW := W / k

	// mesh x nodes in reference pixels: dense near both edges.
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
	f.nx, f.ny = nn, nStops
	for i := 0; i < nn; i++ {
		f.nodesX[i] = theme.Snap(nodes[i] * k)
	}
	for j := range lightStops {
		f.nodesY[j] = theme.Snap(f.yPx(lightStops[j].y))
	}

	// Corner colours once per node.
	var grid [nStops][17]uint32
	for j := range lightStops {
		for i := 0; i < nn; i++ {
			grid[j][i] = f.colors().fieldAt(t, nodes[i], refW-nodes[i], lightStops[j].y)
		}
	}
	f.ncells = 0
	for j := 0; j+1 < nStops; j++ {
		y0, y1 := f.nodesY[j], f.nodesY[j+1]
		if j+2 == nStops {
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

// fillAt is the composited colour of an unfocused surface at reference (x, y).
func (f *Field) fillAt(xRef, yRef float32) uint32 {
	return f.colors().glassAt(f.t, float32(f.w)/f.k, xRef, yRef)
}

// ---- unfocused surface fill mesh ----

const maxMeshLines = maxNodesX + 4

type meshCell struct {
	r     [4]float32
	c     [4]uint32
	radii [4]float32
}

// Mesh is the cached fill of one surface rectangle: a grid of cells whose
// corner colours are the unfocused composite. The outer cells are exactly
// 2·rad so that only they carry the rounded corners. The colours depend on
// nothing but the rectangle and the window, so one mesh can serve any surface.
type Mesh struct {
	valid bool
	rect  [4]float32
	rad   float32
	w, h  int
	k, t  float32
	n     int
	cells [(maxMeshLines - 1) * (maxMeshLines - 1)]meshCell
}

// cut returns the grid lines between lo and hi: both ends, the corner arcs'
// extents and the Field's nodes in between.
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

// Update makes m the mesh of rect r (pixel rectangle, corner radius rad).
func (m *Mesh) Update(f *Field, r [4]float32, rad float32) {
	if m.valid && m.rect == r && m.rad == rad && m.w == f.w && m.h == f.h && m.k == f.k && m.t == f.t {
		return
	}
	m.valid, m.rect, m.rad, m.w, m.h, m.k, m.t = true, r, rad, f.w, f.h, f.k, f.t
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
