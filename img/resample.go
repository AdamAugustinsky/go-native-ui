package img

import "image"

// resample scales the premultiplied image src, shown with EXIF orientation
// orient (1 to 8), to tw×th by area averaging, into dst (stride bytes per
// row). The decoders never enlarge, so each destination pixel averages the
// block of source pixels it covers, split at whole-pixel boundaries.
func resample(dst []byte, stride, tw, th int, src *image.RGBA, orient int) {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
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
			var r, g, b, a, n uint32
			for oy := y0; oy < y1; oy++ {
				for ox := x0; ox < x1; ox++ {
					sx, sy := orientedToStored(ox, oy, sw, sh, orient)
					p := src.Pix[sy*src.Stride+sx*4:]
					r += uint32(p[0])
					g += uint32(p[1])
					b += uint32(p[2])
					a += uint32(p[3])
					n++
				}
			}
			d := dst[dy*stride+dx*4:]
			d[0], d[1], d[2], d[3] = byte((r+n/2)/n), byte((g+n/2)/n), byte((b+n/2)/n), byte((a+n/2)/n)
		}
	}
}

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
