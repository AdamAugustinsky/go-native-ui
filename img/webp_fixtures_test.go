package img

import "encoding/binary"

// webpSolid is a VP8L (lossless) bitstream of one colour: every prefix code
// has a single symbol, so the pixels take no bits at all.
func webpSolid(w, h int, r, g, b, a byte) []byte {
	var bits []bool
	put := func(v uint32, n int) {
		for i := 0; i < n; i++ {
			bits = append(bits, v>>uint(i)&1 == 1)
		}
	}
	put(uint32(w-1), 14)
	put(uint32(h-1), 14)
	put(1, 1)                                // alpha is used
	put(0, 3)                                // version
	put(0, 1)                                // no transform
	put(0, 1)                                // no colour cache
	put(0, 1)                                // no meta prefix codes
	for _, sym := range []byte{g, r, b, a} { // green+length, red, blue, alpha
		put(1, 1) // simple code
		put(0, 1) // one symbol
		put(1, 1) // 8-bit symbol
		put(uint32(sym), 8)
	}
	put(1, 1) // distance: simple code, one symbol, in 1 bit
	put(0, 1)
	put(0, 1)
	put(0, 1)
	out := []byte{0x2f}
	for i := 0; i < len(bits); i += 8 {
		var v byte
		for k := 0; k < 8 && i+k < len(bits); k++ {
			if bits[i+k] {
				v |= 1 << uint(k)
			}
		}
		out = append(out, v)
	}
	return out
}

func riffChunk(tag string, payload []byte) []byte {
	b := append([]byte(tag), 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(payload)))
	b = append(b, payload...)
	if len(payload)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

func le24(v int) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16)} }

// webpAnimation is an animated WebP of n solid frames of the sheet colours.
func webpAnimation(w, h, n, delayMs, loops int) []byte {
	vp8x := append([]byte{0x12, 0, 0, 0}, le24(w-1)...) // animation and alpha flags
	vp8x = append(vp8x, le24(h-1)...)
	body := []byte("WEBP")
	body = append(body, riffChunk("VP8X", vp8x)...)
	body = append(body, riffChunk("ANIM", []byte{0, 0, 0, 0, byte(loops), byte(loops >> 8)})...)
	for i := 0; i < n; i++ {
		c := sheetColour(i, n)
		f := append(le24(0), le24(0)...)
		f = append(f, le24(w-1)...)
		f = append(f, le24(h-1)...)
		f = append(f, le24(delayMs)...)
		f = append(f, 0) // blend, do not dispose
		f = append(f, riffChunk("VP8L", webpSolid(w, h, c[0], c[1], c[2], 255))...)
		body = append(body, riffChunk("ANMF", f)...)
	}
	out := []byte("RIFF\x00\x00\x00\x00")
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	return append(out, body...)
}
