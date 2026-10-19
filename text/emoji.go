package text

import (
	"unicode"
	"unicode/utf8"
)

// MaxJumboEmoji is the most emoji a message may hold, with nothing else but
// white space, to be drawn large: Apple Messages and ZapFast (markup.rs,
// `only_emoji(text) <= 3`, at 2.4 times the text size) draw one to three emoji
// alone in a message large and four or more at the text size.
const MaxJumboEmoji = 3

// JumboScale is the size of a large emoji-only message relative to its text
// size (ZapFast's 2.4).
const JumboScale = 2.4

// EmojiOnly returns the number of emoji in b when b holds emoji and white
// space only, 0 when it holds anything else (or nothing). An emoji is a
// grapheme cluster (UAX #29: a ZWJ sequence, a flag, a keycap, a modified
// emoji) that is drawn as a colour emoji: one that starts with an
// Emoji_Presentation character, a regional indicator, or a pictograph or keycap
// base followed by U+FE0F or U+20E3. It allocates nothing.
func EmojiOnly(b []byte) int {
	n := 0
	for off := 0; off < len(b); {
		next := NextGrapheme(b, off)
		if next <= off {
			return 0
		}
		g := b[off:next]
		r, sz := utf8.DecodeRune(g)
		switch {
		case sz == len(g) && unicode.IsSpace(r):
		case isEmojiCluster(r, g):
			n++
		default:
			return 0
		}
		off = next
	}
	return n
}

// isEmojiCluster reports whether grapheme g, starting with r, is drawn as an
// emoji.
func isEmojiCluster(r rune, g []byte) bool {
	if isEmojiPresentation(r) {
		return true
	}
	if !extPict(r) && !(r >= '0' && r <= '9') && r != '#' && r != '*' {
		return false
	}
	for i := 0; i < len(g); {
		c, n := utf8.DecodeRune(g[i:])
		if c == 0xFE0F || c == 0x20E3 {
			return true
		}
		i += n
	}
	return false
}

// isEmojiPresentation reports whether r is displayed as a colour emoji by
// default (Unicode Emoji_Presentation=Yes, approximately: the supplementary
// ranges are taken whole, the BMP ones listed). It decides when the colour
// emoji font is preferred over a monochrome glyph of the primary font.
func isEmojiPresentation(r rune) bool {
	if r < 0x231A {
		return false
	}
	switch {
	case r >= 0x1F300 && r <= 0x1F64F, r >= 0x1F680 && r <= 0x1F6FF,
		r >= 0x1F900 && r <= 0x1F9FF, r >= 0x1FA70 && r <= 0x1FAFF,
		r >= 0x1F1E6 && r <= 0x1F1FF:
		return true
	case r > 0x2B55:
		return false
	}
	switch r {
	case 0x231A, 0x231B, 0x23E9, 0x23EA, 0x23EB, 0x23EC, 0x23F0, 0x23F3, 0x25FD, 0x25FE,
		0x2614, 0x2615, 0x267F, 0x2693, 0x26A1, 0x26AA, 0x26AB, 0x26BD, 0x26BE, 0x26C4, 0x26C5,
		0x26CE, 0x26D4, 0x26EA, 0x26F2, 0x26F3, 0x26F5, 0x26FA, 0x26FD, 0x2705, 0x270A, 0x270B,
		0x2728, 0x274C, 0x274E, 0x2753, 0x2754, 0x2755, 0x2757, 0x2795, 0x2796, 0x2797, 0x27B0,
		0x27BF, 0x2B1B, 0x2B1C, 0x2B50, 0x2B55:
		return true
	}
	return r >= 0x2648 && r <= 0x2653
}
