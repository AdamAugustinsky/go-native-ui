//go:build darwin

package main

import (
	"math"

	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/text"
)

// The scene is a flat, ordered list of items: prebuilt shape quads and text
// items (a range of shaped glyphs). Rebuilding the draw list for a frame
// walks the items and looks every glyph up in the atlas, so a frame costs
// what a re-render of the whole window costs, with no allocation.

type itemKind uint8

const (
	itemQuad itemKind = iota
	itemText
)

type item struct {
	kind   itemKind
	q      gpu.Quad
	g0, g1 int32      // glyph range (itemText)
	x, y   float32    // pen origin: x, baseline y in pixels
	color  uint32     // RGBA8
	clip   [4]float32 // pixels; all zero = none
	fade   [2]float32
}

type glyph struct {
	face text.FaceID
	gid  uint16
	dx   float32 // pen x relative to the item origin, pixels
}

type rect struct{ x0, y0, x1, y1 float32 }

func (r rect) inset(d float32) rect { return rect{r.x0 + d, r.y0 + d, r.x1 - d, r.y1 - d} }
func (r rect) arr() [4]float32      { return [4]float32{r.x0, r.y0, r.x1, r.y1} }

type scene struct {
	ts    *text.System
	r     *gpu.Renderer
	s     float32 // pixels per point
	k     float32 // pixels per reference pixel (the reference is 2×)
	w, h  int     // target size in pixels
	items []item
	glyph []glyph
	list  gpu.DrawList

	nodesX, nodesY []float32       // pixel positions of the background mesh nodes
	pageTex        []gpu.TextureID // text atlas page → renderer texture
	icons          *iconAtlas
	ic             *iconSet

	// fonts
	fUI, fUISemi, fSub, fMono, fMonoBold text.FontKey

	// terminal grid
	adv       float32 // cell advance, pixels
	rowPitch  float32 // pixels
	monoFace  string
	monoSizeP float32 // chosen mono size in points

	traffic bool  // draw the traffic lights ourselves (offscreen output only)
	textNs  int64 // time spent in the text system while building (fonts, shaping)
}

// shape shapes b (cached) and returns the run; the time is accounted to the
// text share of the startup.
func (sc *scene) shape(f text.FontKey, b []byte) text.RunView {
	t := platform.Now()
	v := sc.ts.Run(sc.ts.Shape(f, b))
	sc.textNs += platform.Now() - t
	return v
}

// ref converts a measurement in reference pixels to this scene's pixels.
func (sc *scene) ref(v float32) float32 { return v * sc.k }

func snap(v float32) float32 { return float32(math.Round(float64(v))) }

// ---- quad helpers ----

func flat(r rect, c uint32) gpu.Quad {
	return gpu.Quad{Rect: r.arr(), Color: [4]uint32{c, c, c, c}}
}

func rounded(r rect, c uint32, rad float32) gpu.Quad {
	q := flat(r, c)
	q.Radii = [4]float32{rad, rad, rad, rad}
	return q
}

func vgrad(r rect, top, bottom uint32) gpu.Quad {
	return gpu.Quad{Rect: r.arr(), Color: [4]uint32{top, top, bottom, bottom}}
}

func (sc *scene) add(q gpu.Quad) { sc.items = append(sc.items, item{kind: itemQuad, q: q}) }

// shadow adds a gaussian shadow of the rounded rect r (offset dy, blur
// sigma, in reference pixels).
func (sc *scene) shadow(r rect, dy, sigma, rad float32, c uint32) {
	r.y0 += sc.ref(dy)
	r.y1 += sc.ref(dy)
	q := rounded(r, c, rad)
	q.Soft = sc.ref(sigma)
	sc.add(q)
}

// ---- text helpers ----

// uiText shapes str as one run and adds it with its pen origin at (x, baseline).
func (sc *scene) uiText(font text.FontKey, str string, x, baseline float32, color uint32, clip rect, fadeX [2]float32) (advance float32) {
	rv := sc.shape(font, []byte(str))
	g0 := int32(len(sc.glyph))
	for i := range rv.Glyphs {
		sc.glyph = append(sc.glyph, glyph{rv.Faces[i], rv.Glyphs[i], rv.X[i]})
	}
	sc.items = append(sc.items, item{
		kind: itemText, g0: g0, g1: int32(len(sc.glyph)),
		x: x, y: snap(baseline), color: color, clip: clip.arr(), fade: fadeX,
	})
	return rv.Advance
}

// width is the advance of str in font.
func (sc *scene) width(font text.FontKey, str string) float32 {
	return sc.shape(font, []byte(str)).Advance
}

// term describes one pane's terminal grid.
type term struct {
	x0, y0 float32 // pixel position of cell (0,0): left edge, row-0 baseline
	clip   rect
	pal    *palette
}

// cell places text at a grid cell. Every character is shaped on its own and
// pinned to its column, so fallback faces never shift the grid.
func (sc *scene) cell(t *term, row, col int, str string, font text.FontKey, color uint32) {
	g0 := int32(len(sc.glyph))
	c := col
	for _, r := range str {
		if r != ' ' {
			rv := sc.shape(font, []byte(string(r)))
			x := float32(c) * sc.adv
			for i := range rv.Glyphs {
				sc.glyph = append(sc.glyph, glyph{rv.Faces[i], rv.Glyphs[i], x + rv.X[i]})
			}
		}
		c++
	}
	if int32(len(sc.glyph)) == g0 {
		return
	}
	sc.items = append(sc.items, item{
		kind: itemText, g0: g0, g1: int32(len(sc.glyph)),
		x: t.x0, y: snap(t.y0 + float32(row)*sc.rowPitch), color: color, clip: t.clip.arr(),
	})
}

// ---- per-frame work ----

// frame rebuilds the draw list from the items and uploads atlas changes.
func (sc *scene) frame() {
	l := &sc.list
	l.Reset()
	for i := range sc.items {
		it := &sc.items[i]
		if it.kind == itemQuad {
			l.Add(it.q)
			continue
		}
		c := it.color
		for _, g := range sc.glyph[it.g0:it.g1] {
			pen := it.x + g.dx
			ix := float32(math.Floor(float64(pen)))
			sub := int((pen-ix)*4 + 0.5)
			if sub == 4 {
				sub, ix = 0, ix+1
			}
			e := sc.ts.Glyph(g.face, g.gid, uint8(sub))
			if e.W == 0 {
				continue
			}
			x0, y0 := ix+e.BearingX, it.y-e.BearingY
			mode := gpu.ModeMaskR8
			if e.Color {
				mode = gpu.ModeRGBA
			}
			l.Add(gpu.Quad{
				Rect:  [4]float32{x0, y0, x0 + float32(e.W), y0 + float32(e.H)},
				Clip:  it.clip,
				UV:    [4]float32{float32(e.X), float32(e.Y), float32(e.X) + float32(e.W), float32(e.Y) + float32(e.H)},
				Color: [4]uint32{c, c, c, c},
				FadeX: it.fade,
				Mode:  mode,
				Tex:   sc.pageTexture(int(e.Page)),
			})
		}
	}
	sc.syncAtlas()
}

// pageTexture returns the renderer texture of atlas page p, creating the
// textures of pages that appeared since the last frame (setup path: only the
// first frame can reach the creating branch).
func (sc *scene) pageTexture(p int) gpu.TextureID {
	for len(sc.pageTex) <= p {
		w, h, color := sc.ts.PageInfo(len(sc.pageTex))
		kind := gpu.TexR8
		if color {
			kind = gpu.TexRGBA8
		}
		sc.pageTex = append(sc.pageTex, sc.r.NewTexture(kind, w, h))
	}
	return sc.pageTex[p]
}

// syncAtlas uploads the changed regions of the text atlas pages.
func (sc *scene) syncAtlas() {
	for p := 0; p < sc.ts.Pages(); p++ {
		if x, y, w, h, pix, stride, ok := sc.ts.Dirty(p); ok {
			sc.r.Upload(sc.pageTexture(p), x, y, w, h, pix, stride)
			sc.ts.ClearDirty(p)
		}
	}
}
