package fs

import (
	"errors"
	"math"
	"unicode/utf8"
)

// Name encoding and arena bounds shared by the Windows and Linux backends.
// They are OS-independent pure functions so they are tested on every OS.
//
// Windows file names are sequences of UTF-16 code units that may contain
// unpaired surrogates (NTFS does not check). Listing replaces nothing: the
// names arena holds WTF-8, "generalized UTF-8" in which a surrogate code
// point is the ordinary 3-byte form (ED A0..BF xx). Valid pairs are the usual
// 4-byte sequences, so names without unpaired surrogates are plain UTF-8.
// appendUTF16 reverses the encoding, so a path built from a listed name
// reaches the same file. Consumers that render names (the text layer) see
// the unpaired surrogates as invalid UTF-8 and show U+FFFD for them.

// appendWTF8 appends the UTF-16 text w as WTF-8. It does not allocate beyond
// growing dst.
func appendWTF8(dst []byte, w []uint16) []byte {
	for i := 0; i < len(w); i++ {
		c := w[i]
		switch {
		case c < 0x80:
			dst = append(dst, byte(c))
		case c < 0x800:
			dst = append(dst, 0xC0|byte(c>>6), 0x80|byte(c&0x3F))
		case c >= 0xD800 && c < 0xDC00 && i+1 < len(w) && w[i+1] >= 0xDC00 && w[i+1] < 0xE000:
			r := (rune(c)-0xD800)<<10 | (rune(w[i+1]) - 0xDC00) + 0x10000
			i++
			dst = append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12&0x3F), 0x80|byte(r>>6&0x3F), 0x80|byte(r&0x3F))
		default: // BMP, including an unpaired surrogate as its 3-byte form
			dst = append(dst, 0xE0|byte(c>>12), 0x80|byte(c>>6&0x3F), 0x80|byte(c&0x3F))
		}
	}
	return dst
}

// appendUTF16 appends s, UTF-8 or WTF-8, as UTF-16 (no terminator); other
// invalid bytes become U+FFFD. ok is false when s contains a NUL, which no
// Windows path can.
func appendUTF16(dst []uint16, s string) (out []uint16, ok bool) {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c < utf8.RuneSelf:
			if c == 0 {
				return dst, false
			}
			dst = append(dst, uint16(c))
			i++
		case c == 0xED && i+2 < len(s) && s[i+1]&0xE0 == 0xA0 && s[i+2]&0xC0 == 0x80:
			// A surrogate code point (D800..DFFF) in its 3-byte form.
			dst = append(dst, 0xD000|uint16(s[i+1]&0x3F)<<6|uint16(s[i+2]&0x3F))
			i += 3
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			i += n
			if r >= 0x10000 {
				r -= 0x10000
				dst = append(dst, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
			} else {
				dst = append(dst, uint16(r)) // invalid UTF-8 became U+FFFD
			}
		}
	}
	return dst, true
}

var (
	errNameTooLong = errors.New("fs: a name is longer than 65535 bytes")
	errArenaFull   = errors.New("fs: the names of the listing exceed 4 GiB")
)

// checkName reports whether a name of n bytes can be appended to a names
// arena that holds arena bytes: Entry.NameOff and NameOff+NameLen are
// uint32 (the sum is computed in uint32 by Listing.Name) and NameLen is
// uint16.
func checkName(arena, n int) error {
	if n > math.MaxUint16 {
		return errNameTooLong
	}
	if uint64(arena)+uint64(n) > math.MaxUint32 {
		return errArenaFull
	}
	return nil
}
