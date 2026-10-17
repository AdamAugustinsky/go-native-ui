//go:build darwin

// textshot shapes a few lines with the text package, rasterizes them, and
// composites the glyphs from the atlas pages on the CPU (dark grey on white)
// into a PNG, plus a PNG of the first R8 atlas page. It doesn't touch the GPU
// code, so it checks the text package alone.
//
//	go run ./cmd/textshot -o /tmp/textshot.png
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"strings"

	"gonativeui/text"
)

const scale = 2

type line struct {
	font text.FontDesc
	text string
}

var lines = []line{
	{text.FontDesc{SizePt: 13, Weight: 600}, "Stress-test Snake demo | rex-snake"},
	{text.FontDesc{SizePt: 13, Weight: 400}, "Stress-test Snake demo | rex-snake"},
	{text.FontDesc{SizePt: 13, Mono: true, Weight: 400}, "I inspected demo/snake.mjs and ran it"},
	{text.FontDesc{SizePt: 13, Weight: 400}, "プロジェクト計画書_v2.docx · 项目计划书 · 보고서 2026-10"},
	{text.FontDesc{SizePt: 13, Weight: 400}, "Snake 🐍 shipped 🎉 family 👨‍👩‍👧 trip 🏖️ — Zoë Ångström"},
	{text.FontDesc{SizePt: 13, Weight: 400}, "mixed English مرحبا بالعالم Привет Ελληνικά"},
	{text.FontDesc{SizePt: 22, Weight: 700}, "Large Bold Text 日本語 😀"},
}

func main() {
	out := flag.String("o", "textshot.png", "output PNG path")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "textshot:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	sys, err := text.New()
	if err != nil {
		return err
	}
	defer sys.Close()

	const margin, width = 24, 1100
	y := margin
	type placed struct {
		line     line
		key      text.FontKey
		baseline int
	}
	var ps []placed
	for _, l := range lines {
		l.font.Scale = scale
		k := sys.Font(l.font)
		m := sys.Metrics(k)
		lh := int(math.Ceil(float64(m.LineHeight))) + 8
		ps = append(ps, placed{l, k, y + int(math.Ceil(float64(m.Ascent))) + 4})
		y += lh
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, y+margin))
	for i := 0; i < len(img.Pix); i++ {
		img.Pix[i] = 255
	}

	// Pass 1: shape and rasterize everything, remembering where each glyph goes.
	type item struct {
		e    text.GlyphEntry
		x, y int
	}
	var items []item
	for _, p := range ps {
		ox := float64(margin) + 0.37 // a fractional origin exercises the subpixel variants
		run := sys.Run(sys.Shape(p.key, []byte(p.line.text)))
		fmt.Printf("%-44q %2d glyphs adv %.1f px\n", p.line.text, len(run.Glyphs), run.Advance)
		for i, g := range run.Glyphs {
			px := ox + float64(run.X[i])
			ix := math.Floor(px)
			q := int(math.Round((px - ix) * 4))
			if q == 4 {
				ix++
				q = 0
			}
			e := sys.Glyph(run.Faces[i], g, uint8(q))
			if e.W == 0 {
				continue
			}
			items = append(items, item{e, int(ix) + int(math.Round(float64(e.BearingX))), p.baseline - int(math.Round(float64(e.BearingY)))})
		}
	}

	// Pass 2: snapshot the pages (Dirty is the region changed since the last
	// ClearDirty, which textshot never calls, so it covers everything drawn).
	snaps := make([]*snapshot, sys.Pages())
	for p := range snaps {
		snaps[p] = takeSnapshot(sys, p)
	}

	// Pass 3: composite.
	fg := [3]float64{0x2a, 0x2a, 0x2a}
	for _, it := range items {
		blit(img, snaps[it.e.Page], it.e, it.x, it.y, fg)
	}
	if err := writePNG(out, img); err != nil {
		return err
	}
	fmt.Println("wrote", out)

	for p, sn := range snaps {
		if sn.color {
			continue
		}
		gray := image.NewGray(image.Rect(0, 0, sn.w, sn.h))
		copy(gray.Pix, sn.pix)
		path := strings.TrimSuffix(out, ".png") + "-atlas.png"
		if err := writePNG(path, gray); err != nil {
			return err
		}
		fmt.Printf("wrote %s (R8 page %d of %d)\n", path, p, len(snaps))
		break
	}
	return nil
}

// snapshot is a full copy of one atlas page rebuilt from Dirty.
type snapshot struct {
	w, h, bpp int
	color     bool
	pix       []byte
}

func takeSnapshot(sys *text.System, p int) *snapshot {
	w, h, color := sys.PageInfo(p)
	bpp := 1
	if color {
		bpp = 4
	}
	sn := &snapshot{w: w, h: h, bpp: bpp, color: color, pix: make([]byte, w*h*bpp)}
	x, y, dw, dh, px, stride, ok := sys.Dirty(p)
	if !ok {
		return sn
	}
	for r := 0; r < dh; r++ {
		copy(sn.pix[((y+r)*w+x)*bpp:((y+r)*w+x+dw)*bpp], px[r*stride:r*stride+dw*bpp])
	}
	return sn
}

// blit composites the glyph's atlas rectangle onto img at (x0, y0).
func blit(img *image.NRGBA, sn *snapshot, e text.GlyphEntry, x0, y0 int, fg [3]float64) {
	for r := 0; r < int(e.H); r++ {
		for c := 0; c < int(e.W); c++ {
			dx, dy := x0+c, y0+r
			if !(image.Pt(dx, dy).In(img.Rect)) {
				continue
			}
			o := ((int(e.Y)+r)*sn.w + int(e.X) + c) * sn.bpp
			d := img.PixOffset(dx, dy)
			if sn.color {
				// Premultiplied source over the existing pixel.
				a := float64(sn.pix[o+3]) / 255
				for k := 0; k < 3; k++ {
					v := float64(sn.pix[o+k]) + float64(img.Pix[d+k])*(1-a)
					img.Pix[d+k] = uint8(math.Min(255, v+0.5))
				}
				continue
			}
			a := float64(sn.pix[o]) / 255
			for k := 0; k < 3; k++ {
				img.Pix[d+k] = uint8(float64(img.Pix[d+k])*(1-a) + fg[k]*a + 0.5)
			}
		}
	}
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
