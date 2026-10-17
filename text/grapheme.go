package text

import "unicode/utf8"

// Grapheme cluster segmentation (UAX #29, Unicode 17) over UTF-8 bytes,
// without allocation. The rules are applied statelessly at a position (the
// two runes around it, plus a bounded look-behind for emoji ZWJ sequences,
// flag pairs and Indic conjuncts), so any offset can be asked about without
// scanning the text from its start. The tables are unidata.go.
//
// Invalid UTF-8 bytes are one-rune clusters of their own, the same units the
// shapers treat as U+FFFD.

// Grapheme_Cluster_Break values as bit flags (the order of the table).
const (
	gbCR uint16 = 1 << iota
	gbControl
	gbExtend
	gbL
	gbLF
	gbLV
	gbLVT
	gbPrepend
	gbRegionalIndicator
	gbSpacingMark
	gbT
	gbV
	gbZWJ
)

// Indic_Conjunct_Break values.
const (
	icbLinker uint8 = 1 << iota
	icbConsonant
	icbExtend
)

func gbClass(r rune) uint16 {
	i := gbLookup(r)
	if i == 0 {
		return 0
	}
	return 1 << (i - 1)
}

func icbClass(r rune) uint8 {
	i := indicCBLookup(r)
	if i == 0 {
		return 0
	}
	return 1 << (i - 1)
}

func extPict(r rune) bool { return emojiLookup(r) == 1 }

// IsGraphemeBoundary reports whether off is a grapheme cluster boundary of b:
// 0 and len(b) always are; an offset inside a UTF-8 sequence never is.
func IsGraphemeBoundary(b []byte, off int) bool {
	if off <= 0 || off >= len(b) {
		return off == 0 || off == len(b)
	}
	if !utf8.RuneStart(b[off]) {
		return false
	}
	r0, n0 := utf8.DecodeLastRune(b[:off])
	r1, _ := utf8.DecodeRune(b[off:])
	g0, g1 := gbClass(r0), gbClass(r1)
	const ctl = gbControl | gbCR | gbLF
	switch {
	case r0 == '\r' && r1 == '\n': // GB3
		return false
	case g0&ctl != 0 || g1&ctl != 0: // GB4, GB5
		return true
	case g0 == gbL && g1&(gbL|gbV|gbLV|gbLVT) != 0: // GB6
		return false
	case g0&(gbLV|gbV) != 0 && g1&(gbV|gbT) != 0: // GB7
		return false
	case g0&(gbLVT|gbT) != 0 && g1 == gbT: // GB8
		return false
	case g1&(gbExtend|gbZWJ) != 0: // GB9
		return false
	case g1 == gbSpacingMark: // GB9a
		return false
	case g0 == gbPrepend: // GB9b
		return false
	}
	if icbClass(r1) == icbConsonant && conjunctBefore(b[:off]) { // GB9c
		return false
	}
	if g0 == gbZWJ && extPict(r1) && pictBeforeZWJ(b[:off-n0]) { // GB11
		return false
	}
	if g0 == gbRegionalIndicator && g1 == gbRegionalIndicator && oddRegionalIndicators(b[:off]) { // GB12, GB13
		return false
	}
	return true // GB999
}

// conjunctBefore reports whether b ends with Consonant [Extend Linker]*
// Linker [Extend Linker]*, the left side of rule GB9c.
func conjunctBefore(b []byte) bool {
	linker := false
	for len(b) > 0 {
		r, n := utf8.DecodeLastRune(b)
		b = b[:len(b)-n]
		switch icbClass(r) {
		case icbLinker:
			linker = true
		case icbExtend:
		case icbConsonant:
			return linker
		default:
			return false
		}
	}
	return false
}

// pictBeforeZWJ reports whether b (the text before a ZWJ) ends with
// Extended_Pictographic Extend*, the left side of rule GB11.
func pictBeforeZWJ(b []byte) bool {
	for len(b) > 0 {
		r, n := utf8.DecodeLastRune(b)
		b = b[:len(b)-n]
		if extPict(r) {
			return true
		}
		if gbClass(r)&gbExtend == 0 {
			return false
		}
	}
	return false
}

// oddRegionalIndicators reports whether b ends with an odd number of regional
// indicators (so that the next one pairs with the last).
func oddRegionalIndicators(b []byte) bool {
	n := 0
	for len(b) > 0 {
		r, sz := utf8.DecodeLastRune(b)
		if gbClass(r) != gbRegionalIndicator {
			break
		}
		b = b[:len(b)-sz]
		n++
	}
	return n&1 == 1
}

// NextGrapheme returns the first grapheme cluster boundary of b after off,
// or len(b) when there is none.
func NextGrapheme(b []byte, off int) int {
	if off < 0 {
		off = 0
	}
	for off < len(b) {
		off++
		if IsGraphemeBoundary(b, off) {
			return off
		}
	}
	return len(b)
}

// PrevGrapheme returns the last grapheme cluster boundary of b before off,
// or 0 when there is none.
func PrevGrapheme(b []byte, off int) int {
	if off > len(b) {
		off = len(b)
	}
	for off > 0 {
		off--
		if IsGraphemeBoundary(b, off) {
			return off
		}
	}
	return 0
}

// GraphemeStart returns the start of the grapheme cluster that contains off:
// the last boundary at or before it (len(b) for off >= len(b)).
func GraphemeStart(b []byte, off int) int {
	if off >= len(b) {
		return len(b)
	}
	if off < 0 {
		return 0
	}
	for off > 0 && !IsGraphemeBoundary(b, off) {
		off--
	}
	return off
}
