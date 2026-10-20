package img

import "image"

// iconFromImage is the result for an icon the system gave as an image (a
// shell bitmap, a theme's PNG): m scaled to fit inside side×side, keeping its
// aspect, in a pooled buffer with the atlas border. Windows and Linux use
// it; macOS draws its icons straight from Core Graphics.
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
	pad := atlasPad(tw, th)
	bw, bh := int(tw+2*pad), int(th+2*pad)
	buf := c.bufs.get(bw*bh*4, &w.fr)
	if buf < 0 {
		return result{buf: -1, err: errTooLarge}
	}
	pix := c.bufs.bytes(buf, bw*bh*4)
	resample(pix[(int(pad)*bw+int(pad))*4:], bw*4, int(tw), int(th), m, 1)
	if pad > 0 {
		padEdges(pix, int(tw), int(th))
	}
	return result{buf: buf, w: tw, h: th, pad: pad, frames: 1}
}

// shellPixels turns top-down BGRA pixels the shell gave into premultiplied
// RGBA, in place. The shell's bitmaps carry premultiplied alpha and an
// icon's straight alpha; a pixel with a channel above its alpha cannot be
// premultiplied, so one such pixel says the image is straight. Pixels with
// no alpha channel anywhere are opaque, except where the icon's mask (1
// where transparent; nil if it has none) says otherwise.
func shellPixels(px, mask []byte) {
	hasAlpha, straight := false, false
	for i := 0; i < len(px); i += 4 {
		a := px[i+3]
		hasAlpha = hasAlpha || a != 0
		straight = straight || px[i] > a || px[i+1] > a || px[i+2] > a
	}
	for i := 0; i < len(px); i += 4 {
		b, g, r, a := px[i], px[i+1], px[i+2], px[i+3]
		switch {
		case !hasAlpha:
			a = 255
			if mask != nil && mask[i] != 0 {
				a, r, g, b = 0, 0, 0, 0
			}
		case straight:
			r, g, b = premul(r, uint32(a)), premul(g, uint32(a)), premul(b, uint32(a))
		}
		px[i], px[i+1], px[i+2], px[i+3] = r, g, b, a
	}
}
