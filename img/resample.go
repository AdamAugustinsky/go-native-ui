package img

import (
	"image"
	"image/color"
)

// resample scales src, shown with EXIF orientation orient (1 to 8), to
// tw×th premultiplied RGBA by area averaging, into dst (stride bytes per
// row). The decoders never enlarge, so each destination pixel averages the
// block of source pixels it covers, split at whole-pixel boundaries. Every
// source pixel is read once, straight from the decoder's image (YCbCr, RGBA,
// NRGBA, paletted and grey without a conversion pass).
func resample(dst []byte, stride, tw, th int, src image.Image, orient int) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	fetch := fetcher(src)
	ow, oh := sw, sh
	if orient >= 5 {
		ow, oh = sh, sw
	}
	for dy := 0; dy < th; dy++ {
		y0, y1 := dy*oh/th, (dy+1)*oh/th
		y1 = max(y1, y0+1)
		for dx := 0; dx < tw; dx++ {
			x0, x1 := dx*ow/tw, (dx+1)*ow/tw
			x1 = max(x1, x0+1)
			var r, g, bl, a, n uint32
			for oy := y0; oy < y1; oy++ {
				for ox := x0; ox < x1; ox++ {
					sx, sy := orientedToStored(ox, oy, sw, sh, orient)
					p := fetch(b.Min.X+sx, b.Min.Y+sy)
					r += uint32(p[0])
					g += uint32(p[1])
					bl += uint32(p[2])
					a += uint32(p[3])
					n++
				}
			}
			d := dst[dy*stride+dx*4:]
			d[0], d[1], d[2], d[3] = byte((r+n/2)/n), byte((g+n/2)/n), byte((bl+n/2)/n), byte((a+n/2)/n)
		}
	}
}

// fetcher returns a function that reads pixel (x, y) of m as premultiplied
// RGBA8.
func fetcher(m image.Image) func(x, y int) [4]uint8 {
	switch m := m.(type) {
	case *image.YCbCr:
		return func(x, y int) [4]uint8 {
			yi, ci := m.YOffset(x, y), m.COffset(x, y)
			r, g, b := color.YCbCrToRGB(m.Y[yi], m.Cb[ci], m.Cr[ci])
			return [4]uint8{r, g, b, 255}
		}
	case *image.RGBA:
		return func(x, y int) [4]uint8 {
			i := m.PixOffset(x, y)
			return [4]uint8{m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3]}
		}
	case *image.NRGBA:
		return func(x, y int) [4]uint8 {
			i := m.PixOffset(x, y)
			a := uint32(m.Pix[i+3])
			return [4]uint8{premul(m.Pix[i], a), premul(m.Pix[i+1], a), premul(m.Pix[i+2], a), uint8(a)}
		}
	case *image.Gray:
		return func(x, y int) [4]uint8 {
			v := m.Pix[m.PixOffset(x, y)]
			return [4]uint8{v, v, v, 255}
		}
	case *image.Paletted:
		var pal [256][4]uint8
		for i, c := range m.Palette {
			r, g, b, a := c.RGBA()
			pal[i] = [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
		}
		return func(x, y int) [4]uint8 { return pal[m.Pix[m.PixOffset(x, y)]] }
	}
	return func(x, y int) [4]uint8 {
		r, g, b, a := m.At(x, y).RGBA()
		return [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
	}
}

func premul(c uint8, a uint32) uint8 { return uint8((uint32(c)*a + 127) / 255) }

// orientInto copies the sw×sh RGBA image stored into dst (stride bytes per
// row) as shown with EXIF orientation orient.
func orientInto(dst []byte, stride int, stored []byte, sw, sh, orient int) {
	ow, oh := sw, sh
	if orient >= 5 {
		ow, oh = sh, sw
	}
	for y := 0; y < oh; y++ {
		row := dst[y*stride:]
		for x := 0; x < ow; x++ {
			sx, sy := orientedToStored(x, y, sw, sh, orient)
			copy(row[x*4:x*4+4], stored[(sy*sw+sx)*4:])
		}
	}
}

// orientedToStored maps a pixel of the image as shown (EXIF orientation
// orient applied) to the stored sw×sh pixel it comes from.
func orientedToStored(x, y, sw, sh, orient int) (int, int) {
	switch orient {
	case 2: // mirrored horizontally
		return sw - 1 - x, y
	case 3: // rotated 180°
		return sw - 1 - x, sh - 1 - y
	case 4: // mirrored vertically
		return x, sh - 1 - y
	case 5: // transposed
		return y, x
	case 6: // shown rotated 90° clockwise
		return y, sh - 1 - x
	case 7: // transversed
		return sw - 1 - y, sh - 1 - x
	case 8: // shown rotated 90° counter-clockwise
		return sw - 1 - y, x
	}
	return x, y
}
