// Package text shapes and rasterizes text through the operating system's
// text stack (CoreText on macOS) and caches the results for a renderer.
//
// Layers:
//
//   - A backend (coretext_darwin.go) shapes a string into glyph ids, pen
//     positions and the concrete font face of each glyph (font fallback is the
//     OS's), and rasterizes single glyphs.
//   - The run cache (cache.go) stores shaped runs in pointer-free slabs keyed
//     by (font, bytes). A hit allocates nothing.
//   - The glyph cache and atlas (cache.go, atlas.go) store rasterized glyphs
//     in pages up to 1024×1024 (R8 coverage, RGBA premultiplied for colour glyphs)
//     and track changed spans per shelf. Consume Dirty/ClearDirty until
//     each page is clean.
//   - Paragraph layout (para.go, D42) breaks styled text (Para: the text,
//     pointer-free Spans and a font per style) into lines at a width. On
//     macOS it is CoreText's typesetter (para_darwin.go), which gives AppKit's
//     line breaks, bidirectional line order, font fallback and line heights;
//     the other backends use the basic layout of para_basic.go. Layouts are
//     cached pointer-free (paracache.go) by everything in Para, with
//     second-chance eviction; ParaView has the hit-testing, caret and range
//     geometry that selection needs, and EmojiOnly the rule for large
//     emoji-only messages.
//
// # Carets
//
// A shaped run knows which source bytes each glyph came from
// (RunView.Clusters, filled by every backend) and RunView.CaretX/HitByte turn
// that into text-editing geometry: the x of a byte offset and the offset
// nearest an x, on grapheme cluster boundaries (grapheme.go, UAX #29), with
// ligatures split evenly and right-to-left runs in visual order (caret.go).
// NextGrapheme, PrevGrapheme and GraphemeStart move over clusters.
//
// Bulk data is pointer-free: glyph ids, pen positions and faces live in
// slabs, and every handle (FontKey, FaceID, RunRef) is an index.
//
// # Threading
//
// A System is single-threaded: use it from one goroutine/OS thread (the main
// thread). CoreText's CTFont and CTLine objects are immutable and documented
// as thread-safe, but this package's caches, scratch buffers, the ffi.Frame
// and the CGBitmapContext are not. Shaping off the main thread therefore
// needs its own System (own Frame, scratch and cache), or a lock; sharing
// the immutable CTFont objects between Systems would additionally need the
// face table to be shared and locked. That is a phase-4 decision.
//
// # Lifetimes
//
// A RunView is valid until the next eviction of the run cache; compare
// RunGeneration before and after shaping a batch. A GlyphEntry is valid
// until an atlas page is recycled; compare AtlasGeneration and re-look-up
// glyphs after it changes. Other pages keep their pixels and glyph entries.
package text

// FontKey identifies a font (family, size, weight, scale) in a System.
type FontKey uint16

// FaceID identifies one concrete font face. Fallback faces chosen by the OS
// for a script the primary font lacks get their own IDs.
type FaceID uint16

// FontDesc describes a font to create. Sizes are in points and every output
// is in pixels, at SizePt*Scale pixels per em. On macOS the optical size and
// the tracking are those of SizePt, as in AppKit, whatever the Scale.
type FontDesc struct {
	Family string  // "" = system UI font (SF Pro on macOS)
	Mono   bool    // system monospace font when Family == ""
	SizePt float32 // points
	Weight int     // CSS-like: 400 regular, 500 medium, 600 semibold, 700 bold
	Italic bool    // the family's italic (oblique) face; upright when it has none
	Scale  float32 // pixels per point (2 on Retina)
	// TabularDigits gives every digit the same advance (OpenType tnum;
	// CoreText's monospaced numbers; DirectWrite's tabular figures), so that
	// sizes, dates and counters line up in columns and do not jitter as they
	// change. A font without such a feature shapes as usual. It is part of
	// the font: the same description with and without it caches separately.
	TabularDigits bool
}

// Metrics are font metrics in pixels.
type Metrics struct {
	// Ascent and Descent are the distances from the baseline to the top and the
	// bottom of the line, LineGap is the extra space between lines and LineHeight
	// is Ascent+Descent+LineGap. CapHeight and XHeight are the heights of capital
	// letters and of the lowercase x. All in pixels and positive.
	Ascent, Descent, LineGap, LineHeight float32
	CapHeight, XHeight                   float32
	// MonoAdvance is the advance of "0". For a monospace font it is the
	// advance of every glyph.
	MonoAdvance float32
	// UnderlinePos is the distance from the baseline down to the centre of
	// an underline and UnderlineThickness its thickness, from the font; both
	// 0 when the backend does not report them (a drawer then picks its own).
	UnderlinePos, UnderlineThickness float32
}

// RunRef identifies a shaped run in the run cache. The zero RunRef is
// invalid and has no glyphs.
type RunRef uint32

// RunView is a shaped string. Glyphs, X, Faces and Clusters have the same
// length. The slices alias cache storage: do not modify or append to them.
//
// Glyphs are in visual order (X increases), also in right-to-left runs.
type RunView struct {
	// Glyphs are the glyph ids, in visual order.
	Glyphs  []uint16
	X       []float32 // pen x per glyph in pixels, relative to the run origin
	Faces   []FaceID  // face per glyph
	Advance float32   // total advance in pixels

	// Clusters maps each glyph back to the source text: the byte offset in
	// Text of the first character of the glyph's cluster (the characters the
	// shaper turned into this glyph, possibly together with its neighbours:
	// a ligature, a base with its marks). Bit 31 (ClusterRTL) is set for
	// glyphs of a right-to-left run, whose clusters descend with X. Offsets
	// are UTF-8 byte offsets; an invalid byte counts as one character.
	// Clusters are what the shaper reports; CaretX and HitByte refine them
	// to grapheme boundaries. Nil for views that carry no cluster
	// information (their carets are spread evenly over the graphemes).
	Clusters []uint32
	// Text is the shaped source string (aliasing cache storage).
	Text []byte
	// carets is present on cached paragraph lines.
	carets caretData
}

// ClusterRTL is set in RunView.Clusters entries of right-to-left glyphs.
const ClusterRTL = 1 << 31

// GlyphVariant is the sub argument of Glyph: a quarter-pixel x offset (0..3)
// and the font-smoothing level of the text colour (SmoothLevel). Backends
// without font smoothing ignore the level.
func GlyphVariant(sub, smooth uint8) uint8 { return sub&3 | smooth<<smoothShift }

const smoothShift = 2

// GlyphEntry locates a rasterized glyph in an atlas page.
type GlyphEntry struct {
	Page  uint8 // index for PageInfo/Dirty
	Color bool  // RGBA page (premultiplied colour); otherwise R8 coverage
	// Rect of the bitmap in the page, in pixels. W == 0 for glyphs without
	// ink (spaces); such entries have no page storage.
	X, Y, W, H uint16
	// Offset of the bitmap's top-left corner from the pen position on the
	// baseline, in pixels: BearingX to the right, BearingY upward. A glyph
	// drawn with its pen at (px, baseline) in y-down screen coordinates has
	// its bitmap's top-left at (px+BearingX, baseline-BearingY).
	BearingX, BearingY float32
}

const (
	// PageSize is the maximum atlas page width and height in pixels, and the
	// default for grayscale pages. Colour pages may be smaller; PageInfo
	// returns each page's actual dimensions and Dirty its byte stride.
	PageSize = 1024
	// maxPagesPerKind bounds each kind's pixels to this many full-size pages.
	maxPagesPerKind = 4
)

// normalizeDesc fills in defaults so equal fonts compare equal.
func normalizeDesc(d FontDesc) FontDesc {
	if d.Scale <= 0 {
		d.Scale = 1
	}
	if d.SizePt <= 0 {
		d.SizePt = 13
	}
	if d.Weight <= 0 {
		d.Weight = 400
	}
	if d.Family != "" {
		d.Mono = false
	}
	return d
}

// scaleSet collects the distinct scales of live fonts, in order of first use.
type scaleSet []float32

func (l *scaleSet) add(scale float32) {
	for _, s := range *l {
		if s == scale {
			return
		}
	}
	*l = append(*l, scale)
}
