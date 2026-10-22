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
// Atlas.Raster picks a source per icon, once per scale, and never per frame.
// It picks it for an icon when the icon is first used (Prepare, Size, Source,
// AlignWidth, MediumWidth), not when it is added: an app that adds a hundred
// icons and shows five rasterises five. The icon is ready, and uploaded, by the
// time the call returns, so it draws in the frame that first asks for it.
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
// atlas, at the same moment: the icon's first use shapes its glyph once so that
// later frames find it cached.
//
// # Threading
//
// An Atlas is used from the main thread (AppKit and the text system require
// it), like the ui.Context it draws into.
package icon

import (
	"log"
	"math"
	"slices"
	"unicode/utf8"

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

// stager is what an Uploader may also offer: gpu.Renderer.Stage, an upload
// into a texture that frames in flight sample, which does not wait for them.
type stager interface {
	Stage(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int)
}

// retiredTex is a texture the atlas has outgrown or reset, kept until the
// frame after the one that may have boxes naming it.
type retiredTex struct {
	tex   gpu.TextureID
	frame uint32
}

// entry is one icon's resolved source and size.
type entry struct {
	done bool // resolved at the current scale; if not, nothing else is set
	src  Source
	w, h float32 // pixels
	aw   float32 // SourceImage: the symbol's alignment width, pixels (exact)
	mw   float32 // SourceImage: its image's width at the medium scale, pixels (exact)
	// SourceImage: the texel rectangle in the atlas.
	x, y, iw, ih int
	// text sources: the font and the UTF-8 of the glyph.
	font  text.FontKey
	glyph [utf8.UTFMax]byte
	gn    uint8
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
	rb           [utf8.UTFMax]byte
	scratch      []byte // a symbol's coverage before it is copied into pix
	resolved     int    // icons resolved since the last Raster
	frame        uint32 // the frame Prepare last saw
	retired      []retiredTex
	keep         []ID // prepared at every Raster (Keep)
	tex          gpu.TextureID
	texW, texH   int // the size of tex
	up           ui.Uploader
	stager       stager
	freer        freer
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
// (the others follow). Nothing is rasterised: an icon is prepared when it is
// first used, so adding costs a slice append. The IDs already given stay
// valid. Main thread, as Raster.
func (a *Atlas) Add(specs ...Spec) ID {
	first := ID(len(a.specs))
	a.specs = append(a.specs, specs...)
	a.ents = append(a.ents, make([]entry, len(specs))...)
	return first
}

// Len is the number of icons.
func (a *Atlas) Len() int { return len(a.specs) }

// probeRune is a codepoint every Segoe icon font defines (GlobalNavigationButton).
const probeRune = 0xE700

// hasGlyph reports whether shaping r in font f finds a glyph.
func (a *Atlas) hasGlyph(f text.FontKey, r rune) bool {
	run := a.ts.Run(a.ts.Shape(f, utf8.AppendRune(a.rb[:0], r)))
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

// Raster sets the scale in pixels per point and forgets how every icon was
// prepared: the atlas is emptied (back to its initial size) and the icons are
// prepared again as they are used. It uploads the empty atlas if a renderer is
// attached. Call it when the window is created and when its scale changes, on
// the main thread.
func (a *Atlas) Raster(scale float32) {
	if !a.checked {
		a.family, a.checked = a.findFamily(symbolFamilies), true
	}
	a.scale = scale
	a.reset()
	clear(a.ents)
	a.resolved = 0
	for _, id := range a.keep {
		a.resolve(int(id))
	}
	a.upload()
}

// Keep makes icons ids prepared ahead of use: now if Raster has run, and at
// every Raster after it (a new scale), instead of when a frame first needs
// them. Use it for the icons that a frame may need for the first time at a
// moment the app does not choose (a fallback shown when a system icon fails to
// decode, an error mark): preparing one costs a little work, and allocates on
// the way (shaping a glyph, rasterising a symbol), which a frame that is
// meant to run steady should not take by chance. The cost is that of
// preparing them at Raster, as every icon was before first use. Unknown ids
// are ignored. Main thread.
func (a *Atlas) Keep(ids ...ID) {
	for _, id := range ids {
		if int(id) >= len(a.specs) || slices.Contains(a.keep, id) {
			continue
		}
		a.keep = append(a.keep, id)
		a.ent(id)
	}
}

// resolve prepares icon i for the current scale and gets it to the renderer:
// a symbol is rasterised into the atlas (which grows to hold it), a text icon
// gets its font and its glyph shaped.
func (a *Atlas) resolve(i int) {
	a.rasterOne(i)
	a.ents[i].done = true
	a.resolved++
}

// ent returns icon id's entry, resolving it if it is the first use at this
// scale; nil for an unknown id. Before Raster nothing resolves.
func (a *Atlas) ent(id ID) *entry {
	if int(id) >= len(a.ents) {
		return nil
	}
	if e := &a.ents[id]; e.done || a.scale == 0 {
		return e
	}
	a.resolve(int(id))
	a.upload()
	return &a.ents[id]
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
		if pix, w, h, ok := rasterSymbolInto(a.scratch, sp.Symbol, float64(sp.Pt)*float64(scale), float64(sp.Weight), sp.Scale); ok {
			a.scratch = pix[:0]
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
	m := a.ts.Metrics(f)
	lh := m.LineHeight
	if lh <= 0 {
		lh = m.Ascent + m.Descent + m.LineGap
	}
	*e = entry{src: src, font: f}
	e.gn = uint8(utf8.EncodeRune(e.glyph[:], r))
	e.w = a.ts.Run(a.ts.Shape(f, e.glyph[:e.gn])).Advance
	e.h = float32(math.Ceil(float64(max(lh, m.Ascent+m.Descent))))
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
	a.stager, _ = u.(stager)
	a.freer, _ = u.(freer)
	a.retired = a.retired[:0] // the old renderer's
	a.dirty = [4]int{0, 0, a.w, a.h}
	a.upload()
}

func (a *Atlas) upload() {
	if a.up == nil {
		return
	}
	fresh := false
	if a.tex == 0 || a.texW != a.w || a.texH != a.h { // a texture of the atlas's size
		if a.tex != 0 {
			a.retire(a.tex)
		}
		a.tex = a.up.NewTexture(gpu.TexR8, a.w, a.h)
		a.texW, a.texH, fresh = a.w, a.h, true
		a.dirty = [4]int{0, 0, a.w, a.h}
	}
	d := a.dirty
	if d[2] <= d[0] || d[3] <= d[1] {
		return
	}
	pix := a.pix[d[1]*a.w+d[0]:]
	if a.stager != nil && !fresh {
		a.stager.Stage(a.tex, d[0], d[1], d[2]-d[0], d[3]-d[1], pix, a.w)
	} else {
		a.up.Upload(a.tex, d[0], d[1], d[2]-d[0], d[3]-d[1], pix, a.w)
	}
	a.dirty = [4]int{}
}

// retire keeps texture t, which boxes of the frame being built may name, until
// the next frame (reap); at once if the uploader cannot free textures.
func (a *Atlas) retire(t gpu.TextureID) {
	if a.freer != nil {
		a.retired = append(a.retired, retiredTex{t, a.frame})
	}
}

// reap frees the retired textures of frames before the current one.
func (a *Atlas) reap() {
	k := 0
	for _, r := range a.retired {
		if r.frame == a.frame {
			a.retired[k] = r
			k++
		} else {
			a.freer.FreeTexture(r.tex)
		}
	}
	a.retired = a.retired[:k]
}

// Source says what icon id is drawn from (SourceNone for an unknown id).
func (a *Atlas) Source(id ID) Source {
	if e := a.ent(id); e != nil {
		return e.src
	}
	return SourceNone
}

// Size returns the size in pixels of the box that shows icon id: the symbol's
// image, or one line of the glyph's font. It is (0, 0) for an icon that draws
// nothing.
func (a *Atlas) Size(id ID) (w, h float32) {
	e := a.ent(id)
	if e == nil {
		return 0, 0
	}
	return e.w, e.h
}

// AlignWidth returns the width in pixels of icon id's content, exactly, as
// layouts measure it: a symbol's alignment rectangle (NSImage.alignmentRect,
// not rounded to whole pixels and without the raster's margin: AppKit sizes
// toolbar buttons by it), a text icon's advance. It is 0 for an icon that
// draws nothing.
func (a *Atlas) AlignWidth(id ID) float32 {
	e := a.ent(id)
	if e == nil {
		return 0
	}
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
	e := a.ent(id)
	if e == nil {
		return 0
	}
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
	a.frame = c.Frame()
	e := a.ent(id)
	if e == nil {
		return 0, nil
	}
	if len(a.retired) > 0 {
		a.reap()
	}
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
		return ui.DrawText, e.glyph[:e.gn]
	}
	return 0, nil
}
