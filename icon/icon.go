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

const (
	atlasW = 512
	atlasH = 256
)

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

	pix []byte // atlas, R8
	tex gpu.TextureID
	up  ui.Uploader
}

// New returns an Atlas of the standard icons followed by extra; Raster
// prepares it. The extra icons have the IDs NumStandard, NumStandard+1, ….
func New(ts TextSystem, extra ...Spec) *Atlas {
	return newAtlas(ts, append(Standard(), extra...))
}

func newAtlas(ts TextSystem, specs []Spec) *Atlas {
	return &Atlas{ts: ts, specs: specs, ents: make([]entry, len(specs)), pix: make([]byte, atlasW*atlasH)}
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
	clear(a.pix)
	x, y, rowH := 0, 0, 0
	for i, sp := range a.specs {
		e := &a.ents[i]
		*e = entry{}
		if sp.Symbol != "" {
			pix, w, h, ok := rasterSymbol(sp.Symbol, float64(sp.Pt)*float64(scale), float64(sp.Weight), sp.Scale)
			// Shelf-pack with a 1 px gutter.
			if ok && w <= atlasW && h <= atlasH {
				if x+w > atlasW {
					x, y, rowH = 0, y+rowH+1, 0
				}
				if y+h <= atlasH {
					for r := 0; r < h; r++ {
						copy(a.pix[(y+r)*atlasW+x:(y+r)*atlasW+x+w], pix[r*w:(r+1)*w])
					}
					*e = entry{src: SourceImage, w: float32(w), h: float32(h), x: x, y: y, iw: w, ih: h}
					// measured at the point size, as AppKit lays out
					// (its sizes are whole or half points there), then scaled
					if aw, _, ok := symbolWidths(sp.Symbol, float64(sp.Pt), float64(sp.Weight), sp.Scale); ok {
						e.aw = float32(aw) * scale
					}
					if _, mw, ok := symbolWidths(sp.Symbol, float64(sp.Pt), float64(sp.Weight), ScaleMedium); ok {
						e.mw = float32(mw) * scale
					}
					x += w + 1
					rowH = max(rowH, h)
					continue
				}
			}
		}
		if sp.Segoe != 0 && a.family != "" && a.textEntry(e, SourceSymbolFont, a.family, sp.Segoe, sp.Pt*scaleFactor(sp.Scale)) {
			continue
		}
		if sp.Fallback != 0 {
			a.textEntry(e, SourceFallback, "", sp.Fallback, sp.Pt*scaleFactor(sp.Scale))
		}
	}
	a.upload()
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
	a.upload()
}

func (a *Atlas) upload() {
	if a.up == nil {
		return
	}
	if a.tex == 0 {
		a.tex = a.up.NewTexture(gpu.TexR8, atlasW, atlasH)
	}
	a.up.Upload(a.tex, 0, 0, atlasW, atlasH, a.pix, atlasW)
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
