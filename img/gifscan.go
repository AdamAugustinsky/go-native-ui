package img

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
			i += 2
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
