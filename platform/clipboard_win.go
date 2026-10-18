package platform

import "unicode/utf8"

// The text conversions of the Windows clipboard (CF_UNICODETEXT: UTF-16, NUL
// terminated, \r\n line ends). They have no build tag so that they are tested
// on every OS.

func putU16(dst []uint16, n int, u uint16) int {
	if dst != nil {
		dst[n] = u
	}
	return n + 1
}

// utf16Encode writes b (UTF-8, \n line ends) to dst as UTF-16 with \r\n line
// ends (a \r\n already in b stays one) and returns the number of units. Each
// run of invalid bytes becomes one U+FFFD. With a nil dst it only counts; a
// non-nil dst must be long enough for that count.
func utf16Encode(dst []uint16, b []byte) int {
	n, bad := 0, false
	for i := 0; i < len(b); {
		r, size := rune(b[i]), 1
		if r >= utf8.RuneSelf {
			r, size = utf8.DecodeRune(b[i:])
		}
		i += size
		switch {
		case r == utf8.RuneError && size == 1:
			if bad {
				continue
			}
			bad = true
		case r == '\r' && i < len(b) && b[i] == '\n':
			bad = false
			continue // the \n writes the pair
		case r == '\n':
			bad = false
			n = putU16(dst, n, '\r')
			n = putU16(dst, n, '\n')
			continue
		default:
			bad = false
		}
		switch {
		case r >= 0x10000:
			r -= 0x10000
			n = putU16(dst, n, uint16(0xD800+r>>10))
			n = putU16(dst, n, uint16(0xDC00+r&0x3FF))
		default:
			n = putU16(dst, n, uint16(r))
		}
	}
	return n
}

// utf16Decode appends the text in u (UTF-16 up to the first NUL, \r\n line
// ends read as \n) to dst as UTF-8. An unpaired surrogate becomes U+FFFD. It
// reports false, with dst as it was, when the text is longer than max bytes.
func utf16Decode(dst []byte, u []uint16, max int) ([]byte, bool) {
	start := len(dst)
	for i := 0; i < len(u); i++ {
		c := rune(u[i])
		switch {
		case c == 0:
			return dst, true
		case c == '\r' && i+1 < len(u) && u[i+1] == '\n':
			continue // the \n follows
		case c >= 0xD800 && c < 0xDC00:
			if i+1 < len(u) && u[i+1] >= 0xDC00 && u[i+1] < 0xE000 {
				c = 0x10000 + (c-0xD800)<<10 + rune(u[i+1]) - 0xDC00
				i++
			} else {
				c = utf8.RuneError
			}
		case c >= 0xDC00 && c < 0xE000:
			c = utf8.RuneError
		}
		dst = utf8.AppendRune(dst, c)
		if len(dst)-start > max {
			return dst[:start], false
		}
	}
	return dst, true
}
