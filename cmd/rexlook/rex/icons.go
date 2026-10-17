package rex

import (
	"gonativeui/gpu"
	"gonativeui/ui"
)

// Icons are system symbols rasterised once into an R8 coverage atlas and
// drawn as tinted images (ui.DrawImage): SF Symbols on macOS (icons_darwin.go).
// Where no symbol rasteriser exists, or a symbol is missing, the icon falls
// back to a text glyph of the system font, so every platform draws something
// in the same place. Nothing here is third-party artwork.

type iconID uint8

const (
	icAgent iconID = iota
	icGit
	icNode
	icSplit2
	icSplit1
	icExpand
	icXmark
	icDevice
	icCommand
	icPlus
	icTabAgent
	icTabFace
	icTabTerm
	numIcons
)

type iconSpec struct {
	name   string  // SF Symbol name
	pt     float64 // size in points
	weight float64 // NSFontWeight: 0 regular, 0.23 medium
	fb     string  // fallback glyph
}

var iconSpecs = [numIcons]iconSpec{
	icAgent:    {"sparkles", 12.5, 0.23, "✦"},
	icGit:      {"plusminus.circle", 12, 0, "±"},
	icNode:     {"hexagon.fill", 12, 0, "⬢"},
	icSplit2:   {"rectangle.split.2x1", 11, 0, "◫"},
	icSplit1:   {"rectangle.split.1x2", 11, 0, "⬒"},
	icExpand:   {"arrow.up.left.and.arrow.down.right", 10.5, 0, "⤢"},
	icXmark:    {"xmark", 10.5, 0, "✕"},
	icDevice:   {"macstudio", 11, 0, "▣"},
	icCommand:  {"command", 12.5, 0.23, "⌘"},
	icPlus:     {"plus", 12.5, 0.23, "+"},
	icTabAgent: {"sparkles", 12, 0.23, "✦"},
	icTabFace:  {"face.smiling", 12, 0, "☺"},
	icTabTerm:  {"terminal", 12, 0.23, ">_"},
}

const (
	atlasW = 512
	atlasH = 256
)

// symRect is the texel rectangle of one rasterised symbol.
type symRect struct{ x, y, w, h int }

type icons struct {
	pix   []byte // atlas, R8
	rects [numIcons]symRect
	have  [numIcons]bool
	tex   gpu.TextureID
	up    ui.Uploader
	fb    [numIcons][]byte
}

func newIcons() *icons {
	ic := &icons{pix: make([]byte, atlasW*atlasH)}
	for i := range ic.fb {
		ic.fb[i] = []byte(iconSpecs[i].fb)
	}
	return ic
}

// raster (re)rasterises the symbols at the given pixels-per-point scale and
// uploads the atlas if a renderer is attached.
func (ic *icons) raster(scale float32) {
	clear(ic.pix)
	ic.have = [numIcons]bool{}
	x, y, rowH := 0, 0, 0
	for i, sp := range iconSpecs {
		pix, w, h, ok := rasterSymbol(sp.name, sp.pt*float64(scale), sp.weight)
		if !ok || w > atlasW || h > atlasH {
			continue
		}
		// Shelf-pack with a 1 px gutter.
		if x+w > atlasW {
			x, y, rowH = 0, y+rowH+1, 0
		}
		if y+h > atlasH {
			continue
		}
		for r := 0; r < h; r++ {
			copy(ic.pix[(y+r)*atlasW+x:(y+r)*atlasW+x+w], pix[r*w:(r+1)*w])
		}
		ic.rects[i] = symRect{x, y, w, h}
		ic.have[i] = true
		x += w + 1
		rowH = max(rowH, h)
	}
	ic.upload()
}

func (ic *icons) attach(u ui.Uploader) {
	if ic.up == u {
		return
	}
	ic.up, ic.tex = u, 0
	ic.upload()
}

func (ic *icons) upload() {
	if ic.up == nil {
		return
	}
	if ic.tex == 0 {
		ic.tex = ic.up.NewTexture(gpu.TexR8, atlasW, atlasH)
	}
	ic.up.Upload(ic.tex, 0, 0, atlasW, atlasH, ic.pix, atlasW)
}

// drawable reports whether icon id is drawn from the atlas (otherwise as text).
func (ic *icons) drawable(id iconID) bool { return ic.have[id] && ic.tex != 0 }

// set prepares the next box to show icon id: it returns the flags to build the
// box with and the display bytes (the fallback glyph; nil for an image). The
// caller sets the text colour and, for the fallback, the font and alignment.
func (ic *icons) set(c *ui.Context, id iconID) (ui.Flags, []byte) {
	if ic.drawable(id) {
		sr := ic.rects[id]
		c.NextImage(ic.tex, [4]float32{float32(sr.x), float32(sr.y), float32(sr.x + sr.w), float32(sr.y + sr.h)}, gpu.ModeMaskR8)
		return ui.DrawImage, nil
	}
	return ui.DrawText, ic.fb[id]
}
