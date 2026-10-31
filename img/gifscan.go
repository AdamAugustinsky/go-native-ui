package img

// gifTextHeader is how many bytes come before the sub-blocks of the extension
// with this label besides the label's own two: a plain text extension's
// header is read as 13 bytes (its size byte and 12) whatever its size byte
// says, as image/gif reads it, so a short one does not leave a graphic control
// inside it to be read as the next image's.
func gifTextHeader(label byte) int {
	if label == 0x01 {
		return 13
	}
	return 0
}

// gifFrames counts the frames of a GIF and returns the first frame's delay in
// milliseconds, by walking its blocks without decoding any (the standard
// decoder's DecodeAll would decode every frame).
func gifFrames(b []byte) (frames, delayMs int32) {
	if len(b) < 13 {
		return 1, 0
	}
	i := 13
	if b[10]&0x80 != 0 { // global colour table
		i += 3 << (b[10]&7 + 1)
	}
	var delay int32 = -1
	pending := int32(0) // the delay of the graphic control extension before the next image
	skipSub := func() bool {
		for i < len(b) {
			n := int(b[i])
			i++
			if n == 0 {
				return true
			}
			i += n
		}
		return false
	}
	for i < len(b) {
		switch b[i] {
		case 0x21: // extension
			if i+2 > len(b) {
				return max(frames, 1), max(delay, 0)
			}
			if b[i+1] == 0xF9 && i+8 <= len(b) && b[i+2] == 4 { // graphic control
				pending = int32(b[i+4]) | int32(b[i+5])<<8
			}
			i += 2 + gifTextHeader(b[i+1])
			if !skipSub() {
				return max(frames, 1), max(delay, 0)
			}
		case 0x2C: // image descriptor
			if i+10 > len(b) {
				return max(frames, 1), max(delay, 0)
			}
			flags := b[i+9]
			i += 10
			if flags&0x80 != 0 { // local colour table
				i += 3 << (flags&7 + 1)
			}
			i++ // LZW minimum code size
			if !skipSub() {
				return max(frames, 1), max(delay, 0)
			}
			if frames == 0 {
				delay = pending * 10
			}
			frames++
			pending = 0
		case 0x3B: // trailer
			return max(frames, 1), max(delay, 0)
		default:
			return max(frames, 1), max(delay, 0)
		}
	}
	return max(frames, 1), max(delay, 0)
}

// gifBackground returns the colour a GIF's logical screen shows where its
// first frame does not cover it: the global palette's background colour. ok
// is false (the screen is transparent) when the first frame has a
// transparent colour, without a global palette, or when b ends before the
// first frame's descriptor. Disposal does not matter: it applies only after
// the first frame has been shown.
func gifBackground(b []byte) (rgb [3]byte, ok bool) {
	if len(b) < 13 || b[10]&0x80 == 0 {
		return rgb, false
	}
	n := 2 << (b[10] & 7) // global palette entries
	i := 13 + 3*n
	if int(b[11]) >= n || i > len(b) {
		return rgb, false
	}
	copy(rgb[:], b[13+3*int(b[11]):])
	for i < len(b) {
		switch b[i] {
		case 0x21: // extension
			if i+2 > len(b) {
				return rgb, false
			}
			if b[i+1] == 0xF9 && i+4 <= len(b) && b[i+2] == 4 && b[i+3]&1 != 0 { // graphic control with a transparent colour
				return rgb, false
			}
			i += 2 + gifTextHeader(b[i+1])
			for {
				if i >= len(b) {
					return rgb, false
				}
				k := int(b[i])
				i += 1 + k
				if k == 0 {
					break
				}
			}
		case 0x2C: // the first frame
			return rgb, true
		default:
			return rgb, false
		}
	}
	return rgb, false
}

// overBackground composites the premultiplied w×h image at (x0, y0) of pix
// (stride bytes per row) over the opaque colour rgb.
func overBackground(pix []byte, stride, x0, y0, w, h int, rgb [3]byte) {
	for y := y0; y < y0+h; y++ {
		row := pix[y*stride+x0*4 : y*stride+(x0+w)*4]
		for x := 0; x < len(row); x += 4 {
			t := 255 - uint32(row[x+3])
			for k := 0; k < 3; k++ {
				row[x+k] = byte(uint32(row[x+k]) + (uint32(rgb[k])*t+127)/255)
			}
			row[x+3] = 255
		}
	}
}
