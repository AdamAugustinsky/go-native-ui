package text

import "unicode/utf8"

// Grapheme cluster segmentation (UAX #29, Unicode 17) over UTF-8 bytes,
// without allocation. Random access looks behind a position for emoji ZWJ
// sequences, flag pairs and Indic conjuncts. Sequential passes use
// GraphemeIterator, which carries that context forward. The tables are
// unidata.go.
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

// runeBoundary reports whether 0 < off < len(b) is where a rune of b starts,
// counting each invalid byte (a stray continuation byte, a truncated or
// overlong sequence, an encoded surrogate) as a rune of its own, which is how
// DecodeRune walks the text and how the shapers see it. Only a continuation
// byte that belongs to a valid sequence starting up to three bytes before it
// is not a boundary.
func runeBoundary(b []byte, off int) bool {
	if utf8.RuneStart(b[off]) {
		return true
	}
	for k := 1; k <= 3 && off-k >= 0; k++ {
		s := off - k
		if !utf8.RuneStart(b[s]) {
			continue
		}
		_, n := utf8.DecodeRune(b[s:])
		return n <= k // a valid sequence reaching past off contains it
	}
	return true
}

// IsGraphemeBoundary reports whether off is a grapheme cluster boundary of b:
// 0 and len(b) always are; an offset inside a UTF-8 sequence never is.
func IsGraphemeBoundary(b []byte, off int) bool {
	if off <= 0 || off >= len(b) {
		return off == 0 || off == len(b)
	}
	if b[off-1] < 0x80 && b[off] < 0x80 {
		return b[off-1] != '\r' || b[off] != '\n'
	}
	if !runeBoundary(b, off) {
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

// GraphemeIterator walks grapheme boundaries in linear time without retaining
// the text. The zero value starts at byte 0. Call Next with the same bytes
// until it returns len(b); reset the iterator to walk different text.
type GraphemeIterator struct {
	off       int
	prev      uint16
	riOdd     bool
	pict      bool // Extended_Pictographic Extend*
	zwjPict   bool // that sequence followed by ZWJ
	consonant bool // Consonant [Extend Linker]*
	linker    bool // a Linker occurred after that consonant
}

// Next returns the next grapheme boundary, or len(b) at the end.
func (it *GraphemeIterator) Next(b []byte) int {
	first := true
	for it.off < len(b) {
		if !first && b[it.off] < utf8.RuneSelf && it.prev != gbPrepend && !(it.prev == gbCR && b[it.off] == '\n') {
			return it.off
		}
		r, n := utf8.DecodeRune(b[it.off:])
		var g uint16
		var ic uint8
		var pict bool
		if r < utf8.RuneSelf {
			switch {
			case r == '\r':
				g = gbCR
			case r == '\n':
				g = gbLF
			case r < 0x20 || r == 0x7f:
				g = gbControl
			}
		} else {
			g, ic, pict = gbClass(r), icbClass(r), extPict(r)
		}
		const ctl = gbControl | gbCR | gbLF
		join := false
		switch {
		case it.prev == gbCR && g == gbLF:
			join = true
		case it.prev&ctl != 0 || g&ctl != 0:
		case it.prev == gbL && g&(gbL|gbV|gbLV|gbLVT) != 0:
			join = true
		case it.prev&(gbLV|gbV) != 0 && g&(gbV|gbT) != 0:
			join = true
		case it.prev&(gbLVT|gbT) != 0 && g == gbT:
			join = true
		case g&(gbExtend|gbZWJ|gbSpacingMark) != 0 || it.prev == gbPrepend:
			join = true
		case ic == icbConsonant && it.consonant && it.linker:
			join = true
		case it.prev == gbZWJ && pict && it.zwjPict:
			join = true
		case it.prev == gbRegionalIndicator && g == gbRegionalIndicator && it.riOdd:
			join = true
		}
		if !first && !join {
			return it.off
		}
		first = false
		it.riOdd = g == gbRegionalIndicator && !it.riOdd
		it.zwjPict = g == gbZWJ && it.pict
		it.pict = pict || g == gbExtend && it.pict
		switch ic {
		case icbConsonant:
			it.consonant, it.linker = true, false
		case icbLinker:
			it.linker = it.consonant
		case icbExtend:
		default:
			it.consonant, it.linker = false, false
		}
		it.prev, it.off = g, it.off+n
	}
	return len(b)
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
