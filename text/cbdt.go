//go:build linux || textengine

package text

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"sort"

	"github.com/go-text/typesetting/font/opentype/tables"
)

// cbdt reads colour bitmap glyphs (Google's CBLC/CBDT, as in Noto Color
// Emoji) one glyph at a time. Only the small location table (CBLC) is kept in
// memory; the image data table (CBDT, megabytes) stays in the file and each
// requested glyph's PNG is read with a single ReadAt. COLRv1, SVG and sbix
// colour fonts are not supported.
type cbdt struct {
	r       io.ReaderAt
	dataOff int64 // file offset of the CBDT table
	dataLen int64
	loc     tables.CBLC
	scratch []byte
}

// openCBDT returns the colour bitmap reader of the font described by d, or
// nil when it has no (usable) CBLC+CBDT pair.
func openCBDT(r io.ReaderAt, d *sfntDir) *cbdt {
	loc, ok1 := d.find(tagCBLC)
	dat, ok2 := d.find(tagCBDT)
	if !ok1 || !ok2 || loc.length < 8 || dat.length < 4 {
		return nil
	}
	raw := make([]byte, loc.length)
	if _, err := r.ReadAt(raw, int64(loc.offset)); err != nil {
		return nil
	}
	parsed, _, err := tables.ParseCBLC(raw)
	if err != nil || len(parsed.BitmapSizes) == 0 {
		return nil
	}
	return &cbdt{r: r, dataOff: int64(dat.offset), dataLen: int64(dat.length), loc: parsed}
}

// strikeFor picks the strike whose ppem is the smallest not below sizePx,
// or the largest if all are smaller.
func (c *cbdt) strikeFor(sizePx float32) (idx, ppem int) {
	best, bestPpem, bestBigEnough := -1, 0, false
	for i, s := range c.loc.BitmapSizes {
		p := int(max(s.PpemX, s.PpemY))
		bigEnough := float32(p) >= sizePx
		switch {
		case best < 0,
			bigEnough && !bestBigEnough,
			bigEnough && bestBigEnough && p < bestPpem,
			!bigEnough && !bestBigEnough && p > bestPpem:
			best, bestPpem, bestBigEnough = i, p, bigEnough
		}
	}
	return best, bestPpem
}

// cbGlyph is the PNG of one glyph and its metrics in strike pixels.
type cbGlyph struct {
	png                []byte // aliases the cbdt scratch buffer
	width, height      int
	bearingX, bearingY int
}

var errNoGlyph = errors.New("no bitmap for glyph")

// glyph reads glyph gid of the strike. The returned png slice is only valid
// until the next call.
func (c *cbdt) glyph(strike int, gid uint16) (cbGlyph, error) {
	if strike < 0 || strike >= len(c.loc.IndexSubTables) {
		return cbGlyph{}, errNoGlyph
	}
	for _, sub := range c.loc.IndexSubTables[strike] {
		if gid < sub.FirstGlyph || gid > sub.LastGlyph {
			continue
		}
		var start, end uint32
		var big *tables.BigGlyphMetrics
		i := int(gid - sub.FirstGlyph)
		switch ix := sub.IndexData.(type) {
		case tables.IndexData1:
			if i+1 >= len(ix.SbitOffsets) {
				return cbGlyph{}, errNoGlyph
			}
			start, end = uint32(ix.SbitOffsets[i]), uint32(ix.SbitOffsets[i+1])
		case tables.IndexData3:
			if i+1 >= len(ix.SbitOffsets) {
				return cbGlyph{}, errNoGlyph
			}
			start, end = uint32(ix.SbitOffsets[i]), uint32(ix.SbitOffsets[i+1])
		case tables.IndexData2:
			start, end = ix.ImageSize*uint32(i), ix.ImageSize*uint32(i+1)
			big = &ix.BigMetrics
		case tables.IndexData4:
			j := -1
			for k := 0; k+1 < len(ix.GlyphArray); k++ {
				if ix.GlyphArray[k].GlyphID == gid {
					j = k
					break
				}
			}
			if j < 0 {
				return cbGlyph{}, errNoGlyph
			}
			start, end = uint32(ix.GlyphArray[j].SbitOffset), uint32(ix.GlyphArray[j+1].SbitOffset)
		case tables.IndexData5:
			j := sort.Search(len(ix.GlyphIdArray), func(k int) bool { return ix.GlyphIdArray[k] >= gid })
			if j >= len(ix.GlyphIdArray) || ix.GlyphIdArray[j] != gid {
				return cbGlyph{}, errNoGlyph
			}
			start, end = ix.ImageSize*uint32(j), ix.ImageSize*uint32(j+1)
			big = &ix.BigMetrics
		default:
			return cbGlyph{}, errNoGlyph
		}
		if end <= start {
			return cbGlyph{}, errNoGlyph
		}
		off := int64(sub.ImageDataOffset) + int64(start)
		n := int64(end - start)
		if off+n > c.dataLen || n > 16<<20 {
			return cbGlyph{}, fmt.Errorf("CBDT glyph %d out of range", gid)
		}
		if cap(c.scratch) < int(n) {
			c.scratch = make([]byte, n)
		}
		buf := c.scratch[:n]
		if _, err := c.r.ReadAt(buf, c.dataOff+off); err != nil {
			return cbGlyph{}, err
		}
		return parseCBImage(sub.ImageFormat, buf, big)
	}
	return cbGlyph{}, errNoGlyph
}

func parseCBImage(format uint16, buf []byte, big *tables.BigGlyphMetrics) (cbGlyph, error) {
	switch format {
	case 17:
		d, _, err := tables.ParseBitmapData17(buf)
		return cbGlyph{d.Image, int(d.Width), int(d.Height), int(d.BearingX), int(d.BearingY)}, err
	case 18:
		d, _, err := tables.ParseBitmapData18(buf)
		return cbGlyph{d.Image, int(d.Width), int(d.Height), int(d.BearingX), int(d.BearingY)}, err
	case 19:
		d, _, err := tables.ParseBitmapData19(buf)
		if big == nil {
			return cbGlyph{}, errors.New("CBDT format 19 without index metrics")
		}
		return cbGlyph{d.Image, int(big.Width), int(big.Height), int(big.BearingX), int(big.BearingY)}, err
	}
	return cbGlyph{}, fmt.Errorf("unsupported CBDT image format %d", format)
}

// pngPremultiplied decodes a PNG into premultiplied RGBA bytes (4 per pixel).
func pngPremultiplied(data []byte) (pix []byte, w, h int, err error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	b := img.Bounds()
	w, h = b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return nil, 0, 0, errors.New("implausible bitmap size")
	}
	pix = make([]byte, w*h*4)
	switch m := img.(type) {
	case *image.NRGBA:
		for y := 0; y < h; y++ {
			row := m.Pix[y*m.Stride : y*m.Stride+w*4]
			out := pix[y*w*4:]
			for x := 0; x < w; x++ {
				r, g, bl, a := uint32(row[4*x]), uint32(row[4*x+1]), uint32(row[4*x+2]), uint32(row[4*x+3])
				out[4*x] = byte((r*a + 127) / 255)
				out[4*x+1] = byte((g*a + 127) / 255)
				out[4*x+2] = byte((bl*a + 127) / 255)
				out[4*x+3] = byte(a)
			}
		}
	case *image.RGBA:
		for y := 0; y < h; y++ {
			copy(pix[y*w*4:(y+1)*w*4], m.Pix[y*m.Stride:y*m.Stride+w*4])
		}
	default:
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := color.RGBAModel.Convert(img.At(b.Min.X+x, b.Min.Y+y)).(color.RGBA)
				o := (y*w + x) * 4
				pix[o], pix[o+1], pix[o+2], pix[o+3] = c.R, c.G, c.B, c.A
			}
		}
	}
	return pix, w, h, nil
}

// scalePremultiplied resamples a premultiplied RGBA image to dw×dh by area
// averaging (downscale) or bilinear interpolation (upscale).
func scalePremultiplied(src []byte, sw, sh, dw, dh int) []byte {
	dst := make([]byte, dw*dh*4)
	if dw == sw && dh == sh {
		copy(dst, src)
		return dst
	}
	fx, fy := float64(sw)/float64(dw), float64(sh)/float64(dh)
	if fx <= 1 && fy <= 1 { // upscale: bilinear
		for y := 0; y < dh; y++ {
			sy := (float64(y)+0.5)*fy - 0.5
			y0 := int(math.Floor(sy))
			ty := sy - float64(y0)
			for x := 0; x < dw; x++ {
				sx := (float64(x)+0.5)*fx - 0.5
				x0 := int(math.Floor(sx))
				tx := sx - float64(x0)
				for c := 0; c < 4; c++ {
					at := func(xx, yy int) float64 {
						xx, yy = min(max(xx, 0), sw-1), min(max(yy, 0), sh-1)
						return float64(src[(yy*sw+xx)*4+c])
					}
					v := (at(x0, y0)*(1-tx)+at(x0+1, y0)*tx)*(1-ty) + (at(x0, y0+1)*(1-tx)+at(x0+1, y0+1)*tx)*ty
					dst[(y*dw+x)*4+c] = byte(v + 0.5)
				}
			}
		}
		return dst
	}
	for y := 0; y < dh; y++ {
		y0, y1 := float64(y)*fy, float64(y+1)*fy
		for x := 0; x < dw; x++ {
			x0, x1 := float64(x)*fx, float64(x+1)*fx
			var acc [4]float64
			var wsum float64
			for sy := int(y0); sy < sh && float64(sy) < y1; sy++ {
				wy := math.Min(float64(sy+1), y1) - math.Max(float64(sy), y0)
				for sx := int(x0); sx < sw && float64(sx) < x1; sx++ {
					wgt := wy * (math.Min(float64(sx+1), x1) - math.Max(float64(sx), x0))
					o := (sy*sw + sx) * 4
					acc[0] += wgt * float64(src[o])
					acc[1] += wgt * float64(src[o+1])
					acc[2] += wgt * float64(src[o+2])
					acc[3] += wgt * float64(src[o+3])
					wsum += wgt
				}
			}
			if wsum > 0 {
				o := (y*dw + x) * 4
				for c := 0; c < 4; c++ {
					dst[o+c] = byte(acc[c]/wsum + 0.5)
				}
			}
		}
	}
	return dst
}
