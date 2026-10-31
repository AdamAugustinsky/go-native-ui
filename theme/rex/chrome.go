package rex

import (
	"unsafe"

	"gonativeui/theme"
	"gonativeui/ui"
)

var (
	keyRoot = ui.HashString(0, "theme.rex")
	keyBG   = ui.HashString(keyRoot, "bg")
	keyBar  = ui.HashString(keyRoot, "titlebar")
)

// fl is the flag set of a floating box.
const fl = theme.Floating

// chrome is the Chrome of one Rex window: the background field and the fill
// meshes of its unfocused surfaces.
type chrome struct {
	fld    Field
	meshes []*Mesh
}

var _ theme.Chrome = (*chrome)(nil)

// ensure builds the background field for the window's size and the theme's
// mix; it is a no-op while neither has changed.
func (ch *chrome) ensure(c *ui.Context, r *theme.Resolved) {
	w, h := c.Size()
	ch.fld.Build(w, h, r.Scale/2, r.T)
}

// The scrim over a backdrop (Background): the light field's pastel and the
// dark window's grey, and how much of them covers the material.
const (
	scrimLight, scrimDark           = 0xF5EEF5, 0x1E1F2A
	scrimAlphaLight, scrimAlphaDark = 0.30, 0.45
)

// tone is a colour with alpha: the RGB is the light or dark one mixed by the
// theme's mix t, the alpha the light or dark one (mixed likewise) times scale.
func tone(r *theme.Resolved, lightRGB, darkRGB uint32, lightA, darkA, scale float32) uint32 {
	return theme.ColA(theme.MixRGB(lightRGB, darkRGB, r.T), theme.Lerp(lightA, darkA, r.T)*scale)
}

// Background builds the mesh of four-corner quads of the field. Over a
// backdrop the material is the field, under one scrim of the field's tone:
// the glass shows what is behind the window (measured on 27.2: light glass
// spans #9E9E9E over a dark window to #FCFCFC over a light one, dark glass
// #2C2C2C to #6D6D6D), and the scrim narrows that range, so that text over
// it keeps AppKit's contrast floors (cmd/rexlook's titlebar,
// docs/design/native-shell-spike.md).
func (ch *chrome) Background(c *ui.Context, r *theme.Resolved) {
	ch.ensure(c, r)
	if r.Backdrop {
		w, h := c.Size()
		theme.At(c, 0, 0, float32(w), float32(h))
		sc := ch.fld.colors().scrim
		c.NextBG(tone(r, sc[0], sc[1], scrimAlphaLight, scrimAlphaDark, 1))
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(keyBG, 1<<20), nil)
		return
	}
	w, h := c.Size()
	theme.At(c, 0, 0, float32(w), float32(h))
	c.Mesh(fl, keyBG, ch.fld.cells[:ch.fld.ncells])
}

// SurfaceShadow builds the two shadow layers of a pane: a tight one and an
// ambient one, stronger while the pane is focused.
func (ch *chrome) SurfaceShadow(c *ui.Context, r *theme.Resolved, s theme.Surface) {
	kk := r.Scale / 2
	d, ft := s.Rect, s.Focus
	rgb := r.Pal.Shadow >> 8
	a1 := theme.Lerp(theme.Lerp(0.035, 0.22, r.T), theme.Lerp(0.07, 0.40, r.T), ft)
	a2 := theme.Lerp(theme.Lerp(0.035, 0.22, r.T), theme.Lerp(0.05, 0.30, r.T), ft)
	c.PushOpacity(s.Opacity)
	theme.At(c, d[0], d[1], d[2]-d[0], d[3]-d[1])
	c.NextRadius(paneRadius(r))
	c.NextShadows(
		ui.Shadow{OffY: 1 * kk, Sigma: 1.5 * kk, Color: theme.ColA(rgb, a1)},
		ui.Shadow{OffY: 3 * kk, Sigma: 5 * kk, Color: theme.ColA(rgb, a2)})
	c.BoxKeyed(ui.DrawShadow|fl, ui.KeyN(s.Key, 900), nil)
	c.PopOpacity()
}

// drawFill builds the translucent fill of an unfocused pane as a mesh of
// four-corner cells, blended toward the focused surface colour by the focus
// animation.
func (ch *chrome) drawFill(c *ui.Context, r *theme.Resolved, s theme.Surface, kg *theme.KeyGen) {
	for len(ch.meshes) <= s.Slot {
		ch.meshes = append(ch.meshes, new(Mesh))
	}
	m := ch.meshes[s.Slot]
	d, ft := s.Rect, s.Focus
	// the glass samples the background where the surface is in the window
	win := [4]float32{d[0] + s.Origin[0], d[1] + s.Origin[1], d[2] + s.Origin[0], d[3] + s.Origin[1]}
	m.Update(&ch.fld, win, paneRadius(r))
	theme.At(c, 0, 0, d[2]-d[0], d[3]-d[1])
	c.Mesh(fl, kg.Next(), m.fillQuads(ft, r.Pal.Surface|0xFF))
}

// BeginSurface opens the glass pane: it pushes the opacity, builds the clipping
// pane box and its fill, the focused surface colour when focused (Focus = 1)
// and a translucent composite over the field otherwise, blending as the focus
// animation runs, with the top highlight and bottom glow.
func (ch *chrome) BeginSurface(c *ui.Context, r *theme.Resolved, s theme.Surface) theme.KeyGen {
	ch.ensure(c, r)
	kk := r.Scale / 2
	rad := paneRadius(r)
	lw := r.Px.BorderW
	d, ft := s.Rect, s.Focus
	w, h := d[2]-d[0], d[3]-d[1]
	dim := 1 - ft

	c.PushOpacity(s.Opacity)
	flags := ui.Clickable | ui.Clip | fl
	// A pane too small to carry the fill mesh (a tiny window) gets a flat fill,
	// and so does every pane over a backdrop: the fill is then really
	// translucent, over the material, where the mesh mixes it with the field.
	tiny := w < 4*rad+2 || h < 4*rad+2
	flat := tiny || r.Backdrop
	surf := r.Pal.Surface
	if ft >= 1 {
		flags |= ui.DrawBackground
		c.NextBG(surf | 0xFF)
	} else if flat {
		flags |= ui.DrawBackground
		a := theme.Lerp(theme.Lerp(fillAlpha, darkAlpha, r.T), 1, ft)
		c.NextBG(theme.ColA(surf>>8, a))
	}
	theme.At(c, d[0], d[1], w, h)
	c.NextRadius(rad)
	// An opaque fill (the focused colour, or the mesh over the field) hides
	// what the window drew under the pane: its shadows, the field, tints.
	if !tiny && (ft >= 1 || !flat) {
		c.NextCover()
	}
	pb := c.BoxKeyed(flags, s.Key, nil)
	c.Push(pb)
	kg := theme.KeyGen{Base: ui.KeyN(s.Key, 500)}

	if ft < 1 && !tiny {
		if !flat {
			ch.drawFill(c, r, s, &kg)
		}
		// Top highlight and bottom glow, the pane's whole width and round its
		// corners (the pane's clip is square: each takes the corners' radii,
		// at least as tall as they are, so that it ends where the pane does
		// rather than short of the corners).
		x0, x1 := rad, w-rad
		th := max(11*kk, rad)
		theme.At(c, 0, 0, w, th)
		hi, hi0 := tone(r, 0xFFFFFF, 0xFFFFFF, 1, 0.035, dim), tone(r, 0xFFFFFF, 0xFFFFFF, 1, 0.035, 0)
		c.NextBG4(hi, hi, hi0, hi0)
		c.NextRadii(rad, rad, 0, 0)
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
		gh := max(16*kk, rad)
		theme.At(c, 0, h-gh, w, gh-3*kk)
		gl, gl0 := tone(r, 0xFFFFE8, 0xFFEFD0, 0.6, 0.05, dim), tone(r, 0xFFFFE8, 0xFFEFD0, 0.6, 0.05, 0)
		c.NextBG4(gl0, gl0, gl, gl)
		c.NextRadii(0, 0, max(rad-3*kk, 0), max(rad-3*kk, 0))
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
		// a glint along the foot, fading out toward the corners
		glint := tone(r, 0xFFFFF4, 0xFFFFFF, 0.85, 0, dim)
		g0 := glint &^ 0xFF
		capW := min(rad, (x1-x0)/2)
		theme.At(c, x0, h-3*kk, capW, kk)
		c.NextBG4(g0, glint, g0, glint)
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
		theme.At(c, x0+capW, h-3*kk, max(x1-x0-2*capW, 0), kk)
		c.NextBG(glint)
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
		theme.At(c, x1-capW, h-3*kk, capW, kk)
		c.NextBG4(glint, g0, glint, g0)
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
		// The last row is a darker line, like the sides' hairline.
		theme.At(c, x0, h-kk, x1-x0, kk)
		c.NextBG(tone(r, 0xCEC6A4, 0x000000, 0.62, 0.30, dim))
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
	}
	if s.Top {
		// The first row under the gutter is a hairline about as light as the gutter itself.
		theme.At(c, rad, 0, w-2*rad, lw)
		c.NextBG(tone(r, 0x907F95, 0xFFFFFF, 0.2, 0.09, 1))
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
	}
	return kg
}

// EndSurface builds the pane's edges (a light inner rim, unfocused only, and
// the hairline, along the sides and round the corners but not along the flat
// top and bottom) and closes the pane BeginSurface opened.
func (ch *chrome) EndSurface(c *ui.Context, r *theme.Resolved, s theme.Surface, kg theme.KeyGen) {
	rad := paneRadius(r)
	lw := r.Px.BorderW
	d, ft := s.Rect, s.Focus
	w, h := d[2]-d[0], d[3]-d[1]
	dim := 1 - ft
	if ft < 1 {
		theme.At(c, lw, lw, w-2*lw, h-2*lw)
		c.NextRadius(rad - lw)
		c.NextBorder(tone(r, 0xFFFFFF, 0xFFFFFF, 0.55, 0.06, dim), 2*lw)
		c.NextBorderSides(ui.SideLeft | ui.SideRight)
		c.BoxKeyed(ui.DrawBorder|fl, kg.Next(), nil)
	}
	theme.At(c, 0, 0, w, h)
	c.NextRadius(rad)
	if r.Settings.IncreaseContrast {
		// the pane's profile outlines it all round, in a colour that holds
		// its contrast (theme.SurfaceProfile)
		p := r.Surface(theme.SurfacePane, false)
		c.NextBorder(p.Stroke, max(lw, p.StrokeW))
	} else {
		c.NextBorder(tone(r, 0x5C5E60, 0xFFFFFF, 0.25, 0.11, 1), lw)
		c.NextBorderSides(ui.SideLeft | ui.SideRight)
	}
	c.BoxKeyed(ui.DrawBorder|fl, kg.Next(), nil)
	c.Pop()
	c.PopOpacity()
}

// Divider builds the grab area of the gutter, at least DividerGrab wide
// across it, with the resize cursor, and the slim handle shown while the
// pointer is on it, it is dragged, or it has the keyboard focus.
func (ch *chrome) Divider(c *ui.Context, r *theme.Resolved, key ui.Key, axis ui.Axis, rect, gut [4]float32) ui.ID {
	s := r.Scale
	g := gut
	cur := ui.CursorResizeEW
	x0, y0, x1, y1 := g[0], rect[1], g[2], rect[3]
	if axis == ui.AxisY {
		cur = ui.CursorResizeNS
		x0, y0, x1, y1 = rect[0], g[1], rect[2], g[3]
	}
	// Grow across the gutter to the grab width, evenly on both sides.
	if axis == ui.AxisY {
		grow := max(r.Px.DividerGrab-(y1-y0), 6*s) / 2
		y0, y1 = theme.Snap(y0-grow), theme.Snap(y1+grow)
	} else {
		grow := max(r.Px.DividerGrab-(x1-x0), 6*s) / 2
		x0, x1 = theme.Snap(x0-grow), theme.Snap(x1+grow)
	}
	show := max(c.HotTOf(key), c.ActiveTOf(key))
	if c.FocusVisible() {
		show = max(show, c.FocusTOf(key)) // a divider focused from the keyboard shows where it is
	}
	theme.At(c, x0, y0, x1-x0, y1-y0)
	c.NextCursor(cur)
	id := c.BoxKeyed(ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|fl, key, nil)
	if show > 0.004 {
		// the handle: a short rounded bar centred in the gutter
		hx, hy := (g[0]+g[2])/2, (g[1]+g[3])/2
		w, h := 3*s, min(56*s, 0.4*(g[3]-g[1]))
		if axis == ui.AxisY {
			w, h = min(56*s, 0.4*(g[2]-g[0])), 3*s
		}
		theme.At(c, theme.Snap(hx-w/2), theme.Snap(hy-h/2), theme.Snap(w), theme.Snap(h))
		c.NextBG(tone(r, 0x6E6378, 0xB9B3CC, 0.45, 0.5, show))
		c.NextRadius(1.5 * s)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 1), nil)
	}
	return id
}

// paneRadius is the corner radius of a pane: its surface profile's, which
// follows the app's density (theme.Resolved.SetDensity).
func paneRadius(r *theme.Resolved) float32 { return r.Surface(theme.SurfacePane, false).Radius }

// strBytes views s as bytes without copying.
func strBytes(s string) []byte { return unsafe.Slice(unsafe.StringData(s), len(s)) }

// Titlebar builds the drag region over the titlebar band, then the traffic
// lights or the caption buttons, then the title.
func (ch *chrome) Titlebar(c *ui.Context, r *theme.Resolved, t theme.Titlebar) theme.TitlebarResult {
	h := theme.Snap(r.Px.TitlebarH)
	theme.At(c, 0, 0, t.Width, h)
	c.BoxKeyed(ui.WindowDrag|fl, ui.KeyN(keyBar, 99), nil)

	// The band the buttons leave: the system's own (ui.Context.SystemButtons;
	// empty where the OS draws none over the content), or the decorative ones,
	// which sit where the system's would, through the same function.
	res := theme.TitlebarResult{Free: theme.FreeBand(t.Width, r.Px.Gap, c.SystemButtons())}
	switch t.Buttons {
	case theme.ButtonsTraffic:
		trafficLights(c, r)
		res.Free = theme.FreeBand(t.Width, r.Px.Gap, theme.TrafficRect(r.Scale))
	case theme.ButtonsCaption:
		res.Free = [2]float32{0, t.Width}
		res.Clicked, res.Free[1] = captionButtons(c, r, t, h)
	}
	if t.Title != "" {
		fm := r.Met(r.Font.UISemi)
		col := r.Pal.Text
		if !t.Active {
			col = r.Pal.TextMuted
		}
		pad := r.Px.Pad
		x0, x1 := res.Free[0]+pad, res.Free[1]-pad
		if x1 > x0 {
			theme.At(c, x0, theme.Snap((h-fm.H)/2), x1-x0, fm.H)
			c.NextFont(r.Font.UISemi)
			c.NextTextColor(col)
			c.NextAlign(ui.AlignCenter)
			c.BoxKeyed(ui.DrawText|ui.FadeOverflowX|fl, ui.KeyN(keyBar, 98), strBytes(t.Title))
		}
	}
	return res
}

// trafficLights draws the three macOS window buttons (for offscreen output;
// in a window the system draws them) at theme.TrafficDiscs.
func trafficLights(c *ui.Context, r *theme.Resolved) {
	kk := r.Scale / 2
	kg := theme.KeyGen{Base: ui.KeyN(keyBar, 4)}
	discs := theme.TrafficDiscs(r.Scale)
	for i, l := range [...]struct {
		top, bottom uint32
		ring        uint32
	}{
		{0xEC766A, 0xE48980, 0xBF3A30},
		{0xF4C653, 0xF2C85E, 0xB5701F},
		{0x8ED064, 0x91D068, 0x3E8F30},
	} {
		d := discs[i]
		theme.At(c, d[0], d[1], d[2]-d[0], d[3]-d[1])
		c.NextBG4(theme.Col(l.top), theme.Col(l.top), theme.Col(l.bottom), theme.Col(l.bottom))
		c.NextRadius(theme.TrafficDiameterPt * r.Scale / 2)
		c.NextBorder(theme.ColA(l.ring, 0.85), max(1, 1.5*kk))
		c.BoxKeyed(ui.DrawBackground|ui.DrawBorder|fl, kg.Next(), nil)
	}
}

var (
	glyphText = [...][]byte{
		theme.GlyphClose:    []byte("✕"),
		theme.GlyphMinimise: []byte("—"),
		theme.GlyphMaximise: []byte("☐"),
		theme.GlyphRestore:  []byte("❐"),
	}
)

// buttonName is the accessible name of a window button.
func buttonName(b theme.WindowButton, maximised bool) string {
	switch {
	case b == theme.ButtonClose:
		return "Close"
	case b == theme.ButtonMinimise:
		return "Minimise"
	case maximised:
		return "Restore"
	}
	return "Maximise"
}

// captionButtons draws minimise, maximise and close at the right end of the
// band, each a full-height hit box with a rounded pill that lights on hover and
// press (red for close). It returns the clicked button and the left edge of
// the group.
func captionButtons(c *ui.Context, r *theme.Resolved, t theme.Titlebar, h float32) (clicked theme.WindowButton, left float32) {
	bw := theme.Snap(r.Px.WindowButtonW)
	inset := theme.Snap(r.Px.GapSmall)
	ph := theme.Snap(r.Px.ControlH)
	left = t.Width - 3*bw - inset
	fm := r.Met(r.Font.Symbols)
	order := [3]theme.WindowButton{theme.ButtonMinimise, theme.ButtonMaximise, theme.ButtonClose}
	for i, b := range order {
		key := ui.KeyN(keyBar, uint64(10+i))
		hot, press := c.HotTOf(key), c.ActiveTOf(key)
		x := left + float32(i)*bw
		theme.At(c, x, 0, bw, h)
		c.NextCursor(ui.CursorArrow)
		c.NextRole(ui.RoleButton)
		c.NextSemName(buttonName(b, t.Maximised))
		id := c.BoxKeyed(ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|fl, key, nil)
		if c.Signal(id).Clicked {
			clicked = b
		}

		glyph := glyphFor(b, t.Maximised)
		fg := r.Pal.Text
		if !t.Active {
			fg = r.Pal.TextMuted
		}
		var bg uint32
		if b == theme.ButtonClose {
			bg = theme.WithAlpha(r.Pal.Danger, max(hot, press))
			fg = theme.MixRGBA(fg, 0xFFFFFFFF, max(hot, press))
		} else {
			bg = theme.WithAlpha(r.Pal.HoverOverlay, hot)
			if press > 0 {
				bg = theme.MixRGBA(bg, r.Pal.PressOverlay, press)
			}
		}
		c.Push(id)
		theme.At(c, inset/2, theme.Snap((h-ph)/2), bw-inset, ph)
		c.NextBG(bg)
		c.NextRadius(r.Px.RadiusMedium)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 1), nil)
		size := theme.Snap(r.Px.IconSize * 0.6)
		if t.Glyphs == nil || !t.Glyphs.Glyph(c, ui.KeyN(key, 2), glyph, bw/2, h/2, size, fg) {
			theme.At(c, 0, theme.Snap((h-fm.H)/2), bw, fm.H)
			c.NextFont(r.Font.Symbols)
			c.NextTextColor(fg)
			c.NextAlign(ui.AlignCenter)
			c.BoxKeyed(ui.DrawText|fl, ui.KeyN(key, 2), glyphText[glyph])
		}
		c.Pop()
	}
	return clicked, left
}

func glyphFor(b theme.WindowButton, maximised bool) theme.Glyph {
	switch b {
	case theme.ButtonClose:
		return theme.GlyphClose
	case theme.ButtonMinimise:
		return theme.GlyphMinimise
	}
	if maximised {
		return theme.GlyphRestore
	}
	return theme.GlyphMaximise
}
