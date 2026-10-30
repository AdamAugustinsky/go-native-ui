//go:build linux || textengine

package text

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"testing"
	"time"

	"github.com/go-text/typesetting/font/opentype/tables"
)

// At most 64 KiB per input table. Decode at most 64K pixels; larger legal
// images are outside this campaign, but oversized/invalid headers still
// reach the decoder. Budget: 2 s + 100 us/input byte, 16 MiB + 512 B/input
// byte + 32 B/pixel (two decodes and the standard-library reference).
func FuzzCBDT(f *testing.F) {
	for _, tc := range [][2]int{{0, 1}, {0, 2}, {0, 4}, {0, 8}, {0, 16}, {2, 8}, {2, 16}, {3, 1}, {3, 2}, {3, 4}, {3, 8}, {4, 8}, {4, 16}, {6, 8}, {6, 16}} {
		for _, interlace := range []bool{false, true} {
			p := testBitmapPNG(tc[0], tc[1], interlace)
			loc, _ := cbTables(5, p, 13, 11, 0)
			f.Add(p, loc, uint16(5))
		}
	}
	f.Add([]byte{}, []byte{}, uint16(0))
	f.Add([]byte("\x89PNG\r\n\x1a\n"), []byte{0xff}, uint16(65535))
	f.Fuzz(func(t *testing.T, data, loc []byte, gid uint16) {
		if len(data) > 64<<10 || len(loc) > 64<<10 {
			t.Skip()
		}
		limit := 2*time.Second + time.Duration(len(data)+len(loc))*100*time.Microsecond
		timer := time.AfterFunc(limit, func() { panic("CBDT fuzz time bound exceeded") })
		defer timer.Stop()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		// Repair only CRCs in a second copy, so payload mutations reach PNG
		// filters and the inflater while the original tests checksum rejection.
		repaired := bytes.Clone(data)
		if len(repaired) >= 8 && string(repaired[:8]) == "\x89PNG\r\n\x1a\n" {
			for off := 8; off+12 <= len(repaired); {
				n := uint64(binary.BigEndian.Uint32(repaired[off:]))
				if n > uint64(len(repaired)-off-12) {
					break
				}
				end := off + 8 + int(n)
				binary.BigEndian.PutUint32(repaired[end:], crc32.ChecksumIEEE(repaired[off+4:end]))
				off = end + 4
			}
		}
		var scratch pngScratch
		pixels := 0
		for _, p := range [][]byte{data, repaired} {
			pixels += checkFuzzBitmap(t, &scratch, p)
		}
		// The existing format-17 fixture supplies CBDT; arbitrary loc bytes
		// exercise ParseCBLC and glyph indexing without requiring a font file.
		_, bitmap := cbTables(gid, data, 13, 11, 0)
		file := append(bytes.Clone(loc), bitmap...)
		dir := sfntDir{tables: []sfntTable{{tag: tagCBLC, length: uint32(len(loc))}, {tag: tagCBDT, offset: uint32(len(loc)), length: uint32(len(bitmap))}}}
		if c := openCBDT(bytes.NewReader(file), &dir); c != nil {
			strike, ppem := c.strikeFor(float32(gid))
			if strike < 0 || strike >= len(c.loc.BitmapSizes) || ppem < 0 || ppem > 255 {
				t.Fatalf("invalid strike %d/%d", strike, ppem)
			}
			for _, id := range []uint16{gid, 0, 5, 65535} {
				g, err := c.glyph(strike, id)
				if err == nil {
					if len(g.png) > len(bitmap) || g.width < 0 || g.width > 255 || g.height < 0 || g.height > 255 {
						t.Fatalf("invalid glyph %+v", g)
					}
					pixels += checkFuzzBitmap(t, &scratch, g.png)
				}
			}
			if cap(c.scratch) > 16<<20 {
				t.Fatal("glyph scratch exceeds cap")
			}
		}
		for _, format := range []uint16{17, 18, 19} {
			g, err := parseCBImage(format, data, &tables.BigGlyphMetrics{})
			if err == nil && len(g.png) > len(data) {
				t.Fatal("image record escaped its table")
			}
		}
		runtime.ReadMemStats(&after)
		if n := after.TotalAlloc - before.TotalAlloc; n > uint64(16<<20)+512*uint64(len(data)+len(loc))+32*uint64(pixels) {
			t.Fatalf("allocated %d bytes", n)
		}
	})
}

func checkFuzzBitmap(t *testing.T, d *pngScratch, data []byte) int {
	t.Helper()
	cfg, configErr := png.DecodeConfig(bytes.NewReader(data))
	// Inspect IHDR independently, including headers DecodeConfig rejects.
	if len(data) >= 33 && string(data[12:16]) == "IHDR" {
		w, h := uint64(binary.BigEndian.Uint32(data[16:])), uint64(binary.BigEndian.Uint32(data[20:]))
		if w > 0 && h > 0 && w <= 4096 && h <= 4096 && w*h > 1<<16 {
			return 0
		}
	}
	pix, w, h, err := d.decode(data)
	if cap(d.raw) > 4096*(4096*8+7) || cap(d.pix) > 4096*4096*4 || cap(d.idat) > 2*len(data)+1024 {
		t.Fatal("PNG scratch exceeds bounds")
	}
	if configErr != nil || cfg.Width > 4096 || cfg.Height > 4096 || cfg.Width <= 0 || cfg.Height <= 0 {
		if err == nil {
			t.Fatal("invalid/oversized PNG config accepted")
		}
		return 0
	}
	ref, refErr := png.Decode(bytes.NewReader(data))
	// Both implementations may reject malformed compressed data differently.
	if err != nil {
		if refErr == nil {
			t.Fatalf("valid PNG rejected: %v", err)
		}
		return cfg.Width * cfg.Height
	}
	if refErr != nil {
		t.Fatalf("PNG accepted but reference rejects it: %v", refErr)
	}
	if w != cfg.Width || h != cfg.Height || len(pix) != 4*w*h {
		t.Fatalf("invalid decoded dimensions %dx%d", w, h)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var want color.RGBA
			if m, ok := ref.(*image.NRGBA); ok {
				c := m.NRGBAAt(x, y)
				a := uint32(c.A)
				want = color.RGBA{uint8((uint32(c.R)*a + 127) / 255), uint8((uint32(c.G)*a + 127) / 255), uint8((uint32(c.B)*a + 127) / 255), c.A}
			} else {
				want = color.RGBAModel.Convert(ref.At(x, y)).(color.RGBA)
			}
			at := (y*w + x) * 4
			got := color.RGBA{pix[at], pix[at+1], pix[at+2], pix[at+3]}
			if got != want {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
	return w * h
}
