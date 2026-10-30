package img

import "encoding/binary"

// gifLZW names dictionary expansions in the already decoded indices. Entry c
// starts at offsets[c] and ends one byte after offsets[c+1]: the previous
// expansion followed by the first byte of the next one. A clear only resets
// the live range; the pointer-free table needs no clearing between frames.
type gifLZW struct {
	offsets [4097]uint32
}

// decode reads GIF sub-blocks straight into dst. Literal validation also
// validates every dictionary expansion, without another pass over the pixels.
func (d *gifLZW) decode(dst, data []byte, litWidth, colors int) error {
	if litWidth < 2 || litWidth > 8 {
		return errGIF
	}
	clear := uint32(1) << uint(litWidth)
	eoi := clear + 1
	next, width := eoi+1, uint(litWidth+1)
	mask := uint32(1)<<width - 1
	var bits uint64
	var nbits uint
	pos, end, out, last := 0, 0, 0, -1
	for {
		for nbits < width {
			if pos == end {
				if pos >= len(data) {
					return errGIF
				}
				n := int(data[pos])
				pos++
				if n == 0 {
					// image/gif accepts a missing EOI after all pixels.
					if out == len(dst) {
						return nil
					}
					return errGIF
				}
				end = pos + n
				if end > len(data) {
					return errGIF
				}
			}
			if end-pos >= 4 {
				bits |= uint64(binary.LittleEndian.Uint32(data[pos:])) << nbits
				pos += 4
				nbits += 32
			} else {
				bits |= uint64(data[pos]) << nbits
				pos++
				nbits += 8
			}
		}
		code := uint32(bits) & mask
		bits >>= width
		nbits -= width
		switch code {
		case clear:
			next, width, last = eoi+1, uint(litWidth+1), -1
			mask = uint32(1)<<width - 1
			continue
		case eoi:
			if out != len(dst) {
				return errGIF
			}
			// Discount prefetched bytes when checking image/gif's allowance
			// for unused bytes in the EOI block, or one trailing byte block.
			pos -= int(nbits / 8)
			if pos == end {
				if end < len(data) && data[end] == 1 {
					end += 2
				}
			}
			if end >= len(data) || data[end] != 0 {
				return errGIF
			}
			return nil
		}
		start := out
		if code < clear {
			if out == len(dst) || int(code) >= colors {
				return errGIF
			}
			dst[out] = byte(code)
			out++
		} else {
			if code > next || last < 0 {
				return errGIF
			}
			// The newest expansion has not appeared in the output yet.
			// It is the previous expansion followed by its own first byte.
			if code == next {
				n := out - last
				if n+1 > len(dst)-out {
					return errGIF
				}
				copy(dst[out:out+n], dst[last:out])
				dst[out+n] = dst[last]
				out += n + 1
			} else {
				off := int(d.offsets[code])
				n := int(d.offsets[code+1]) - off + 1
				if n > len(dst)-out {
					return errGIF
				}
				copy(dst[out:out+n], dst[off:off+n])
				out += n
			}
		}
		if last >= 0 && next < 4096 {
			d.offsets[next], d.offsets[next+1] = uint32(last), uint32(start)
			next++
		}
		last = start
		if next > mask && width < 12 {
			width++
			mask = uint32(1)<<width - 1
		}
	}
}
