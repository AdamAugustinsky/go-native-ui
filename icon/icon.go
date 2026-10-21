// Package icon draws system symbols: SF Symbols on macOS, Segoe Fluent Icons
// or Segoe MDL2 Assets on Windows, and glyphs of the system fonts on Linux.
// Nothing here is third-party artwork, and no symbol font is redistributed.
//
// An icon is a Spec: the name of its SF Symbol, its Segoe codepoint and a
// fallback rune. Standard lists the icons the widgets use; an app adds its
// own after them.
//
// # Where an icon comes from
//
// # Text styles and scales
//
// An icon next to text is sized for the text's style, as
// NSImage.SymbolConfiguration(textStyle:scale:) does: Spec.In gives a spec the
// point size and weight of a text style (StyleBody, StyleHeadline,
// StyleTitle1 …) and a scale (ScaleSmall, ScaleMedium, ScaleLarge). On macOS
// the symbol is rasterised by AppKit's own configuration for that size,
// weight and scale (a test compares it with textStyle:scale: for every style
// and scale); on Windows and Linux the drawn glyph takes the style's size
// and the scale's factor.
//
// Atlas.Raster picks a source per icon, once per scale, and never per frame:
//
//   - On macOS the SF Symbol is rasterised through AppKit into an R8 coverage
//     atlas, which Attach uploads, and drawn as an image tinted by the text
//     colour (SourceImage).
//   - Where that fails (no such symbol on this macOS, or a platform without
//     SF Symbols), the Segoe codepoint is set as text in the symbol font if the
//     system has one (SourceSymbolFont; Windows only).
//   - Failing that, the fallback rune is set in the system UI font
//     (SourceFallback), so every platform draws something in the same place.
//   - An icon with none of these draws nothing (SourceNone).
//
// Text icons are shaped and rasterised by the text system, into its own glyph
// atlas, at the same moment: Raster shapes each glyph once so that frames find
// it cached.
//
// # Threading
//
// An Atlas is used from the main thread (AppKit and the text system require
// it), like the ui.Context it draws into.
package icon

import (
	"log"
	"math"

	"gonativeui/gpu"
	"gonativeui/text"
	"gonativeui/ui"
)

// ID indexes the Spec list an Atlas was made from: the standard icons first
// (ChevronDown…Error), then the app's.
type ID uint16

// Spec describes one icon.
type Spec struct {
	// Symbol is the SF Symbol's name (macOS); empty for none.
	Symbol string
	// Segoe is the codepoint in Segoe Fluent Icons and Segoe MDL2 Assets
	// (Windows); 0 for none. Use codepoints both fonts define.
	Segoe rune
	// Fallback is a glyph of the system UI font, used where no symbol is
	// available; 0 for none.
	Fallback rune
	// Pt is the size in points.
	Pt float32
	// Weight is the SF Symbol weight as NSFontWeight: 0 regular, 0.23 medium,
	// 0.3 semibold. Other sources ignore it.
	Weight float32
	// Scale is the symbol's scale within its size (NSImageSymbolScale): the
	// zero value is medium. Spec.In sets Pt, Weight and Scale together from a
	// text style.
	Scale Scale
}

// Source says what an icon is drawn from.
type Source uint8

const (
	// SourceNone: nothing is available; the icon draws nothing.
	SourceNone Source = iota
	// SourceImage: an SF Symbol, rasterised into the atlas.
	SourceImage
	// SourceSymbolFont: the Segoe codepoint, set in the system symbol font.
	SourceSymbolFont
	// SourceFallback: the fallback rune, set in the system UI font.
	SourceFallback
)

// TextSystem is what an Atlas needs from the text system; *text.System
// satisfies it.
type TextSystem interface {
	Font(d text.FontDesc) text.FontKey
	Metrics(f text.FontKey) text.Metrics
	Shape(f text.FontKey, utf8 []byte) text.RunRef
	Run(r text.RunRef) text.RunView
}

var _ TextSystem = (*text.System)(nil)

// The atlas starts at atlasW×atlasH texels and grows when it fills: see
// Atlas.grow.
const (
	atlasW = 512
	atlasH = 256
)

// maxAtlas is the largest side the atlas grows to, in texels: a size every
// GPU the renderers target creates (Metal, Direct3D 11 and OpenGL all go
// beyond it). A variable so that a test can reach it.
var maxAtlas = 4096

// freer is what an Uploader may also offer: freeing a texture. An atlas that
// grows makes a new texture and frees the old one if its uploader can.
type freer interface{ FreeTexture(gpu.TextureID) }

// entry is one icon's resolved source and size.
type entry struct {
	src  Source
	w, h float32 // pixels
	aw   float32 // SourceImage: the symbol's alignment width, pixels (exact)
	mw   float32 // SourceImage: its image's width at the medium scale, pixels (exact)
	// SourceImage: the texel rectangle in the atlas.
	x, y, iw, ih int
	// text sources: the font and the UTF-8 of the glyph.
	font text.FontKey
	utf8 []byte
}

// Atlas holds the icons of one window: the rasterised symbols and the fonts of
// the text icons.
type Atlas struct {
	ts      TextSystem
	specs   []Spec
	ents    []entry
	scale   float32
	family  string // the symbol font in use, "" if none
	checked bool   // family has been looked for

	pix  []byte // atlas, R8, w×h
	w, h int    // its size in texels: it grows (grow)
	// the shelf the next symbol goes on: its start, its top, the tallest
	// symbol on it
	cx, cy, rowH int
	dirty        [4]int // what the texture lacks: x0, y0, x1, y1 (empty if x1 <= x0)
	warned       bool   // an overflow has been logged
	tex          gpu.TextureID
	texW, texH   int // the size of tex
	up           ui.Uploader
}

// New returns an Atlas of the standard icons followed by extra; Raster
// prepares it. The extra icons have the IDs NumStandard, NumStandard+1, ….
func New(ts TextSystem, extra ...Spec) *Atlas {
	return newAtlas(ts, append(Standard(), extra...))
}

func newAtlas(ts TextSystem, specs []Spec) *Atlas {
	return &Atlas{ts: ts, specs: specs, ents: make([]entry, len(specs)), pix: make([]byte, atlasW*atlasH), w: atlasW, h: atlasH}
}

// Add appends icons after the existing ones and returns the ID of the first
// (the others follow). Once Raster has run the new icons are rasterised at the
// same scale and added to the atlas (the icons already there keep their place
// unless the atlas has to grow, and then only its texture changes); the
// atlas is uploaded again. Call it when an icon is wanted, not every frame:
// a box built earlier in the frame that grew the atlas shows nothing until
// the next frame. The IDs already given stay valid. Main thread, as Raster.
func (a *Atlas) Add(specs ...Spec) ID {
	first := ID(len(a.specs))
	a.specs = append(a.specs, specs...)
	a.ents = append(a.ents, make([]entry, len(specs))...)
	if a.scale != 0 && len(specs) > 0 {
		for i := int(first); i < len(a.specs); i++ {
			a.rasterOne(i)
		}
		a.upload()
	}
	return first
}

// Len is the number of icons.
func (a *Atlas) Len() int { return len(a.specs) }

// probeRune is a codepoint every Segoe icon font defines (GlobalNavigationButton).
const probeRune = 0xE700

// hasGlyph reports whether shaping r in font f finds a glyph.
func (a *Atlas) hasGlyph(f text.FontKey, r rune) bool {
	run := a.ts.Run(a.ts.Shape(f, []byte(string(r))))
	return len(run.Glyphs) > 0 && run.Glyphs[0] != 0
}

// findFamily returns the first of families that the text system can set the
// Segoe codepoints in, "" if none. A family that is not installed resolves to
// the default font, which has no glyph for the probe codepoint.
func (a *Atlas) findFamily(families []string) string {
	for _, fam := range families {
		f := a.ts.Font(text.FontDesc{Family: fam, SizePt: 12, Weight: 400, Scale: 1})
		if a.hasGlyph(f, probeRune) {
			return fam
		}
	}
	return ""
}

// SymbolFamily returns the symbol font the Segoe codepoints are set in, "" if
// there is none (always, outside Windows). It is known after Raster.
func (a *Atlas) SymbolFamily() string { return a.family }

// Raster (re)prepares every icon for scale pixels per point: it rasterises the
// SF Symbols into the atlas, creates the fonts of the text icons and shapes
// their glyphs, and uploads the atlas if a renderer is attached. Call it when
// the window is created and when its scale changes, on the main thread.
func (a *Atlas) Raster(scale float32) {
	if !a.checked {
		a.family, a.checked = a.findFamily(symbolFamilies), true
	}
	a.scale = scale
	a.reset()
	for i := range a.specs {
		a.rasterOne(i)
	}
	a.upload()
}

// reset empties the atlas at its initial size, for a new scale.
func (a *Atlas) reset() {
	if len(a.pix) == atlasW*atlasH {
		clear(a.pix)
	} else {
		a.pix = make([]byte, atlasW*atlasH)
	}
	a.w, a.h = atlasW, atlasH
	a.cx, a.cy, a.rowH = 0, 0, 0
	a.dirty = [4]int{0, 0, a.w, a.h}
}

// rasterOne resolves icon i at the current scale: a symbol goes in the atlas
// (which grows to hold it), a text icon gets its font.
func (a *Atlas) rasterOne(i int) {
	sp := a.specs[i]
	scale := a.scale
	e := &a.ents[i]
	*e = entry{}
	if sp.Symbol != "" {
		if pix, w, h, ok := rasterSymbol(sp.Symbol, float64(sp.Pt)*float64(scale), float64(sp.Weight), sp.Scale); ok {
			if x, y, fit := a.place(w, h); fit {
				for r := 0; r < h; r++ {
					copy(a.pix[(y+r)*a.w+x:(y+r)*a.w+x+w], pix[r*w:(r+1)*w])
				}
				a.markDirty(x, y, w, h)
				*e = entry{src: SourceImage, w: float32(w), h: float32(h), x: x, y: y, iw: w, ih: h}
				// measured at the point size, as AppKit lays out
				// (its sizes are whole or half points there), then scaled
				if aw, _, ok := symbolWidths(sp.Symbol, float64(sp.Pt), float64(sp.Weight), sp.Scale); ok {
					e.aw = float32(aw) * scale
				}
				if _, mw, ok := symbolWidths(sp.Symbol, float64(sp.Pt), float64(sp.Weight), ScaleMedium); ok {
					e.mw = float32(mw) * scale
				}
				return
			}
			if !a.warned {
				a.warned = true
				log.Printf("icon: the atlas is full at %d×%d texels: symbol %q (%d×%d) and any that follow fall back to text", a.w, a.h, sp.Symbol, w, h)
			}
		}
	}
	if sp.Segoe != 0 && a.family != "" && a.textEntry(e, SourceSymbolFont, a.family, sp.Segoe, sp.Pt*scaleFactor(sp.Scale)) {
		return
	}
	if sp.Fallback != 0 {
		a.textEntry(e, SourceFallback, "", sp.Fallback, sp.Pt*scaleFactor(sp.Scale))
	}
}

// place finds room for a w×h symbol with a 1 px gutter, growing the atlas
// while there is none and it can. The symbols already placed never move.
func (a *Atlas) place(w, h int) (x, y int, ok bool) {
	for {
		x, y = a.cx, a.cy
		if x > 0 && x+w > a.w { // the next shelf
			x, y = 0, y+a.rowH+1
		}
		if x+w <= a.w && y+h <= a.h {
			if y != a.cy {
				a.rowH = 0
			}
			a.cx, a.cy, a.rowH = x+w+1, y, max(a.rowH, h)
			return x, y, true
		}
		if !a.grow() {
			return 0, 0, false
		}
	}
}

// grow doubles the atlas, its height first and then its width, up to maxAtlas,
// and reports whether it could. The pixels keep their coordinates, so the icons
// already in it keep their texel rectangles; the texture is made anew by
// upload.
func (a *Atlas) grow() bool {
	nw, nh := a.w, a.h
	if a.h < a.w {
		nh *= 2
	} else {
		nw *= 2
	}
	if nw > maxAtlas || nh > maxAtlas {
		return false
	}
	if nw == a.w {
		a.pix = append(a.pix, make([]byte, a.w*(nh-a.h))...)
	} else {
		pix := make([]byte, nw*nh)
		for r := 0; r < a.h; r++ {
			copy(pix[r*nw:], a.pix[r*a.w:(r+1)*a.w])
		}
		a.pix = pix
	}
	a.w, a.h = nw, nh
	a.dirty = [4]int{0, 0, nw, nh}
	return true
}

// markDirty adds a rectangle to what the texture lacks.
func (a *Atlas) markDirty(x, y, w, h int) {
	d := &a.dirty
	if d[2] <= d[0] {
		*d = [4]int{x, y, x + w, y + h}
		return
	}
	d[0], d[1], d[2], d[3] = min(d[0], x), min(d[1], y), max(d[2], x+w), max(d[3], y+h)
}

// textEntry makes e the glyph r set in family (the system UI font if empty),
// and reports whether the font has that glyph; if not, e is left alone.
func (a *Atlas) textEntry(e *entry, src Source, family string, r rune, pt float32) bool {
	f := a.ts.Font(text.FontDesc{Family: family, SizePt: pt, Weight: 400, Scale: a.scale})
	if !a.hasGlyph(f, r) {
		return false
	}
	utf8 := []byte(string(r))
	m := a.ts.Metrics(f)
	lh := m.LineHeight
	if lh <= 0 {
		lh = m.Ascent + m.Descent + m.LineGap
	}
	*e = entry{
		src: src, font: f, utf8: utf8,
		w: a.ts.Run(a.ts.Shape(f, utf8)).Advance,
		h: float32(math.Ceil(float64(max(lh, m.Ascent+m.Descent)))),
	}
	return true
}

// Attach gives the atlas a texture on u (the renderer) and uploads it. Call it
// before the first frame and again when the renderer is recreated; image icons
// draw nothing until it has been called.
func (a *Atlas) Attach(u ui.Uploader) {
	if a.up == u {
		return
	}
	a.up, a.tex = u, 0
	a.dirty = [4]int{0, 0, a.w, a.h}
	a.upload()
}

func (a *Atlas) upload() {
	if a.up == nil {
		return
	}
	if a.tex == 0 || a.texW != a.w || a.texH != a.h { // a texture of the atlas's size
		if f, ok := a.up.(freer); ok && a.tex != 0 {
			f.FreeTexture(a.tex)
		}
		a.tex = a.up.NewTexture(gpu.TexR8, a.w, a.h)
		a.texW, a.texH = a.w, a.h
		a.dirty = [4]int{0, 0, a.w, a.h}
	}
	d := a.dirty
	if d[2] <= d[0] || d[3] <= d[1] {
		return
	}
	a.up.Upload(a.tex, d[0], d[1], d[2]-d[0], d[3]-d[1], a.pix[d[1]*a.w+d[0]:], a.w)
	a.dirty = [4]int{}
}

// Source says what icon id is drawn from (SourceNone for an unknown id).
func (a *Atlas) Source(id ID) Source {
	if int(id) >= len(a.ents) {
		return SourceNone
	}
	return a.ents[id].src
}

// Size returns the size in pixels of the box that shows icon id: the symbol's
// image, or one line of the glyph's font. It is (0, 0) for an icon that draws
// nothing.
func (a *Atlas) Size(id ID) (w, h float32) {
	if int(id) >= len(a.ents) {
		return 0, 0
	}
	e := &a.ents[id]
	return e.w, e.h
}

// AlignWidth returns the width in pixels of icon id's content, exactly, as
// layouts measure it: a symbol's alignment rectangle (NSImage.alignmentRect,
// not rounded to whole pixels and without the raster's margin: AppKit sizes
// toolbar buttons by it), a text icon's advance. It is 0 for an icon that
// draws nothing.
func (a *Atlas) AlignWidth(id ID) float32 {
	if int(id) >= len(a.ents) {
		return 0
	}
	e := &a.ents[id]
	if e.src == SourceImage && e.aw > 0 {
		return e.aw
	}
	return e.w
}

// MediumWidth returns the exact width in pixels of icon id's image at the
// medium symbol scale, whatever scale it is drawn at (NSImage.size; AppKit
// sizes a toolbar menu item by it): the text icon's advance, 0 for an icon
// that draws nothing.
func (a *Atlas) MediumWidth(id ID) float32 {
	if int(id) >= len(a.ents) {
		return 0
	}
	e := &a.ents[id]
	if e.src == SourceImage && e.mw > 0 {
		return e.mw
	}
	return e.w
}

// Prepare readies the next box to show icon id in colour (RGBA): it sets the
// box's size to Size, its tint, and for a text icon its font and alignment. It
// returns the flags and the display bytes to build the box with (nil for an
// image). An icon that draws nothing returns ui.Flags(0), which builds an empty
// box of size 0.
func (a *Atlas) Prepare(c *ui.Context, id ID, color uint32) (ui.Flags, []byte) {
	if int(id) >= len(a.ents) {
		return 0, nil
	}
	e := &a.ents[id]
	switch e.src {
	case SourceImage:
		if a.tex == 0 {
			return 0, nil
		}
		c.NextImage(a.tex, [4]float32{float32(e.x), float32(e.y), float32(e.x + e.iw), float32(e.y + e.ih)}, gpu.ModeMaskR8)
		c.NextTextColor(color)
		c.NextSize(ui.Px(e.w), ui.Px(e.h))
		return ui.DrawImage, nil
	case SourceSymbolFont, SourceFallback:
		c.NextFont(e.font)
		c.NextAlign(ui.AlignCenter)
		c.NextTextColor(color)
		c.NextSize(ui.Px(e.w), ui.Px(e.h))
		return ui.DrawText, e.utf8
	}
	return 0, nil
}
