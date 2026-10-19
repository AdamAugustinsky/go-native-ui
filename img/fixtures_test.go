package img

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"gonativeui/gpu"
)

// Fixtures are generated here: no image files are checked in.

// quadrants is a w×h image whose quadrants are red (top left), green (top
// right), blue (bottom left) and white (bottom right), as stored.
func quadrants(w, h int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.RGBA{255, 255, 255, 255}
			switch {
			case x < w/2 && y < h/2:
				c = color.RGBA{255, 0, 0, 255}
			case y < h/2:
				c = color.RGBA{0, 255, 0, 255}
			case x < w/2:
				c = color.RGBA{0, 0, 255, 255}
			}
			m.SetRGBA(x, y, c)
		}
	}
	return m
}

// gradient is a smooth w×h image (no edges for resampling filters to
// disagree on), with alpha falling off to the right.
func gradient(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 160, uint8(255 - x*96/w)})
		}
	}
	return m
}

func encPNG(t testing.TB, m image.Image) []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, m); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encJPEG(t testing.TB, m image.Image, q int) []byte {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, m, &jpeg.Options{Quality: q}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withOrientation inserts an APP1 Exif segment holding orientation o right
// after a JPEG's SOI marker (big-endian TIFF, IFD0 with one entry).
func withOrientation(j []byte, o int) []byte {
	var tiff bytes.Buffer
	tiff.WriteString("MM")
	binary.Write(&tiff, binary.BigEndian, uint16(42))
	binary.Write(&tiff, binary.BigEndian, uint32(8))
	binary.Write(&tiff, binary.BigEndian, uint16(1)) // one entry
	binary.Write(&tiff, binary.BigEndian, uint16(0x0112))
	binary.Write(&tiff, binary.BigEndian, uint16(3)) // SHORT
	binary.Write(&tiff, binary.BigEndian, uint32(1))
	binary.Write(&tiff, binary.BigEndian, uint16(o))
	binary.Write(&tiff, binary.BigEndian, uint16(0))
	binary.Write(&tiff, binary.BigEndian, uint32(0)) // no next IFD
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	out := append([]byte{}, j[:2]...)
	out = append(out, 0xFF, 0xE1, byte((len(seg)+2)>>8), byte(len(seg)+2))
	out = append(out, seg...)
	return append(out, j[2:]...)
}

// twoFrameGIF is a GIF of two w×h frames, red then blue, shown 70 ms and
// 120 ms.
func twoFrameGIF(t testing.TB, w, h int) []byte {
	g := &gif.GIF{Delay: []int{7, 12}}
	for _, c := range []color.Color{color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}} {
		f := image.NewPaletted(image.Rect(0, 0, w, h), palette.Plan9)
		idx := uint8(f.Palette.Index(c))
		for i := range f.Pix {
			f.Pix[i] = idx
		}
		g.Image = append(g.Image, f)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, g); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// shown is where a stored pixel lands with orientation o: the expected
// colour of the shown image at fractional position (fx, fy) is the stored
// image's colour at the returned fractions.
func shownToStored(fx, fy float64, o int) (float64, float64) {
	switch o {
	case 2:
		return 1 - fx, fy
	case 3:
		return 1 - fx, 1 - fy
	case 4:
		return fx, 1 - fy
	case 5:
		return fy, fx
	case 6:
		return fy, 1 - fx
	case 7:
		return 1 - fy, 1 - fx
	case 8:
		return 1 - fy, fx
	}
	return fx, fy
}

// ---- a fake renderer ----------------------------------------------------------

type fakeTex struct {
	w, h int
	live bool
}

// fakeGPU records textures and uploads without allocating once its table has
// grown.
type fakeGPU struct {
	tex                  []fakeTex
	uploads, frees, news int
	upBytes              int64
}

func newFakeGPU() *fakeGPU { return &fakeGPU{tex: make([]fakeTex, 1, 4096)} }

func (g *fakeGPU) NewTexture(kind gpu.TextureKind, w, h int) gpu.TextureID {
	g.news++
	g.tex = append(g.tex, fakeTex{w, h, true})
	return gpu.TextureID(len(g.tex) - 1)
}

func (g *fakeGPU) Upload(t gpu.TextureID, x, y, w, h int, pixels []byte, stride int) {
	tx := g.tex[t]
	if !tx.live {
		panic("upload to a freed texture")
	}
	if x < 0 || y < 0 || x+w > tx.w || y+h > tx.h || len(pixels) < (h-1)*stride+w*4 {
		panic("upload outside the texture")
	}
	g.uploads++
	g.upBytes += int64(w * h * 4)
}

func (g *fakeGPU) FreeTexture(t gpu.TextureID) {
	if !g.tex[t].live {
		panic("double free")
	}
	g.tex[t].live = false
	g.frees++
}

func (g *fakeGPU) live() (n int, bytes int64) {
	for _, t := range g.tex[1:] {
		if t.live {
			n++
			bytes += int64(t.w * t.h * 4)
		}
	}
	return
}

// newTestCache is a cache on a fake renderer whose workers wake nothing.
func newTestCache(t testing.TB, o Options) (*Cache, *fakeGPU) {
	g := newFakeGPU()
	if o.Renderer == nil {
		o.Renderer = g
	}
	if o.Wake == nil {
		o.Wake = func() {}
	}
	c := NewCache(o)
	t.Cleanup(func() { c.Close(time.Second) })
	return c, g
}

// settle runs frames, each a Poll then build (the frame's Requests and Uses),
// until no decode is out and no upload is left (or fails the test after a
// few seconds).
func settle(t testing.TB, c *Cache, build func()) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		more := c.Poll()
		if build != nil {
			build()
		}
		if !more && c.pool.Pending() == 0 && c.upN == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the cache did not settle: %+v", c.Stats())
		}
		time.Sleep(time.Millisecond)
	}
}

// decoded is a decode result's pixels, read back from the pool.
type decoded struct {
	w, h int
	pix  []byte // w×h RGBA, the padding removed
	r    result
}

func (d decoded) at(fx, fy float64) [4]int {
	x, y := min(int(fx*float64(d.w)), d.w-1), min(int(fy*float64(d.h)), d.h-1)
	p := d.pix[(y*d.w+x)*4:]
	return [4]int{int(p[0]), int(p[1]), int(p[2]), int(p[3])}
}

// decodeNow runs the OS decoder on this goroutine.
func decodeNow(t testing.TB, c *Cache, src Source, w, h int32, flags Flags) decoded {
	t.Helper()
	j := job{src: src, copy: -1, w: w, h: h, flags: flags}
	var data []byte
	switch src.kind {
	case srcBytes:
		data = src.bytes
	case srcLease:
		data = src.lease.Bytes()
	}
	wk := new(worker)
	r := wk.decode(c, &j, data)
	if r.buf < 0 {
		return decoded{r: r}
	}
	bw := int(r.w + 2*r.pad)
	all := c.bufs.bytes(r.buf, bw*int(r.h+2*r.pad)*4)
	d := decoded{w: int(r.w), h: int(r.h), pix: make([]byte, int(r.w*r.h*4)), r: r}
	for y := 0; y < d.h; y++ {
		copy(d.pix[y*d.w*4:(y+1)*d.w*4], all[((y+int(r.pad))*bw+int(r.pad))*4:])
	}
	if r.pad > 0 { // the border repeats the edge
		if string(all[bw*4+0:bw*4+4]) != string(all[bw*4+4:bw*4+8]) {
			t.Errorf("left border pixel differs from the edge")
		}
	}
	c.bufs.put(r.buf, c.fr)
	return d
}

func near(t testing.TB, what string, got, want [4]int, tol int) {
	t.Helper()
	for i := range got {
		if d := got[i] - want[i]; d > tol || d < -tol {
			t.Errorf("%s: got %v, want %v (±%d)", what, got, want, tol)
			return
		}
	}
}
