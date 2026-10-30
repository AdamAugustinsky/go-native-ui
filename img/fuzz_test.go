package img

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/png"
	"runtime"
	"testing"
	"time"

	"gonativeui/internal/animgen"
)

// Scanners see every <=64 KiB input, including forged huge dimensions.
// Pixel decoding is limited to 64K pixels/frame and 256K pixels/animation;
// index-only scans still exercise the production 25 MP and 65536-frame caps.
// Budget: 2 s + 100 us/byte, 16 MiB + 512 B/byte + 128 B/decoded pixel.
func FuzzImageParsers(f *testing.F) {
	for _, b := range [][]byte{nil, []byte(pngSig), []byte("GIF89a"), zeroPNG(3, 5), zeroGIF(3, 5), animgen.GIF(8, 8, disposalFrames(), 0), animgen.APNG(8, 8, disposalFrames(), 0), encPNG(f, gradient(9, 7))} {
		f.Add(b, uint32(8), uint32(8))
	}
	f.Add(animgen.APNG(8, 8, disposalFrames(), 0), uint32(0xffffffff), uint32(0xffffffff))
	header := animgen.APNG(1, 1, []animgen.Frame{{Image: animgen.Solid(1, 1, cRed), DelayMs: 10}}, 0)
	f.Fuzz(func(t *testing.T, data []byte, w, h uint32) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		finish := imageFuzzBudget(t, len(data))
		pixels := 0
		defer func() { finish(pixels) }()
		frames, delay := gifFrames(data)
		gifBackground(data)
		if frames < 1 || int64(frames) > int64(len(data))+1 || delay < 0 || delay > 655350 {
			t.Fatalf("invalid GIF scan %d/%d", frames, delay)
		}
		if g, err := openGIF(data); err == nil {
			if !fitsCanvas(int64(g.w), int64(g.h)) || g.count() < 1 || g.count() > maxAnimFrames {
				t.Fatal("GIF index exceeds caps")
			}
			end := int32(0)
			for _, frame := range g.frames {
				if frame.start < end || frame.end < frame.start || int64(frame.end) > int64(len(data)) {
					t.Fatal("GIF frame span out of order/bounds")
				}
				end = frame.end
			}
			if g.w*g.h <= 1<<16 && g.maxFrame <= 1<<16 && g.count() <= 64 && max(g.w*g.h, g.maxFrame)*g.count() <= 1<<18 {
				pixels += max(g.w*g.h, g.maxFrame) * g.count()
				checkFuzzFrames(t, g)
				checkFuzzGIFReference(t, g, data)
			}
		}
		for _, b := range [][]byte{data, fuzzPNGChecksums(data)} {
			if a, err := openAPNG(b); err == nil {
				if !fitsCanvas(int64(a.w), int64(a.h)) || a.count() < 1 || a.count() > maxAnimFrames {
					t.Fatal("APNG index exceeds caps")
				}
				for _, frame := range a.frames {
					if frame.rect.Empty() || !frame.rect.In(image.Rect(0, 0, a.w, a.h)) || frame.disposal > disposePrevious {
						t.Fatal("APNG frame outside canvas")
					}
					end := int64(0)
					for _, s := range frame.data {
						if int64(s.off) < end || s.n < 0 || int64(s.off)+int64(s.n) > int64(len(b)) {
							t.Fatal("APNG data span out of bounds")
						}
						end = int64(s.off) + int64(s.n)
					}
				}
				if a.w*a.h <= 1<<16 && a.count() <= 64 && a.w*a.h*a.count() <= 1<<18 {
					pixels += a.w * a.h * a.count()
					checkFuzzFrames(t, a)
				}
			}
			cfg, err := png.DecodeConfig(bytes.NewReader(b))
			if err == nil && cfg.Width > 0 && cfg.Height > 0 && int64(cfg.Width)*int64(cfg.Height) <= 1<<16 {
				pixels += cfg.Width * cfg.Height
				if m, err := png.Decode(bytes.NewReader(b)); err == nil && m.Bounds() != image.Rect(0, 0, cfg.Width, cfg.Height) {
					t.Fatal("PNG bounds differ from header")
				}
			}
		}
		// A valid checksummed APNG header carries arbitrary dimensions all
		// the way to the cap check without allocating a corresponding image.
		b := bytes.Clone(header)
		binary.BigEndian.PutUint32(b[16:], w)
		binary.BigEndian.PutUint32(b[20:], h)
		b = fuzzPNGChecksums(b)
		wantFits := w > 0 && h > 0 && uint64(w)*uint64(h) <= uint64(animMaxCanvas)
		if fitsCanvas(int64(w), int64(h)) != wantFits {
			t.Fatal("canvas multiplication/cap disagrees")
		}
		a, err := openAPNG(b)
		if w > 0 && h > 0 && !wantFits && !errors.Is(err, errAnimLarge) {
			t.Fatalf("oversized header: %v", err)
		}
		if a != nil && !wantFits {
			t.Fatal("oversized header accepted")
		}
	})
}

func imageFuzzBudget(t *testing.T, n int) func(int) {
	t.Helper()
	limit := 2*time.Second + time.Duration(n)*100*time.Microsecond
	timer := time.AfterFunc(limit, func() { panic("image fuzz time bound exceeded") })
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	return func(pixels int) {
		defer timer.Stop()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		if used := after.TotalAlloc - before.TotalAlloc; used > uint64(16<<20)+512*uint64(n)+128*uint64(pixels) {
			t.Fatalf("allocated %d bytes for %d source bytes/%d pixels", used, n, pixels)
		}
	}
}

func fuzzPNGChecksums(data []byte) []byte {
	b := bytes.Clone(data)
	if len(b) < 8 || string(b[:8]) != pngSig {
		return b
	}
	for off := 8; off+12 <= len(b); {
		n := uint64(binary.BigEndian.Uint32(b[off:]))
		if n > uint64(len(b)-off-12) {
			break
		}
		end := off + 8 + int(n)
		binary.BigEndian.PutUint32(b[end:], crc32.ChecksumIEEE(b[off+4:end]))
		off = end + 4
	}
	return b
}

func checkFuzzFrames(t *testing.T, a frameSource) {
	t.Helper()
	w, h := a.size()
	if _, err := a.frame(-1); err == nil {
		t.Fatal("negative frame accepted")
	}
	if _, err := a.frame(a.count()); err == nil {
		t.Fatal("past-end frame accepted")
	}
	var saved [][]byte
	for i := 0; i < a.count(); i++ {
		m, err := a.frame(i)
		if err != nil {
			break
		}
		if m.Rect != image.Rect(0, 0, w, h) || m.Stride != w*4 || len(m.Pix) != w*h*4 {
			t.Fatalf("invalid canvas: %v", m.Rect)
		}
		for k := 0; k < len(m.Pix); k += 4 {
			if m.Pix[k] > m.Pix[k+3] || m.Pix[k+1] > m.Pix[k+3] || m.Pix[k+2] > m.Pix[k+3] {
				t.Fatal("non-premultiplied canvas")
			}
		}
		saved = append(saved, bytes.Clone(m.Pix))
	}
	a.trim()
	for i := len(saved) - 1; i >= 0; i-- {
		m, err := a.frame(i)
		if err != nil || !bytes.Equal(saved[i], m.Pix) {
			t.Fatalf("frame %d changed after trim/seek: %v", i, err)
		}
	}
}

// The reference uses standard GIF pixel decoding and image/draw, with a
// separate disposal implementation. Like browsers, background disposal
// clears to transparent, independently of the file's background hint.
func checkFuzzGIFReference(t *testing.T, g *gifAnim, data []byte) {
	t.Helper()
	ref, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return
	}
	if len(ref.Image) != g.count() {
		t.Fatal("GIF frame count differs from reference")
	}
	if n, delay := gifFrames(data); int(n) != len(ref.Image) || int(delay) != ref.Delay[0]*10 {
		t.Fatal("GIF scanner count/delay differs from reference")
	}
	canvas := image.NewRGBA(image.Rect(0, 0, g.w, g.h))
	var previous []byte
	g.trim()
	for i, m := range ref.Image {
		if i > 0 {
			switch ref.Disposal[i-1] {
			case gif.DisposalBackground:
				draw.Draw(canvas, ref.Image[i-1].Bounds(), image.Transparent, image.Point{}, draw.Src)
			case gif.DisposalPrevious:
				copy(canvas.Pix, previous)
			}
		}
		previous = append(previous[:0], canvas.Pix...)
		draw.Draw(canvas, m.Bounds(), m, m.Bounds().Min, draw.Over)
		got, err := g.frame(i)
		if err != nil {
			t.Fatalf("valid GIF frame rejected: %v", err)
		}
		if !bytes.Equal(got.Pix, canvas.Pix) {
			t.Fatalf("GIF frame %d differs from standard decode/composition", i)
		}
	}
}

// Arbitrary JPEG and raw TIFF metadata; the pixel transform runs all eight
// orientations on <=17x19 pixels, with inverse round-trips and optimized vs
// generic agreement. The same linear time/allocation budget applies.
func FuzzImageOrientation(f *testing.F) {
	f.Add([]byte{}, byte(0), byte(0))
	for o := 1; o <= 8; o++ {
		f.Add(withOrientation(encJPEG(f, quadrants(3, 5), 90), o), byte(3), byte(5))
	}
	f.Add([]byte("II\x2a\x00\x08\x00\x00\x00\x00\x00"), byte(7), byte(9))
	f.Fuzz(func(t *testing.T, b []byte, width, height byte) {
		if len(b) > 64<<10 {
			t.Skip()
		}
		finish := imageFuzzBudget(t, len(b))
		defer finish(17 * 19 * 16)
		for _, o := range []int{Orientation(b), tiffOrientation(b)} {
			if o < 1 || o > 8 {
				t.Fatalf("orientation %d", o)
			}
		}
		w, h := int(width%17)+1, int(height%19)+1
		base := image.NewNRGBA(image.Rect(2, 3, w+2, h+3))
		for i := range base.Pix {
			if len(b) > 0 {
				base.Pix[i] = b[i%len(b)]
			}
		}
		before := bytes.Clone(base.Pix)
		for o := 1; o <= 8; o++ {
			m := Orient(base, o)
			ow, oh := w, h
			if o >= 5 {
				ow, oh = h, w
			}
			if m.Bounds().Dx() != ow || m.Bounds().Dy() != oh {
				t.Fatal("orientation dimensions")
			}
			inverse := o
			if o == 6 {
				inverse = 8
			}
			if o == 8 {
				inverse = 6
			}
			back := Orient(m, inverse)
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					if back.At(back.Bounds().Min.X+x, back.Bounds().Min.Y+y) != base.At(x+2, y+3) {
						t.Fatalf("orientation %d inverse differs", o)
					}
				}
			}
			// Generic conversion cannot preserve hidden RGB of zero-alpha
			// pixels; compare RGBA values, which describe visible colour.
			generic := Orient(rgbaReference{base}, o)
			for y := 0; y < oh; y++ {
				for x := 0; x < ow; x++ {
					a := color.NRGBAModel.Convert(m.At(m.Bounds().Min.X+x, m.Bounds().Min.Y+y)).(color.NRGBA)
					c := color.NRGBAModel.Convert(generic.At(generic.Bounds().Min.X+x, generic.Bounds().Min.Y+y)).(color.NRGBA)
					if a != c {
						t.Fatalf("orientation %d generic differs", o)
					}
				}
			}
		}
		if !bytes.Equal(base.Pix, before) {
			t.Fatal("Orient changed source")
		}
	})
}
