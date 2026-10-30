//go:build linux || textengine

package text

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/adler32"
	"hash/crc32"
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
	png     pngScratch
	scaled  []byte
}

// openCBDT returns the colour bitmap reader of the font described by d, or
// nil when it has no (usable) CBLC+CBDT pair.
func openCBDT(r io.ReaderAt, d *sfntDir) *cbdt {
	loc, ok1 := d.find(tagCBLC)
	dat, ok2 := d.find(tagCBDT)
	// maxCBLC bounds the index table read whole (Noto Color Emoji's is ~50 KB);
	// the directory is also checked against the file size when it's known.
	const maxCBLC = 16 << 20
	if !ok1 || !ok2 || loc.length < 8 || dat.length < 4 || loc.length > maxCBLC {
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

// pngScratch decodes PNG glyphs into reusable storage. It supports every
// standard PNG colour/depth combination and Adam7 passes. The inflater and
// all pixel buffers are reused across misses; only a new high-water mark grows.
type pngScratch struct {
	idat, raw, pix []byte
	rd             bytes.Reader
	inflater       io.ReadCloser
	palette        [256][4]byte
	end            [1]byte
}

var errBitmapPNG = errors.New("invalid bitmap PNG")

// pngPremultiplied is the unreserved form, used by the format probes.
func pngPremultiplied(data []byte) ([]byte, int, int, error) {
	var d pngScratch
	return d.decode(data)
}

func (d *pngScratch) decode(data []byte) ([]byte, int, int, error) {
	if len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return nil, 0, 0, errBitmapPNG
	}
	d.idat = d.idat[:0]
	for i := range d.palette {
		d.palette[i] = [4]byte{0, 0, 0, 255}
	}
	var w, h, depth, kind, interlace, colors int
	var transparent [3]uint16
	var hasTransparent, header, ended, idat, idatEnd, trns, plte bool
	channels := 0
	for off := 8; off < len(data); {
		if len(data)-off < 12 {
			return nil, 0, 0, errBitmapPNG
		}
		n := uint64(binary.BigEndian.Uint32(data[off:]))
		if n > uint64(len(data)-off-12) {
			return nil, 0, 0, errBitmapPNG
		}
		tag := string(data[off+4 : off+8])
		body := data[off+8 : off+8+int(n)]
		if crc32.ChecksumIEEE(data[off+4:off+8+int(n)]) != binary.BigEndian.Uint32(data[off+8+int(n):]) {
			return nil, 0, 0, errBitmapPNG
		}
		off += int(n) + 12
		if !header && tag != "IHDR" {
			return nil, 0, 0, errBitmapPNG
		}
		if idat && tag != "IDAT" {
			idatEnd = true
		}
		switch tag {
		case "IHDR":
			if header || len(body) != 13 {
				return nil, 0, 0, errBitmapPNG
			}
			header = true
			uw, uh := binary.BigEndian.Uint32(body), binary.BigEndian.Uint32(body[4:])
			if uw == 0 || uh == 0 || uw > 4096 || uh > 4096 {
				return nil, 0, 0, errBitmapPNG
			}
			w, h, depth, kind, interlace = int(uw), int(uh), int(body[8]), int(body[9]), int(body[12])
			if body[10] != 0 || body[11] != 0 || interlace > 1 {
				return nil, 0, 0, errBitmapPNG
			}
			switch kind {
			case 0:
				channels = 1
			case 2:
				channels = 3
			case 3:
				channels = 1
			case 4:
				channels = 2
			case 6:
				channels = 4
			default:
				return nil, 0, 0, errBitmapPNG
			}
			if depth != 8 && depth != 16 && !(depth == 1 || depth == 2 || depth == 4) {
				return nil, 0, 0, errBitmapPNG
			}
			if (kind == 3 && depth == 16) || (kind != 0 && kind != 3 && depth < 8) {
				return nil, 0, 0, errBitmapPNG
			}
		case "PLTE":
			if plte || trns || idat || kind == 0 || kind == 4 || len(body) == 0 || len(body)%3 != 0 || len(body) > 768 {
				return nil, 0, 0, errBitmapPNG
			}
			plte, colors = true, len(body)/3
			if kind == 3 && colors > 1<<depth {
				return nil, 0, 0, errBitmapPNG
			}
			for i := 0; i < colors; i++ {
				copy(d.palette[i][:3], body[3*i:3*i+3])
			}
		case "tRNS":
			if trns || idat {
				return nil, 0, 0, errBitmapPNG
			}
			trns = true
			switch kind {
			case 3:
				if !plte || len(body) > colors {
					return nil, 0, 0, errBitmapPNG
				}
				for i, a := range body {
					d.palette[i][3] = a
				}
			case 0:
				if len(body) != 2 {
					return nil, 0, 0, errBitmapPNG
				}
				transparent[0], hasTransparent = binary.BigEndian.Uint16(body), true
			case 2:
				if len(body) != 6 {
					return nil, 0, 0, errBitmapPNG
				}
				for i := range transparent {
					transparent[i] = binary.BigEndian.Uint16(body[2*i:])
				}
				hasTransparent = true
			default:
				return nil, 0, 0, errBitmapPNG
			}
		case "IDAT":
			if idatEnd || (kind == 3 && !plte) {
				return nil, 0, 0, errBitmapPNG
			}
			idat = true
			d.idat = append(d.idat, body...)
		case "IEND":
			if !idat || len(body) != 0 {
				return nil, 0, 0, errBitmapPNG
			}
			ended = true
		default:
			if data[off-int(n)-8]&32 == 0 {
				return nil, 0, 0, errBitmapPNG
			} // unknown critical chunk
		}
		if ended {
			break
		}
	}
	if !ended {
		return nil, 0, 0, errBitmapPNG
	}
	// Adam7's (x0, y0, dx, dy), or one full-resolution pass.
	passes := [7][4]int{{0, 0, 1, 1}}
	npass := 1
	if interlace != 0 {
		passes = [7][4]int{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}}
		npass = 7
	}
	total := 0
	for _, p := range passes[:npass] {
		pw, ph := max(0, (w-p[0]+p[2]-1)/p[2]), max(0, (h-p[1]+p[3]-1)/p[3])
		if pw > 0 && ph > 0 {
			total += (1 + (pw*channels*depth+7)/8) * ph
		}
	}
	if cap(d.raw) < total {
		d.raw = make([]byte, total)
	}
	raw := d.raw[:total]
	// zlib.Reader.Reset creates a new Adler digest each time. Read its
	// small PNG wrapper here and reuse flate directly instead.
	if len(d.idat) < 6 || d.idat[0]&15 != 8 || d.idat[0]>>4 > 7 || binary.BigEndian.Uint16(d.idat)%31 != 0 || d.idat[1]&32 != 0 {
		return nil, 0, 0, errBitmapPNG
	}
	d.rd.Reset(d.idat[2 : len(d.idat)-4])
	var err error
	if d.inflater == nil {
		d.inflater = flate.NewReader(&d.rd)
	} else {
		err = d.inflater.(flate.Resetter).Reset(&d.rd, nil)
	}
	if err != nil {
		return nil, 0, 0, err
	}
	_, err = io.ReadFull(d.inflater, raw)
	if err == nil {
		n, endErr := d.inflater.Read(d.end[:])
		if n != 0 || endErr != io.EOF {
			err = errBitmapPNG
		}
	}
	closeErr := d.inflater.Close()
	if err != nil {
		return nil, 0, 0, err
	}
	if closeErr != nil {
		return nil, 0, 0, closeErr
	}
	if d.rd.Len() != 0 || adler32.Checksum(raw) != binary.BigEndian.Uint32(d.idat[len(d.idat)-4:]) {
		return nil, 0, 0, errBitmapPNG
	}
	if cap(d.pix) < w*h*4 {
		d.pix = make([]byte, w*h*4)
	}
	pix := d.pix[:w*h*4]
	bpp := max(1, (channels*depth+7)/8)
	for _, p := range passes[:npass] {
		pw, ph := max(0, (w-p[0]+p[2]-1)/p[2]), max(0, (h-p[1]+p[3]-1)/p[3])
		if pw == 0 || ph == 0 {
			continue
		}
		stride := (pw*channels*depth + 7) / 8
		var prev []byte
		for y := 0; y < ph; y++ {
			filter, row := raw[0], raw[1:stride+1]
			if !unfilterPNG(row, prev, bpp, filter) {
				return nil, 0, 0, errBitmapPNG
			}
			for x := 0; x < pw; x++ {
				at := ((p[1]+y*p[3])*w + p[0] + x*p[2]) * 4
				var r, g, b, a uint32
				if kind == 3 {
					index := pngSample(row, x, depth)
					if int(index) >= colors {
						return nil, 0, 0, errBitmapPNG
					}
					c := d.palette[index]
					a = uint32(c[3])
					// Paletted colours use color.NRGBA.RGBA's 16-bit conversion.
					r, g, b = uint32(c[0])*257*a/255>>8, uint32(c[1])*257*a/255>>8, uint32(c[2])*257*a/255>>8
				} else {
					r = pngSample(row, x*channels, depth)
					g, b, a = r, r, (1<<depth)-1
					switch kind {
					case 2, 6:
						g, b = pngSample(row, x*channels+1, depth), pngSample(row, x*channels+2, depth)
						if kind == 6 {
							a = pngSample(row, x*channels+3, depth)
						}
					case 4:
						a = pngSample(row, x*channels+1, depth)
					}
					if hasTransparent && r == uint32(transparent[0]) && (kind == 0 || (g == uint32(transparent[1]) && b == uint32(transparent[2]))) {
						a = 0
					}
					switch depth {
					case 16:
						r, g, b, a = r*a/65535>>8, g*a/65535>>8, b*a/65535>>8, a>>8
					case 8:
						r, g, b = (r*a+127)/255, (g*a+127)/255, (b*a+127)/255
					default:
						r = r * 255 / ((1 << depth) - 1)
						if a == 0 {
							r = 0
						} else {
							a = 255
						}
						g, b = r, r
					}
				}
				pix[at], pix[at+1], pix[at+2], pix[at+3] = byte(r), byte(g), byte(b), byte(a)
			}
			prev, raw = row, raw[stride+1:]
		}
	}
	return pix, w, h, nil
}

func pngSample(row []byte, i, depth int) uint32 {
	switch depth {
	case 8:
		return uint32(row[i])
	case 16:
		return uint32(binary.BigEndian.Uint16(row[2*i:]))
	}
	return uint32(row[i*depth/8]>>uint(8-depth-(i*depth&7))) & ((1 << depth) - 1)
}

func unfilterPNG(row, prev []byte, bpp int, filter byte) bool {
	if filter > 4 {
		return false
	}
	for x := range row {
		var left, up, ul byte
		if x >= bpp {
			left = row[x-bpp]
		}
		if prev != nil {
			up = prev[x]
			if x >= bpp {
				ul = prev[x-bpp]
			}
		}
		switch filter {
		case 1:
			row[x] += left
		case 2:
			row[x] += up
		case 3:
			row[x] += byte((int(left) + int(up)) / 2)
		case 4:
			p := int(left) + int(up) - int(ul)
			dl, du, dul := absPNG(p-int(left)), absPNG(p-int(up)), absPNG(p-int(ul))
			switch {
			case dl <= du && dl <= dul:
				row[x] += left
			case du <= dul:
				row[x] += up
			default:
				row[x] += ul
			}
		}
	}
	return true
}

func absPNG(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// scalePremultiplied resamples a premultiplied RGBA image to dw×dh by area
// averaging (downscale) or bilinear interpolation (upscale).
func scalePremultiplied(dst, src []byte, sw, sh, dw, dh int) []byte {
	if cap(dst) < dw*dh*4 {
		dst = make([]byte, dw*dh*4)
	}
	dst = dst[:dw*dh*4]
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
