package ui

import (
	"math"

	"gonativeui/gpu"
)

// Uploader is the part of gpu.Renderer that creates and fills the textures
// of the text atlas pages; every OS backend's *gpu.Renderer satisfies it.
type Uploader interface {
	NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID
	Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int)
}

var _ Uploader = (*gpu.Renderer)(nil)

// AtlasSync registers u as the texture uploader of the text atlas and syncs
// now: textures are created for atlas pages that have none and the regions
// the shaper reports dirty are uploaded. End calls the same sync after
// emitting, so the textures are current when the app encodes the draw list;
// call AtlasSync once up front (before the first End that draws text, since
// glyph quads need their page's texture id) and again if the renderer is
// recreated. Atlas resets mark every page dirty, so they are re-uploaded by
// the next sync.
func (c *Context) AtlasSync(u Uploader) {
	if c.up != u {
		c.up = u
		c.pageTex = c.pageTex[:0]
	}
	c.syncAtlas()
}

// pageTexture returns the texture of atlas page p, creating the textures of
// pages that appeared since the last sync (a setup path).
func (c *Context) pageTexture(p int) gpu.TextureID {
	if c.up == nil {
		return 0
	}
	for len(c.pageTex) <= p {
		w, h, color := c.sh.PageInfo(len(c.pageTex))
		kind := gpu.TexR8
		if color {
			kind = gpu.TexRGBA8
		}
		c.pageTex = append(c.pageTex, c.up.NewTexture(kind, w, h))
	}
	return c.pageTex[p]
}

func (c *Context) syncAtlas() {
	if c.up == nil {
		return
	}
	for p := 0; p < c.sh.Pages(); p++ {
		t := c.pageTexture(p)
		if x, y, w, h, pix, stride, ok := c.sh.Dirty(p); ok {
			c.up.Upload(t, x, y, w, h, pix, stride)
			c.sh.ClearDirty(p)
		}
	}
}

// End finishes the frame: it lays out the boxes, steps the animations,
// prunes boxes that were not built, and appends the frame's quads to list
// (which it resets first). It must follow a Begin with balanced Push/Pop.
func (c *Context) End(list *gpu.DrawList) {
	if c.nparents != 1 {
		panic("ui: unbalanced Push/Pop at End")
	}
	c.layout()
	c.animateBoxes()
	c.prune()

	list.Reset()
	win := [4]float32{0, 0, float32(c.w), float32(c.h)}
	for try := 0; ; try++ {
		gen := c.sh.AtlasGeneration()
		c.emit(list, c.root, win)
		// A full atlas resets mid-frame and invalidates the glyph entries
		// used so far: draw the frame again against the fresh atlas.
		if gen == c.sh.AtlasGeneration() || try >= 2 {
			break
		}
		list.Reset()
	}
	c.syncAtlas()
}

func intersect(a, b [4]float32) [4]float32 {
	return [4]float32{max(a[0], b[0]), max(a[1], b[1]), min(a[2], b[2]), min(a[3], b[3])}
}

func empty(r [4]float32) bool { return r[0] >= r[2] || r[1] >= r[3] }

// col applies the box's palette transform and opacity to an RGBA8 colour.
func (b *box) col(c uint32) uint32 {
	if b.tint.T > 0 {
		t := min(b.tint.T, 1)
		tc := b.tint.Color
		r := lerp8(c>>24, tc>>24, t)
		g := lerp8((c>>16)&255, (tc>>16)&255, t)
		bl := lerp8((c>>8)&255, (tc>>8)&255, t)
		c = r<<24 | g<<16 | bl<<8 | c&255
	}
	if b.opacity < 1 {
		a := uint32(float32(c&255)*max(b.opacity, 0) + 0.5)
		c = c&^255 | a
	}
	return c
}

func lerp8(a, b uint32, t float32) uint32 {
	return uint32(float32(a) + (float32(b)-float32(a))*t + 0.5)
}

func scaleAlpha(c uint32, f float32) uint32 {
	return c&^255 | uint32(float32(c&255)*f+0.5)
}

// emit draws box id and its subtree in tree order (RAD): shadows,
// background and border, hover/press overlay, text, then the children, the
// floating ones after their siblings.
func (c *Context) emit(l *gpu.DrawList, id ID, clip [4]float32) {
	b := &c.boxes[id]
	r := b.rect
	sized := r[2] > r[0] && r[3] > r[1]

	if b.flags&DrawShadow != 0 {
		for i := range b.shadow {
			s := &b.shadow[i]
			if s.Color&255 == 0 {
				continue
			}
			grow := 3*s.Sigma + 1
			sr := [4]float32{r[0] + s.OffX, r[1] + s.OffY, r[2] + s.OffX, r[3] + s.OffY}
			if empty(intersect([4]float32{sr[0] - grow, sr[1] - grow, sr[2] + grow, sr[3] + grow}, clip)) {
				continue
			}
			col := b.col(s.Color)
			l.Add(gpu.Quad{Rect: sr, Clip: clip, Color: [4]uint32{col, col, col, col}, Radii: b.radii, Soft: s.Sigma})
		}
	}

	if sized && !empty(intersect(r, clip)) {
		if b.flags&(DrawBackground|DrawBorder) != 0 {
			q := gpu.Quad{Rect: r, Clip: clip, Radii: b.radii}
			draw := false
			if b.flags&DrawBackground != 0 {
				for i, bg := range b.bg {
					q.Color[i] = b.col(bg)
					draw = draw || bg&255 != 0
				}
			}
			partial := false
			if b.flags&DrawBorder != 0 && b.borderW > 0 && b.borderColor&255 != 0 {
				if b.sides == SidesAll {
					q.Border, q.BorderColor = b.borderW, b.col(b.borderColor)
					draw = true
				} else {
					partial = b.sides&SidesAll != 0
				}
			}
			if draw {
				l.Add(q)
			}
			if partial {
				c.emitSides(l, b, clip)
			}
		}
		if b.flags&DrawHotEffects != 0 {
			c.overlay(l, b, clip, c.Theme.HotOverlay, b.hotT)
			c.overlay(l, b, clip, c.Theme.ActiveOverlay, b.activeT)
		}
		if b.flags&DrawImage != 0 && b.img.Tex != 0 {
			c.emitImage(l, b, clip)
		}
		if b.flags&DrawText != 0 && b.txt.Len > 0 {
			c.emitText(l, b, clip)
		}
	}

	cclip := clip
	if b.flags&Clip != 0 {
		cclip = intersect(clip, r)
		if empty(cclip) {
			return
		}
	}
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if c.boxes[ch].flags&floatingMask == 0 {
			c.emit(l, ch, cclip)
		}
	}
	for ch := b.first; ch != 0; ch = c.boxes[ch].next {
		if c.boxes[ch].flags&floatingMask != 0 {
			c.emit(l, ch, cclip)
		}
	}
}

// emitSides draws the outline of b along some of its sides only (b.sides is
// not SidesAll): the full outline quad, once per strip of the box rectangle
// that holds a selected side, each strip clipped by the shader. A strip along
// a left or right side spans the whole height and is as wide as the corner
// radius there, so it carries the arcs of both corners on that side; strips
// along the top and bottom cover only what the side strips leave.
func (c *Context) emitSides(l *gpu.DrawList, b *box, clip [4]float32) {
	r := b.rect
	bw := b.borderW
	ring := gpu.Quad{Rect: r, Radii: b.radii, Border: bw, BorderColor: b.col(b.borderColor)}
	wl := max(b.radii[0], b.radii[3], bw)
	wr := max(b.radii[1], b.radii[2], bw)
	ht := max(b.radii[0], b.radii[1], bw)
	hb := max(b.radii[3], b.radii[2], bw)
	left, right := b.sides&SideLeft != 0, b.sides&SideRight != 0
	if left {
		strip(l, &ring, intersect([4]float32{r[0] - 1, r[1] - 1, r[0] + wl, r[3] + 1}, clip))
	}
	if right {
		strip(l, &ring, intersect([4]float32{r[2] - wr, r[1] - 1, r[2] + 1, r[3] + 1}, clip))
	}
	xa, xb := r[0]-1, r[2]+1
	if left {
		xa = r[0] + wl
	}
	if right {
		xb = r[2] - wr
	}
	if b.sides&SideTop != 0 {
		strip(l, &ring, intersect([4]float32{xa, r[1] - 1, xb, r[1] + ht}, clip))
	}
	if b.sides&SideBottom != 0 {
		strip(l, &ring, intersect([4]float32{xa, r[3] - hb, xb, r[3] + 1}, clip))
	}
}

func strip(l *gpu.DrawList, ring *gpu.Quad, clip [4]float32) {
	if empty(clip) {
		return
	}
	q := *ring
	q.Clip = clip
	l.Add(q)
}

// emitImage draws b's image centred in the padded rectangle.
func (c *Context) emitImage(l *gpu.DrawList, b *box, clip [4]float32) {
	uv := b.img.UV
	w, h := uv[2]-uv[0], uv[3]-uv[1]
	r := b.rect
	cx := (r[0] + b.pad[0] + r[2] - b.pad[2]) / 2
	cy := (r[1] + b.pad[1] + r[3] - b.pad[3]) / 2
	x0, y0 := snap(cx-w/2), snap(cy-h/2)
	col := b.col(b.textColor)
	if b.img.Mode == gpu.ModeRGBA {
		col = 0xFFFFFF00 | col&255
	}
	if col&255 == 0 {
		return
	}
	l.Add(gpu.Quad{
		Rect:  [4]float32{x0, y0, x0 + w, y0 + h},
		Clip:  clip,
		UV:    uv,
		Color: [4]uint32{col, col, col, col},
		Mode:  b.img.Mode,
		Tex:   b.img.Tex,
	})
}

func (c *Context) overlay(l *gpu.DrawList, b *box, clip [4]float32, color uint32, t float32) {
	if t <= 0.002 || color&255 == 0 {
		return
	}
	col := b.col(scaleAlpha(color, t))
	l.Add(gpu.Quad{Rect: b.rect, Clip: clip, Color: [4]uint32{col, col, col, col}, Radii: b.radii})
}

// emitText draws the display text of b inside its padded rectangle.
func (c *Context) emitText(l *gpu.DrawList, b *box, clip [4]float32) {
	col := b.col(b.textColor)
	if col&255 == 0 {
		return
	}
	if c.sh.RunGeneration() != b.runGen { // the run cache evicted since the build: shape again
		bytes := c.fr.Cur().Bytes(b.txt)
		b.run = c.sh.Shape(b.font, bytes)
		b.runGen = c.sh.RunGeneration()
	}
	v := c.sh.Run(b.run)
	if len(v.Glyphs) == 0 {
		return
	}
	fi := c.fontInfo(b.font)
	r := b.rect
	x0, x1 := r[0]+b.pad[0], r[2]-b.pad[2]
	y0, y1 := r[1]+b.pad[1], r[3]-b.pad[3]
	avail := x1 - x0
	over := v.Advance > avail+0.01

	x := x0
	if !over {
		switch b.align {
		case AlignCenter:
			x = x0 + (avail-v.Advance)/2
		case AlignRight:
			x = x1 - v.Advance
		}
	}
	baseline := snap(y0 + ((y1-y0)-(fi.ascent+fi.descent))/2 + fi.ascent)

	tclip := clip
	var fade [2]float32
	if over && b.flags&FadeOverflowX != 0 {
		tclip[0], tclip[2] = max(clip[0], x0), min(clip[2], x1)
		fade = [2]float32{x1 - 1.5*(fi.ascent+fi.descent), x1}
		if fade[0] < x0 {
			fade[0] = x0
		}
	}
	if empty(tclip) {
		return
	}

	for i, gid := range v.Glyphs {
		pen := x + v.X[i]
		ix := float32(math.Floor(float64(pen)))
		sub := int((pen-ix)*4 + 0.5)
		if sub == 4 {
			sub, ix = 0, ix+1
		}
		e := c.sh.Glyph(v.Faces[i], gid, uint8(sub))
		if e.W == 0 {
			continue
		}
		gx, gy := ix+e.BearingX, baseline-e.BearingY
		gr := [4]float32{gx, gy, gx + float32(e.W), gy + float32(e.H)}
		if gr[2] <= tclip[0] || gr[0] >= tclip[2] || gr[3] <= tclip[1] || gr[1] >= tclip[3] {
			continue
		}
		mode := gpu.ModeMaskR8
		if e.Color {
			mode = gpu.ModeRGBA
		}
		l.Add(gpu.Quad{
			Rect:  gr,
			Clip:  tclip,
			UV:    [4]float32{float32(e.X), float32(e.Y), float32(e.X) + float32(e.W), float32(e.Y) + float32(e.H)},
			Color: [4]uint32{col, col, col, col},
			FadeX: fade,
			Mode:  mode,
			Tex:   c.pageTexture(int(e.Page)),
		})
	}
}
