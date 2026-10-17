package main

import "gonativeui/ui"

// The rows of the list are synthetic: every field derives from the row index,
// so a million rows cost no memory and any row can be built at once.

// mix is a splitmix64 finalizer, the pseudo-random source of a row.
func mix(r int) uint64 {
	z := uint64(r)*0x9E3779B97F4A7C15 + 0xD1B54A32D192ED03
	z = (z ^ z>>30) * 0xBF58476D1CE4E5B9
	z = (z ^ z>>27) * 0x94D049BB133111EB
	return z ^ z>>31
}

// padInt appends v zero-padded to width digits.
func padInt(f *ui.Fmt, v, width int) {
	n := 1
	for x := v; x >= 10; x /= 10 {
		n++
	}
	for ; n < width; n++ {
		f.Byte('0')
	}
	f.Int(v)
}

var exts = [...]string{".dat", ".txt", ".png", ".bin"}

// fileKind selects the extension and the icon colour of row r.
func fileKind(r int) int { return int(mix(r) >> 60 & 3) }

// rowName builds "file_0001234.dat" for row r.
func rowName(c *ui.Context, r int) []byte {
	f := c.Fmt().Str("file_")
	padInt(f, r, 7)
	return f.Str(exts[fileKind(r)]).Bytes()
}

// rowSize builds a size like "834 KB" for row r.
func rowSize(c *ui.Context, r int) []byte {
	sz := (mix(r+1) >> 34) >> (mix(r+3) % 22) // up to 1 GiB, spread over the magnitudes
	f := c.Fmt()
	switch {
	case sz < 1<<10:
		f.Uint64(sz).Str(" B")
	case sz < 1<<20:
		f.Float(float64(sz)/(1<<10), 1).Str(" KB")
	default:
		f.Float(float64(sz)/(1<<20), 1).Str(" MB")
	}
	return f.Bytes()
}

// rowDate builds "2026-03-14 09:41" for row r.
func rowDate(c *ui.Context, r int) []byte {
	h := mix(r + 2)
	f := c.Fmt()
	f.Int(2019 + int(h%7)).Byte('-')
	padInt(f, 1+int(h>>8%12), 2)
	f.Byte('-')
	padInt(f, 1+int(h>>16%28), 2)
	f.Byte(' ')
	padInt(f, int(h>>24%24), 2)
	f.Byte(':')
	padInt(f, int(h>>32%60), 2)
	return f.Bytes()
}
