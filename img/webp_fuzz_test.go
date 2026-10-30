//go:build !darwin

package img

import (
	"bytes"
	"image"
	"testing"
)

// Portable WebP/still-image decoding is excluded by production build tags
// on macOS. Keep this target under those same tags for Linux/Windows runs.
// Bounds are the same as FuzzImageParsers; valid larger images are scanned
// only, whereas over-limit configurations must fail before pixel decoding.
func FuzzWebPDecoders(f *testing.F) {
	for _, b := range [][]byte{nil, []byte("RIFF\x04\x00\x00\x00WEBP"), webpSolid(7, 5, 90, 40, 10, 127), webpAnimation(8, 8, 4, 30, 0), zeroPNG(3, 5), zeroJPEG(3, 5), zeroGIF(3, 5)} {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 64<<10 {
			t.Skip()
		}
		finish := imageFuzzBudget(t, len(data))
		pixels := 0
		defer func() { finish(pixels) }()
		end := 0
		webpChunks(data, func(_ string, off, n int) bool {
			if off < end || n < 0 || off > len(data)-n {
				t.Fatal("WebP chunk outside input")
			}
			end = off + n
			return true
		})
		frames, delay := webpFrames(data)
		if frames < 1 || frames > maxAnimFrames || delay < 0 {
			t.Fatal("WebP scan outside bounds")
		}
		if a, err := openWebP(data); err == nil {
			if !fitsCanvas(int64(a.w), int64(a.h)) || a.count() < 1 || a.count() > maxAnimFrames {
				t.Fatal("WebP index exceeds caps")
			}
			for _, frame := range a.frames {
				if frame.rect.Empty() || !frame.rect.In(image.Rect(0, 0, a.w, a.h)) || frame.data.off < 0 || frame.data.n < 0 || int64(frame.data.off)+int64(frame.data.n) > int64(len(data)) {
					t.Fatal("WebP frame outside bounds")
				}
			}
			if a.w*a.h <= 1<<16 && a.count() <= 64 && a.w*a.h*a.count() <= 1<<18 {
				pixels += a.w * a.h * a.count()
				checkFuzzFrames(t, a)
			}
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return
		}
		p := int64(cfg.Width) * int64(cfg.Height)
		if p > 1<<16 && p <= maxPixels {
			return
		}
		m, code := decodeImage(data, 32)
		if cfg.Width <= 0 || cfg.Height <= 0 || p > maxPixels {
			if code != errTooLarge {
				t.Fatalf("oversized header returned %d", code)
			}
			return
		}
		pixels += int(p)
		if code == errNone && (m == nil || m.Rect.Min != (image.Point{}) || m.Rect.Dx() <= 0 || m.Rect.Dy() <= 0 || m.Rect.Dx() > 32 || m.Rect.Dy() > 32 || len(m.Pix) != m.Rect.Dx()*m.Rect.Dy()*4) {
			t.Fatal("still decode output outside requested box")
		}
	})
}
