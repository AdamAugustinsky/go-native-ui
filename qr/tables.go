package qr

// Error-correction block structure, from ISO/IEC 18004 Table 9, indexed by
// level (L, M, Q, H) and version-1.
//
// eccPerBlock is the number of error-correction codewords in each block, and
// numBlocks the number of blocks the data and error-correction codewords of the
// symbol are divided into. (The standard's table lists a few blocks with one
// more data codeword than the others; the split follows from the totals, see
// blockLayout.)
var eccPerBlock = [4][maxVersion]uint8{
	{ // L
		7, 10, 15, 20, 26, 18, 20, 24, 30, 18,
		20, 24, 26, 30, 22, 24, 28, 30, 28, 28,
		28, 28, 30, 30, 26, 28, 30, 30, 30, 30,
		30, 30, 30, 30, 30, 30, 30, 30, 30, 30,
	},
	{ // M
		10, 16, 26, 18, 24, 16, 18, 22, 22, 26,
		30, 22, 22, 24, 24, 28, 28, 26, 26, 26,
		26, 28, 28, 28, 28, 28, 28, 28, 28, 28,
		28, 28, 28, 28, 28, 28, 28, 28, 28, 28,
	},
	{ // Q
		13, 22, 18, 26, 18, 24, 18, 22, 20, 24,
		28, 26, 24, 20, 30, 24, 28, 28, 26, 30,
		28, 30, 30, 30, 30, 28, 30, 30, 30, 30,
		30, 30, 30, 30, 30, 30, 30, 30, 30, 30,
	},
	{ // H
		17, 28, 22, 16, 22, 28, 26, 26, 24, 28,
		24, 28, 22, 24, 24, 30, 28, 28, 26, 28,
		30, 24, 30, 30, 30, 30, 30, 30, 30, 30,
		30, 30, 30, 30, 30, 30, 30, 30, 30, 30,
	},
}

var numBlocks = [4][maxVersion]uint8{
	{ // L
		1, 1, 1, 1, 1, 2, 2, 2, 2, 4,
		4, 4, 4, 4, 6, 6, 6, 6, 7, 8,
		8, 9, 9, 10, 12, 12, 12, 13, 14, 15,
		16, 17, 18, 19, 19, 20, 21, 22, 24, 25,
	},
	{ // M
		1, 1, 1, 2, 2, 4, 4, 4, 5, 5,
		5, 8, 9, 9, 10, 10, 11, 13, 14, 16,
		17, 17, 18, 20, 21, 23, 25, 26, 28, 29,
		31, 33, 35, 37, 38, 40, 43, 45, 47, 49,
	},
	{ // Q
		1, 1, 2, 2, 4, 4, 6, 6, 8, 8,
		8, 10, 12, 16, 12, 17, 16, 18, 21, 20,
		23, 23, 25, 27, 29, 34, 34, 35, 38, 40,
		43, 45, 48, 51, 53, 56, 59, 62, 65, 68,
	},
	{ // H
		1, 1, 2, 4, 4, 4, 5, 6, 8, 8,
		11, 11, 16, 16, 18, 16, 19, 21, 25, 25,
		25, 34, 30, 32, 35, 37, 40, 42, 45, 48,
		51, 54, 57, 60, 63, 66, 70, 74, 77, 81,
	},
}

// levelIndex maps a Level to its row in the tables above.
func levelIndex(l Level) int { return int(l) }

// formatBits is the 2-bit error-correction indicator of the format
// information (ISO/IEC 18004 Table 12), indexed by Level.
var formatBits = [4]uint32{1, 0, 3, 2} // L=01, M=00, Q=11, H=10

// numRawModules is the number of modules of a version that are not part of a
// function pattern (finder, separator, timing, alignment, format and version
// information, the dark module): the ones the codewords and remainder bits
// are placed into.
func numRawModules(v int) int {
	r := (16*v+128)*v + 64
	if v >= 2 {
		n := v/7 + 2
		r -= (25*n-10)*n - 55
		if v >= 7 {
			r -= 36
		}
	}
	return r
}

// numCodewords is the number of codewords (data plus error correction) of a
// version.
func numCodewords(v int) int { return numRawModules(v) / 8 }

// numDataCodewords is the number of data codewords of a version at a level.
func numDataCodewords(v int, l Level) int {
	li := levelIndex(l)
	return numCodewords(v) - int(eccPerBlock[li][v-1])*int(numBlocks[li][v-1])
}

// alignPositions writes the centre coordinates of the alignment patterns of a
// version into dst (ascending, the same on both axes) and returns how many
// there are. Version 1 has none. The spacing is the standard's (Annex E): the
// first is at 6, the last at size-7, the rest evenly spaced by the smallest even
// step, with the first gap taking the remainder.
func alignPositions(v int, dst *[7]int) int {
	if v == 1 {
		return 0
	}
	n := v/7 + 2
	size := v*4 + 17
	step := 26
	if v != 32 {
		step = (v*8 + n*3 + 5) / (n*4 - 4) * 2
	}
	dst[0] = 6
	for i := n - 1; i >= 1; i-- {
		dst[i] = size - 7 - (n-1-i)*step
	}
	return n
}
