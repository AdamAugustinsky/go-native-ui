package qr

import (
	"bytes"
	"errors"
	"testing"
)

// Encode, read back by a reader written from the standard apart from the
// encoder (its own function-module map, zigzag, masks, deinterleaving,
// Reed–Solomon syndromes and bit-stream parser; only Table 9's block counts,
// the alignment centres and the field arithmetic are shared): the payload
// comes back; the mode and version are the smallest that hold it, and a
// payload no version holds is ErrTooLong; finders, separators, timing and
// the dark module are in place; both format copies agree, pass their BCH
// check and name the level and mask; both version copies do too (version 7
// and up); every block's syndromes are zero; the terminator, padding bits,
// pad codewords and remainder bits are as the standard writes them; and
// encoding again into the same Code gives the same symbol.
func FuzzQREncode(f *testing.F) {
	for _, s := range []string{"", "0", "01234567", "HELLO WORLD", "hello", "https://wa.me/5511999999999?text=oi",
		"日本語", "\x00\xff", "AC-42 $%*+-./:", "1234567890123456789012345678901234567890"} {
		for l := range uint8(4) {
			f.Add([]byte(s), l)
		}
	}
	f.Add(bytes.Repeat([]byte("7"), 7089), uint8(LevelL)) // version 40-L's numeric capacity
	f.Add(bytes.Repeat([]byte("7"), 7090), uint8(LevelL))
	f.Add(bytes.Repeat([]byte{0xa5}, 2953), uint8(LevelL))
	f.Add(bytes.Repeat([]byte("A"), 1852), uint8(LevelH))
	f.Add([]byte("x"), uint8(4)) // not a level
	var c, again Code
	f.Fuzz(func(t *testing.T, data []byte, lv uint8) {
		if len(data) > 7100 {
			return
		}
		level := Level(lv % 5) // 4 is invalid
		err := Encode(&c, data, level)
		if level > LevelH {
			if !errors.Is(err, ErrLevel) || c.Size() != 0 {
				t.Fatalf("level %d: err %v, size %d", level, err, c.Size())
			}
			return
		}
		mode, version := wantModeVersion(data, level)
		if version == 0 {
			if !errors.Is(err, ErrTooLong) || c.Size() != 0 {
				t.Fatalf("%d bytes at %v fit no version: err %v, size %d", len(data), level, err, c.Size())
			}
			return
		}
		if err != nil {
			t.Fatalf("%d bytes at %v: %v", len(data), level, err)
		}
		if c.Version() != version || c.Size() != 17+4*version || c.Level() != level || c.Mask() < 0 || c.Mask() > 7 {
			t.Fatalf("%d bytes at %v: version %d size %d level %v mask %d, want version %d",
				len(data), level, c.Version(), c.Size(), c.Level(), c.Mask(), version)
		}
		got, err := readSymbol(&c, mode)
		if err != nil {
			t.Fatalf("%d bytes at %v, version %d mask %d: %v", len(data), level, version, c.Mask(), err)
		}
		if !bytes.Equal(got, data) {
			t.Fatalf("read back %q, encoded %q", got, data)
		}
		if Encode(&again, data, level) != nil || again.mod != c.mod || again.mask != c.mask {
			t.Fatal("encoding again gave another symbol")
		}
	})
}

// wantModeVersion is the smallest mode and the smallest version that hold
// data at level (version 0: none).
func wantModeVersion(data []byte, level Level) (mode, version int) {
	mode = modeNumeric
	for _, b := range data {
		switch {
		case b >= '0' && b <= '9':
		case bytes.IndexByte([]byte(alnumChars), b) >= 0:
			mode = max(mode, modeAlnum)
		default:
			mode = modeByte
		}
	}
	if len(data) == 0 {
		mode = modeByte
	}
	n := len(data)
	payload := 8 * n
	switch mode {
	case modeNumeric:
		payload = n/3*10 + [3]int{0, 4, 7}[n%3]
	case modeAlnum:
		payload = n/2*11 + n%2*6
	}
	for v := 1; v <= 40; v++ {
		if 4+countLen(mode, v)+payload <= 8*numDataCodewords(v, level) {
			return mode, v
		}
	}
	return mode, 0
}

// countLen is the character count indicator's length (Table 3).
func countLen(mode, v int) int {
	i := 0
	if v >= 27 {
		i = 2
	} else if v >= 10 {
		i = 1
	}
	switch mode {
	case modeNumeric:
		return [3]int{10, 12, 14}[i]
	case modeAlnum:
		return [3]int{9, 11, 13}[i]
	}
	return [3]int{8, 16, 16}[i]
}

// readSymbol reads c as a reader would and returns the payload of its one
// segment, which must be in mode.
func readSymbol(c *Code, mode int) ([]byte, error) {
	n, v := c.size, c.version
	dark := func(x, y int) bool { return c.Dark(x, y) }

	// Function patterns.
	fn := make([]bool, n*n)
	mark := func(x0, y0, x1, y1 int) {
		for y := y0; y <= y1; y++ {
			for x := x0; x <= x1; x++ {
				fn[y*n+x] = true
			}
		}
	}
	mark(0, 0, 8, 8)     // top-left finder, separator, format
	mark(n-8, 0, n-1, 8) // top-right
	mark(0, n-8, 8, n-1) // bottom-left, with the dark module
	mark(6, 0, 6, n-1)   // timing
	mark(0, 6, n-1, 6)
	var pos [7]int
	na := alignPositions(v, &pos)
	for i := range na {
		for j := range na {
			if i == 0 && j == 0 || i == 0 && j == na-1 || i == na-1 && j == 0 {
				continue
			}
			mark(pos[i]-2, pos[j]-2, pos[i]+2, pos[j]+2)
			for d := -2; d <= 2; d++ {
				for e := -2; e <= 2; e++ {
					if want := max(abs(d), abs(e)) != 1; dark(pos[i]+d, pos[j]+e) != want {
						return nil, errors.New("an alignment pattern is wrong")
					}
				}
			}
		}
	}
	if v >= 7 {
		mark(n-11, 0, n-9, 5)
		mark(0, n-11, 5, n-9)
	}
	for _, o := range [3][2]int{{0, 0}, {n - 7, 0}, {0, n - 7}} {
		for y := -1; y <= 7; y++ {
			for x := -1; x <= 7; x++ {
				px, py := o[0]+x, o[1]+y
				if px < 0 || py < 0 || px >= n || py >= n {
					continue
				}
				d := max(abs(x-3), abs(y-3))
				if want := d <= 1 || d == 3; dark(px, py) != want {
					return nil, errors.New("a finder or separator is wrong")
				}
			}
		}
	}
	for i := 8; i < n-8; i++ {
		if dark(i, 6) != (i%2 == 0) || dark(6, i) != (i%2 == 0) {
			return nil, errors.New("a timing pattern is wrong")
		}
	}
	if !dark(8, n-8) {
		return nil, errors.New("the dark module is light")
	}
	raw := 0
	for _, f := range fn {
		if !f {
			raw++
		}
	}
	if raw != numRawModules(v) {
		return nil, errors.New("the function patterns leave the wrong number of modules")
	}

	// Format and version information.
	var f1, f2 uint32
	for i := range 15 {
		var a, b bool
		switch {
		case i <= 5:
			a = dark(8, i)
		case i == 6:
			a = dark(8, 7)
		case i == 7:
			a = dark(8, 8)
		case i == 8:
			a = dark(7, 8)
		default:
			a = dark(14-i, 8)
		}
		if i < 8 {
			b = dark(n-1-i, 8)
		} else {
			b = dark(8, n-15+i)
		}
		f1 |= b2u(a) << i
		f2 |= b2u(b) << i
	}
	if f1 != f2 {
		return nil, errors.New("the format copies differ")
	}
	f1 ^= 0x5412
	if polyMod(f1, 0x537) != 0 {
		return nil, errors.New("the format information fails its BCH check")
	}
	if lvl, mask := f1>>13, int(f1>>10&7); lvl != formatBits[c.level] || mask != c.mask {
		return nil, errors.New("the format information names another level or mask")
	}
	if v >= 7 {
		var v1, v2 uint32
		for i := range 18 {
			v1 |= b2u(dark(n-11+i%3, i/3)) << i
			v2 |= b2u(dark(i/3, n-11+i%3)) << i
		}
		if v1 != v2 || polyMod(v1, 0x1F25) != 0 || int(v1>>12) != v {
			return nil, errors.New("the version information is wrong")
		}
	}

	// Codewords: the zigzag from the bottom right, two columns at a time,
	// skipping the vertical timing column; data modules unmasked.
	var bits []bool
	for right := n - 1; right >= 1; right -= 2 {
		if right == 6 {
			right--
		}
		up := (n-1-right)/2%2 == 0
		for k := range n {
			row := k
			if up {
				row = n - 1 - k
			}
			for _, col := range [2]int{right, right - 1} {
				if !fn[row*n+col] {
					bits = append(bits, dark(col, row) != maskBit(c.mask, row, col))
				}
			}
		}
	}
	total := numCodewords(v)
	for _, b := range bits[total*8:] {
		if b {
			return nil, errors.New("a remainder bit is dark")
		}
	}
	words := make([]uint8, total)
	for i := range words {
		for _, b := range bits[8*i : 8*i+8] {
			words[i] = words[i]<<1 | uint8(b2u(b))
		}
	}

	// Deinterleave: data codewords column by column over the blocks (the
	// short blocks have no last column), then the error correction.
	li := levelIndex(c.level)
	nb, ecc := int(numBlocks[li][v-1]), int(eccPerBlock[li][v-1])
	dataTotal := total - nb*ecc
	short := nb - dataTotal%nb
	shortLen := dataTotal / nb
	blocks := make([][]uint8, nb)
	k := 0
	for i := 0; i <= shortLen; i++ {
		for j := range nb {
			if i == shortLen && j < short {
				continue
			}
			blocks[j] = append(blocks[j], words[k])
			k++
		}
	}
	var data []uint8
	for _, b := range blocks {
		data = append(data, b...)
	}
	for range ecc {
		for j := range nb {
			blocks[j] = append(blocks[j], words[k])
			k++
		}
	}
	for _, b := range blocks {
		for i := range ecc { // the block's polynomial at α^i
			var s uint8
			for _, w := range b {
				s = gfMul(s, gfExp[i]) ^ w
			}
			if s != 0 {
				return nil, errors.New("a block has a nonzero syndrome")
			}
		}
	}

	// The bit stream.
	r := bitReader{b: data}
	if m := r.read(4); m != mode {
		return nil, errors.New("the segment is in another mode")
	}
	count := r.read(countLen(mode, v))
	var out []byte
	switch mode {
	case modeNumeric:
		for left := count; left > 0; {
			k := min(left, 3)
			x := r.read([4]int{0, 4, 7, 10}[k])
			if x >= [4]int{1, 10, 100, 1000}[k] {
				return nil, errors.New("a numeric group is too large")
			}
			for p := [4]int{1, 1, 10, 100}[k]; p > 0; p /= 10 {
				out = append(out, byte('0'+x/p%10))
			}
			left -= k
		}
	case modeAlnum:
		for left := count; left > 0; left -= 2 {
			if left == 1 {
				out = append(out, alnumChars[r.read(6)])
				break
			}
			x := r.read(11)
			if x >= 45*45 {
				return nil, errors.New("an alphanumeric pair is too large")
			}
			out = append(out, alnumChars[x/45], alnumChars[x%45])
		}
	default:
		for range count {
			out = append(out, byte(r.read(8)))
		}
	}
	if r.over {
		return nil, errors.New("the segment runs past the data codewords")
	}
	if r.read(min(4, 8*len(data)-r.at)) != 0 || r.at%8 != 0 && r.read(8-r.at%8) != 0 {
		return nil, errors.New("the terminator or the bits after it are not zero")
	}
	for i, w := range data[r.at/8:] {
		if w != [2]uint8{0xEC, 0x11}[i%2] {
			return nil, errors.New("the pad codewords are not 0xEC 0x11")
		}
	}
	return out, nil
}

// maskBit is mask pattern m at (row, col) (Table 10).
func maskBit(m, i, j int) bool {
	switch m {
	case 0:
		return (i+j)%2 == 0
	case 1:
		return i%2 == 0
	case 2:
		return j%3 == 0
	case 3:
		return (i+j)%3 == 0
	case 4:
		return (i/2+j/3)%2 == 0
	case 5:
		return i*j%2+i*j%3 == 0
	case 6:
		return (i*j%2+i*j%3)%2 == 0
	}
	return ((i+j)%2+i*j%3)%2 == 0
}

// polyMod is the remainder of x over GF(2) polynomial g.
func polyMod(x, g uint32) uint32 {
	gl := bitLen(g)
	for bitLen(x) >= gl {
		x ^= g << (bitLen(x) - gl)
	}
	return x
}

type bitReader struct {
	b    []uint8
	at   int
	over bool
}

func (r *bitReader) read(n int) int {
	x := 0
	for range n {
		bit := 0
		if r.at < 8*len(r.b) {
			bit = int(r.b[r.at/8] >> (7 - r.at%8) & 1)
		} else {
			r.over = true
		}
		x = x<<1 | bit
		r.at++
	}
	return x
}
