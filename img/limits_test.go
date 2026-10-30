package img

import (
	"bytes"
	"compress/lzw"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

// Valid images of any size in a few kilobytes, all one colour: what a
// decompression bomb looks like. Only their headers should ever be read.

// zeroPNG is a valid w×h 1-bit grey PNG, all black.
func zeroPNG(w, h int) []byte {
	var b bytes.Buffer
	chunk := func(typ string, data []byte) {
		binary.Write(&b, binary.BigEndian, uint32(len(data)))
		b.WriteString(typ)
		b.Write(data)
		binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
	}
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], uint32(w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(h))
	ihdr[8] = 1 // bit depth; colour type 0, grey
	chunk("IHDR", ihdr)
	var z bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&z, zlib.BestSpeed)
	row := make([]byte, 1+(w+7)/8) // filter 0, then the pixels
	for y := 0; y < h; y++ {
		zw.Write(row)
	}
	zw.Close()
	chunk("IDAT", z.Bytes())
	chunk("IEND", nil)
	return b.Bytes()
}

// zeroJPEG is a valid baseline greyscale w×h JPEG, all mid-grey: each block
// is a zero DC difference and an end of block, a one-bit code each.
func zeroJPEG(w, h int) []byte {
	var b bytes.Buffer
	seg := func(m byte, data []byte) {
		b.Write([]byte{0xFF, m, byte((len(data) + 2) >> 8), byte(len(data) + 2)})
		b.Write(data)
	}
	b.Write([]byte{0xFF, 0xD8})
	q := make([]byte, 65) // table 0, all ones
	for i := 1; i < len(q); i++ {
		q[i] = 1
	}
	seg(0xDB, q)
	seg(0xC0, []byte{8, byte(h >> 8), byte(h), byte(w >> 8), byte(w), 1, 1, 0x11, 0})
	one := make([]byte, 16) // one code of length 1
	one[0] = 1
	seg(0xC4, append(append([]byte{0x00}, one...), 0x00)) // DC table 0: difference category 0
	seg(0xC4, append(append([]byte{0x10}, one...), 0x00)) // AC table 0: end of block
	seg(0xDA, []byte{1, 1, 0x00, 0, 63, 0})
	blocks := ((w + 7) / 8) * ((h + 7) / 8)
	b.Write(make([]byte, (2*blocks+7)/8))
	b.Write([]byte{0xFF, 0xD9})
	return b.Bytes()
}

// subBlocks frames a GIF's image data in sub-blocks of up to 255 bytes.
type subBlocks struct {
	b   *bytes.Buffer
	buf []byte
}

func (s *subBlocks) Write(p []byte) (int, error) {
	s.buf = append(s.buf, p...)
	for len(s.buf) >= 255 {
		s.b.WriteByte(255)
		s.b.Write(s.buf[:255])
		s.buf = s.buf[255:]
	}
	return len(p), nil
}

func (s *subBlocks) end() {
	if len(s.buf) > 0 {
		s.b.WriteByte(byte(len(s.buf)))
		s.b.Write(s.buf)
	}
	s.b.WriteByte(0)
}

// zeroGIF is a valid GIF with one w×h frame of colour 0 (black).
func zeroGIF(w, h int) []byte {
	var b bytes.Buffer
	b.WriteString("GIF89a")
	b.Write([]byte{byte(w), byte(w >> 8), byte(h), byte(h >> 8), 0x80, 0, 0}) // a two-colour table follows
	b.Write([]byte{0, 0, 0, 255, 255, 255})
	b.Write([]byte{0x2C, 0, 0, 0, 0, byte(w), byte(w >> 8), byte(h), byte(h >> 8), 0})
	b.WriteByte(2) // LZW minimum code size
	sb := &subBlocks{b: &b}
	zw := lzw.NewWriter(sb, lzw.LSB, 2)
	row := make([]byte, w)
	for y := 0; y < h; y++ {
		zw.Write(row)
	}
	zw.Close()
	sb.end()
	b.WriteByte(0x3B)
	return b.Bytes()
}

// TestSourcePixelLimit: a valid image of more than 100 megapixels (10241 ×
// 10240) is refused as too large from its header, for every format, though
// the display size asked for is small; the same image at 64×48 decodes.
func TestSourcePixelLimit(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	for k, tc := range []struct {
		name string
		make func(w, h int) []byte
	}{
		{"png", zeroPNG},
		{"jpeg", zeroJPEG},
		{"gif", zeroGIF},
	} {
		small, _ := c.Request(Key{Hash: uint64(2*k + 1), W: 64, H: 64}, Bytes(tc.make(64, 48)))
		huge, _ := c.Request(Key{Hash: uint64(2*k + 2), W: 64, H: 64}, Bytes(tc.make(10241, 10240)))
		settle(t, c, func() { c.Use(small) })
		if st := c.State(small); st != Ready {
			t.Errorf("%s of 64×48: %v, %v; want Ready", tc.name, st, c.Err(small))
		}
		if st := c.State(huge); st != Failed || !errors.Is(c.Err(huge), ErrTooLarge) {
			t.Errorf("%s of 10241×10240: %v, %v; want Failed, ErrTooLarge", tc.name, st, c.Err(huge))
		}
	}
}

// TestGIFFrameOnCanvas: a GIF whose first frame is 20×20 at (40, 30) on a
// 100×100 logical screen decodes at the screen's size, the frame at its
// offset and the rest transparent.
func TestGIFFrameOnCanvas(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	pal := color.Palette{color.RGBA{0, 0, 0, 0}, color.RGBA{255, 0, 0, 255}}
	f := image.NewPaletted(image.Rect(40, 30, 60, 50), pal)
	for i := range f.Pix {
		f.Pix[i] = 1
	}
	var buf bytes.Buffer
	g := &gif.GIF{Image: []*image.Paletted{f}, Delay: []int{0},
		Config: image.Config{ColorModel: pal, Width: 100, Height: 100}}
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	d := decodeNow(t, c, Bytes(buf.Bytes()), 100, 100, 0)
	if d.r.err != errNone || d.w != 100 || d.h != 100 {
		t.Fatalf("decoded %d×%d (err %d), want the 100×100 screen", d.w, d.h, d.r.err)
	}
	near(t, "inside the frame", d.at(0.5, 0.4), [4]int{255, 0, 0, 255}, 2)
	near(t, "the screen outside it", d.at(0.1, 0.1), [4]int{0, 0, 0, 0}, 0)
	near(t, "left of it", d.at(0.35, 0.4), [4]int{0, 0, 0, 0}, 0)
	near(t, "below it", d.at(0.5, 0.55), [4]int{0, 0, 0, 0}, 0)
}

// TestGIFOpaqueBackground: an opaque GIF (no transparent colour) whose
// first frame, red, covers only part of its screen shows the global
// palette's background colour, blue, around the frame.
func TestGIFOpaqueBackground(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	pal := color.Palette{color.RGBA{0, 0, 255, 255}, color.RGBA{255, 0, 0, 255}}
	f := image.NewPaletted(image.Rect(40, 30, 60, 50), pal)
	for i := range f.Pix {
		f.Pix[i] = 1
	}
	var buf bytes.Buffer
	g := &gif.GIF{Image: []*image.Paletted{f}, Delay: []int{0}, BackgroundIndex: 0,
		Config: image.Config{ColorModel: pal, Width: 100, Height: 100}}
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bg.gif")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, src := range []Source{Bytes(buf.Bytes()), File(path)} {
		d := decodeNow(t, c, src, 100, 100, 0)
		if d.r.err != errNone || d.w != 100 || d.h != 100 {
			t.Fatalf("source %d: decoded %d×%d (err %d), want the 100×100 screen", src.kind, d.w, d.h, d.r.err)
		}
		near(t, "inside the frame", d.at(0.5, 0.4), [4]int{255, 0, 0, 255}, 2)
		near(t, "the background around it", d.at(0.1, 0.1), [4]int{0, 0, 255, 255}, 2)
		near(t, "below it", d.at(0.5, 0.55), [4]int{0, 0, 255, 255}, 2)
	}
}

// TestResampleFractionalArea: three pixels, black, white, black, down to two:
// each output pixel covers one and a half inputs, half of the white one, so
// both are a third white.
func TestResampleFractionalArea(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 3, 1))
	m.SetRGBA(0, 0, color.RGBA{0, 0, 0, 255})
	m.SetRGBA(1, 0, color.RGBA{255, 255, 255, 255})
	m.SetRGBA(2, 0, color.RGBA{0, 0, 0, 255})
	dst := make([]byte, 8)
	resample(dst, 8, 2, 1, m, 1)
	for x := 0; x < 2; x++ {
		p := dst[x*4:]
		near(t, "output pixel", [4]int{int(p[0]), int(p[1]), int(p[2]), int(p[3])}, [4]int{85, 85, 85, 255}, 0)
	}
}

// TestResampleLargeSum: 24 megapixels of white averaged into one pixel stays
// white (the sums exceed 32 bits).
func TestResampleLargeSum(t *testing.T) {
	m := image.NewGray(image.Rect(0, 0, 6000, 4000))
	for i := range m.Pix {
		m.Pix[i] = 255
	}
	dst := make([]byte, 4)
	resample(dst, 4, 1, 1, m, 1)
	near(t, "24 MP of white in one pixel", [4]int{int(dst[0]), int(dst[1]), int(dst[2]), int(dst[3])}, [4]int{255, 255, 255, 255}, 0)
}

// rgbaReference forces the generic sampler to serve as an independent reference.
type rgbaReference struct{ image.Image }

func TestResampleRGBAOrientations(t *testing.T) {
	// A subimage exercises non-zero bounds and a stride wider than its rows.
	base := image.NewRGBA(image.Rect(0, 0, 21, 17))
	for i := range base.Pix {
		base.Pix[i] = byte(i * 37)
	}
	m := base.SubImage(image.Rect(3, 2, 19, 14)).(*image.RGBA)
	for o := 1; o <= 8; o++ {
		for _, size := range [][2]int{{8, 6}, {6, 8}, {5, 7}, {1, 1}, {19, 23}} {
			stride := size[0]*4 + 8
			want, got := make([]byte, stride*size[1]), make([]byte, stride*size[1])
			resample(want, stride, size[0], size[1], rgbaReference{m}, o)
			resample(got, stride, size[0], size[1], m, o)
			if !bytes.Equal(got, want) {
				t.Fatalf("orientation %d, size %v differs", o, size)
			}
		}
	}
}
