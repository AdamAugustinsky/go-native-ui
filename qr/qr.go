// Package qr is a QR Code encoder written from ISO/IEC 18004 (QR Code model 2),
// and a helper that draws the symbol with ui boxes.
//
// It encodes the numeric, alphanumeric and byte modes (the smallest that holds
// the whole payload), in versions 1 to 40 (21×21 to 177×177 modules) at the
// four error-correction levels, chooses the version, picks the mask by the
// standard's four penalty rules, and writes the format and version
// information.
//
//	var m qr.Code
//	if err := qr.Encode(&m, []byte("https://wa.me/…"), qr.LevelM); err != nil { … }
//	for y := 0; y < m.Size(); y++ { for x := 0; x < m.Size(); x++ { m.Dark(x, y) … } }
//
// # Mask selection
//
// ISO/IEC 18004 §7.8.3 scores each of the eight masks by the four penalty rules
// and takes the lowest score, but leaves two points to the implementer, and no
// reader depends on them (any mask decodes). We score the finished symbol, with
// the format and version information drawn, and count a 1:1:3:1:1 pattern once
// per occurrence when it has four light modules before or after it (the edge
// of the symbol counts as light). That is the reading of ZXing, Nayuki's
// QR Code generator and python-qrcode; the second edition of the standard can
// be read as scoring before the function information is drawn, as segno does.
// testdata/gen.py makes the reference follow ours on these two points, so the
// reference vectors check the arithmetic of the rules and the encoding, not
// which reading is normative.
//
// # Memory
//
// A Code holds all its storage (about 15 KB: the modules, the function-module
// map and the codeword buffers), so Encode allocates nothing; a Code may be
// reused for the next payload, and a zero Code is empty. Draw builds boxes the
// way any widget does: nothing allocates once the context's box cache has
// grown to hold them.
//
// # Not covered
//
// Kanji and ECI modes, structured append, FNC1, Micro QR and mixed-mode
// segmentation are not implemented: the payload is one segment in one mode.
// Byte mode carries the bytes as given; readers take them as UTF-8 or Latin-1.
package qr

import (
	"errors"
	mbits "math/bits"
)

// Level is an error-correction level: how much of the symbol can be damaged
// and still be read.
type Level uint8

// The error-correction levels, with the codeword fraction they can restore
// (ISO/IEC 18004 Table 12): about 7 %, 15 %, 25 % and 30 %.
const (
	// LevelL restores about 7 % of the codewords.
	LevelL Level = iota
	// LevelM restores about 15 %.
	LevelM
	// LevelQ restores about 25 %.
	LevelQ
	// LevelH restores about 30 %.
	LevelH
)

// String returns "L", "M", "Q" or "H".
func (l Level) String() string {
	if l > LevelH {
		return "?"
	}
	return "LMQH"[l : l+1]
}

// Errors returned by Encode.
var (
	// ErrTooLong is returned when the payload does not fit version 40 at the
	// requested level (2953 bytes at L, 1273 at H).
	ErrTooLong = errors.New("qr: payload too long for any version at this level")
	// ErrLevel is returned for a Level other than the four constants.
	ErrLevel = errors.New("qr: invalid error-correction level")
)

const (
	minVersion = 1
	maxVersion = 40
	maxSize    = maxVersion*4 + 17 // 177
	maxModules = maxSize * maxSize
	words      = (maxModules + 63) / 64

	maxCodewords     = 3706 // data plus error correction, version 40
	maxDataCodewords = 2956 // version 40-L
)

// Code is an encoded QR symbol. It owns its storage; see the package comment.
type Code struct {
	size, version, mask int
	level               Level
	mod                 [words]uint64 // dark modules, row-major
	fn                  [words]uint64 // function modules (not data, not masked)
	data                [maxDataCodewords]uint8
	final               [maxCodewords]uint8
}

// Size is the number of modules along a side (21 to 177), 0 for a Code that
// holds nothing. It excludes the quiet zone.
func (c *Code) Size() int { return c.size }

// Version is the symbol version, 1 to 40, 0 for an empty Code.
func (c *Code) Version() int { return c.version }

// Level is the error-correction level the symbol was encoded at.
func (c *Code) Level() Level { return c.level }

// Mask is the mask pattern, 0 to 7, the encoder chose.
func (c *Code) Mask() int { return c.mask }

// Dark reports whether the module at column x and row y (0,0 the top left
// corner of the symbol) is dark. Positions outside the symbol, the quiet zone,
// are light.
func (c *Code) Dark(x, y int) bool {
	if uint(x) >= uint(c.size) || uint(y) >= uint(c.size) {
		return false
	}
	return c.get(x, y)
}

func (c *Code) get(x, y int) bool {
	i := y*c.size + x
	return c.mod[i>>6]>>(i&63)&1 != 0
}

func (c *Code) set(x, y int, dark bool) {
	i := y*c.size + x
	if dark {
		c.mod[i>>6] |= 1 << (i & 63)
	} else {
		c.mod[i>>6] &^= 1 << (i & 63)
	}
}

func (c *Code) isFn(x, y int) bool {
	i := y*c.size + x
	return c.fn[i>>6]>>(i&63)&1 != 0
}

// setFn sets a function module: its colour, and that masking and data
// placement leave it alone.
func (c *Code) setFn(x, y int, dark bool) {
	c.set(x, y, dark)
	i := y*c.size + x
	c.fn[i>>6] |= 1 << (i & 63)
}

// Encode encodes data at the given error-correction level into c, in the
// smallest version that holds it. The mode is numeric if data is all ASCII
// digits, alphanumeric if it is all of 0-9 A-Z space $ % * + - . / :, and byte
// mode otherwise. An empty payload is a valid, byte-mode symbol. On error c is
// left empty.
func Encode(c *Code, data []byte, level Level) error {
	return c.encode(data, level, -1)
}

// Modes of a segment.
const (
	modeNumeric = 1
	modeAlnum   = 2
	modeByte    = 4
)

func (c *Code) encode(data []byte, level Level, forceMask int) error {
	*c = Code{}
	if level > LevelH {
		return ErrLevel
	}
	mode := segmentMode(data)
	v, ok := pickVersion(len(data), mode, level)
	if !ok {
		return ErrTooLong
	}
	c.version, c.level, c.size = v, level, v*4+17
	c.writeData(data, mode)
	c.addEcc()
	c.drawFunctions()
	c.placeCodewords()
	c.chooseMask(forceMask)
	return nil
}

// segmentMode is the most compact mode data can be written in.
func segmentMode(data []byte) int {
	if len(data) == 0 {
		return modeByte
	}
	mode := modeNumeric
	for _, b := range data {
		switch {
		case b >= '0' && b <= '9':
		case alnumValue(b) >= 0:
			mode = max(mode, modeAlnum)
		default:
			return modeByte
		}
	}
	if mode == modeNumeric {
		return modeNumeric
	}
	return modeAlnum
}

const alnumChars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

// alnumValue is the value of b in the alphanumeric table, -1 if absent.
func alnumValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'A' && b <= 'Z':
		return int(b-'A') + 10
	}
	switch b {
	case ' ':
		return 36
	case '$':
		return 37
	case '%':
		return 38
	case '*':
		return 39
	case '+':
		return 40
	case '-':
		return 41
	case '.':
		return 42
	case '/':
		return 43
	case ':':
		return 44
	}
	return -1
}

// countBits is the width of the character count indicator (Table 3).
func countBits(mode, v int) int {
	k := 0
	switch {
	case v >= 27:
		k = 2
	case v >= 10:
		k = 1
	}
	switch mode {
	case modeNumeric:
		return [3]int{10, 12, 14}[k]
	case modeAlnum:
		return [3]int{9, 11, 13}[k]
	}
	return [3]int{8, 16, 16}[k]
}

// dataBits is the number of bits n characters take in a mode.
func dataBits(mode, n int) int {
	switch mode {
	case modeNumeric:
		return n/3*10 + [3]int{0, 4, 7}[n%3]
	case modeAlnum:
		return n/2*11 + n%2*6
	}
	return n * 8
}

// pickVersion is the smallest version whose capacity at level holds n
// characters of a mode.
func pickVersion(n, mode int, level Level) (int, bool) {
	for v := minVersion; v <= maxVersion; v++ {
		cb := countBits(mode, v)
		if n >= 1<<cb { // the count does not fit its field
			continue
		}
		if 4+cb+dataBits(mode, n) <= numDataCodewords(v, level)*8 {
			return v, true
		}
	}
	return 0, false
}

// bitWriter appends bits, most significant first, to a codeword buffer.
type bitWriter struct {
	buf []uint8
	n   int // bits written
}

func (w *bitWriter) put(val uint32, nbits int) {
	for i := nbits - 1; i >= 0; i-- {
		w.buf[w.n>>3] |= uint8(val>>i&1) << (7 - w.n&7)
		w.n++
	}
}

// writeData fills c.data with the data codewords: mode indicator, count,
// payload, terminator, padding to a byte and the alternating pad codewords
// 0xEC, 0x11 (§7.4).
func (c *Code) writeData(data []byte, mode int) {
	nd := numDataCodewords(c.version, c.level)
	w := bitWriter{buf: c.data[:nd]}
	w.put(uint32(mode), 4)
	w.put(uint32(len(data)), countBits(mode, c.version))
	switch mode {
	case modeNumeric:
		for i := 0; i < len(data); i += 3 {
			n := min(3, len(data)-i)
			var v uint32
			for _, d := range data[i : i+n] {
				v = v*10 + uint32(d-'0')
			}
			w.put(v, [4]int{0, 4, 7, 10}[n])
		}
	case modeAlnum:
		for i := 0; i < len(data); i += 2 {
			if i+1 < len(data) {
				w.put(uint32(alnumValue(data[i])*45+alnumValue(data[i+1])), 11)
			} else {
				w.put(uint32(alnumValue(data[i])), 6)
			}
		}
	default:
		for _, b := range data {
			w.put(uint32(b), 8)
		}
	}
	w.put(0, min(4, nd*8-w.n)) // terminator
	w.n = (w.n + 7) &^ 7       // pad to a codeword boundary
	for pad := uint8(0xEC); w.n < nd*8; pad ^= 0xEC ^ 0x11 {
		w.put(uint32(pad), 8)
	}
}

// addEcc splits the data codewords into blocks, appends each block's
// Reed–Solomon codewords and interleaves them into c.final (§7.6).
func (c *Code) addEcc() {
	li := levelIndex(c.level)
	nb := int(numBlocks[li][c.version-1])
	ecc := int(eccPerBlock[li][c.version-1])
	total := numCodewords(c.version)
	shortBlocks := nb - total%nb // blocks one data codeword shorter than the rest
	shortLen := total/nb - ecc   // data codewords of a short block

	// Error-correction codewords are written straight into their interleaved
	// places: block j's i-th goes to dataTotal + i*nb + j.
	dataTotal := total - ecc*nb
	k := 0 // start of the block's data in c.data
	for j := 0; j < nb; j++ {
		n := shortLen
		if j >= shortBlocks {
			n++
		}
		blk := c.data[k : k+n]
		var e [maxECC]uint8
		rsRemainder(e[:], blk, ecc)
		for i, b := range blk {
			p := i*nb + j
			if i >= shortLen { // only the long blocks reach this column
				p = shortLen*nb + (j - shortBlocks)
			}
			c.final[p] = b
		}
		for i := 0; i < ecc; i++ {
			c.final[dataTotal+i*nb+j] = e[i]
		}
		k += n
	}
}

// drawFunctions draws the function patterns: finders with their separators,
// timing, alignment patterns, the dark module, and reserves the format and
// version information areas (written with their final values by drawFormat and
// here).
func (c *Code) drawFunctions() {
	n := c.size
	for i := 0; i < n; i++ { // timing patterns
		c.setFn(6, i, i%2 == 0)
		c.setFn(i, 6, i%2 == 0)
	}
	c.finder(3, 3)
	c.finder(n-4, 3)
	c.finder(3, n-4)

	var pos [7]int
	na := alignPositions(c.version, &pos)
	for i := 0; i < na; i++ {
		for j := 0; j < na; j++ {
			// The three corners that hold finders get no alignment pattern.
			if (i == 0 && j == 0) || (i == 0 && j == na-1) || (i == na-1 && j == 0) {
				continue
			}
			c.alignment(pos[i], pos[j])
		}
	}
	c.drawFormat(0) // reserve the areas; the final bits are drawn per mask
	c.drawVersion()
}

// finder draws a finder pattern with its separator centred on (cx, cy).
func (c *Code) finder(cx, cy int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			x, y := cx+dx, cy+dy
			if x < 0 || y < 0 || x >= c.size || y >= c.size {
				continue
			}
			d := max(abs(dx), abs(dy)) // Chebyshev distance: rings of 1, 0, 1, 0, 1, separator
			c.setFn(x, y, d != 2 && d != 4)
		}
	}
}

// alignment draws a 5×5 alignment pattern centred on (cx, cy).
func (c *Code) alignment(cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			c.setFn(cx+dx, cy+dy, max(abs(dx), abs(dy)) != 1)
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// bchRemainder is the remainder of val·x^deg modulo gen (a polynomial of
// degree deg) over GF(2): the check bits of the BCH codes of the format and
// version information (Annex C).
func bchRemainder(val, gen uint32, deg int) uint32 {
	r := val << deg
	for b := bitLen(r) - 1; b >= deg; b-- {
		if r>>b&1 != 0 {
			r ^= gen << (b - deg)
		}
	}
	return r
}

func bitLen(x uint32) int {
	n := 0
	for ; x != 0; x >>= 1 {
		n++
	}
	return n
}

// drawFormat writes both copies of the 15-bit format information (level and
// mask pattern, BCH(15,5) coded and XORed with 101010000010010, §7.9) and the
// always-dark module.
func (c *Code) drawFormat(mask int) {
	data := formatBits[levelIndex(c.level)]<<3 | uint32(mask)
	bits := (data<<10 | bchRemainder(data, 0x537, 10)) ^ 0x5412
	bit := func(i int) bool { return bits>>i&1 != 0 }
	n := c.size
	// First copy, around the top-left finder.
	for i := 0; i <= 5; i++ {
		c.setFn(8, i, bit(i))
	}
	c.setFn(8, 7, bit(6))
	c.setFn(8, 8, bit(7))
	c.setFn(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		c.setFn(14-i, 8, bit(i))
	}
	// Second copy, split between the other two finders.
	for i := 0; i < 8; i++ {
		c.setFn(n-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		c.setFn(8, n-15+i, bit(i))
	}
	c.setFn(8, n-8, true) // the dark module
}

// drawVersion writes the two copies of the 18-bit version information
// (version and BCH(18,6) check, §7.10) of versions 7 and up.
func (c *Code) drawVersion() {
	if c.version < 7 {
		return
	}
	v := uint32(c.version)
	bits := v<<12 | bchRemainder(v, 0x1F25, 12)
	n := c.size
	for i := 0; i < 18; i++ {
		b := bits>>i&1 != 0
		a, o := n-11+i%3, i/3
		c.setFn(a, o, b) // bottom-left block is the transpose
		c.setFn(o, a, b)
	}
}

// placeCodewords writes the codeword bits of c.final, most significant bit
// first, into the data modules in the standard's two-column zigzag, from the
// bottom right going up, skipping the vertical timing pattern (§7.7.3). Modules
// left over (the remainder bits) stay light.
func (c *Code) placeCodewords() {
	n := c.size
	total := numCodewords(c.version) * 8
	i := 0
	for right := n - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5 // column 6 is the timing pattern: the pairs shift left
		}
		for vert := 0; vert < n; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				upward := (right+1)&2 == 0 // pair index parity counted from the right edge
				y := vert
				if upward {
					y = n - 1 - vert
				}
				if c.isFn(x, y) || i >= total {
					continue
				}
				c.set(x, y, c.final[i>>3]>>(7-i&7)&1 != 0)
				i++
			}
		}
	}
}

// applyMask XORs mask pattern m into the data modules (§7.8.2). Applying it
// twice restores the modules.
func (c *Code) applyMask(m int) {
	for y := 0; y < c.size; y++ {
		for x := 0; x < c.size; x++ {
			if c.isFn(x, y) {
				continue
			}
			var flip bool
			switch m {
			case 0:
				flip = (x+y)%2 == 0
			case 1:
				flip = y%2 == 0
			case 2:
				flip = x%3 == 0
			case 3:
				flip = (x+y)%3 == 0
			case 4:
				flip = (y/2+x/3)%2 == 0
			case 5:
				flip = x*y%2+x*y%3 == 0
			case 6:
				flip = (x*y%2+x*y%3)%2 == 0
			default:
				flip = ((x+y)%2+x*y%3)%2 == 0
			}
			if flip {
				c.set(x, y, !c.get(x, y))
			}
		}
	}
}

// chooseMask tries the eight mask patterns and keeps the one with the lowest
// penalty (the lowest number on a tie); force >= 0 picks that one (tests).
func (c *Code) chooseMask(force int) {
	best, bestScore := 0, int(^uint(0)>>1)
	if force >= 0 {
		best = force
	} else {
		for m := 0; m < 8; m++ {
			c.applyMask(m)
			c.drawFormat(m)
			if s := c.penalty(); s < bestScore {
				best, bestScore = m, s
			}
			c.applyMask(m)
		}
	}
	c.applyMask(best)
	c.drawFormat(best)
	c.mask = best
}

// Penalty weights of the four evaluation rules (§7.8.3, Table 11).
const (
	penRun    = 3  // N1: each run of five or more modules of one colour, plus one per extra
	penBlock  = 3  // N2: each 2×2 block of one colour
	penFinder = 40 // N3: each 1:1:3:1:1 pattern with four light modules on a side
	penBal    = 10 // N4: each 5 % step of the dark proportion away from 50 %
)

// penalty is the standard's mask score of the symbol as it stands.
func (c *Code) penalty() int {
	n := c.size
	score := 0
	for t := 0; t < 2; t++ { // the rows, then the columns
		for a := 0; a < n; a++ {
			run, prev := 0, false
			// win holds the last 15 modules of the line, the oldest in the
			// highest bit. The line is padded with four light modules on each
			// side: the quiet zone is where a pattern at the edge of the
			// symbol finds its light area.
			win := uint32(0)
			for k := 0; k < n+8; k++ {
				cur := false
				if b := k - 4; b >= 0 && b < n {
					if t == 0 {
						cur = c.get(b, a)
					} else {
						cur = c.get(a, b)
					}
					if b > 0 && cur == prev {
						run++
						if run == 5 {
							score += penRun
						} else if run > 5 {
							score++
						}
					} else {
						run = 1
					}
					prev = cur
				}
				win = win<<1&0x7FFF | b2u(cur)
				// Four light modules, the 1011101 core, four light modules: a
				// pattern counts once when it has the light area before or
				// after it (or both).
				if k >= 14 && win>>4&0x7F == 0x5D && (win>>11 == 0 || win&0xF == 0) {
					score += penFinder
				}
			}
		}
	}
	for y := 0; y < n-1; y++ {
		for x := 0; x < n-1; x++ {
			if v := c.get(x, y); v == c.get(x+1, y) && v == c.get(x, y+1) && v == c.get(x+1, y+1) {
				score += penBlock
			}
		}
	}
	dark := 0
	for _, w := range c.mod {
		dark += mbits.OnesCount64(w)
	}
	total := n * n
	score += penBal * (abs(dark*20-total*10) / total)
	return score
}

func b2u(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}
