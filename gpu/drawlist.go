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
	// alpha of Color (colour emoji, images). A YUV texture (yuv.go: video
	// frames) is converted to opaque RGB as it is sampled.
	ModeRGBA
	// ModeBackdrop blurs what the quads before it drew under Rect and draws
	// that, tinted, in its place (a backdrop: the scroll edge over content
	// scrolled under a toolbar, D54). Its recipe is the list's
	// Backdrops[Tex]: add it with DrawList.AddBackdrop. Rect, Clip, Radii and
	// FadeX place it as they place a shape; the blur sees only what lies
	// under Rect and inside Clip and the target (its edges are normalised,
	// nothing beyond them leaks in), and the result is opaque, but where its
	// recipe fades it out (Backdrop.Fade). Color is the
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
	// Base, if its alpha is not 0, is what the blur sees where the quads
	// before the backdrop drew nothing this frame, instead of the target's
	// clear colour (a window's white in dark mode, or transparent where the
	// system's materials show): the renderer fills the backdrop's rectangle
	// (inside its clip, rounded by its corner radii) with it, opaque, before
	// the frame's first quad (a fading one's fill fades out with it). Where
	// no quad before the
	// backdrop reaches its rectangle at all, every renderer draws it flat,
	// Over(Base), without blurring.
	Base uint32
	// Fade, if above 0, makes the backdrop fade out over the last Fade
	// target pixels of its rectangle towards FadeEdge, as macOS 26's soft
	// scroll edge does under a toolbar: along a smoothstep from where the
	// fade begins, the blur's radius (Sigma), the tint's Mix, Saturation's
	// distance from 1 and Add ease off to nothing, and over the fade's
	// outer half the result also mixes into the sharp content under it, so
	// that at FadeEdge the backdrop is that content, with no band or edge.
	// The fade spans the rectangle's whole width. A renderer without the
	// blur fades its fallback colour out over the same pixels, square.
	Fade     float32
	FadeEdge FadeEdge
}

// FadeEdge is the edge of a backdrop's rectangle at which it fades out
// (Backdrop.Fade).
type FadeEdge uint8

const (
	// FadeBottom fades out at the bottom edge: a band under a toolbar, the
	// content scrolling up under it.
	FadeBottom FadeEdge = iota
	// FadeTop fades out at the top edge: a band over a bar at the bottom.
	FadeTop
)

// fadeSpan is where backdrop q of recipe b fades: from y = from, where it
// starts to fade, to y = to, its FadeEdge, where it is gone (to < from for
// FadeTop); ok is false if it does not fade.
func fadeSpan(q *Quad, b *Backdrop) (from, to float32, ok bool) {
	if !(b.Fade > 0) {
		return 0, 0, false
	}
	if b.FadeEdge == FadeTop {
		return q.Rect[1] + b.Fade, q.Rect[1], true
	}
	return q.Rect[3] - b.Fade, q.Rect[3], true
}

// fadeShape makes shape q (a fading backdrop's fallback, or its Base's
// fill) fade out from y = from to y = to: a shadow (Soft) of a rectangle
// whose edge is halfway between them and whose other sides lie far
// outside q's rectangle, cut to that rectangle (and q's clip) by its clip,
// so that the shadow's falloff (±3 sigma, sigma a sixth of the fade) is the
// fade. Its corner radii are dropped.
func fadeShape(q *Quad, from, to float32) {
	r := q.Rect
	if c := q.Clip; c != [4]float32{} {
		r = [4]float32{max(r[0], c[0]), max(r[1], c[1]), min(r[2], c[2]), min(r[3], c[3])}
	}
	if !(r[0] < r[2] && r[1] < r[3]) {
		q.Color, q.Mode = [4]uint32{}, ModeShape // nothing to draw (an all-zero clip would be none)
		return
	}
	sigma := max(from-to, to-from) / 6
	pad := 3*sigma + 2
	mid := (from + to) / 2
	s := [4]float32{r[0] - pad, min(r[1], from) - pad, r[2] + pad, mid}
	if to < from {
		s = [4]float32{r[0] - pad, mid, r[2] + pad, max(r[3], from) + pad}
	}
	q.Rect, q.Clip, q.Radii, q.Soft, q.Border, q.Mode = s, r, [4]float32{}, sigma, 0, ModeShape
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

// ShapeContinuous, as the Tex of a ModeShape quad (which names no texture),
// gives its rounded corners the continuous curve macOS draws, a squircle,
// where zero gives circular arcs. The corner of radius r reaches
// ContinuousReach·r along both edges (no further than half the rect's smaller
// side) and follows a superellipse of exponent ContinuousExponent: fitted to
// Core Animation's cornerCurve .continuous for radii up to 30 pt
// (testdata/appkit-corner-27.2.json, tools/appkitref/corner.sh), where it is
// within 0.3 pt. Shadows (Soft > 0) are blurred circular shapes either way.
const ShapeContinuous TextureID = 1

// ContinuousReach and ContinuousExponent are the continuous corner's shape:
// the shaders' CORNER_REACH and CORNER_N, which must match.
const (
	ContinuousReach    = 1.52
	ContinuousExponent = 3.3
)

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

// Reset empties the list and keeps its capacity, reserving space for a few
// backdrops even before any are drawn.
func (l *DrawList) Reset() {
	l.Quads, l.Backdrops = l.Quads[:0], l.Backdrops[:0]
	// Scroll edges can first appear together well after warm-up. Reserve
	// eight recipes (320 bytes); larger lists still grow normally.
	if cap(l.Backdrops) < 8 {
		l.Backdrops = make([]Backdrop, 0, 8)
	}
}

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
// of l's quads, into shapes (backdropShape): the backdrops of a renderer
// without the blur.
func backdropsAsShapes(dst []Quad, l *DrawList) {
	if l == nil || len(l.Backdrops) == 0 {
		return
	}
	for i := range dst {
		if dst[i].Mode == ModeBackdrop {
			backdropShape(dst, i, l)
		}
	}
}

// backdropShape turns backdrop dst[i] (dst a renderer's copy of l's quads)
// into a shape: of its fallback colour, or of Over(Base) where it has a Base
// and nothing before it reaches it (Backdrop.Base), fading out as the
// backdrop does (Backdrop.Fade).
func backdropShape(dst []Quad, i int, l *DrawList) {
	q := &dst[i]
	b, ok := l.backdropOf(q)
	if ok && b.Base&0xFF != 0 && backdropOverNothing(l.Quads, i) {
		c := b.Over(b.Base)
		q.Color = [4]uint32{c, c, c, c}
	}
	q.Mode = ModeShape
	if from, to, fades := fadeSpan(q, &b); ok && fades {
		fadeShape(q, from, to)
	}
}

// backdropOverNothing reports whether no quad before quads[i], a backdrop,
// reaches its rectangle (inside its clip): all it would blur is what the
// target was cleared to, or its Base.
func backdropOverNothing(quads []Quad, i int) bool {
	r := quads[i].Rect
	if c := quads[i].Clip; c != [4]float32{} {
		r = [4]float32{max(r[0], c[0]), max(r[1], c[1]), min(r[2], c[2]), min(r[3], c[3])}
	}
	for j := range i {
		p := &quads[j]
		b := p.Rect
		if p.Mode == ModeShape && p.Soft > 0 { // a shadow reaches 3 sigma out (the shaders' pad)
			g := 3*p.Soft + 1
			b = [4]float32{b[0] - g, b[1] - g, b[2] + g, b[3] + g}
		}
		if c := p.Clip; c != [4]float32{} {
			b = [4]float32{max(b[0], c[0]), max(b[1], c[1]), min(b[2], c[2]), min(b[3], c[3])}
		}
		if b[0] < b[2] && b[1] < b[3] && b[0] < r[2] && b[2] > r[0] && b[1] < r[3] && b[3] > r[1] {
			return false
		}
	}
	return true
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
