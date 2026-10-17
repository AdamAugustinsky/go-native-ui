//go:build darwin

package main

import (
	"math"
	"sort"

	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/text"
)

// newScene builds the whole Rex look for a w×h pixel target at scale pixels
// per point. All coordinates below that are written in "reference pixels"
// (the 2× screenshot the look was measured from) go through sc.ref, so the
// scene is exact at 2× and scales elsewhere.
func newScene(ts *text.System, r *gpu.Renderer, w, h int, scale float32, traffic bool) *scene {
	sc := &scene{ts: ts, r: r, traffic: traffic}
	sc.items = make([]item, 0, 1024)
	sc.glyph = make([]glyph, 0, 4096)
	sc.build(w, h, scale)
	return sc
}

// build (re)creates every item for a w×h target. Fonts and icons are only
// recreated when the scale changes, so a resize reuses the atlas textures.
func (sc *scene) build(w, h int, scale float32) {
	rescale := sc.s != scale || sc.icons == nil
	sc.w, sc.h, sc.s, sc.k = w, h, scale, scale/2
	sc.items, sc.glyph = sc.items[:0], sc.glyph[:0]
	if rescale {
		t := platform.Now()
		sc.setupFonts()
		sc.textNs += platform.Now() - t
		sc.ic = sc.setupIcons()
	}
	sc.buildBackground()
	sc.buildTitlebar(sc.ic)
	sc.buildPanes(sc.ic)
	if sc.traffic {
		sc.buildTrafficLights()
	}
}

func (sc *scene) setupFonts() {
	ts, s := sc.ts, sc.s
	sc.fUI = ts.Font(text.FontDesc{SizePt: 14, Weight: 400, Scale: s})
	sc.fUISemi = ts.Font(text.FontDesc{SizePt: 12.75, Weight: 600, Scale: s})
	sc.fSub = ts.Font(text.FontDesc{SizePt: 10.8, Weight: 400, Scale: s})

	// Monospace: a system face (Menlo) sized so that the cell advance is
	// 7.5 pt, the pitch measured on the reference (see rex-regions.json).
	// If Menlo is missing CoreText substitutes a proportional font; then
	// the system monospaced design is used.
	family := "Menlo"
	probe := ts.Font(text.FontDesc{Family: family, SizePt: 12.5, Weight: 400, Scale: s})
	narrow, wide := sc.width(probe, "i"), sc.width(probe, "m")
	if math.Abs(float64(narrow-wide)) > 0.01*float64(wide) {
		family = ""
		probe = ts.Font(text.FontDesc{Mono: true, SizePt: 12.5, Weight: 400, Scale: s})
	}
	sc.monoFace = family
	if family == "" {
		sc.monoFace = "system monospaced"
	}
	adv := ts.Metrics(probe).MonoAdvance
	size := 12.5 * (7.5 * s) / adv
	sc.monoSizeP = size
	mk := func(weight int) text.FontKey {
		return ts.Font(text.FontDesc{Family: family, Mono: family == "", SizePt: size, Weight: weight, Scale: s})
	}
	sc.fMono, sc.fMonoBold = mk(400), mk(700)
	sc.adv = ts.Metrics(sc.fMono).MonoAdvance
	sc.rowPitch = 16 * s
}

// icon names → rasterised symbols
type iconSet struct {
	agent, git, node       symRect
	split2, split1, expand symRect
	xmark                  symRect
	device, command, plus  symRect
	tabAgent, tabFace      symRect
	tabTerm                symRect
}

func (sc *scene) setupIcons() *iconSet {
	a := newIconAtlas()
	px := func(pt float64) float64 { return pt * float64(sc.s) }
	reg, med := 0.0, 0.23
	var ic iconSet
	for _, e := range []struct {
		dst    *symRect
		name   string
		pt, wt float64
	}{
		{&ic.agent, "sparkles", 12.5, med},
		{&ic.git, "plusminus.circle", 12, reg},
		{&ic.node, "hexagon.fill", 12, reg},
		{&ic.split2, "rectangle.split.2x1", 11, reg},
		{&ic.split1, "rectangle.split.1x2", 11, reg},
		{&ic.expand, "arrow.up.left.and.arrow.down.right", 10.5, reg},
		{&ic.xmark, "xmark", 10.5, reg},
		{&ic.device, "macstudio", 11, reg},
		{&ic.command, "command", 12.5, med},
		{&ic.plus, "plus", 12.5, med},
		{&ic.tabAgent, "sparkles", 12, med},
		{&ic.tabFace, "face.smiling", 12, reg},
		{&ic.tabTerm, "terminal", 12, med},
	} {
		if r, ok := a.add(e.name, px(e.pt), e.wt); ok {
			*e.dst = r
		}
	}
	a.upload(sc.r)
	sc.icons = a
	return &ic
}

// glyphQuad draws symbol sr centred at (cx, cy) in pixels, tinted.
func (sc *scene) symbol(sr symRect, cx, cy float32, c uint32) {
	if sr.w == 0 {
		return
	}
	w, h := float32(sr.w), float32(sr.h)
	x0, y0 := snap(cx-w/2), snap(cy-h/2)
	sc.add(gpu.Quad{
		Rect:  [4]float32{x0, y0, x0 + w, y0 + h},
		UV:    [4]float32{float32(sr.x), float32(sr.y), float32(sr.x + sr.w), float32(sr.y + sr.h)},
		Color: [4]uint32{c, c, c, c},
		Mode:  gpu.ModeMaskR8,
		Tex:   sc.icons.tex,
	})
}

// ---- background ----

func (sc *scene) buildBackground() {
	W, H := float32(sc.w), float32(sc.h)
	refW := W / sc.k
	xs := []float32{0, 8, 16, 30, 60, 120, 240, 400}
	for _, d := range []float32{650, 400, 240, 120, 60, 30, 16, 8, 0} {
		xs = append(xs, refW-d)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	// Drop nodes that would not advance (very narrow windows).
	nodes := xs[:1]
	for _, x := range xs[1:] {
		if x > nodes[len(nodes)-1]+1 && x <= refW {
			nodes = append(nodes, x)
		}
	}
	my := sc.yPx
	px := func(refX float32) float32 { return snap(refX * sc.k) }
	sc.nodesX, sc.nodesY = sc.nodesX[:0], sc.nodesY[:0]
	for _, x := range nodes {
		sc.nodesX = append(sc.nodesX, px(x))
	}
	for _, st := range bgStops {
		sc.nodesY = append(sc.nodesY, snap(my(st.y)))
	}
	for j := 0; j+1 < len(bgStops); j++ {
		ya, yb := bgStops[j].y, bgStops[j+1].y
		y0, y1 := snap(my(ya)), snap(my(yb))
		if j+2 == len(bgStops) {
			y1 = H
		}
		for i := 0; i+1 < len(nodes); i++ {
			xa, xb := nodes[i], nodes[i+1]
			x0, x1 := px(xa), px(xb)
			if i+2 == len(nodes) {
				x1 = W
			}
			c := func(x, y float32) uint32 { return bgAt(x, refW-x, y).rgba(255) }
			sc.add(gpu.Quad{
				Rect:  [4]float32{x0, y0, x1, y1},
				Color: [4]uint32{c(xa, ya), c(xb, ya), c(xa, yb), c(xb, yb)},
			})
		}
	}
}

// yPx is the pixel position of a reference height: the top 90 reference
// pixels (the titlebar) keep their size, the rest stretches with the window.
func (sc *scene) yPx(y float32) float32 {
	if y <= 90 {
		return y * sc.k
	}
	return 90*sc.k + (y-90)*(float32(sc.h)-90*sc.k)/(bgStops[len(bgStops)-1].y-90)
}

// yRef is the inverse of yPx.
func (sc *scene) yRef(py float32) float32 {
	if py <= 90*sc.k {
		return py / sc.k
	}
	return 90 + (py-90*sc.k)*(bgStops[len(bgStops)-1].y-90)/(float32(sc.h)-90*sc.k)
}

// ---- titlebar ----

// tab icon: a front card with a symbol and coloured cards peeking out behind.
func (sc *scene) tabIcon(x0, step float32, front uint32, frontBorder uint32, back []uint32, sym symRect, symColor uint32) {
	k := sc.k
	for i := len(back); i >= 1; i-- {
		fi := float32(i)
		r := rect{(x0 + step*fi) * k, (27 + 3*fi) * k, (x0 + step*fi + 45) * k, (65 - 3*fi) * k}
		sc.add(rounded(sc.snapRect(r), col(uint32(back[i-1])>>8), 8*k))
	}
	r := sc.snapRect(rect{x0 * k, 27 * k, (x0 + 45) * k, 65 * k})
	q := rounded(r, col(front>>8), 8*k)
	q.Border, q.BorderColor = max(1, k), frontBorder
	sc.shadow(r, 1.5, 2, 8*k, 0x00000030)
	sc.add(q)
	sc.symbol(sym, (r.x0+r.x1)/2, (r.y0+r.y1)/2, symColor)
}

func (sc *scene) snapRect(r rect) rect {
	return rect{snap(r.x0), snap(r.y0), snap(r.x1), snap(r.y1)}
}

func (sc *scene) buildTitlebar(ic *iconSet) {
	k := sc.k
	W := float32(sc.w)
	rightEdge := func(x float32) float32 { return W - (2114-x)*k } // anchored to the right edge

	// Window rim: a light line along the top edge.
	// (The top rows of the background stops carry it.)

	// Tab capsule and active pill.
	capsule := sc.snapRect(rect{402 * k, 12 * k, 1734 * k, 80 * k})
	sc.add(func() gpu.Quad {
		q := vgrad(capsule, col(0xE8DCE9), col(0xE2DCE2))
		q.Radii = [4]float32{34 * k, 34 * k, 34 * k, 34 * k}
		return q
	}())

	pill := sc.snapRect(rect{405 * k, 16 * k, 847 * k, 76 * k})
	// Shadows of the pill stay inside the capsule.
	n := len(sc.items)
	sc.shadow(pill, 2, 2.5, 30*k, colA(0x3A2A4A, 0.13))
	sc.shadow(pill, 0, 1.5, 30*k, colA(0x3A2A4A, 0.14))
	for i := n; i < len(sc.items); i++ {
		sc.items[i].q.Clip = capsule.arr()
	}
	q := vgrad(pill, col(0xFFFAFF), col(0xFCF8FC))
	q.Radii = [4]float32{30 * k, 30 * k, 30 * k, 30 * k}
	sc.add(q)
	// The edge ring shows around the two end caps, not along the flat top and bottom.
	ring := rounded(pill, 0, 30*k)
	ring.Border, ring.BorderColor = max(1, k), col(0xBEB9C0)
	for _, clip := range []rect{{pill.x0 - 1, pill.y0 - 1, pill.x0 + 30*k, pill.y1 + 1}, {pill.x1 - 30*k, pill.y0 - 1, pill.x1 + 1, pill.y1 + 1}} {
		ring.Clip = clip.arr()
		sc.add(ring)
	}

	// Separators between inactive tabs.
	noClip := rect{}
	sc.add(flat(sc.snapRect(rect{1288 * k, 28 * k, 1290 * k, 63 * k}), colA(0x8C8290, 0.45)))

	// Tab icons.
	sc.tabIcon(428, 11, 0xF6F6F4FF, colA(0x000000, 0.18), []uint32{0x52B35BFF, 0x2F8F4EFF}, ic.tabAgent, col(0x23262B))
	sc.tabIcon(868, 12, 0xF6EFD6FF, colA(0x000000, 0.18), []uint32{0x3C7DBEFF}, ic.tabFace, col(0x3A3A3C))
	sc.tabIcon(1310, 8, 0x2B2D30FF, colA(0x000000, 0.35), []uint32{0xF0963CFF, 0xA266DAFF}, ic.tabTerm, col(0x6FE08A))

	// Tab titles. The active title fades out where it is cut off; the
	// inactive ones show a name and a lighter path.
	title := "Stress-test Snake demo | rex-snake"
	clip1 := rect{510 * k, 20 * k, 832 * k, 72 * k}
	sc.uiText(sc.fUI, title, 517*k, 55*k, col(0x1E2227), clip1, [2]float32{780 * k, 830 * k})

	tab := func(x float32, name, path string, clipRight, fade0, fade1 float32) {
		clip := rect{x * k, 20 * k, clipRight * k, 72 * k}
		adv := sc.uiText(sc.fUI, name, x*k, 55*k, col(0x5A5A60), clip, [2]float32{fade0 * k, fade1 * k})
		sc.uiText(sc.fUI, path, x*k+adv, 55*k, col(0x928F95), clip, [2]float32{fade0 * k, fade1 * k})
	}
	tab(946, "Bun ", "~/sites/announcement-draft", 1286, 1205, 1262)
	tab(1386, "fish ", "~/sites/macos-client-update", 1706, 1640, 1700)

	// Host block: device glyph, host name over machine name.
	sc.symbol(ic.device, 209*k, 43*k, col(0x5F5E63))
	sc.uiText(sc.fUISemi, "rex-snake", 245*k, 42*k, col(0x5F5D64), noClip, [2]float32{})
	sc.uiText(sc.fSub, "Mac Studio", 245*k, 66*k, col(0x87848B), noClip, [2]float32{})

	// Right edge: ⌘ and +.
	sc.symbol(ic.command, rightEdge(1998.5), 45*k, col(0x5F5D62))
	sc.symbol(ic.plus, rightEdge(2067.5), 45*k, col(0x5F5D62))
}

// ---- traffic lights (offscreen output only: AppKit draws them in a window) ----

func (sc *scene) buildTrafficLights() {
	k := sc.k
	for _, l := range []struct {
		cx          float32
		top, bottom uint32
		ring        uint32
	}{
		{46, 0xEC766A, 0xE48980, 0xBF3A30},
		{92, 0xF4C653, 0xF2C85E, 0xB5701F},
		{138, 0x8ED064, 0x91D068, 0x3E8F30},
	} {
		d := 28 * k
		r := sc.snapRect(rect{l.cx*k - d/2, 46*k - d/2, l.cx*k + d/2, 46*k + d/2})
		q := vgrad(r, col(l.top), col(l.bottom))
		q.Radii = [4]float32{d / 2, d / 2, d / 2, d / 2}
		q.Border, q.BorderColor = max(1, 1.5*k), colA(l.ring, 0.85)
		sc.add(q)
	}
}

// ---- panes ----

type pane struct {
	r              rect
	focused        bool
	noTopHighlight bool // the pane's top row is white (it sits under another pane's gutter)
}

func (sc *scene) layout() (p1, p2, p3 rect) {
	s := sc.s
	W, H := float32(sc.w), float32(sc.h)
	wpt, hpt := W/s, H/s
	innerW := wpt - 7.5*2 - 7
	leftW := innerW * 0.54638
	innerH := hpt - 45.5 - 7.5 - 7.5
	h1 := innerH * 0.43302
	x0 := snap(7.5 * s)
	x1 := snap((7.5 + leftW) * s)
	x2 := x1 + snap(7*s)
	x3 := W - snap(7.5*s)
	y0 := snap(45.5 * s)
	y1 := snap((45.5 + h1) * s)
	y2 := y1 + snap(7.5*s)
	y3 := H - snap(7.5*s)
	return rect{x0, y0, x1, y1}, rect{x0, y2, x1, y3}, rect{x2, y0, x3, y3}
}

func (sc *scene) buildPanes(ic *iconSet) {
	k := sc.k
	p1, p2, p3 := sc.layout()
	panes := [3]pane{{p1, true, false}, {p2, false, true}, {p3, false, false}}
	rad := 10.5 * sc.s

	// All shadows first, so no pane's shadow lands on a neighbour's fill.
	for _, p := range panes {
		if p.focused {
			sc.shadow(p.r, 1, 1.5, rad, colA(0x2A2030, 0.07))
			sc.shadow(p.r, 3, 5, rad, colA(0x2A2030, 0.05))
		} else {
			sc.shadow(p.r, 1, 1.5, rad, colA(0x2A2030, 0.035))
			sc.shadow(p.r, 3, 5, rad, colA(0x2A2030, 0.035))
		}
	}

	titles := [3]string{"Stress-test Snake demo | rex-snake", "Git Changes ~/Sites/rex-snake", "Node ~/Sites/rex-snake"}
	icons := [3]symRect{ic.agent, ic.git, ic.node}
	for i, p := range panes {
		sc.paneFill(p, rad)

		pal := &paletteDimmed
		titleCol, iconCol := col(0x606064), col(0x606064)
		if p.focused {
			pal = &paletteFocused
			titleCol, iconCol = col(0x202228), col(0x202228)
		}
		inner := p.r.inset(max(1, k))
		// Header.
		hc := p.r.y0 + 36.5*k
		sc.symbol(icons[i], p.r.x0+39*k, hc, iconCol)
		sc.uiText(sc.fUISemi, titles[i], p.r.x0+62*k, p.r.y0+47*k, titleCol, inner, [2]float32{})
		if p.focused {
			ctl := col(0x96999B)
			sc.symbol(ic.split2, p.r.x1-181.5*k, hc+k, ctl)
			sc.symbol(ic.split1, p.r.x1-133.5*k, hc+k, ctl)
			sc.symbol(ic.expand, p.r.x1-83*k, hc+k, ctl)
			sc.symbol(ic.xmark, p.r.x1-37.5*k, hc+k, ctl)
		}
		t := &term{x0: p.r.x0 + 20*k, y0: p.r.y0 + 101*k, clip: inner, pal: pal}
		switch i {
		case 0:
			sc.contentAgent(t)
		case 1:
			sc.contentGit(t)
		case 2:
			sc.contentSnake(t)
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
func (sc *scene) fillAt(xRef, yRef float32) uint32 {
	refW := float32(sc.w) / sc.k
	u := bgAt(xRef, refW-xRef, yRef)
	// The backdrop picks up the edge tints in a different hue balance than
	// the visible gutters: less green, more blue.
	v, _, _ := stopAt(yRef)
	u = v.add(vec3{u[0] - v[0], (u[1] - v[1]) * 0.5, (u[2] - v[2]) * 1.6})
	u[2] += backdropBlue(yRef)
	white := vec3{255, 255, 255}
	return white.mul(fillAlpha).add(u.mul(1 - fillAlpha)).rgba(255)
}

// meshFill covers r with a grid of cells whose corner colours come from
// fillAt. The outer cells are exactly 2·rad so that only they carry the
// rounded corners.
func (sc *scene) meshFill(r rect, rad float32) {
	cut := func(lo, hi float32, nodes []float32) []float32 {
		out := []float32{lo, lo + 2*rad}
		for _, n := range nodes {
			if n > lo+2*rad+1 && n < hi-2*rad-1 {
				out = append(out, n)
			}
		}
		return append(out, hi-2*rad, hi)
	}
	xs, ys := cut(r.x0, r.x1, sc.nodesX), cut(r.y0, r.y1, sc.nodesY)
	c := func(x, y float32) uint32 { return sc.fillAt(x/sc.k, sc.yRef(y)) }
	for j := 0; j+1 < len(ys); j++ {
		for i := 0; i+1 < len(xs); i++ {
			q := gpu.Quad{
				Rect:  [4]float32{xs[i], ys[j], xs[i+1], ys[j+1]},
				Color: [4]uint32{c(xs[i], ys[j]), c(xs[i+1], ys[j]), c(xs[i], ys[j+1]), c(xs[i+1], ys[j+1])},
			}
			if i == 0 && j == 0 {
				q.Radii[0] = rad
			}
			if i+2 == len(xs) && j == 0 {
				q.Radii[1] = rad
			}
			if i+2 == len(xs) && j+2 == len(ys) {
				q.Radii[2] = rad
			}
			if i == 0 && j+2 == len(ys) {
				q.Radii[3] = rad
			}
			sc.add(q)
		}
	}
}

// paneFill draws one pane's card: fill, light inner highlights and the
// hairline edge. The hairline runs along the sides and round the corners (the
// reference has no dark line along the flat top or bottom edge).
func (sc *scene) paneFill(p pane, rad float32) {
	k := sc.k
	if p.focused {
		sc.add(rounded(p.r, col(0xFFFFFF), rad))
	} else {
		sc.meshFill(p.r, rad)
		// Top highlight and bottom glow, straight parts of the edges only.
		x0, x1 := p.r.x0+rad, p.r.x1-rad
		sc.add(vgrad(rect{x0, p.r.y0, x1, p.r.y0 + 11*k}, colA(0xFFFFFF, 1), colA(0xFFFFFF, 0)))
		sc.add(vgrad(rect{x0, p.r.y1 - 16*k, x1, p.r.y1 - 3*k}, colA(0xFFFFE8, 0), colA(0xFFFFE8, 0.6)))
		sc.add(flat(rect{x0, p.r.y1 - 3*k, x1, p.r.y1 - 2*k}, colA(0xFFFFF4, 0.85)))
		// The last row is a darker line, like the sides' hairline.
		sc.add(flat(rect{x0, p.r.y1 - k, x1, p.r.y1}, colA(0xCEC6A4, 0.62)))
	}
	lw := max(1, k)
	if !p.noTopHighlight {
		// The first row under the gutter is a hairline about as light as the gutter itself.
		sc.add(flat(rect{p.r.x0 + rad, p.r.y0, p.r.x1 - rad, p.r.y0 + lw}, colA(0x907F95, 0.2)))
	}
	edge := rounded(p.r, 0, rad)
	edge.Border, edge.BorderColor = lw, colA(0x5C5E60, 0.25)
	rim := rounded(p.r.inset(lw), 0, rad-lw)
	rim.Border, rim.BorderColor = 2*lw, colA(0xFFFFFF, 0.55)
	for _, clip := range []rect{{p.r.x0 - 1, p.r.y0 - 1, p.r.x0 + rad + 1, p.r.y1 + 1}, {p.r.x1 - rad - 1, p.r.y0 - 1, p.r.x1 + 1, p.r.y1 + 1}} {
		if !p.focused {
			rim.Clip = clip.arr()
			sc.add(rim)
		}
		edge.Clip = clip.arr()
		sc.add(edge)
	}
}
