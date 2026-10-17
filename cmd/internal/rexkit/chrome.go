package rexkit

import "gonativeui/ui"

// DrawBackground builds the window background: the mesh of four-corner quads
// of Fld (Fld.Build must have run for the window size).
func (k *Kit) DrawBackground(c *ui.Context) {
	kg := KeyGen{Base: keyBG}
	f := &k.Fld
	for i := 0; i < f.ncells; i++ {
		cell := &f.cells[i]
		At(c, cell.r[0], cell.r[1], cell.r[2]-cell.r[0], cell.r[3]-cell.r[1])
		c.NextBG4(cell.c[0], cell.c[1], cell.c[2], cell.c[3])
		c.BoxKeyed(ui.DrawBackground|Fl, kg.Next(), nil)
	}
}

// TitlebarDrag builds the titlebar band, w pixels wide, as a window-drag
// region under whatever else the titlebar holds (ui.Context.DragRegions).
func (k *Kit) TitlebarDrag(c *ui.Context, w float32) {
	At(c, 0, 0, w, Snap(TitlebarPt*k.Scale))
	c.BoxKeyed(ui.WindowDrag|Fl, ui.KeyN(keyBar, 99), nil)
}

// DrawTrafficLights draws the window's traffic lights (for offscreen output;
// in a window the system draws them).
func (k *Kit) DrawTrafficLights(c *ui.Context) {
	kk := k.K
	kg := KeyGen{Base: ui.KeyN(keyBar, 4)}
	for _, l := range [...]struct {
		cx          float32
		top, bottom uint32
		ring        uint32
	}{
		{46, 0xEC766A, 0xE48980, 0xBF3A30},
		{92, 0xF4C653, 0xF2C85E, 0xB5701F},
		{138, 0x8ED064, 0x91D068, 0x3E8F30},
	} {
		d := 28 * kk
		x0, y0 := Snap(l.cx*kk-d/2), Snap(46*kk-d/2)
		At(c, x0, y0, Snap(l.cx*kk+d/2)-x0, Snap(46*kk+d/2)-y0)
		c.NextBG4(Col(l.top), Col(l.top), Col(l.bottom), Col(l.bottom))
		c.NextRadius(d / 2)
		c.NextBorder(ColA(l.ring, 0.85), max(1, 1.5*kk))
		c.BoxKeyed(ui.DrawBackground|ui.DrawBorder|Fl, kg.Next(), nil)
	}
}

// Radius is the corner radius of a pane.
func (k *Kit) Radius() float32 { return 10.5 * k.Scale }

// FillAlpha is the opacity of the white an unfocused pane is made of.
const FillAlpha = fillAlpha

// PaneKey is the key of a pane with the caller's id.
func PaneKey(uid uint32) ui.Key { return ui.KeyN(keyPane, uint64(uid)) }

// DrawShadow builds the shadow of the pane drawn at d, with the pane's focus
// animation at ft and its opacity opa.
func (k *Kit) DrawShadow(c *ui.Context, uid uint32, d [4]float32, ft, opa float32) {
	kk := k.K
	c.PushOpacity(opa)
	At(c, d[0], d[1], d[2]-d[0], d[3]-d[1])
	c.NextRadius(k.Radius())
	c.NextShadows(
		ui.Shadow{OffY: 1 * kk, Sigma: 1.5 * kk, Color: ColA(0x2A2030, Lerp(0.035, 0.07, ft))},
		ui.Shadow{OffY: 3 * kk, Sigma: 5 * kk, Color: ColA(0x2A2030, Lerp(0.035, 0.05, ft))})
	c.BoxKeyed(ui.DrawShadow|Fl, ui.KeyN(ui.KeyN(keyPane, uint64(uid)), 900), nil)
	c.PopOpacity()
}

// DrawFill builds the translucent fill of an unfocused pane as a mesh of
// four-corner cells, blended toward white by the focus animation.
func (k *Kit) DrawFill(c *ui.Context, kg *KeyGen, slot int, d [4]float32, ft float32) {
	m := &k.Meshes[slot]
	m.Update(&k.Fld, d, k.Radius())
	for i := 0; i < m.n; i++ {
		cell := &m.cells[i]
		At(c, cell.r[0]-d[0], cell.r[1]-d[1], cell.r[2]-cell.r[0], cell.r[3]-cell.r[1])
		c.NextBG4(MixRGBA(cell.c[0], 0xFFFFFFFF, ft), MixRGBA(cell.c[1], 0xFFFFFFFF, ft),
			MixRGBA(cell.c[2], 0xFFFFFFFF, ft), MixRGBA(cell.c[3], 0xFFFFFFFF, ft))
		c.NextRadii(cell.radii[0], cell.radii[1], cell.radii[2], cell.radii[3])
		c.BoxKeyed(ui.DrawBackground|Fl, kg.Next(), nil)
	}
}

// BeginPane opens the glass pane drawn at d: it pushes opacity opa, builds the
// clipping pane box (a Clickable box with key key) and its fill, a white one
// when focused (ft = 1) and a translucent composite over the field otherwise,
// blending as the focus animation runs, with the top highlight and bottom glow.
// top says the pane touches the top of the work area (a hairline under the
// gutter). The caller builds the pane's content with the returned KeyGen and
// then calls EndPane with the same arguments.
func (k *Kit) BeginPane(c *ui.Context, key ui.Key, slot int, d [4]float32, ft, opa float32, top bool) KeyGen {
	kk := k.K
	rad := k.Radius()
	lw := max(1, kk)
	w, h := d[2]-d[0], d[3]-d[1]
	dim := 1 - ft

	c.PushOpacity(opa)
	flags := ui.Clickable | ui.Clip | Fl
	// A pane too small to carry the fill mesh (a tiny window) gets a flat fill.
	flat := w < 4*rad+2 || h < 4*rad+2
	if ft >= 1 {
		flags |= ui.DrawBackground
		c.NextBG(0xFFFFFFFF)
	} else if flat {
		flags |= ui.DrawBackground
		c.NextBG(ColA(0xFFFFFF, Lerp(fillAlpha, 1, ft)))
	}
	At(c, d[0], d[1], w, h)
	c.NextRadius(rad)
	pb := c.BoxKeyed(flags, key, nil)
	c.Push(pb)
	kg := KeyGen{Base: ui.KeyN(key, 500)}

	if ft < 1 && !flat {
		k.DrawFill(c, &kg, slot, d, ft)
		// Top highlight and bottom glow, straight parts of the edges only.
		x0, x1 := rad, w-rad
		At(c, x0, 0, x1-x0, 11*kk)
		c.NextBG4(ColA(0xFFFFFF, dim), ColA(0xFFFFFF, dim), ColA(0xFFFFFF, 0), ColA(0xFFFFFF, 0))
		c.BoxKeyed(ui.DrawBackground|Fl, kg.Next(), nil)
		At(c, x0, h-16*kk, x1-x0, 13*kk)
		c.NextBG4(ColA(0xFFFFE8, 0), ColA(0xFFFFE8, 0), ColA(0xFFFFE8, 0.6*dim), ColA(0xFFFFE8, 0.6*dim))
		c.BoxKeyed(ui.DrawBackground|Fl, kg.Next(), nil)
		At(c, x0, h-3*kk, x1-x0, kk)
		c.NextBG(ColA(0xFFFFF4, 0.85*dim))
		c.BoxKeyed(ui.DrawBackground|Fl, kg.Next(), nil)
		// The last row is a darker line, like the sides' hairline.
		At(c, x0, h-kk, x1-x0, kk)
		c.NextBG(ColA(0xCEC6A4, 0.62*dim))
		c.BoxKeyed(ui.DrawBackground|Fl, kg.Next(), nil)
	}
	if top {
		// The first row under the gutter is a hairline about as light as the gutter itself.
		At(c, rad, 0, w-2*rad, lw)
		c.NextBG(ColA(0x907F95, 0.2))
		c.BoxKeyed(ui.DrawBackground|Fl, kg.Next(), nil)
	}
	return kg
}

// EndPane builds the pane's edges (a light inner rim, unfocused only, and the
// hairline, along the sides and round the corners but not along the flat top
// and bottom) and closes the pane BeginPane opened.
func (k *Kit) EndPane(c *ui.Context, kg *KeyGen, d [4]float32, ft float32) {
	kk := k.K
	rad := k.Radius()
	lw := max(1, kk)
	w, h := d[2]-d[0], d[3]-d[1]
	dim := 1 - ft
	if ft < 1 {
		At(c, lw, lw, w-2*lw, h-2*lw)
		c.NextRadius(rad - lw)
		c.NextBorder(ColA(0xFFFFFF, 0.55*dim), 2*lw)
		c.NextBorderSides(ui.SideLeft | ui.SideRight)
		c.BoxKeyed(ui.DrawBorder|Fl, kg.Next(), nil)
	}
	At(c, 0, 0, w, h)
	c.NextRadius(rad)
	c.NextBorder(ColA(0x5C5E60, 0.25), lw)
	c.NextBorderSides(ui.SideLeft | ui.SideRight)
	c.BoxKeyed(ui.DrawBorder|Fl, kg.Next(), nil)
	c.Pop()
	c.PopOpacity()
}

// GutterGrabPt is the least width, in points, of a divider's grab area across
// the gutter.
const GutterGrabPt = 13

// Divider builds the grab area of the gutter gut between two panes that
// together fill rect: a box centred on the gutter, at least GutterGrabPt wide
// across it and running the whole length of the split. It carries the resize
// cursor, so the window shows it over the whole box (and, through the active
// box, wherever the pointer goes while the divider is dragged). A divider
// shows a slim handle while the pointer is on it or it is dragged.
func (k *Kit) Divider(c *ui.Context, key ui.Key, axis ui.Axis, rect, gut [4]float32) {
	s := k.Scale
	g := gut
	cur := ui.CursorResizeEW
	x0, y0, x1, y1 := g[0], rect[1], g[2], rect[3]
	if axis == ui.AxisY {
		cur = ui.CursorResizeNS
		x0, y0, x1, y1 = rect[0], g[1], rect[2], g[3]
	}
	// Grow across the gutter to the grab width, evenly on both sides.
	if axis == ui.AxisY {
		grow := max(GutterGrabPt*s-(y1-y0), 6*s) / 2
		y0, y1 = Snap(y0-grow), Snap(y1+grow)
	} else {
		grow := max(GutterGrabPt*s-(x1-x0), 6*s) / 2
		x0, x1 = Snap(x0-grow), Snap(x1+grow)
	}
	show := max(c.HotTOf(key), c.ActiveTOf(key))
	At(c, x0, y0, x1-x0, y1-y0)
	c.NextCursor(cur)
	c.BoxKeyed(ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|Fl, key, nil)
	if show > 0.004 {
		// the handle: a short rounded bar centred in the gutter
		hx, hy := (g[0]+g[2])/2, (g[1]+g[3])/2
		w, h := 3*s, min(56*s, 0.4*(g[3]-g[1]))
		if axis == ui.AxisY {
			w, h = min(56*s, 0.4*(g[2]-g[0])), 3*s
		}
		At(c, Snap(hx-w/2), Snap(hy-h/2), Snap(w), Snap(h))
		c.NextBG(ColA(0x6E6378, 0.45*show))
		c.NextRadius(1.5 * s)
		c.BoxKeyed(ui.DrawBackground|Fl, ui.KeyN(key, 1), nil)
	}
}
