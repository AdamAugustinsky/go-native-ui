package gpu

import "unsafe"

// Mode selects how a Quad is shaded.
type Mode uint32

const (
	// ModeShape is an SDF rounded rect: fill (four-corner bilinear colour),
	// optional border, AA edge. Soft > 0 turns it into a gaussian-blurred
	// shadow of the rounded rect.
	ModeShape Mode = iota
	// ModeMaskR8 samples an R8 coverage texture and multiplies Color by it
	// (grayscale text, symbol icons).
	ModeMaskR8
	// ModeRGBA samples a premultiplied RGBA texture and multiplies it by the
	// alpha of Color (colour emoji, images).
	ModeRGBA
	// ModeBackdrop blurs what the quads before it drew under Rect and draws
	// that, tinted, in its place (a backdrop: the scroll edge over content
	// scrolled under a toolbar, D54). Its recipe is the list's
	// Backdrops[Tex]: add it with DrawList.AddBackdrop. Rect, Clip, Radii and
	// FadeX place it as they place a shape; the blur sees only what lies
	// under Rect and inside Clip and the target (its edges are normalised,
	// nothing beyond them leaks in), and the result is opaque. Color is the
	// fallback: a renderer that cannot read back its target (Direct3D 11 and
	// OpenGL today, a Metal target without shader-read usage) fills the quad
	// with it as a shape instead (Renderer.Backdrops). UV, Border,
	// BorderColor and Soft are ignored.
	ModeBackdrop
)

// Backdrop is the recipe of a ModeBackdrop quad. Each output pixel is
//
//	mix(clamp(sat(blur, Saturation) + Add), clamp(Tint + Add), Mix)
//
// or, with TintUnder,
//
//	clamp(sat(mix(blur, Tint, Mix), Saturation) + Add)
//
// on the target's encoded (sRGB) values, clamped to 0..1, where blur is what
// lies under the quad, scaled down by Down, blurred with a Gaussian of Sigma
// whose edges are normalised (a tap outside the region is dropped and the
// weights of the others renormalised), and scaled back up bilinearly; sat
// moves a colour away from its luma (Rec. 709 weights) by Saturation, 1
// leaving it as is. AppKit's scroll edge (macOS 27.2) is Sigma 6 pt, Down
// 4, the window's colour at Mix 0.8245, Add 0.03 ("Hard": the colour is
// replayed over the blur), or Sigma about 6 pt, Down 2, Mix 0.5 (light) or
// 0.6 (dark), Saturation 1.25, Add 0.03, TintUnder ("ThinFilm": replayed
// under it); see docs/research/glass-over-metal.md.
type Backdrop struct {
	// Sigma is the Gaussian's standard deviation in target pixels; 0 does
	// not blur (only the scale down and back up).
	Sigma float32
	// Down is the factor the content is scaled down by before the blur: 1,
	// 2, 4 or 8 (others are taken as the next of those below them, and less
	// than 1 as 1). AppKit's backdrops run at a quarter (4) or half (2) of
	// the backing scale.
	Down float32
	// Tint is the colour (0xRRGGBBAA; its alpha is ignored) mixed in by Mix
	// (0: none, 1: only Tint).
	Tint uint32
	Mix  float32
	// Saturation scales the blur's distance from its luma (1: unchanged).
	Saturation float32
	// Add is added to every channel of the blur, and of Tint (AppKit's
	// +0.03).
	Add float32
	// TintUnder mixes Tint into the blur before Saturation and Add
	// (ThinFilm's background replay, below its blur layer); otherwise Tint
	// plus Add is mixed over the result (Hard's, above it).
	TintUnder bool
}

// Over is the colour b draws over the uniform opaque colour c (0xRRGGBBAA,
// alpha ignored; the result is opaque): the blur of c is c.
func (b *Backdrop) Over(c uint32) uint32 {
	ch := func(v uint32, i int) float32 { return float32(v>>(24-8*i)&0xFF) / 255 }
	clamp := func(v float32) float32 { return min(max(v, 0), 1) }
	m := clamp(b.Mix)
	var v, t [3]float32
	for i := range v {
		v[i], t[i] = ch(c, i), ch(b.Tint, i)
		if b.TintUnder {
			v[i] += (t[i] - v[i]) * m
		}
	}
	l := 0.2126*v[0] + 0.7152*v[1] + 0.0722*v[2]
	out := uint32(0xFF)
	for i := range v {
		x := clamp(l + b.Saturation*(v[i]-l) + b.Add)
		if !b.TintUnder {
			x += (clamp(t[i]+b.Add) - x) * m
		}
		out |= uint32(x*255+0.5) << (24 - 8*i)
	}
	return out
}

// TextureID names a texture created by a Renderer. Zero is a reserved 1×1
// texture that shape quads use.
type TextureID uint32

// Quad is one instance. The renderer uploads Quads byte-for-byte as the
// shader's instance record, so the layout (108 bytes, 4-byte alignment) is
// part of the shader contract; do not reorder fields.
//
// All coordinates are physical pixels with the origin at the top left.
//
// Texture modes draw the texel rectangle UV (in texels, not normalised)
// stretched over Rect, sampled with linear filtering. The quad is shaded as
// a plain rectangle unless a corner radius is set, in which case the
// rounded-rect coverage is applied to the texture too (rounded images).
//
// For ModeShape with Soft > 0, Rect is the rounded rect being shadowed (the
// caller applies any offset or spread) and the renderer grows the drawn
// geometry by 3×Soft+1 pixels on every side; Border is ignored.
type Quad struct {
	Rect        [4]float32 // x0, y0, x1, y1
	Clip        [4]float32 // x0, y0, x1, y1; pixels outside get alpha 0 (in the shader, so clips never break batches). All zero = no clip
	UV          [4]float32 // texel rect u0, v0, u1, v1 (texture modes)
	Color       [4]uint32  // RGBA8 (0xRRGGBBAA), straight alpha, per corner TL, TR, BL, BR (bilinear across the quad, interpolated premultiplied)
	Radii       [4]float32 // corner radii TL, TR, BR, BL in pixels
	Border      float32    // border width in pixels; 0 = filled shape
	BorderColor uint32     // RGBA8; with Border>0 and fill alpha 0 you get an outline
	Soft        float32    // 0 = crisp 1-px AA edge; >0 = shadow blur sigma in pixels (ModeShape)
	FadeX       [2]float32 // horizontal alpha fade: ×1 at x≤FadeX[0] ramping linearly to ×0 at x≥FadeX[1]; both 0 = off. Used for truncated text
	Mode        Mode
	Tex         TextureID
}

// quadSize is the instance stride the shaders expect.
const quadSize = 108

var _ = [1]struct{}{}[unsafe.Sizeof(Quad{})-quadSize] // Quad must be exactly quadSize bytes

// DrawList is the pointer-free instance list of one frame. Quads are drawn
// in order.
type DrawList struct {
	// Quads are the instances, in draw order.
	Quads []Quad
	// Backdrops are the recipes of the list's ModeBackdrop quads, each
	// naming its own by index (Quad.Tex).
	Backdrops []Backdrop
}

// Reset empties the list and keeps its capacity.
func (l *DrawList) Reset() { l.Quads, l.Backdrops = l.Quads[:0], l.Backdrops[:0] }

// Add appends a quad.
func (l *DrawList) Add(q Quad) { l.Quads = append(l.Quads, q) }

// AddBackdrop appends q as a ModeBackdrop quad of recipe b: it blurs what the
// quads added before it drew under q.Rect (see ModeBackdrop). q's Mode and
// Tex are set here, and its Border, BorderColor and Soft cleared: ignored by
// the blur, they must not turn its fallback shape into an outline or a
// shadow.
func (l *DrawList) AddBackdrop(q Quad, b Backdrop) {
	q.Mode, q.Tex = ModeBackdrop, TextureID(len(l.Backdrops))
	q.Border, q.BorderColor, q.Soft = 0, 0, 0
	l.Backdrops = append(l.Backdrops, b)
	l.Quads = append(l.Quads, q)
}

// backdropOf is the recipe of ModeBackdrop quad q of l, and whether l has it.
func (l *DrawList) backdropOf(q *Quad) (Backdrop, bool) {
	if i := int(q.Tex); i < len(l.Backdrops) {
		return l.Backdrops[i], true
	}
	return Backdrop{}, false
}

// backdropsAsShapes turns the ModeBackdrop quads of dst, a renderer's copy
// of l's quads, into shapes of their fallback colour: the backdrops of a
// renderer without the blur.
func backdropsAsShapes(dst []Quad, l *DrawList) {
	if l == nil || len(l.Backdrops) == 0 {
		return
	}
	for i := range dst {
		if dst[i].Mode == ModeBackdrop {
			dst[i].Mode = ModeShape
		}
	}
}

// backdropDown is b.Down as the renderers use it: 1, 2, 4 or 8.
func backdropDown(b *Backdrop) int {
	switch {
	case b.Down >= 8:
		return 8
	case b.Down >= 4:
		return 4
	case b.Down >= 2:
		return 2
	}
	return 1
}

// backdropRegion is the pixel region a backdrop quad blurs in a w×h target:
// its Rect inside its Clip (if any) and the target, rounded out to whole
// pixels; ok is false if that is empty.
func backdropRegion(q *Quad, w, h int) (x0, y0, x1, y1 int, ok bool) {
	r := q.Rect
	if q.Clip != [4]float32{} {
		r = [4]float32{max(r[0], q.Clip[0]), max(r[1], q.Clip[1]), min(r[2], q.Clip[2]), min(r[3], q.Clip[3])}
	}
	fx0, fy0 := max(r[0], 0), max(r[1], 0)
	fx1, fy1 := min(r[2], float32(w)), min(r[3], float32(h))
	if fx0 >= fx1 || fy0 >= fy1 {
		return 0, 0, 0, 0, false
	}
	x0, y0 = int(fx0), int(fy0) // non-negative: truncation is floor
	x1, y1 = int(fx1), int(fy1)
	if float32(x1) < fx1 {
		x1++
	}
	if float32(y1) < fy1 {
		y1++
	}
	return x0, y0, x1, y1, true
}
