//go:build linux || textengine

package text

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"testing"
)

// A PNG over the size limit must be rejected before the decoder allocates
// its pixels. 4097×128 is one column over the 4096 limit; decoding it would
// allocate ~2 MB, so a check made only after decoding fails the allocation
// bound below.
func TestPNGOversizeRejectedBeforeDecode(t *testing.T) {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 4097, 128))); err != nil {
		t.Fatal(err)
	}
	data := b.Bytes()

	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	_, _, _, err := pngPremultiplied(data)
	runtime.ReadMemStats(&m1)
	if err == nil {
		t.Fatal("oversized PNG accepted")
	}
	if d := m1.TotalAlloc - m0.TotalAlloc; d > 1<<20 {
		t.Fatalf("rejecting it allocated %d bytes (decoded before checking the size?)", d)
	}
}

// A table directory entry that runs past the end of the file is rejected, so
// loaders never allocate its forged length.
func TestSfntTablePastEOFRejected(t *testing.T) {
	f := make([]byte, 12+16+4)
	binary.BigEndian.PutUint32(f[0:], 0x00010000)
	binary.BigEndian.PutUint16(f[4:], 1)
	rec := f[12:]
	copy(rec[0:4], "CBLC")
	binary.BigEndian.PutUint32(rec[8:], 28)
	binary.BigEndian.PutUint32(rec[12:], 0xFFFFFFF0)
	if _, err := readSfntDirectory(bytes.NewReader(f), 0); err == nil {
		t.Fatal("table past EOF accepted")
	}
}

// Compare reused output against the standard decoder across PNG depths,
// palette transparency, all row filters and Adam7 pass boundaries.
func TestPNGReusableDecode(t *testing.T) {
	var d pngScratch
	for _, tc := range []struct{ kind, depth int }{{0, 1}, {0, 2}, {0, 4}, {0, 8}, {0, 16}, {2, 8}, {2, 16}, {3, 1}, {3, 2}, {3, 4}, {3, 8}, {4, 8}, {4, 16}, {6, 8}, {6, 16}} {
		for _, interlace := range []bool{false, true} {
			data := testBitmapPNG(tc.kind, tc.depth, interlace)
			wantImage, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			got, w, h, err := d.decode(data)
			if err != nil {
				t.Fatalf("kind %d depth %d interlace %v: %v", tc.kind, tc.depth, interlace, err)
			}
			want := make([]byte, w*h*4)
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					var rgba color.RGBA
					if m, ok := wantImage.(*image.NRGBA); ok {
						c := m.NRGBAAt(x, y)
						a := uint32(c.A)
						rgba = color.RGBA{uint8((uint32(c.R)*a + 127) / 255), uint8((uint32(c.G)*a + 127) / 255), uint8((uint32(c.B)*a + 127) / 255), c.A}
					} else {
						rgba = color.RGBAModel.Convert(wantImage.At(x, y)).(color.RGBA)
					}
					i := (y*w + x) * 4
					want[i], want[i+1], want[i+2], want[i+3] = rgba.R, rgba.G, rgba.B, rgba.A
				}
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("kind %d depth %d interlace %v: pixels differ", tc.kind, tc.depth, interlace)
			}
			if n := testing.AllocsPerRun(20, func() { _, _, _, err = d.decode(data) }); n != 0 || err != nil {
				t.Fatalf("reused decoder: %v allocs, err=%v", n, err)
			}
			corrupt := bytes.Clone(data)
			corrupt[len(corrupt)-1] ^= 1
			if _, _, _, err := d.decode(corrupt); err == nil {
				t.Fatal("bad chunk checksum accepted")
			}
			if _, _, _, err := d.decode(data[:len(data)-1]); err == nil {
				t.Fatal("truncated PNG accepted")
			}
		}
	}
}

// Build deterministic PNG samples independently of the decoder, using every
// filter and optionally interlacing. IDAT is split across two chunks.
func testBitmapPNG(kind, depth int, interlace bool) []byte {
	const w, h = 13, 11
	channels := map[int]int{0: 1, 2: 3, 3: 1, 4: 2, 6: 4}[kind]
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(tag string, data []byte) {
		binary.Write(&out, binary.BigEndian, uint32(len(data)))
		out.WriteString(tag)
		out.Write(data)
		sum := crc32.NewIEEE()
		sum.Write([]byte(tag))
		sum.Write(data)
		binary.Write(&out, binary.BigEndian, sum.Sum32())
	}
	hdr := make([]byte, 13)
	binary.BigEndian.PutUint32(hdr, w)
	binary.BigEndian.PutUint32(hdr[4:], h)
	hdr[8], hdr[9] = byte(depth), byte(kind)
	if interlace {
		hdr[12] = 1
	}
	chunk("IHDR", hdr)
	if kind == 3 {
		n := min(1<<depth, 256)
		pal, alpha := make([]byte, 3*n), make([]byte, n)
		for i := 0; i < n; i++ {
			pal[3*i], pal[3*i+1], pal[3*i+2], alpha[i] = byte(43*i), byte(71*i), byte(97*i), byte(29*i)
		}
		chunk("PLTE", pal)
		chunk("tRNS", alpha)
	}
	if kind == 0 || kind == 2 {
		trans := make([]byte, 2*channels)
		chunk("tRNS", trans)
	}
	passes := [][4]int{{0, 0, 1, 1}}
	if interlace {
		passes = [][4]int{{0, 0, 8, 8}, {4, 0, 8, 8}, {0, 4, 4, 8}, {2, 0, 4, 4}, {0, 2, 2, 4}, {1, 0, 2, 2}, {0, 1, 1, 2}}
	}
	var raw bytes.Buffer
	for _, p := range passes {
		pw, ph := max(0, (w-p[0]+p[2]-1)/p[2]), max(0, (h-p[1]+p[3]-1)/p[3])
		if pw == 0 || ph == 0 {
			continue
		}
		stride, bpp := (pw*channels*depth+7)/8, max(1, (channels*depth+7)/8)
		var prev []byte
		for y := 0; y < ph; y++ {
			row := make([]byte, stride)
			for x := 0; x < pw; x++ {
				for c := 0; c < channels; c++ {
					i := x*channels + c
					v := uint16((p[0]+x*p[2])*1717+(p[1]+y*p[3])*3433+c*6161) & uint16((1<<depth)-1)
					switch depth {
					case 8:
						row[i] = byte(v)
					case 16:
						binary.BigEndian.PutUint16(row[2*i:], v)
					default:
						row[i*depth/8] |= byte(v) << uint(8-depth-(i*depth&7))
					}
				}
			}
			filter := byte(y % 5)
			raw.WriteByte(filter)
			for x, v := range row {
				var l, u, ul byte
				if x >= bpp {
					l = row[x-bpp]
				}
				if prev != nil {
					u = prev[x]
					if x >= bpp {
						ul = prev[x-bpp]
					}
				}
				switch filter {
				case 1:
					v -= l
				case 2:
					v -= u
				case 3:
					v -= byte((int(l) + int(u)) / 2)
				case 4:
					a, b, c := int(u)-int(ul), int(l)-int(ul), int(l)+int(u)-2*int(ul)
					if a < 0 {
						a = -a
					}
					if b < 0 {
						b = -b
					}
					if c < 0 {
						c = -c
					}
					switch {
					case a <= b && a <= c:
						v -= l
					case b <= c:
						v -= u
					default:
						v -= ul
					}
				}
				raw.WriteByte(v)
			}
			prev = row
		}
	}
	var compressed bytes.Buffer
	z := zlib.NewWriter(&compressed)
	z.Write(raw.Bytes())
	z.Close()
	data := compressed.Bytes()
	chunk("IDAT", data[:len(data)/2])
	chunk("IDAT", data[len(data)/2:])
	chunk("IEND", nil)
	return out.Bytes()
}
