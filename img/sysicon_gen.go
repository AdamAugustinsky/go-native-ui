package img

import "image"

// iconFromImage is the result for an icon the system gave as an image (a
// shell bitmap, a theme's PNG): always side×side, m scaled to fit inside it
// with its aspect kept and centred, the rest transparent, in a pooled buffer
// with the atlas border. Windows and Linux use it; macOS draws its icons
// straight from Core Graphics.
func (w *worker) iconFromImage(c *Cache, m image.Image, side int32) result {
	b := m.Bounds()
	sw, sh := int32(b.Dx()), int32(b.Dy())
	if sw <= 0 || sh <= 0 {
		return result{buf: -1, err: errDecode}
	}
	if int64(sw)*int64(sh) > maxPixels {
		return result{buf: -1, err: errTooLarge}
	}
	tw, th := scaled(sw, sh, float64(side)/float64(max(sw, sh)))
	tw, th = min(tw, side), min(th, side)
	pad := atlasPad(side, side)
	bw := int(side + 2*pad)
	buf := c.bufs.get(bw*bw*4, &w.fr)
	if buf < 0 {
		return result{buf: -1, err: errTooLarge}
	}
	pix := c.bufs.bytes(buf, bw*bw*4)
	clear(pix) // pooled buffers are not zeroed
	ox, oy := int(pad+(side-tw)/2), int(pad+(side-th)/2)
	resample(pix[(oy*bw+ox)*4:], bw*4, int(tw), int(th), m, 1)
	if pad > 0 {
		padEdges(pix, int(side), int(side))
	}
	return result{buf: buf, w: side, h: side, pad: pad, frames: 1}
}

// shellPixels turns top-down BGRA pixels the shell gave into premultiplied
// RGBA, in place. Both of the shell's sources give straight alpha, which was
// measured, not assumed: on the Windows runners the bitmap
// IShellItemImageFactory renders for a folder has pixels with a channel
// above their alpha (65 of 16384 at 128 pixels), and so has an icon's colour
// bitmap (137 of 65536 at 256). A bitmap with no alpha anywhere is opaque,
// except where the icon's mask (1 where transparent; nil if it has none)
// says otherwise.
func shellPixels(px, mask []byte) {
	hasAlpha := false
	for i := 3; i < len(px); i += 4 {
		if px[i] != 0 {
			hasAlpha = true
			break
		}
	}
	for i := 0; i < len(px); i += 4 {
		b, g, r, a := px[i], px[i+1], px[i+2], px[i+3]
		if !hasAlpha {
			a = 255
			if mask != nil && mask[i] != 0 {
				a, r, g, b = 0, 0, 0, 0
			}
		} else {
			r, g, b = premul(r, uint32(a)), premul(g, uint32(a)), premul(b, uint32(a))
		}
		px[i], px[i+1], px[i+2], px[i+3] = r, g, b, a
	}
}
