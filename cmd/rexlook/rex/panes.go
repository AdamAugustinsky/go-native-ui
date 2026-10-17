package rex

import "gonativeui/ui"

const (
	glideRate   = 30   // 1/s: how fast panes move to their places after a split or close
	instantRate = 4000 // a rate that reaches the target within the frame
	dimRate     = 20   // 1/s: focus dimming
	maxRate     = 20   // 1/s: maximise and restore
	fadeRate    = 18   // 1/s: content fade-in on a tab change, a new pane
)

var (
	keyBorn = ui.HashString(keyAnim, "born")
)

type paneTitle struct {
	title string
	icon  iconID
}

var paneTitles = [...]paneTitle{
	kindAgent: {"Stress-test Snake demo | rex-snake", icAgent},
	kindGit:   {"Git Changes ~/Sites/rex-snake", icGit},
	kindSnake: {"Node ~/Sites/rex-snake", icNode},
	kindBun:   {"Bun ~/sites/announcement-draft", icNode},
	kindShell: {"fish ~/Sites/rex-snake", icTabTerm},
}

// work is the area the panes live in, in pixels.
func (r *Rex) work() [4]float32 {
	s := r.scale
	W, H := float32(r.lastW), float32(r.lastH)
	return [4]float32{snap(7.5 * s), snap(45.5 * s), W - snap(7.5*s), H - snap(7.5*s)}
}

func (r *Rex) gutters() gutters {
	s := r.scale
	return gutters{x: snap(7 * s), y: snap(7.5 * s), minW: 150 * s, minH: 100 * s}
}

func (r *Rex) drawPanes(c *ui.Context) {
	t := &r.tabs[r.active]
	work := r.work()
	g := r.gutters()
	t.layout(work, &g)

	initial := float32(1)
	if r.switchN > 0 {
		initial = 0
	}
	fade := c.AnimateValueFrom(ui.KeyN(keyFade, uint64(r.switchN)), initial, 1, fadeRate)
	anyMax := float32(0)
	if t.max != 0 {
		anyMax = 1
	}
	anyMax = c.AnimateValue(ui.KeyN(keyAnyT, uint64(t.uid)), anyMax, maxRate)

	// Displayed rectangle, focus and opacity of each pane.
	var (
		ord     [maxPanes]int8
		n       int
		ft, opa [maxPanes]float32
	)
	gk := float32(instantRate)
	if r.glide {
		gk = glideRate
	}
	r.moving = false
	for slot := range t.panes {
		p := &t.panes[slot]
		if !p.used {
			continue
		}
		isMax := p.uid == t.max
		if !isMax && anyMax >= 1 {
			continue // hidden behind the maximised pane
		}
		tr := t.rects[slot]
		var d [4]float32
		for i := 0; i < 4; i++ {
			key := ui.KeyN(ui.KeyN(keyGlide, uint64(p.uid)), uint64(i))
			var v float32
			if p.born {
				v = c.AnimateValueFrom(key, p.from[i], tr[i], gk)
			} else {
				v = c.AnimateValue(key, tr[i], gk)
			}
			if v != tr[i] {
				r.moving = true
			}
			d[i] = v
		}
		mt := float32(0)
		if isMax {
			mt = 1
		}
		mt = c.AnimateValue(ui.KeyN(keyMaxT, uint64(p.uid)), mt, maxRate)
		for i := 0; i < 4; i++ {
			d[i] = snap(lerp(d[i], work[i], mt))
		}
		r.disp[slot] = d

		foc := float32(0)
		if t.focus == p.uid {
			foc = 1
		}
		ft[slot] = c.AnimateValue(ui.KeyN(keyFocus, uint64(p.uid)), foc, dimRate)
		bornInit := float32(1)
		if p.born {
			bornInit = 0
		}
		o := c.AnimateValueFrom(ui.KeyN(keyBorn, uint64(p.uid)), bornInit, 1, fadeRate)
		if !isMax {
			o *= 1 - anyMax
		}
		opa[slot] = o
		p.born = false

		ord[n] = int8(slot)
		n++
	}
	if !r.moving {
		r.glide = false
	}
	// The maximised pane goes last.
	for i := 0; i < n; i++ {
		if t.panes[ord[i]].uid == t.max {
			ord[i], ord[n-1] = ord[n-1], ord[i]
			break
		}
	}

	c.PushOpacity(fade)
	// All shadows first, so no pane's shadow lands on a neighbour's fill.
	for _, slot := range ord[:n] {
		r.drawShadow(c, t.panes[slot].uid, r.disp[slot], ft[slot], opa[slot])
	}
	for _, slot := range ord[:n] {
		r.drawPane(c, t, slot, r.disp[slot], ft[slot], opa[slot])
	}
	if t.max == 0 && anyMax < 0.001 {
		r.drawDividers(c, t)
	}
	c.PopOpacity()
}

func (r *Rex) radius() float32 { return 10.5 * r.scale }

func (r *Rex) drawShadow(c *ui.Context, uid uint32, d [4]float32, ft, opa float32) {
	k := r.k
	c.PushOpacity(opa)
	at(c, d[0], d[1], d[2]-d[0], d[3]-d[1])
	c.NextRadius(r.radius())
	c.NextShadows(
		ui.Shadow{OffY: 1 * k, Sigma: 1.5 * k, Color: colA(0x2A2030, lerp(0.035, 0.07, ft))},
		ui.Shadow{OffY: 3 * k, Sigma: 5 * k, Color: colA(0x2A2030, lerp(0.035, 0.05, ft))})
	c.BoxKeyed(ui.DrawShadow|fl, ui.KeyN(ui.KeyN(keyPane, uint64(uid)), 900), nil)
	c.PopOpacity()
}

// drawPane builds one pane: its fill (white when focused, a translucent
// composite when not, blending as the focus animation runs), the header, the
// terminal text and the hairline edge.
func (r *Rex) drawPane(c *ui.Context, t *tab, slot int8, d [4]float32, ft, opa float32) {
	k, s := r.k, r.scale
	p := &t.panes[slot]
	rad := r.radius()
	lw := max(1, k)
	w, h := d[2]-d[0], d[3]-d[1]
	key := paneKey(p.uid)
	dim := 1 - ft

	c.PushOpacity(opa)
	defer c.PopOpacity()
	flags := ui.Clickable | ui.Clip | fl
	// A pane too small to carry the fill mesh (a tiny window) gets a flat fill.
	flat := w < 4*rad+2 || h < 4*rad+2
	if ft >= 1 {
		flags |= ui.DrawBackground
		c.NextBG(0xFFFFFFFF)
	} else if flat {
		flags |= ui.DrawBackground
		c.NextBG(colA(0xFFFFFF, lerp(fillAlpha, 1, ft)))
	}
	at(c, d[0], d[1], w, h)
	c.NextRadius(rad)
	pb := c.BoxKeyed(flags, key, nil)
	c.Push(pb)
	defer c.Pop()
	kg := keyGen{base: ui.KeyN(key, 500)}

	if ft < 1 && !flat {
		r.drawFill(c, &kg, slot, d, ft)
		// Top highlight and bottom glow, straight parts of the edges only.
		x0, x1 := rad, w-rad
		at(c, x0, 0, x1-x0, 11*k)
		c.NextBG4(colA(0xFFFFFF, dim), colA(0xFFFFFF, dim), colA(0xFFFFFF, 0), colA(0xFFFFFF, 0))
		c.BoxKeyed(ui.DrawBackground|fl, kg.next(), nil)
		at(c, x0, h-16*k, x1-x0, 13*k)
		c.NextBG4(colA(0xFFFFE8, 0), colA(0xFFFFE8, 0), colA(0xFFFFE8, 0.6*dim), colA(0xFFFFE8, 0.6*dim))
		c.BoxKeyed(ui.DrawBackground|fl, kg.next(), nil)
		at(c, x0, h-3*k, x1-x0, k)
		c.NextBG(colA(0xFFFFF4, 0.85*dim))
		c.BoxKeyed(ui.DrawBackground|fl, kg.next(), nil)
		// The last row is a darker line, like the sides' hairline.
		at(c, x0, h-k, x1-x0, k)
		c.NextBG(colA(0xCEC6A4, 0.62*dim))
		c.BoxKeyed(ui.DrawBackground|fl, kg.next(), nil)
	}
	if d[1] <= r.work()[1]+0.5 {
		// The first row under the gutter is a hairline about as light as the gutter itself.
		at(c, rad, 0, w-2*rad, lw)
		c.NextBG(colA(0x907F95, 0.2))
		c.BoxKeyed(ui.DrawBackground|fl, kg.next(), nil)
	}

	// Header: process icon and title; the buttons only on the focused pane.
	hc := 36.5 * k
	titleCol := col(mixRGB(0x606064, 0x202228, ft))
	pt := paneTitles[p.kind]
	r.iconAt(c, &kg, pt.icon, 39*k, hc, titleCol)
	avail := w - 62*k - lerp(20*k, 206*k, ft)
	if avail > 4 {
		adv := r.width(r.fUISemi, pt.title)
		if adv > avail {
			r.put(c, &kg, r.fUISemi, pt.title, 62*k, 47*k, titleCol, avail, ui.FadeOverflowX)
		} else {
			r.put(c, &kg, r.fUISemi, pt.title, 62*k, 47*k, titleCol, adv+2, 0)
		}
	}
	if ft > 0.02 {
		c.PushOpacity(ft)
		icons := [numBtns]iconID{icSplit2, icSplit1, icExpand, icXmark}
		dx := [numBtns]float32{181.5, 133.5, 83, 37.5}
		for b := 0; b < numBtns; b++ {
			enabled := ft > 0.5
			switch b {
			case btnSplitX, btnSplitY:
				enabled = enabled && r.canSplit()
			case btnClose:
				enabled = enabled && r.canClose()
			}
			r.iconButton(c, btnKey(p.uid, b), icons[b], w-dx[b]*k, hc+k, 28*k, 0x96999B, 0x3F4246, enabled)
		}
		c.PopOpacity()
	}

	// Terminal content.
	g := grid{r: r, c: c, kg: keyGen{base: ui.KeyN(key, 700)}, x0: 20 * k, y0: 101 * k, pal: mixPalette(ft), ft: ft, w: w, h: h}
	switch p.kind {
	case kindAgent:
		g.agent()
	case kindGit:
		g.git()
	case kindSnake:
		g.snake()
	case kindBun:
		g.bun()
	default:
		g.shell()
	}

	// Edges: a light inner rim (unfocused only) and the hairline, along the
	// sides and round the corners but not along the flat top and bottom.
	if ft < 1 {
		at(c, lw, lw, w-2*lw, h-2*lw)
		c.NextRadius(rad - lw)
		c.NextBorder(colA(0xFFFFFF, 0.55*dim), 2*lw)
		c.NextBorderSides(ui.SideLeft | ui.SideRight)
		c.BoxKeyed(ui.DrawBorder|fl, kg.next(), nil)
	}
	at(c, 0, 0, w, h)
	c.NextRadius(rad)
	c.NextBorder(colA(0x5C5E60, 0.25), lw)
	c.NextBorderSides(ui.SideLeft | ui.SideRight)
	c.BoxKeyed(ui.DrawBorder|fl, kg.next(), nil)
	_ = s
}

// drawFill builds the translucent fill of an unfocused pane as a mesh of
// four-corner cells, blended toward white by the focus animation.
func (r *Rex) drawFill(c *ui.Context, kg *keyGen, slot int8, d [4]float32, ft float32) {
	m := &r.meshes[slot]
	m.update(&r.fld, d, r.radius())
	for i := 0; i < m.n; i++ {
		cell := &m.cells[i]
		at(c, cell.r[0]-d[0], cell.r[1]-d[1], cell.r[2]-cell.r[0], cell.r[3]-cell.r[1])
		c.NextBG4(mixRGBA(cell.c[0], 0xFFFFFFFF, ft), mixRGBA(cell.c[1], 0xFFFFFFFF, ft),
			mixRGBA(cell.c[2], 0xFFFFFFFF, ft), mixRGBA(cell.c[3], 0xFFFFFFFF, ft))
		c.NextRadii(cell.radii[0], cell.radii[1], cell.radii[2], cell.radii[3])
		c.BoxKeyed(ui.DrawBackground|fl, kg.next(), nil)
	}
}

// gutterGrabPt is the least width, in points, of a divider's grab area across
// the gutter.
const gutterGrabPt = 13

// drawDividers builds the grab areas of the gutters between panes: a box
// centred on the gutter, at least gutterGrabPt wide across it and running the
// whole length of the split. It carries the resize cursor, so the window shows
// it over the whole box (and, through the active box, wherever the pointer
// goes while the divider is dragged). A divider shows a slim handle while the
// pointer is on it or it is dragged.
func (r *Rex) drawDividers(c *ui.Context, t *tab) {
	s := r.scale
	for i := range t.nodes {
		n := &t.nodes[i]
		if !n.used || n.leaf {
			continue
		}
		key := divKey(n.uid)
		g := n.gut
		cur := ui.CursorResizeEW
		x0, y0, x1, y1 := g[0], n.rect[1], g[2], n.rect[3]
		if n.axis == ui.AxisY {
			cur = ui.CursorResizeNS
			x0, y0, x1, y1 = n.rect[0], g[1], n.rect[2], g[3]
		}
		// Grow across the gutter to the grab width, evenly on both sides.
		if n.axis == ui.AxisY {
			grow := max(gutterGrabPt*s-(y1-y0), 6*s) / 2
			y0, y1 = snap(y0-grow), snap(y1+grow)
		} else {
			grow := max(gutterGrabPt*s-(x1-x0), 6*s) / 2
			x0, x1 = snap(x0-grow), snap(x1+grow)
		}
		show := max(c.HotTOf(key), c.ActiveTOf(key))
		at(c, x0, y0, x1-x0, y1-y0)
		c.NextCursor(cur)
		c.BoxKeyed(ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|fl, key, nil)
		if show > 0.004 {
			// the handle: a short rounded bar centred in the gutter
			hx, hy := (g[0]+g[2])/2, (g[1]+g[3])/2
			w, h := 3*s, min(56*s, 0.4*(g[3]-g[1]))
			if n.axis == ui.AxisY {
				w, h = min(56*s, 0.4*(g[2]-g[0])), 3*s
			}
			at(c, snap(hx-w/2), snap(hy-h/2), snap(w), snap(h))
			c.NextBG(colA(0x6E6378, 0.45*show))
			c.NextRadius(1.5 * s)
			c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 1), nil)
		}
	}
}
