package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
)

// icnsEntries are the PNG-carrying entries of an .icns file, largest last:
// the type code and the pixel size of its PNG.
var icnsEntries = []struct {
	typ  string
	size int
}{
	{"icp4", 16}, {"icp5", 32}, {"ic11", 32}, {"ic12", 64},
	{"ic07", 128}, {"ic13", 256}, {"ic08", 256}, {"ic14", 512},
	{"ic09", 512}, {"ic10", 1024},
}

// makeICNS builds an .icns file from the square PNG at path. Sizes above the
// source's are left out, so a 512 px source yields an icon set up to 512 px.
func makeICNS(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("icon %s: %w", path, err)
	}
	return icnsFromImage(src)
}

func icnsFromImage(src image.Image) ([]byte, error) {
	b := src.Bounds()
	if b.Dx() != b.Dy() {
		return nil, fmt.Errorf("icon must be square, got %d×%d px", b.Dx(), b.Dy())
	}
	if b.Dx() < 16 {
		return nil, fmt.Errorf("icon is %d px; 1024 px is recommended and 16 px is the minimum", b.Dx())
	}
	rgba := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)

	var body bytes.Buffer
	n := 0
	for _, e := range icnsEntries {
		if e.size > b.Dx() {
			continue
		}
		img := image.Image(rgba)
		if e.size != b.Dx() {
			img = resizeArea(rgba, e.size)
		}
		var p bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestCompression}
		if err := enc.Encode(&p, img); err != nil {
			return nil, err
		}
		var h [8]byte
		copy(h[:4], e.typ)
		binary.BigEndian.PutUint32(h[4:], uint32(8+p.Len()))
		body.Write(h[:])
		body.Write(p.Bytes())
		n++
	}
	if n == 0 {
		return nil, fmt.Errorf("icon too small for any icns entry")
	}
	var out bytes.Buffer
	var h [8]byte
	copy(h[:4], "icns")
	binary.BigEndian.PutUint32(h[4:], uint32(8+body.Len()))
	out.Write(h[:])
	out.Write(body.Bytes())
	return out.Bytes(), nil
}

// resizeArea scales a square image down to size×size by averaging the source
// pixels each destination pixel covers, in premultiplied alpha, which is the
// right filter for shrinking an icon.
func resizeArea(src *image.NRGBA, size int) *image.NRGBA {
	sw := src.Bounds().Dx()
	// premultiplied float planes
	px := make([]float32, sw*sw*4)
	for y := 0; y < sw; y++ {
		for x := 0; x < sw; x++ {
			c := src.NRGBAAt(x, y)
			a := float32(c.A) / 255
			o := (y*sw + x) * 4
			px[o] = float32(c.R) * a
			px[o+1] = float32(c.G) * a
			px[o+2] = float32(c.B) * a
			px[o+3] = float32(c.A)
		}
	}
	scale := float64(sw) / float64(size)
	// horizontal pass: sw rows × size columns
	tmp := make([]float32, sw*size*4)
	for x := 0; x < size; x++ {
		lo, hi := float64(x)*scale, float64(x+1)*scale
		for i := int(lo); i < sw && float64(i) < hi; i++ {
			w := float32(minf(hi, float64(i+1)) - maxf(lo, float64(i)))
			for y := 0; y < sw; y++ {
				for ch := 0; ch < 4; ch++ {
					tmp[(y*size+x)*4+ch] += px[(y*sw+i)*4+ch] * w
				}
			}
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	inv := float32(1 / (scale * scale))
	for y := 0; y < size; y++ {
		lo, hi := float64(y)*scale, float64(y+1)*scale
		var acc [4]float32
		for x := 0; x < size; x++ {
			acc = [4]float32{}
			for i := int(lo); i < sw && float64(i) < hi; i++ {
				w := float32(minf(hi, float64(i+1)) - maxf(lo, float64(i)))
				for ch := 0; ch < 4; ch++ {
					acc[ch] += tmp[(i*size+x)*4+ch] * w
				}
			}
			a := acc[3] * inv
			var c color.NRGBA
			if a > 0 {
				c = color.NRGBA{clamp8(acc[0] * inv * 255 / a), clamp8(acc[1] * inv * 255 / a), clamp8(acc[2] * inv * 255 / a), clamp8(a)}
			}
			dst.SetNRGBA(x, y, c)
		}
	}
	return dst
}

func clamp8(v float32) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
