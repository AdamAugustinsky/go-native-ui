package rexkit

import (
	"gonativeui/gpu"
	"gonativeui/ui"
)

// Icons are system symbols rasterised once into an R8 coverage atlas and
// drawn as tinted images (ui.DrawImage): SF Symbols on macOS (icons_darwin.go).
// Where no symbol rasteriser exists, or a symbol is missing, the icon falls
// back to a text glyph of the system font, so every platform draws something
// in the same place. Nothing here is third-party artwork.

// IconID indexes the IconSpec list an Icons was made from.
type IconID uint8

// IconSpec describes one icon: an SF Symbol and its glyph fallback.
type IconSpec struct {
	Name   string  // SF Symbol name
	Pt     float64 // size in points
	Weight float64 // NSFontWeight: 0 regular, 0.23 medium
	FB     string  // fallback glyph
}

const (
	atlasW = 512
	atlasH = 256
)

// symRect is the texel rectangle of one rasterised symbol.
type symRect struct{ x, y, w, h int }

// Icons is the atlas of rasterised symbols.
type Icons struct {
	pix   []byte // atlas, R8
	specs []IconSpec
	rects []symRect
	have  []bool
	tex   gpu.TextureID
	up    ui.Uploader
	fb    [][]byte
}

// NewIcons returns an empty atlas for specs; Raster fills it.
func NewIcons(specs []IconSpec) *Icons {
	ic := &Icons{
		pix: make([]byte, atlasW*atlasH), specs: specs,
		rects: make([]symRect, len(specs)), have: make([]bool, len(specs)), fb: make([][]byte, len(specs)),
	}
	for i := range ic.fb {
		ic.fb[i] = []byte(specs[i].FB)
	}
	return ic
}

// Raster (re)rasterises the symbols at the given pixels-per-point scale and
// uploads the atlas if a renderer is attached.
func (ic *Icons) Raster(scale float32) {
	clear(ic.pix)
	clear(ic.have)
	x, y, rowH := 0, 0, 0
	for i, sp := range ic.specs {
		pix, w, h, ok := rasterSymbol(sp.Name, sp.Pt*float64(scale), sp.Weight)
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

func (ic *Icons) Attach(u ui.Uploader) {
	if ic.up == u {
		return
	}
	ic.up, ic.tex = u, 0
	ic.upload()
}

func (ic *Icons) upload() {
	if ic.up == nil {
		return
	}
	if ic.tex == 0 {
		ic.tex = ic.up.NewTexture(gpu.TexR8, atlasW, atlasH)
	}
	ic.up.Upload(ic.tex, 0, 0, atlasW, atlasH, ic.pix, atlasW)
}

// drawable reports whether icon id is drawn from the atlas (otherwise as text).
func (ic *Icons) drawable(id IconID) bool { return ic.have[id] && ic.tex != 0 }

// Set prepares the next box to show icon id: it returns the flags to build the
// box with and the display bytes (the fallback glyph; nil for an image). The
// caller sets the text colour and, for the fallback, the font and alignment.
func (ic *Icons) Set(c *ui.Context, id IconID) (ui.Flags, []byte) {
	if ic.drawable(id) {
		sr := ic.rects[id]
		c.NextImage(ic.tex, [4]float32{float32(sr.x), float32(sr.y), float32(sr.x + sr.w), float32(sr.y + sr.h)}, gpu.ModeMaskR8)
		return ui.DrawImage, nil
	}
	return ui.DrawText, ic.fb[id]
}
