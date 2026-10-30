package img

import (
	"image"
	"image/color"
)

// resample scales src, shown with EXIF orientation orient (1 to 8), to
// tw×th premultiplied RGBA by area averaging, into dst (stride bytes per
// row): each destination pixel is the mean of the source area it covers,
// every source pixel weighted by the part of it inside (a fractional
// reduction splits a source pixel between two destination pixels). Every
// source pixel is read straight from the decoder's image (YCbCr, RGBA, NRGBA,
// paletted and grey without a conversion pass).
func resample(dst []byte, stride, tw, th int, src image.Image, orient int) {
	if rgba, ok := src.(*image.RGBA); ok {
		resampleRGBA(dst, stride, tw, th, rgba, orient)
		return
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	fetch := fetcher(src)
	ow, oh := sw, sh
	if orient >= 5 {
		ow, oh = sh, sw
	}
	// In x, a source pixel is tw units wide and a destination pixel ow, so
	// the weights of one destination pixel add up to ow (to oh in y), and
	// a sum of 8-bit samples is at most 255·ow·oh: 64 bits hold it for any
	// image.
	total := uint64(ow) * uint64(oh)
	for dy := 0; dy < th; dy++ {
		ya, yb := dy*oh, (dy+1)*oh
		y0, y1 := ya/th, (yb+th-1)/th
		for dx := 0; dx < tw; dx++ {
			xa, xb := dx*ow, (dx+1)*ow
			x0, x1 := xa/tw, (xb+tw-1)/tw
			var r, g, bl, a uint64
			for oy := y0; oy < y1; oy++ {
				wy := uint64(min((oy+1)*th, yb) - max(oy*th, ya))
				for ox := x0; ox < x1; ox++ {
					wt := wy * uint64(min((ox+1)*tw, xb)-max(ox*tw, xa))
					sx, sy := orientedToStored(ox, oy, sw, sh, orient)
					p := fetch(b.Min.X+sx, b.Min.Y+sy)
					r += uint64(p[0]) * wt
					g += uint64(p[1]) * wt
					bl += uint64(p[2]) * wt
					a += uint64(p[3]) * wt
				}
			}
			d := dst[dy*stride+dx*4:]
			d[0], d[1], d[2], d[3] = byte((r+total/2)/total), byte((g+total/2)/total), byte((bl+total/2)/total), byte((a+total/2)/total)
		}
	}
}

// resampleRGBA hoists orientation into a starting offset and pixel steps.
func resampleRGBA(dst []byte, stride, tw, th int, src *image.RGBA, orient int) {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	ow, oh := sw, sh
	base, xs, ys := 0, 4, src.Stride
	switch orient {
	case 2:
		base, xs = (sw-1)*4, -4
	case 3:
		base, xs, ys = (sh-1)*src.Stride+(sw-1)*4, -4, -src.Stride
	case 4:
		base, ys = (sh-1)*src.Stride, -src.Stride
	case 5:
		xs, ys = src.Stride, 4
	case 6:
		base, xs, ys = (sh-1)*src.Stride, -src.Stride, 4
	case 7:
		base, xs, ys = (sh-1)*src.Stride+(sw-1)*4, -src.Stride, -4
	case 8:
		base, xs, ys = (sw-1)*4, src.Stride, -4
	}
	if orient >= 5 {
		ow, oh = sh, sw
	}
	if ow%tw == 0 && oh%th == 0 {
		rx, ry := ow/tw, oh/th
		if rx == 2 && ry == 2 {
			for y := 0; y < th; y++ {
				d := dst[y*stride:][:tw*4]
				p := base + 2*y*ys
				for x := 0; x < tw; x++ {
					a, b := src.Pix[p:][:4], src.Pix[p+xs:][:4]
					c, e := src.Pix[p+ys:][:4], src.Pix[p+ys+xs:][:4]
					for k := 0; k < 4; k++ {
						d[x*4+k] = byte((uint16(a[k]) + uint16(b[k]) + uint16(c[k]) + uint16(e[k]) + 2) >> 2)
					}
					p += 2 * xs
				}
			}
			return
		}
		total := uint64(rx) * uint64(ry)
		for dy := 0; dy < th; dy++ {
			row := dst[dy*stride:][:tw*4]
			for dx := 0; dx < tw; dx++ {
				var r, g, b, a uint64
				start := base + dy*ry*ys + dx*rx*xs
				for y := 0; y < ry; y++ {
					p := start + y*ys
					for x := 0; x < rx; x++ {
						s := src.Pix[p:][:4]
						r += uint64(s[0])
						g += uint64(s[1])
						b += uint64(s[2])
						a += uint64(s[3])
						p += xs
					}
				}
				d := row[dx*4:][:4]
				d[0], d[1], d[2], d[3] = byte((r+total/2)/total), byte((g+total/2)/total), byte((b+total/2)/total), byte((a+total/2)/total)
			}
		}
		return
	}
	total := uint64(ow) * uint64(oh)
	for dy := 0; dy < th; dy++ {
		ya, yb := dy*oh, (dy+1)*oh
		y0, y1 := ya/th, (yb+th-1)/th
		for dx := 0; dx < tw; dx++ {
			xa, xb := dx*ow, (dx+1)*ow
			x0, x1 := xa/tw, (xb+tw-1)/tw
			var r, g, b, a uint64
			for y := y0; y < y1; y++ {
				wy := uint64(min((y+1)*th, yb) - max(y*th, ya))
				p := base + y*ys + x0*xs
				for x := x0; x < x1; x++ {
					wt := wy * uint64(min((x+1)*tw, xb)-max(x*tw, xa))
					s := src.Pix[p:][:4]
					r += uint64(s[0]) * wt
					g += uint64(s[1]) * wt
					b += uint64(s[2]) * wt
					a += uint64(s[3]) * wt
					p += xs
				}
			}
			d := dst[dy*stride+dx*4:][:4]
			d[0], d[1], d[2], d[3] = byte((r+total/2)/total), byte((g+total/2)/total), byte((b+total/2)/total), byte((a+total/2)/total)
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
