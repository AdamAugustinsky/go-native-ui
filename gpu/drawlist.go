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
)

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
}

// Reset empties the list and keeps its capacity.
func (l *DrawList) Reset() { l.Quads = l.Quads[:0] }

// Add appends a quad.
func (l *DrawList) Add(q Quad) { l.Quads = append(l.Quads, q) }
