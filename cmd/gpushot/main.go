//go:build darwin

// Command gpushot renders a showcase DrawList (every quad mode: gradients,
// rounded rects, hairlines, shadows, glyph masks, RGBA images, clips, fades)
// offscreen with the Metal renderer and writes it as a PNG. For eyeballing
// the renderer without a window.
//
//	go run ./cmd/gpushot -o shot.png
package main

import (
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"runtime"

	"gonativeui/gpu"
)

const W, H = 1200, 800

func main() {
	runtime.LockOSThread() // the renderer pushes autorelease pools, which are per thread
	out := flag.String("o", "gpushot.png", "output PNG path")
	flag.Parse()

	r, err := gpu.NewOffscreenRenderer()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gpushot:", err)
		os.Exit(1)
	}
	defer r.Destroy()

	var list gpu.DrawList
	showcase(r, &list)

	pix := make([]byte, W*H*4)
	r.RenderToRGBA(W, H, [4]float32{0, 0, 0, 1}, &list, pix)
	img := &image.NRGBA{Pix: pix, Stride: W * 4, Rect: image.Rect(0, 0, W, H)} // opaque: premultiplied == straight
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gpushot:", err)
		os.Exit(1)
	}
	if err := png.Encode(f, img); err != nil {
		fmt.Fprintln(os.Stderr, "gpushot:", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "gpushot:", err)
		os.Exit(1)
	}
	fmt.Printf("%d quads, %d draw calls -> %s\n", len(list.Quads), r.DrawCalls(), *out)
}

func quad(x0, y0, x1, y1 float32, c uint32) gpu.Quad {
	return gpu.Quad{Rect: [4]float32{x0, y0, x1, y1}, Color: [4]uint32{c, c, c, c}}
}

func round(q gpu.Quad, r float32) gpu.Quad {
	q.Radii = [4]float32{r, r, r, r}
	return q
}

func vgrad(q gpu.Quad, top, bottom uint32) gpu.Quad {
	q.Color = [4]uint32{top, top, bottom, bottom}
	return q
}

func shadow(rect [4]float32, dx, dy, spread, sigma, radius float32, c uint32) gpu.Quad {
	q := quad(rect[0]+dx-spread, rect[1]+dy-spread, rect[2]+dx+spread, rect[3]+dy+spread, c)
	q.Soft = sigma
	return round(q, radius+spread)
}

func outline(q gpu.Quad, width float32, c uint32) gpu.Quad {
	q.Color = [4]uint32{}
	q.Border, q.BorderColor = width, c
	return q
}

func showcase(r *gpu.Renderer, l *gpu.DrawList) {
	glyphs, glyphW, glyphH := makeGlyphAtlas(r)
	disc := makeDisc(r)
	photo := makeImage(r)

	// Window background: four-corner gradient.
	bg := quad(0, 0, W, H, 0)
	bg.Color = [4]uint32{0x14203dFF, 0x2e1a52FF, 0x0a1220FF, 0x1d3156FF}
	l.Add(bg)

	// ---- Rex-like card: two shadows, rounded body, hairline, inner highlight.
	card := [4]float32{48, 48, 648, 408}
	l.Add(shadow(card, 0, 22, 0, 26, 14, 0x00000070))
	l.Add(shadow(card, 0, 2, 0, 3, 14, 0x00000080))
	body := round(vgrad(quad(card[0], card[1], card[2], card[3], 0), 0x20232fFF, 0x171924FF), 14)
	l.Add(body)
	l.Add(outline(round(quad(card[0], card[1], card[2], card[3], 0), 14), 1, 0xFFFFFF22))
	l.Add(outline(round(quad(card[0]+1, card[1]+1, card[2]-1, card[3]-1, 0), 13), 1, 0xFFFFFF0C))

	// Tab capsule with an active pill; the last tab title fades out.
	l.Add(round(quad(68, 64, 628, 100, 0xFFFFFF0F), 18))
	l.Add(outline(round(quad(68, 64, 628, 100, 0), 18), 1, 0xFFFFFF14))
	l.Add(round(quad(72, 68, 232, 96, 0x4C7DFFFF), 14))
	text(l, glyphs, glyphW, glyphH, 88, 74, 12, 1, 0xFFFFFFFF, 0)
	text(l, glyphs, glyphW, glyphH, 266, 74, 14, 3, 0xFFFFFFB0, 0)
	text(l, glyphs, glyphW, glyphH, 442, 74, 22, 5, 0xFFFFFFB0, 540) // fades out at x=540..610

	// Terminal-like rows in ANSI-ish colours, clipped to the pane.
	pane := [4]float32{68, 116, 628, 392}
	l.Add(round(quad(pane[0], pane[1], pane[2], pane[3], 0x0E0F16FF), 8))
	colours := []uint32{0xE6E6E6FF, 0x5FD38DFF, 0x6CB6FFFF, 0xF2C94CFF, 0xFF7B72FF, 0xC792EAFF, 0x89DDFFFF}
	for i := 0; i < 15; i++ {
		x := float32(84)
		y := float32(128 + i*17)
		for w := 0; w < 5; w++ {
			n := 3 + (i*7+w*5)%9
			x = textClipped(l, glyphs, glyphW, glyphH, x, y, n, i*13+w*3, colours[(i+w*2)%len(colours)], pane)
			x += 9
		}
	}

	// ---- Right column: mode demos.
	label := func(x, y float32) { text(l, glyphs, glyphW, glyphH, x, y, 10, int(x+y), 0xFFFFFF90, 0) }

	// Corner radii, independent per corner.
	label(690, 48)
	for i, radii := range [][4]float32{{0, 0, 0, 0}, {12, 12, 12, 12}, {28, 0, 28, 0}, {0, 28, 0, 28}, {36, 36, 36, 36}} {
		x := float32(690 + i*100)
		q := vgrad(quad(x, 72, x+84, 156, 0), 0xFF8A5BFF, 0xE23E6BFF)
		q.Radii = radii
		l.Add(q)
	}

	// Hairlines and borders: 1, 2, 4 px, outlines on translucent fill.
	label(690, 176)
	for i, bw := range []float32{1, 1, 2, 4} {
		x := float32(690 + i*124)
		q := round(quad(x, 200, x+108, 270, 0x2A2F45FF), []float32{0, 10, 16, 24}[i])
		q.Border, q.BorderColor = bw, 0x7AA2FFFF
		l.Add(q)
	}

	// Soft shadows of increasing sigma on a light plate.
	plate := quad(670, 290, 1170, 420, 0xE9ECF5FF)
	l.Add(round(plate, 12))
	for i, sg := range []float32{2, 6, 12, 20} {
		x := float32(700 + i*118)
		rect := [4]float32{x, 316, x + 76, 392}
		l.Add(shadow(rect, 0, 6, 0, sg, 10, 0x1B2340A0))
		l.Add(round(quad(rect[0], rect[1], rect[2], rect[3], 0xFFFFFFFF), 10))
	}

	// Glyph masks: 1:1, scaled, tinted by corner colours; RGBA image: 1:1, scaled, rounded.
	for i, c := range []uint32{0xFFFFFFFF, 0x5FD38DFF, 0xF2C94CFF, 0xFF7B72FF} {
		x := float32(48 + i*70)
		g := quad(x, 440, x+32, 472, c)
		g.Mode, g.Tex, g.UV = gpu.ModeMaskR8, disc, [4]float32{0, 0, 32, 32}
		l.Add(g)
	}
	big := quad(330, 430, 458, 558, 0)
	big.Color = [4]uint32{0xFFB86CFF, 0xFF5C8AFF, 0x6CB6FFFF, 0x7C5CFFFF}
	big.Mode, big.Tex, big.UV = gpu.ModeMaskR8, disc, [4]float32{0, 0, 32, 32}
	l.Add(big)

	im := quad(48, 490, 112, 554, 0xFFFFFFFF)
	im.Mode, im.Tex, im.UV = gpu.ModeRGBA, photo, [4]float32{0, 0, 64, 64}
	l.Add(im)
	im2 := round(quad(130, 490, 258, 618, 0xFFFFFFFF), 28)
	im2.Mode, im2.Tex, im2.UV = gpu.ModeRGBA, photo, [4]float32{0, 0, 64, 64}
	l.Add(im2)
	im3 := round(quad(276, 490, 340, 554, 0xFFFFFF90), 32)
	im3.Mode, im3.Tex, im3.UV = gpu.ModeRGBA, photo, [4]float32{0, 0, 64, 64}
	l.Add(im3)

	// Clip: a big gradient shape cut by a clip rect (no scissor, same batch).
	clipped := round(vgrad(quad(490, 440, 690, 640, 0), 0x6EE7B7FF, 0x3B82F6FF), 40)
	clipped.Clip = [4]float32{500, 460, 670, 600}
	l.Add(clipped)
	l.Add(outline(quad(500, 460, 670, 600, 0), 1, 0xFFFFFF60))

	// Faded text-like bars (truncated titles), several fade widths.
	for i := 0; i < 5; i++ {
		y := float32(450 + i*26)
		b := round(quad(730, y, 1150, y+14, 0xFFFFFFFF), 7)
		b.Color = [4]uint32{0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF}
		b.FadeX = [2]float32{1150 - 40 - float32(i)*70, 1150}
		l.Add(b)
	}

	// Translucent layered shapes over the gradient.
	for i := 0; i < 5; i++ {
		x := float32(740 + i*56)
		l.Add(round(quad(x, 600, x+120, 720, []uint32{0xFF3B6BA0, 0x3BFFB0A0, 0x3B8BFFA0, 0xFFD23BA0, 0xB03BFFA0}[i]), 60))
	}

	// Pixel-aligned hairline grid: must be one crisp pixel per line.
	for i := 0; i < 24; i++ {
		x := float32(60 + i*4)
		l.Add(quad(x, 640, x+1, 760, 0xFFFFFFB0))
	}
	for i := 0; i < 12; i++ {
		y := float32(640 + i*10)
		l.Add(quad(190, y, 330, y+1, 0xFFFFFF70))
	}
	// Shape-only rounded pill buttons.
	for i, c := range []uint32{0x4C7DFFFF, 0x5FD38DFF, 0xFF7B72FF} {
		x := float32(360 + i*100)
		l.Add(shadow([4]float32{x, 680, x + 88, 716}, 0, 6, 0, 8, 18, c&0xFFFFFF00|0x60))
		l.Add(round(vgrad(quad(x, 680, x+88, 716, 0), c, c&0xFFFFFF00|0xC8), 18))
		l.Add(outline(round(quad(x, 680, x+88, 716, 0), 18), 1, 0xFFFFFF30))
	}
}

// text draws n pseudo-random glyph cells starting at (x, y); fadeEnd > 0
// fades the run out between fadeEnd-70 and fadeEnd.
func text(l *gpu.DrawList, tex gpu.TextureID, gw, gh int, x, y float32, n, seed int, col uint32, fadeEnd float32) {
	for i := 0; i < n; i++ {
		g := (seed*31 + i*17 + i*i) % 16
		q := quad(x, y, x+float32(gw), y+float32(gh), col)
		q.Mode, q.Tex = gpu.ModeMaskR8, tex
		q.UV = [4]float32{float32(g * gw), 0, float32(g*gw + gw), float32(gh)}
		if fadeEnd > 0 {
			q.FadeX = [2]float32{fadeEnd - 70, fadeEnd}
		}
		l.Add(q)
		x += float32(gw)
	}
}

// textClipped draws a word of n glyphs clipped to pane, returning the next x.
func textClipped(l *gpu.DrawList, tex gpu.TextureID, gw, gh int, x, y float32, n, seed int, col uint32, pane [4]float32) float32 {
	for i := 0; i < n; i++ {
		g := (seed*31 + i*17 + i*i) % 16
		q := quad(x, y, x+float32(gw), y+float32(gh), col)
		q.Mode, q.Tex = gpu.ModeMaskR8, tex
		q.UV = [4]float32{float32(g * gw), 0, float32(g*gw + gw), float32(gh)}
		q.Clip = pane
		l.Add(q)
		x += float32(gw)
	}
	return x
}

// makeGlyphAtlas builds a 16-cell R8 atlas of letter-like strokes.
func makeGlyphAtlas(r *gpu.Renderer) (gpu.TextureID, int, int) {
	const gw, gh, n = 8, 14, 16
	pix := make([]byte, gw*n*gh)
	set := func(cell, x, y int, v byte) {
		if x >= 0 && x < gw && y >= 0 && y < gh {
			pix[y*gw*n+cell*gw+x] = v
		}
	}
	for c := 0; c < n; c++ {
		// Vertical stem, a bowl, and a crossbar chosen by the cell index.
		for y := 3; y < 12; y++ {
			set(c, 1+c%3, y, 255)
		}
		for x := 1; x < 6; x++ {
			if c&1 == 0 {
				set(c, x, 3, 255)
			}
			if c&2 == 0 {
				set(c, x, 7, 200)
			}
			if c&4 == 0 {
				set(c, x, 11, 255)
			}
		}
		if c&8 != 0 {
			for y := 3; y < 12; y++ {
				set(c, 6, y, 230)
			}
		}
	}
	t := r.NewTexture(gpu.TexR8, gw*n, gh)
	r.Upload(t, 0, 0, gw*n, gh, pix, gw*n)
	return t, gw, gh
}

// makeDisc builds a 32×32 R8 coverage disc with a soft edge and a ring.
func makeDisc(r *gpu.Renderer) gpu.TextureID {
	const n = 32
	pix := make([]byte, n*n)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			d := math.Hypot(float64(x)+0.5-n/2, float64(y)+0.5-n/2)
			cov := math.Max(0, math.Min(1, n/2-1-d+0.5))
			hole := math.Max(0, math.Min(1, 6-d+0.5))
			pix[y*n+x] = byte(255 * cov * (1 - 0.65*hole))
		}
	}
	t := r.NewTexture(gpu.TexR8, n, n)
	r.Upload(t, 0, 0, n, n, pix, n)
	return t
}

// makeImage builds a 64×64 premultiplied RGBA image: a hue sweep with a
// radial alpha falloff.
func makeImage(r *gpu.Renderer) gpu.TextureID {
	const n = 64
	pix := make([]byte, n*n*4)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			c := color.NRGBAModel.Convert(color.RGBA{
				R: byte(40 + 200*x/n), G: byte(90 + 120*y/n), B: byte(255 - 180*x/n), A: 255,
			}).(color.NRGBA)
			d := math.Hypot(float64(x)+0.5-n/2, float64(y)+0.5-n/2) / (n / 2)
			a := math.Max(0, math.Min(1, 1.35-d))
			i := (y*n + x) * 4
			pix[i], pix[i+1], pix[i+2], pix[i+3] = byte(float64(c.R)*a), byte(float64(c.G)*a), byte(float64(c.B)*a), byte(255*a)
		}
	}
	t := r.NewTexture(gpu.TexRGBA8, n, n)
	r.Upload(t, 0, 0, n, n, pix, n*4)
	return t
}
